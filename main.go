package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src"
	"github.com/hsqbyte/nlink/src/client"
	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/core/vpn"
	modelConfig "github.com/hsqbyte/nlink/src/models/config"
	"github.com/hsqbyte/nlink/src/router"
	_ "github.com/hsqbyte/nlink/src/router/handle"
	"github.com/hsqbyte/nlink/src/services"
	"github.com/hsqbyte/nlink/src/services/audit"
)

var Version = "dev"

func main() {
	cfgFile := flag.String("c", "config/nlink.yaml", "配置文件路径")
	dashboard := flag.Bool("dashboard", false, "强制启动管理面板 (默认端口 18080)")
	dashboardPort := flag.Int("dashboard-port", 0, "管理面板端口 (配合 -dashboard 使用)")
	showVersion := flag.Bool("v", false, "显示版本号")
	flag.Parse()

	if *showVersion {
		fmt.Println("nlink", Version)
		os.Exit(0)
	}

	if err := config.InitConfig(*cfgFile); err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志（同时输出到控制台和文件）
	logger.InitWithPath("data/logs")

	cfg := config.GlobalConfig

	// 应用审计日志保留策略 (默认 30 天)
	if cfg.Node.Dashboard != nil {
		days := cfg.Node.Dashboard.AuditRetainDays
		if days == 0 {
			days = 30
		}
		audit.SetRetainDays(days)
	}

	// CLI -dashboard 开关: 如果配置中没有 dashboard 但指定了 -dashboard 则创建
	if *dashboard && cfg.Node.Dashboard == nil {
		port := 18080
		if *dashboardPort > 0 {
			port = *dashboardPort
		}
		cfg.Node.Dashboard = &modelConfig.DashboardConfig{Port: port, ShutdownTimeout: 30}
	}

	// 启动节点监听（如果配置了 listen）
	var srv *src.Server
	var standaloneHTTP *http.Server
	if cfg.Node.Listen != nil {
		logger.Info("启动 NLink 节点 [%s] 监听...", cfg.Node.Name)
		var err error
		srv, err = src.NewServer()
		if err != nil {
			logger.Error("创建节点服务失败: %v", err)
			os.Exit(1)
		}
		if err := srv.Start(); err != nil {
			logger.Error("启动节点服务失败: %v", err)
			os.Exit(1)
		}
	} else if cfg.Node.Dashboard.IsEnabled() {
		// 无 listen 但有 dashboard: 启动独立 HTTP 面板
		services.EnsureTunnelService()
		addr := fmt.Sprintf(":%d", cfg.Node.Dashboard.Port)
		standaloneHTTP = &http.Server{Addr: addr, Handler: router.Engine}
		go func() {
			dc := cfg.Node.Dashboard
			if dc.TLSEnabled() {
				logger.Info("启动独立管理面板: https://localhost%s", addr)
				if err := standaloneHTTP.ListenAndServeTLS(dc.TLSCertFile, dc.TLSKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("HTTPS服务器异常退出: %v", err)
				}
			} else {
				logger.Info("启动独立管理面板: http://localhost%s", addr)
				if err := standaloneHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("HTTP服务器异常退出: %v", err)
				}
			}
		}()
	}

	// 连接对端节点
	if len(cfg.Peers) > 0 {
		// 确保 TunnelService 已初始化（供 upstream peer 跟踪使用）
		services.EnsureTunnelService()
	}

	// VPN DHCP 引导：如果 vpn.virtual_ip 为空或 "auto"，向首个 peer 请求分配
	if cfg.Node.VPN.IsEnabled() && (cfg.Node.VPN.VirtualIP == "" || cfg.Node.VPN.VirtualIP == "auto") {
		if len(cfg.Peers) == 0 {
			logger.Warn("VPN virtual_ip 为 auto 但没有配置 peers，跳过 DHCP")
		} else {
			first := &cfg.Peers[0]
			logger.Info("VPN DHCP 引导: 向 %s:%d 请求分配", first.Addr, first.Port)
			cidr, err := client.RequestDHCP(cfg.Node.Name, "", first)
			if err != nil {
				fmt.Fprintf(os.Stderr, "VPN DHCP 失败: %v (使用静态或跳过)\n", err)
				logger.Error("VPN DHCP 失败: %v", err)
			} else {
				logger.Info("VPN DHCP 分配成功: %s", cidr)
				fmt.Printf("  VPN DHCP:       %s\n", cidr)
				cfg.Node.VPN.VirtualIP = cidr
			}
		}
	}

	for i := range cfg.Peers {
		peer := cfg.Peers[i]
		logger.Info("连接对端节点: %s:%d", peer.Addr, peer.Port)
		go func() {
			if err := client.Run(cfg.Node.Name, &peer); err != nil {
				logger.Error("对端连接退出: %v", err)
			}
		}()
	}

	// 启动 VPN 引擎（如果配置了 vpn）
	var vpnEngine *vpn.Engine
	if cfg.Node.VPN.IsEnabled() && cfg.Node.VPN.VirtualIP != "" && cfg.Node.VPN.VirtualIP != "auto" {
		var err error
		vpnEngine, err = vpn.NewEngine(cfg.Node.VPN, cfg.Node.Token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "VPN 引擎启动失败: %v\n", err)
			logger.Error("VPN 引擎启动失败: %v", err)
		} else {
			vpnEngine.Start()
			fmt.Printf("  VPN 虚拟网络:   %s (UDP :%d)\n", cfg.Node.VPN.VirtualIP, cfg.Node.VPN.ListenPort)
			vpn.SetGlobalEngine(vpnEngine)
			// 添加对端 VPN 节点
			for _, peer := range cfg.Peers {
				if peer.VPNPort > 0 && peer.VirtualIP != "" {
					endpoint := fmt.Sprintf("%s:%d", peer.Addr, peer.VPNPort)
					if err := vpnEngine.AddPeer(peer.VirtualIP, endpoint); err != nil {
						logger.Error("VPN 添加对端失败: %v", err)
					} else {
						fmt.Printf("  VPN 对端:       %s -> %s\n", peer.VirtualIP, endpoint)
					}
				}
			}
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGHUP)

loop:
	for {
		select {
		case <-quit:
			break loop
		case <-reload:
			logger.Info("收到 SIGHUP，尝试热重载配置: %s", *cfgFile)
			newCfg, err := config.ReloadConfig(*cfgFile)
			if err != nil {
				logger.Error("热重载失败: %v", err)
				continue
			}
			config.ApplyReload(newCfg)
			logger.Info("热重载完成 (Note: 仅 token/token_prev 实时生效，其他字段需重启)")
		}
	}

	logger.Info("正在关闭服务...")
	if vpnEngine != nil {
		vpnEngine.Stop()
	}
	if srv != nil {
		if err := srv.Stop(); err != nil {
			logger.Error("节点服务关闭失败: %v", err)
		}
	}
	if standaloneHTTP != nil {
		standaloneHTTP.Close()
	}
	logger.Info("NLink 已关闭")
}
