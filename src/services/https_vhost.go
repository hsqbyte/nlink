package services

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/fastgox/utils/logger"
	"golang.org/x/crypto/acme/autocert"
)

// HTTPSVhostService HTTPS 虚拟主机（通过 autocert 自动签发 Let's Encrypt 证书）
// 复用 HTTPVhostService 的 domain -> proxy 注册表：启动后两端均可接受同一 vhost 的流量。
type HTTPSVhostService struct {
	listener net.Listener
	server   *http.Server
	manager  *autocert.Manager
	port     int
}

var httpsVhostSvc *HTTPSVhostService

// ACMEOptions ACME 启动参数
type ACMEOptions struct {
	Email    string
	CacheDir string
	Domains  []string // 白名单；空=放行所有已注册 vhost 域名
}

// StartHTTPSVhost 启动 HTTPS vhost（依赖已启动的 HTTP vhost 作为 domain 路由源）
func StartHTTPSVhost(port int, opts ACMEOptions) error {
	if port <= 0 {
		return nil
	}
	if httpVhostSvc == nil {
		return fmt.Errorf("HTTPS vhost 依赖 vhost_http_port，需先启用 HTTP vhost")
	}
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = "data/acme"
	}

	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cacheDir),
		Email:  opts.Email,
	}
	// HostPolicy：白名单域名 或 已注册 vhost 域名
	whitelist := make(map[string]struct{}, len(opts.Domains))
	for _, d := range opts.Domains {
		whitelist[strings.ToLower(strings.TrimSpace(d))] = struct{}{}
	}
	m.HostPolicy = func(ctx context.Context, host string) error {
		host = strings.ToLower(host)
		if len(whitelist) > 0 {
			if _, ok := whitelist[host]; !ok {
				return fmt.Errorf("acme: host %q not whitelisted", host)
			}
			return nil
		}
		// 无白名单：检查 host 是否已注册到 vhost
		httpVhostSvc.mu.RLock()
		_, ok := httpVhostSvc.domains[host]
		httpVhostSvc.mu.RUnlock()
		if !ok {
			return fmt.Errorf("acme: host %q not registered", host)
		}
		return nil
	}

	tlsCfg := m.TLSConfig()
	ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", port), tlsCfg)
	if err != nil {
		return fmt.Errorf("监听 HTTPS vhost %d 失败: %w", port, err)
	}

	svc := &HTTPSVhostService{listener: ln, manager: m, port: port}
	svc.server = &http.Server{
		Handler:           http.HandlerFunc(svc.serveHTTPS),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("[Vhost] HTTPS 监听 :%d (ACME email=%s cache=%s)", port, opts.Email, cacheDir)
		if err := svc.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("[Vhost-HTTPS] serve 错误: %v", err)
		}
	}()
	httpsVhostSvc = svc
	return nil
}

// serveHTTPS 复用 HTTP vhost 的 domain -> proxy 映射进行转发
func (s *HTTPSVhostService) serveHTTPS(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)

	httpVhostSvc.mu.RLock()
	proxy := httpVhostSvc.domains[host]
	httpVhostSvc.mu.RUnlock()
	if proxy == nil {
		http.Error(w, "no backend for host "+host, http.StatusNotFound)
		return
	}
	if proxy.acl != nil {
		clientIP := clientIPFromRequest(r)
		if !proxy.acl.Allow(clientIP) {
			proxy.RejectedACL.Add(1)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	target := &url.URL{Scheme: "http", Host: host}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			if proxy.HostRewrite != "" {
				req.Host = proxy.HostRewrite
			}
			// B2 WebSocket 透传：保留 Upgrade / Connection 头（httputil.ReverseProxy 默认处理 Upgrade）
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return proxy.dialWorkConn(ctx)
			},
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("[Vhost-HTTPS:%s] 后端错误: %v", proxy.Name, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	proxy.TotalConns.Add(1)
	proxy.ActiveConns.Add(1)
	defer proxy.ActiveConns.Add(-1)
	rp.ServeHTTP(w, r)
}

// Close 关闭 HTTPS vhost
func (s *HTTPSVhostService) Close() {
	if s == nil || s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
}

// GetHTTPSVhost 返回 HTTPS vhost 单例
func GetHTTPSVhost() *HTTPSVhostService { return httpsVhostSvc }
