package vpn

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fastgox/utils/logger"
	modelConfig "github.com/hsqbyte/nlink/src/models/config"
)

// Engine 虚拟局域网引擎，串联 TUN 设备和 UDP 传输
type Engine struct {
	tun       *TunDevice
	transport *UDPTransport
	config    *modelConfig.VPNConfig
	token     string
	subnet    *net.IPNet // VPN 子网，用于过滤非本网段流量

	cachedPublicAddr string
	publicAddrMu     sync.RWMutex

	stopOnce sync.Once
	stopCh   chan struct{}
}

// 全局 VPN 引擎实例
var globalEngine *Engine

// SetGlobalEngine 设置全局 VPN 引擎
func SetGlobalEngine(e *Engine) {
	globalEngine = e
}

// GetGlobalEngine 获取全局 VPN 引擎
func GetGlobalEngine() *Engine {
	return globalEngine
}

// NewEngine 创建 VPN 引擎
func NewEngine(cfg *modelConfig.VPNConfig, token string) (*Engine, error) {
	if cfg == nil || !cfg.IsEnabled() {
		return nil, fmt.Errorf("VPN 未启用")
	}

	mtu := cfg.MTU
	if mtu <= 0 {
		mtu = defaultMTU
	}

	// 创建 TUN 设备
	tunDev, err := NewTunDevice(cfg.VirtualIP, mtu)
	if err != nil {
		return nil, fmt.Errorf("创建 TUN 设备失败: %w", err)
	}

	// 解析本机虚拟 IP
	ip, subnet, err := net.ParseCIDR(cfg.VirtualIP)
	if err != nil {
		tunDev.Close()
		return nil, fmt.Errorf("解析虚拟 IP 失败: %w", err)
	}

	// 创建 UDP 传输层
	transport, err := NewUDPTransport(cfg.ListenPort, token, ip.To4())
	if err != nil {
		tunDev.Close()
		return nil, fmt.Errorf("创建 UDP 传输失败: %w", err)
	}

	return &Engine{
		tun:       tunDev,
		transport: transport,
		config:    cfg,
		token:     token,
		subnet:    subnet,
		stopCh:    make(chan struct{}),
	}, nil
}

// Start 启动 VPN 引擎（TUN↔UDP 双向转发）
func (e *Engine) Start() {
	logger.Info("[VPN] 引擎启动: TUN=%s, VirtualIP=%s, UDP=:%d",
		e.tun.Name(), e.config.VirtualIP, e.config.ListenPort)

	go e.tunToUDP()
	go e.udpToTUN()
	go e.refreshPublicAddr()
	e.StartProbeLoop(30 * time.Second)
}

// Stop 停止 VPN 引擎
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopCh)
		e.tun.Close()
		e.transport.Close()
		logger.Info("[VPN] 引擎已停止")
	})
}

// refreshPublicAddr 定期刷新公网地址缓存
func (e *Engine) refreshPublicAddr() {
	// 立即探测一次
	e.DiscoverPublicAddr()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.DiscoverPublicAddr()
		case <-e.stopCh:
			return
		}
	}
}

// AddPeer 添加对端节点
func (e *Engine) AddPeer(virtualIP string, endpoint string) error {
	vip := net.ParseIP(virtualIP)
	if vip == nil {
		return fmt.Errorf("无效虚拟 IP: %s", virtualIP)
	}

	addr, err := net.ResolveUDPAddr("udp4", endpoint)
	if err != nil {
		return fmt.Errorf("解析对端地址 %s 失败: %w", endpoint, err)
	}

	e.transport.AddPeer(vip, addr)
	return nil
}

