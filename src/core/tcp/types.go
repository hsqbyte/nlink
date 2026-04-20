package tcp

import "encoding/json"

// Message TCP消息
type Message struct {
	Cmd  string          `json:"cmd"`            // 命令路由标识
	Seq  string          `json:"seq,omitempty"`  // 请求序列号
	Data json.RawMessage `json:"data,omitempty"` // 业务数据
}

// Response TCP响应消息
type Response struct {
	Cmd     string      `json:"cmd"`
	Seq     string      `json:"seq,omitempty"`
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Handler TCP消息处理器
type Handler func(ctx *Context) error

// Middleware TCP中间件
type Middleware func(Handler) Handler

// ---- 隧道业务消息结构 ----

// AuthData 认证请求
type AuthData struct {
	Token string `json:"token"`
	Name  string `json:"name"` // 节点名称
}

// NewProxyData 注册代理请求
type NewProxyData struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	RemotePort    int      `json:"remote_port"`
	LocalIP       string   `json:"local_ip"`
	LocalPort     int      `json:"local_port"`
	CustomDomains []string `json:"custom_domains,omitempty"`
	HostRewrite   string   `json:"host_rewrite,omitempty"`
	AllowCIDR     []string `json:"allow_cidr,omitempty"`
	DenyCIDR      []string `json:"deny_cidr,omitempty"`
	RateLimit     int64    `json:"rate_limit,omitempty"`
}

// NewProxyResp 注册代理响应
type NewProxyResp struct {
	Name       string `json:"name"`
	RemotePort int    `json:"remote_port"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// StartWorkConnData 请求建立工作连接
type StartWorkConnData struct {
	ProxyName string `json:"proxy_name"`
}

// NewWorkConnData 工作连接注册
type NewWorkConnData struct {
	ProxyName string `json:"proxy_name"`
	ConnID    string `json:"conn_id,omitempty"` // 全局池连接: 控制连接ID
}

// ---- 远程管理指令 ----

// PeerConfigData 对端节点配置（节点→服务端响应）
type PeerConfigData struct {
	PeerAddr  string        `json:"peer_addr"`
	PeerPort  int           `json:"peer_port"`
	PoolCount int           `json:"pool_count"`
	Proxies   []ProxyDetail `json:"proxies"`
}

// ProxyDetail 代理配置详情
type ProxyDetail struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
}

// AddProxyData 下发：添加代理
type AddProxyData struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	LocalIP       string   `json:"local_ip"`
	LocalPort     int      `json:"local_port"`
	RemotePort    int      `json:"remote_port"`
	RemotePortEnd int      `json:"remote_port_end,omitempty"` // F5: 端口范围终点
	LocalBackends []string `json:"local_backends,omitempty"`  // F3: 多后端
	LBStrategy    string   `json:"lb_strategy,omitempty"`     // F3: 负载均衡策略
	ProxyProtocol string   `json:"proxy_protocol,omitempty"`  // F6: v1 / v2
	CustomDomains []string `json:"custom_domains,omitempty"`
	HostRewrite   string   `json:"host_rewrite,omitempty"`
	AllowCIDR     []string `json:"allow_cidr,omitempty"`
	DenyCIDR      []string `json:"deny_cidr,omitempty"`
	RateLimit     int64    `json:"rate_limit,omitempty"`
}

// RemoveProxyData 下发：删除代理
type RemoveProxyData struct {
	Name string `json:"name"`
}

// UpdatePoolData 下发：修改连接池
type UpdatePoolData struct {
	PoolCount int `json:"pool_count"`
}

// ForwardCmdData 下发：转发命令给下游节点
type ForwardCmdData struct {
	TargetID string          `json:"target_id"`      // 目标节点 connID（直接下游）
	Path     []string        `json:"path,omitempty"` // 多跳路径：中继节点 connID 列表
	Cmd      string          `json:"cmd"`            // 要转发的命令
	Data     json.RawMessage `json:"data,omitempty"` // 命令数据
}

// ForwardCmdResp 转发命令响应
type ForwardCmdResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// DownstreamPeer 下游节点信息
type DownstreamPeer struct {
	ConnID  string   `json:"conn_id"`
	Name    string   `json:"name"`
	Proxies []string `json:"proxies"`
}

// ---- VPN 打洞信令 ----

// VPNEndpointData VPN 端点信息（用于打洞信令交换）
type VPNEndpointData struct {
	VirtualIP  string `json:"virtual_ip"`  // 本机虚拟 IP
	PublicAddr string `json:"public_addr"` // STUN 探测到的公网地址 (ip:port)
	ListenPort int    `json:"listen_port"` // VPN UDP 监听端口
}

// VPNPunchData 打洞请求（携带对端信息）
type VPNPunchData struct {
	PeerName   string `json:"peer_name"`   // 目标节点名称
	VirtualIP  string `json:"virtual_ip"`  // 请求端虚拟 IP
	PublicAddr string `json:"public_addr"` // 请求端公网地址
	ListenPort int    `json:"listen_port"` // 请求端 VPN UDP 端口
}
