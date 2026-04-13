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
	modelConfig "github.com/hsqbyte/nlink/src/models/config"
	"github.com/hsqbyte/nlink/src/router"
	_ "github.com/hsqbyte/nlink/src/router/handle"
	"github.com/hsqbyte/nlink/src/services"
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

	cfg := config.GlobalConfig

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
	for i := range cfg.Peers {
		peer := cfg.Peers[i]
		logger.Info("连接对端节点: %s:%d", peer.Addr, peer.Port)
		go func() {
			if err := client.Run(cfg.Node.Name, &peer); err != nil {
				logger.Error("对端连接退出: %v", err)
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("正在关闭服务...")
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