// AddPeerWithPolicy 添加对端并配置子网路由 / ACL
func (e *Engine) AddPeerWithPolicy(virtualIP, endpoint string, routesCIDR, allowCIDR, denyCIDR []string) error {
	vip := net.ParseIP(virtualIP)
	if vip == nil {
		return fmt.Errorf("无效虚拟 IP: %s", virtualIP)
	}
	addr, err := net.ResolveUDPAddr("udp4", endpoint)
	if err != nil {
		return fmt.Errorf("解析对端地址 %s 失败: %w", endpoint, err)
	}
	parse := func(list []string) ([]*net.IPNet, error) {
		out := make([]*net.IPNet, 0, len(list))
		for _, s := range list {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			// 支持单 IP：补 /32
			if !strings.Contains(s, "/") {
				s = s + "/32"
			}
			_, ipnet, err := net.ParseCIDR(s)
			if err != nil {
				return nil, fmt.Errorf("无效 CIDR %s: %w", s, err)
			}
			out = append(out, ipnet)
		}
		return out, nil
	}
	routes, err := parse(routesCIDR)
	if err != nil {
		return err
	}
	allow, err := parse(allowCIDR)
	if err != nil {
		return err
	}
	deny, err := parse(denyCIDR)
	if err != nil {
		return err
	}
	e.transport.AddPeerWithOptions(vip, addr, routes, allow, deny)
	return nil
}

// DiscoverPublicAddr 通过 STUN 探测本机公网地址
func (e *Engine) DiscoverPublicAddr() (*STUNResult, error) {
	result, err := STUNDiscover(e.transport.Conn(), 3*time.Second)
	if err == nil && result.PublicAddr != nil {
		e.publicAddrMu.Lock()
		e.cachedPublicAddr = result.PublicAddr.String()
		e.publicAddrMu.Unlock()
	}
	return result, err
}

// CachedPublicAddr 返回缓存的公网地址（非阻塞）
func (e *Engine) CachedPublicAddr() string {
	e.publicAddrMu.RLock()
	defer e.publicAddrMu.RUnlock()
	return e.cachedPublicAddr
}

// PunchPeer 尝试对指定对端进行 UDP 打洞
func (e *Engine) PunchPeer(peerPublicAddr string, peerVirtualIP string) (*PunchResult, error) {
	return HolePunch(e.transport, peerPublicAddr, peerVirtualIP)
}

// Transport 返回底层 UDP 传输层
func (e *Engine) Transport() *UDPTransport {
	return e.transport
}

// Config 返回 VPN 配置
func (e *Engine) Config() *modelConfig.VPNConfig {
	return e.config
}

// VirtualIP 返回本节点的虚拟 IP 字符串 (CIDR)
func (e *Engine) VirtualIP() string {
	return e.config.VirtualIP
}

// tunToUDP 从 TUN 读取 IP 包 → 加密 → UDP 发送到对端
func (e *Engine) tunToUDP() {
	batchSize := e.tun.BatchSize()
	bufs := make([][]byte, batchSize)
	const offset = TunPacketOffset
	for i := range bufs {
		bufs[i] = make([]byte, e.config.MTU+offset)
	}
	sizes := make([]int, batchSize)

	for {
		select {
		case <-e.stopCh:
			return
		default:
		}

		n, err := e.tun.Read(bufs, sizes, offset)
		if err != nil {
			select {
			case <-e.stopCh:
				return
			default:
				logger.Error("[VPN] TUN 读取失败: %v", err)
				continue
			}
		}

		for i := 0; i < n; i++ {
			packet := bufs[i][offset : offset+sizes[i]]
			if len(packet) < 20 {
				continue
			}

			// 提取源和目标 IP（IPv4 头）
			srcIP := net.IP(packet[12:16])
			dstIP := net.IP(packet[16:20])

			// 跳过发给自己的包
			if dstIP.Equal(e.transport.localIP) {
				continue
			}

			// 跳过组播/广播
			if dstIP[0] >= 224 {
				continue
			}

			// 目标在 VPN 子网内 -> 直接发
			// 否则查询路由表，看是否有对端宣告了包含该 IP 的路由
			if !e.subnet.Contains(dstIP) {
				if _, ok := e.transport.LookupPeerForDst(dstIP); !ok {
					continue
				}
			}

			logger.Debug("[VPN] TUN->UDP: %s -> %s (%d bytes)", srcIP, dstIP, sizes[i])
			if err := e.transport.SendTo(packet, dstIP); err != nil {
				logger.Error("[VPN] 发送失败 -> %s: %v", dstIP.String(), err)
			}
		}
	}
}

