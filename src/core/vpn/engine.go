package vpn

import (
	"fmt"
	"net"
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

// DiscoverPublicAddr 通过 STUN 探测本机公网地址
func (e *Engine) DiscoverPublicAddr() (*STUNResult, error) {
	return STUNDiscover(e.transport.Conn(), 3*time.Second)
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

			// 只转发目标在 VPN 子网内的包
			if !e.subnet.Contains(dstIP) {
				continue
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
