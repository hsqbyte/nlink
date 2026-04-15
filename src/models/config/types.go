package config

// NodeConfig 节点配置
type NodeConfig struct {
	Name      string           `yaml:"name"`      // 节点名称（唯一标识）
	Token     string           `yaml:"token"`     // 认证令牌
	Listen    *ListenConfig    `yaml:"listen"`    // 监听配置（可选）
	Dashboard *DashboardConfig `yaml:"dashboard"` // 管理面板配置（可选）
	VPN       *VPNConfig       `yaml:"vpn"`       // 虚拟局域网配置（可选）
}

// ListenConfig 监听配置
type ListenConfig struct {
	Port              int `yaml:"port"`                 // TCP 控制通道端口
	MaxMessageSize    int `yaml:"max_message_size"`     // 最大消息大小
	HeartbeatTimeout  int `yaml:"heartbeat_timeout"`    // 心跳超时(秒)
	MaxProxiesPerPeer int `yaml:"max_proxies_per_peer"` // 每对端最大代理数
	WorkConnTimeout   int `yaml:"work_conn_timeout"`    // 工作连接超时(秒)
	PoolCount         int `yaml:"pool_count"`           // 全局连接池大小(0=禁用)
}

// DashboardConfig 管理面板配置
type DashboardConfig struct {
	Enabled         *bool  `yaml:"enabled"`          // 是否启用 (默认 true)
	Port            int    `yaml:"port"`             // HTTP 端口
	ShutdownTimeout int    `yaml:"shutdown_timeout"` // 优雅关闭超时(秒)
	Username        string `yaml:"username"`         // 登录用户名 (留空则不启用认证)
	Password        string `yaml:"password"`         // 登录密码
	TLSCertFile     string `yaml:"tls_cert_file"`    // TLS 证书文件 (留空则 HTTP)
	TLSKeyFile      string `yaml:"tls_key_file"`     // TLS 私钥文件
}

// IsEnabled 返回面板是否启用
func (d *DashboardConfig) IsEnabled() bool {
	if d == nil {
		return false
	}
	if d.Enabled == nil {
		return true // 默认启用
	}
	return *d.Enabled
}

// AuthRequired 返回是否需要登录认证
func (d *DashboardConfig) AuthRequired() bool {
	if d == nil {
		return false
	}
	return d.Username != "" && d.Password != ""
}

// TLSEnabled 返回是否启用 HTTPS
func (d *DashboardConfig) TLSEnabled() bool {
	if d == nil {
		return false
	}
	return d.TLSCertFile != "" && d.TLSKeyFile != ""
}

// PeerConfig 对端节点配置
type PeerConfig struct {
	Addr      string        `yaml:"addr"`       // 对端地址
	Port      int           `yaml:"port"`       // 对端控制端口
	Token     string        `yaml:"token"`      // 认证令牌
	PoolCount int           `yaml:"pool_count"` // 预建连接数(0=禁用)
	Proxies   []ProxyConfig `yaml:"proxies"`    // 代理列表
	VPNPort   int           `yaml:"vpn_port"`   // 对端 VPN UDP 端口（可选）
	VirtualIP string        `yaml:"virtual_ip"` // 对端虚拟 IP（可选）
}

// ProxyConfig 单个代理配置
type ProxyConfig struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	LocalIP    string `yaml:"local_ip"`
	LocalPort  int    `yaml:"local_port"`
	RemotePort int    `yaml:"remote_port"`
}

// VPNConfig 虚拟局域网配置
type VPNConfig struct {
	Enabled    *bool  `yaml:"enabled"`     // 是否启用 (默认 false)
	VirtualIP  string `yaml:"virtual_ip"`  // 虚拟 IP (CIDR 格式, 如 "10.0.0.1/24")
	ListenPort int    `yaml:"listen_port"` // UDP 监听端口
	MTU        int    `yaml:"mtu"`         // MTU (默认 1400)
}

// IsEnabled 返回 VPN 是否启用
func (v *VPNConfig) IsEnabled() bool {
	if v == nil {
		return false
	}
	if v.Enabled == nil {
		return false // 默认不启用
	}
	return *v.Enabled
}
