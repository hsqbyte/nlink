package services

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsqbyte/nlink/src/core/tcp"
)

// TunnelService 隧道服务 —— 所有跨节点代理 / 远程指令 / 连接池 / 上游管理的中枢。
//
// 为保持文件可读性，具体职责按职责拆分到同包内多个文件：
//   - peer_registry.go   : 对端名称 / 延迟 / 断开记录 / ListPeers
//   - peer_pool.go       : peerPool 结构与并发安全语义
//   - proxy_manager.go   : 代理注册 / 删除 / CloseAll / ListProxies / ServerStats
//   - tcp_proxy.go       : TCPProxy 结构、监听 & 双向转发
//   - upstream.go        : 上游连接管理
//   - remote_command.go  : RPC over TCP（SendCommandToPeer / 延迟探测 / 远程代理操作）
type TunnelService struct {
	mu      sync.RWMutex
	proxies map[string]*TCPProxy

	// 对端连接映射: connID -> 代理名列表
	peerProxies map[string][]string

	// 对端名称映射
	peerNames    map[string]string // connID -> peerName
	nameToConnID map[string]string // peerName -> connID

	// 对端全局连接池: connID -> *peerPool
	peerPools map[string]*peerPool

	// 已断开的对端（保留供 Dashboard 显示）
	disconnectedPeers map[string]*DisconnectedPeer // key: peerName

	// 对端延迟 (ms)
	peerLatencies map[string]int64 // connID -> latency ms

	// 上游连接（本节点主动连接的对端）
	upstreamMu    sync.RWMutex
	upstreamPeers map[string]*UpstreamPeer // key: addr:port

	tcpServer       *tcp.Server
	workConnTimeout time.Duration
	startTime       time.Time

	// 远程指令: 等待对端回复
	pendingMu       sync.Mutex
	pendingRequests map[string]chan *tcp.Response // key: connID:seq
	seqCounter      atomic.Uint64
}

var tunnelSvc *TunnelService

// InitTunnelService 初始化隧道服务并启动延迟探测
func InitTunnelService(tcpServer *tcp.Server, workConnTimeout int) {
	tunnelSvc = newTunnelService(tcpServer, workConnTimeout)
	tunnelSvc.StartLatencyProbe()
}

// GetTunnelService 获取隧道服务单例
func GetTunnelService() *TunnelService {
	return tunnelSvc
}

// EnsureTunnelService 确保隧道服务已初始化（Dashboard-only 场景）
func EnsureTunnelService() {
	if tunnelSvc != nil {
		return
	}
	tunnelSvc = newTunnelService(nil, 0)
}

// newTunnelService 构造空的 TunnelService
func newTunnelService(tcpServer *tcp.Server, workConnTimeout int) *TunnelService {
	return &TunnelService{
		proxies:           make(map[string]*TCPProxy),
		peerProxies:       make(map[string][]string),
		peerNames:         make(map[string]string),
		nameToConnID:      make(map[string]string),
		peerPools:         make(map[string]*peerPool),
		disconnectedPeers: make(map[string]*DisconnectedPeer),
		peerLatencies:     make(map[string]int64),
		upstreamPeers:     make(map[string]*UpstreamPeer),
		tcpServer:         tcpServer,
		workConnTimeout:   time.Duration(workConnTimeout) * time.Second,
		startTime:         time.Now(),
		pendingRequests:   make(map[string]chan *tcp.Response),
	}
}

// uptime 返回运行时长（供 ServerStats 等使用）
func (ts *TunnelService) uptime() time.Duration {
	return time.Since(ts.startTime)
}
