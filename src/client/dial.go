package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"
)

// dialPeer 根据 PeerConfig.TLS 选择 plain TCP 或 TLS 拨号。
// addr 为 host:port (host 取自 cfg.Addr，端口可能是控制端口或 work 端口)。
func (c *Client) dialPeer(addr string, timeout time.Duration) (net.Conn, error) {
	if !c.cfg.TLS {
		return net.DialTimeout("tcp", addr, timeout)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = c.cfg.Addr
	}
	serverName := c.cfg.TLSServerName
	if serverName == "" {
		serverName = host
	}
	tlsCfg := &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: c.cfg.TLSInsecureSkip,
		MinVersion:         tls.VersionTLS12,
	}
	if c.cfg.TLSCAFile != "" {
		pem, err := os.ReadFile(c.cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("读取 CA 证书失败: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA 证书解析失败: %s", c.cfg.TLSCAFile)
		}
		tlsCfg.RootCAs = pool
	}
	dialer := &net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
}
