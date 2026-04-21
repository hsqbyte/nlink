package client

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	modelConfig "github.com/hsqbyte/nlink/src/models/config"
	"gopkg.in/yaml.v3"
)

// runtimeProxyFile 返回本节点对端代理的持久化文件路径。
// 以 nodeName + addr:port 为 key，支持同一节点连接多对端。
func runtimeProxyFile(nodeName, peerAddr string, peerPort int) string {
	safe := func(s string) string {
		out := make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			switch {
			case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
				out = append(out, c)
			case c == '-' || c == '_':
				out = append(out, c)
			default:
				out = append(out, '_')
			}
		}
		return string(out)
	}
	return filepath.Join("data", fmt.Sprintf("runtime_%s_%s_%d.yaml",
		safe(nodeName), safe(peerAddr), peerPort))
}

var runtimePersistMu sync.Mutex

// loadRuntimeProxies 从 delta 文件加载运行时追加的代理（若不存在返回 nil）
func loadRuntimeProxies(path string) ([]modelConfig.ProxyConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []modelConfig.ProxyConfig
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("解析 runtime 代理失败: %w", err)
	}
	return out, nil
}

// saveRuntimeProxies 原子写入 delta 代理列表
func saveRuntimeProxies(path string, proxies []modelConfig.ProxyConfig) error {
	runtimePersistMu.Lock()
	defer runtimePersistMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(proxies)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// mergeProxies 合并 static + dynamic，以 Name 去重，后者覆盖前者
func mergeProxies(static, dynamic []modelConfig.ProxyConfig) []modelConfig.ProxyConfig {
	seen := make(map[string]int, len(static)+len(dynamic))
	out := make([]modelConfig.ProxyConfig, 0, len(static)+len(dynamic))
	for _, p := range static {
		seen[p.Name] = len(out)
		out = append(out, p)
	}
	for _, p := range dynamic {
		if idx, ok := seen[p.Name]; ok {
			out[idx] = p
			continue
		}
		seen[p.Name] = len(out)
		out = append(out, p)
	}
	return out
}

// persistRuntimeProxies 将 cfg.Proxies 中非静态部分写入 delta 文件（幂等）
func (c *Client) persistRuntimeProxies() {
	if c.runtimeFile == "" {
		return
	}
	dyn := make([]modelConfig.ProxyConfig, 0)
	for _, p := range c.cfg.Proxies {
		if _, isStatic := c.staticProxyNames[p.Name]; !isStatic {
			dyn = append(dyn, p)
		}
	}
	if err := saveRuntimeProxies(c.runtimeFile, dyn); err != nil {
		fmt.Fprintf(os.Stderr, "[Node:%s] 持久化运行时代理失败: %v\n", c.nodeName, err)
	}
}
