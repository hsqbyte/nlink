package services

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastgox/utils/logger"
)

// HTTPProxy 表示一个 HTTP 虚拟主机代理（共享 vhost 端口）
type HTTPProxy struct {
	Name          string
	CustomDomains []string
	HostRewrite   string
	PeerConnID    string
	WorkConnCh    chan net.Conn
	tunnel        *TunnelService

	acl          *ACL
	rateLimitBps int64

	TotalConns  atomic.Int64
	ActiveConns atomic.Int64
	BytesIn     atomic.Int64
	BytesOut    atomic.Int64
	RejectedACL atomic.Int64

	mu     sync.Mutex
	closed bool
}

// HTTPVhostService 管理 HTTP 虚拟主机
type HTTPVhostService struct {
	mu       sync.RWMutex
	domains  map[string]*HTTPProxy // host -> proxy
	proxies  map[string]*HTTPProxy // name -> proxy
	server   *http.Server
	listener net.Listener
	tunnel   *TunnelService
	port     int
}

var httpVhostSvc *HTTPVhostService

// StartHTTPVhost 启动 vhost HTTP 监听
func StartHTTPVhost(port int, tunnel *TunnelService) error {
	if port <= 0 {
		return nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("监听 vhost %d 失败: %w", port, err)
	}
	svc := &HTTPVhostService{
		domains:  make(map[string]*HTTPProxy),
		proxies:  make(map[string]*HTTPProxy),
		listener: ln,
		tunnel:   tunnel,
		port:     port,
	}
	svc.server = &http.Server{Handler: svc, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		logger.Info("[Vhost] HTTP 监听 :%d", port)
		if err := svc.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("[Vhost] serve 错误: %v", err)
		}
	}()
	httpVhostSvc = svc
	return nil
}

// GetHTTPVhost 返回 vhost 单例
func GetHTTPVhost() *HTTPVhostService { return httpVhostSvc }

// ServeHTTP 按 Host 路由到对应 HTTPProxy
func (s *HTTPVhostService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)

	s.mu.RLock()
	proxy := s.domains[host]
	s.mu.RUnlock()
	if proxy == nil {
		http.Error(w, "no backend for host "+host, http.StatusNotFound)
		return
	}

	// ACL 过滤
	if proxy.acl != nil {
		clientIP := clientIPFromRequest(r)
		if !proxy.acl.Allow(clientIP) {
			proxy.RejectedACL.Add(1)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// Host rewrite
	target := &url.URL{Scheme: "http", Host: host}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			if proxy.HostRewrite != "" {
				req.Host = proxy.HostRewrite
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return proxy.dialWorkConn(ctx)
			},
			DisableKeepAlives:     true, // 每次请求一个 workConn
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("[Vhost:%s] 后端错误: %v", proxy.Name, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	proxy.TotalConns.Add(1)
	proxy.ActiveConns.Add(1)
	defer proxy.ActiveConns.Add(-1)
	rp.ServeHTTP(w, r)
}

// Register 注册 HTTP 代理，返回错误（域名冲突 / 缺少域名）
func (s *HTTPVhostService) Register(p *HTTPProxy) error {
	if len(p.CustomDomains) == 0 {
		return fmt.Errorf("type=http 需要 custom_domains")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.proxies[p.Name]; ok {
		return fmt.Errorf("代理已存在: %s", p.Name)
	}
	for _, d := range p.CustomDomains {
		key := strings.ToLower(strings.TrimSpace(d))
		if key == "" {
			continue
		}
		if _, ok := s.domains[key]; ok {
			return fmt.Errorf("域名冲突: %s", key)
		}
	}
	for _, d := range p.CustomDomains {
		key := strings.ToLower(strings.TrimSpace(d))
		if key == "" {
			continue
		}
		s.domains[key] = p
	}
	s.proxies[p.Name] = p
	logger.Info("[Vhost] 代理注册: name=%s domains=%v", p.Name, p.CustomDomains)
	return nil
}

// Unregister 移除代理
func (s *HTTPVhostService) Unregister(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proxies[name]
	if !ok {
		return false
	}
	for _, d := range p.CustomDomains {
		key := strings.ToLower(strings.TrimSpace(d))
		if s.domains[key] == p {
			delete(s.domains, key)
		}
	}
	delete(s.proxies, name)
	p.Close()
	return true
}

// List 列出所有 HTTP 代理
func (s *HTTPVhostService) List() []*HTTPProxy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*HTTPProxy, 0, len(s.proxies))
	for _, p := range s.proxies {
		out = append(out, p)
	}
	return out
}

// Deliver 将 workConn 投递给指定 HTTPProxy
func (s *HTTPVhostService) Deliver(name string, conn net.Conn) bool {
	s.mu.RLock()
	p, ok := s.proxies[name]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case p.WorkConnCh <- conn:
		return true
	default:
		return false
	}
}

// Close 关闭 vhost
func (s *HTTPVhostService) Close() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	}
}

// dialWorkConn 向对端请求一条 workConn 作为该 HTTP 请求的 transport
func (p *HTTPProxy) dialWorkConn(ctx context.Context) (net.Conn, error) {
	// 先尝试池
	if pool := p.tunnel.getPeerPool(p.PeerConnID); pool != nil {
		if wc, ok := pool.TryGet(); ok {
			if err := sendActivation(wc, p.Name); err != nil {
				wc.Close()
			} else {
				return wrapHTTPConn(wc, p), nil
			}
		}
	}
	// 按需
	p.tunnel.RequestWorkConn(p.Name, p.PeerConnID)
	timeout := p.tunnel.workConnTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case wc, ok := <-p.WorkConnCh:
		if !ok {
			return nil, fmt.Errorf("workConn 通道已关闭")
		}
		if err := sendActivation(wc, p.Name); err != nil {
			wc.Close()
			return nil, err
		}
		return wrapHTTPConn(wc, p), nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("等待 workConn 超时")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close 幂等关闭
func (p *HTTPProxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.WorkConnCh)
}

// httpCountingConn 统计 HTTP 转发的字节数
type httpCountingConn struct {
	net.Conn
	p *HTTPProxy
}

func wrapHTTPConn(c net.Conn, p *HTTPProxy) net.Conn {
	if p.rateLimitBps > 0 {
		c = wrapConnWithRateLimit(c, p.rateLimitBps)
	}
	return &httpCountingConn{Conn: c, p: p}
}

func (c *httpCountingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.p.BytesIn.Add(int64(n))
	}
	return n, err
}

func (c *httpCountingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.p.BytesOut.Add(int64(n))
	}
	return n, err
}

func clientIPFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
