package src

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src/core/config"
	"github.com/hsqbyte/nlink/src/core/tcp"
	"github.com/hsqbyte/nlink/src/router"
	_ "github.com/hsqbyte/nlink/src/router/handle"
	"github.com/hsqbyte/nlink/src/services"
	"github.com/panjf2000/gnet/v2"
)

// Server 节点服务器（监听部分）
type Server struct {
	HTTP     *http.Server
	TCP      *tcp.Server
	WorkConn *services.WorkConnListener
}

// NewServer 创建新的服务器实例
func NewServer() (*Server, error) {
	logger.Info("创建节点服务实例..")

	cfg := config.GlobalConfig
	lc := cfg.Node.Listen
	server := &Server{}

	// 创建TCP控制通道服务器
	opts := []gnet.Option{
		gnet.WithReuseAddr(true),
		gnet.WithReusePort(true),
	}
	server.TCP = tcp.NewServer(lc.Port, lc.MaxMessageSize, lc.HeartbeatTimeout, router.TCPRouter, opts...)

	// 启用控制通道加密
	if cfg.Node.Token != "" {
		cr, err := tcp.NewCrypto(cfg.Node.Token)
		if err != nil {
			return nil, fmt.Errorf("加密初始化失败: %w", err)
		}
		if cfg.Node.TokenPrev != "" {
			if err := cr.AddFallbackKey(cfg.Node.TokenPrev); err != nil {
				return nil, fmt.Errorf("旧密钥加载失败: %w", err)
			}
			logger.Info("Token 轮换模式：已加载 token_prev，过渡期内兼容旧客户端")
		}
		server.TCP.Codec().SetCrypto(cr)
		logger.Info("控制通道加密已启用 (AES-256-GCM)")
	}

	// 初始化隧道服务
	services.InitTunnelService(server.TCP, lc.WorkConnTimeout)

	// 启动统计历史采样（30s 采样周期，保留 1440 点 ≈ 12h）
	services.StartStatsSampler(30*time.Second, 1440)

	// 启动 HTTP 虚拟主机服务（若配置了端口）
	if lc.VhostHTTPPort > 0 {
		if err := services.StartHTTPVhost(lc.VhostHTTPPort, services.GetTunnelService()); err != nil {
			return nil, fmt.Errorf("HTTP vhost 启动失败: %w", err)
		}
	}

	// 设置断连回调: 对端断线时清理代理
	server.TCP.OnDisconnect = func(connID string) {
		services.GetTunnelService().RemovePeerProxies(connID)
	}

	// 创建工作连接监听器 (控制端口+1)
	workConnPort := lc.Port + 1
	wl, err := services.NewWorkConnListener(workConnPort, services.GetTunnelService())
	if err != nil {
		return nil, fmt.Errorf("工作连接监听失败 :%d: %w", workConnPort, err)
	}
	server.WorkConn = wl

	// 创建HTTP管理面板（如果启用）
	if cfg.Node.Dashboard.IsEnabled() {
		dc := cfg.Node.Dashboard
		addr := fmt.Sprintf(":%d", dc.Port)
		server.HTTP = &http.Server{
			Addr:    addr,
			Handler: router.Engine,
		}
	}

	logger.Info("节点服务实例创建完成")
	return server, nil
}

// Start 非阻塞启动服务器
func (s *Server) Start() error {
	cfg := config.GlobalConfig
	lc := cfg.Node.Listen

	logger.Info("启动节点服务..")
	fmt.Println("================================")
	fmt.Printf("  NLink 节点 [%s] 已启动:\n", cfg.Node.Name)
	fmt.Printf("  控制通道:      tcp://localhost:%d\n", lc.Port)
	fmt.Printf("  工作连接:      tcp://localhost:%d\n", lc.Port+1)
	fmt.Println("================================")

	// 启动HTTP（如果有）
	if s.HTTP != nil {
		dc := cfg.Node.Dashboard
		go func() {
			var err error
			if dc.TLSEnabled() {
				fmt.Printf("  管理面板:      https://localhost%s\n", s.HTTP.Addr)
				err = s.HTTP.ListenAndServeTLS(dc.TLSCertFile, dc.TLSKeyFile)
			} else {
				fmt.Printf("  管理面板:      http://localhost%s\n", s.HTTP.Addr)
				err = s.HTTP.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("HTTP服务器异常退出: %v", err)
			}
		}()
	}

	// 启动TCP控制通道
	go func() {
		if err := s.TCP.Start(); err != nil {
			logger.Error("TCP服务器异常退出: %v", err)
		}
	}()

	// 启动工作连接监听
	go s.WorkConn.Start()

	return nil
}

// Stop 优雅关闭服务器
func (s *Server) Stop() error {
	cfg := config.GlobalConfig
	timeout := 30
	if cfg.Node.Dashboard != nil {
		timeout = cfg.Node.Dashboard.ShutdownTimeout
	}
	if timeout <= 0 {
		timeout = 30
	}

	logger.Info("正在优雅关闭节点，最长等待 %d 秒...", timeout)

	// 关闭所有代理
	services.GetTunnelService().CloseAll()

	// 关闭工作连接监听
	if err := s.WorkConn.Stop(); err != nil {
		logger.Error("工作连接监听关闭失败: %v", err)
	}

	if s.HTTP != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		if err := s.HTTP.Shutdown(ctx); err != nil {
			logger.Error("HTTP服务器优雅关闭超时: %v", err)
			return err
		}
	}

	if err := s.TCP.Stop(); err != nil {
		logger.Error("TCP服务器关闭失败: %v", err)
	}

	logger.Info("节点已安全关闭")
	return nil
}