// udpToTUN 从 UDP 接收加密包 → 解密 → 写入 TUN
func (e *Engine) udpToTUN() {
	buf := make([]byte, e.config.MTU+100)
	const offset = TunPacketOffset

	for {
		select {
		case <-e.stopCh:
			return
		default:
		}

		n, remoteAddr, err := e.transport.RecvFrom(buf)
		if err != nil {
			select {
			case <-e.stopCh:
				return
			default:
				logger.Debug("[VPN] UDP 接收失败: %v", err)
				continue
			}
		}

		if n == 0 {
			continue
		}

		// 控制帧（RTT 探测）：首字节 0xFE=请求，0xFF=响应
		if n >= 1 && (buf[0] == 0xFE || buf[0] == 0xFF) {
			e.handleProbeFrame(buf[:n], remoteAddr)
			continue
		}

		if n >= 20 {
			srcIP := net.IP(buf[12:16])
			dstIP := net.IP(buf[16:20])
			logger.Debug("[VPN] UDP->TUN: %s -> %s (%d bytes, from %v)", srcIP, dstIP, n, remoteAddr)
		}

		// 写入 TUN 设备，需要加上 offset
		writeBuf := make([]byte, offset+n)
		copy(writeBuf[offset:], buf[:n])

		bufs := [][]byte{writeBuf}
		if _, err := e.tun.Write(bufs, offset); err != nil {
			logger.Error("[VPN] TUN 写入失败: %v", err)
		}
	}
}

// ---- 控制帧：RTT 探测 ----
// 格式: [kind:1][senderVIP:4][timestampNs:8] = 13 bytes
// kind: 0xFE = 请求；0xFF = 响应（回显 timestamp）

const (
	probeKindRequest  byte = 0xFE
	probeKindResponse byte = 0xFF
	probeFrameLen          = 13
)

// ProbePeer 向指定对端发送 RTT 探测请求
func (e *Engine) ProbePeer(virtualIP string) error {
	vip := net.ParseIP(virtualIP).To4()
	if vip == nil {
		return fmt.Errorf("无效虚拟 IP: %s", virtualIP)
	}
	peer, ok := e.transport.GetPeer(vip)
	if !ok {
		return fmt.Errorf("未知对端: %s", virtualIP)
	}
	myVIP := e.transport.localIP.To4()
	if myVIP == nil {
		return fmt.Errorf("本节点 VIP 无效")
	}
	frame := make([]byte, probeFrameLen)
	frame[0] = probeKindRequest
	copy(frame[1:5], myVIP)
	ts := time.Now().UnixNano()
	for i := 0; i < 8; i++ {
		frame[5+i] = byte(ts >> (56 - 8*i))
	}
	enc, err := e.transport.encrypt(frame)
	if err != nil {
		return err
	}
	_, err = e.transport.conn.WriteToUDP(enc, peer.Endpoint)
	return err
}

// handleProbeFrame 处理收到的探测帧
func (e *Engine) handleProbeFrame(frame []byte, remoteAddr *net.UDPAddr) {
	if len(frame) < probeFrameLen {
		return
	}
	kind := frame[0]
	senderVIP := net.IP(frame[1:5])
	var ts int64
	for i := 0; i < 8; i++ {
		ts = (ts << 8) | int64(frame[5+i])
	}

	switch kind {
	case probeKindRequest:
		// 回显
		resp := make([]byte, probeFrameLen)
		resp[0] = probeKindResponse
		copy(resp[1:5], senderVIP)
		copy(resp[5:13], frame[5:13])
		enc, err := e.transport.encrypt(resp)
		if err != nil {
			return
		}
		e.transport.conn.WriteToUDP(enc, remoteAddr)
	case probeKindResponse:
		rtt := time.Now().UnixNano() - ts
		if rtt < 0 {
			return
		}
		if v, ok := e.transport.peers.Load(senderVIP.String()); ok {
			peer := v.(*UDPPeer)
			peer.LastRTTNs.Store(rtt)
		}
	}
}

// StartProbeLoop 周期性对所有对端做 RTT 探测
func (e *Engine) StartProbeLoop(interval time.Duration) {
	go func() {
		if interval <= 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-e.stopCh:
				return
			case <-ticker.C:
				for _, p := range e.transport.ListPeers() {
					_ = e.ProbePeer(p.VirtualIP.String())
				}
			}
		}
	}()
}
