package client

import (
	"fmt"

	modelConfig "github.com/hsqbyte/nlink/src/models/config"
)

// expandProxyConfig expands port-range proxies into multiple independent entries.
// If p.RemotePortEnd > p.RemotePort, create one proxy per port q in [RemotePort, RemotePortEnd]:
//   Name = "<name>-<offset>", RemotePort = q, LocalPort = LocalPort+offset.
// Other fields (backends / health-check / PROXY protocol / ACL / rate-limit) are reused.
func expandProxyConfig(cfg *modelConfig.PeerConfig) {
	if cfg == nil {
		return
	}
	expanded := make([]modelConfig.ProxyConfig, 0, len(cfg.Proxies))
	for _, p := range cfg.Proxies {
		if p.RemotePortEnd <= 0 || p.RemotePortEnd <= p.RemotePort {
			expanded = append(expanded, p)
			continue
		}
		for off := 0; off <= p.RemotePortEnd-p.RemotePort; off++ {
			sub := p
			sub.Name = fmt.Sprintf("%s-%d", p.Name, off)
			sub.RemotePort = p.RemotePort + off
			sub.LocalPort = p.LocalPort + off
			sub.RemotePortEnd = 0
			expanded = append(expanded, sub)
		}
	}
	cfg.Proxies = expanded
}
