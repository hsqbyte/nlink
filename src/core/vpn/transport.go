package vpn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fastgox/utils/logger"
	"golang.org/x/crypto/hkdf"
)

// UDPPeer 表示一个虚拟局域网中的对端节点
type UDPPeer struct {
	VirtualIP net.IP       // 对端虚拟 IP
	Endpoint  *net.UDPAddr // 对端真实 UDP 地址
	LastSeen  time.Time    // 最后活跃时间

	// 子网路由（除 VirtualIP 外，还把这些 CIDR 的流量交给该对端）
	Routes []*net.IPNet

	// ACL：allow 非空则仅允许匹配的源/目标；deny 仅在 allow 为空时生效
	AllowNets []*net.IPNet
	DenyNets  []*net.IPNet

	// 指标
	RxBytes   atomic.Uint64
	TxBytes   atomic.Uint64
	RxPackets atomic.Uint64
	TxPackets atomic.Uint64
	RxDropped atomic.Uint64 // 解密失败 / ACL 拒绝
	TxDropped atomic.Uint64
	LastRTTNs atomic.Int64 // 最近一次探测 RTT (ns)，0 表示未知
}

// PeerACLAllowsPacket 判断 IP 包的源/目标是否被对端 ACL 允许
// Allow 列表非空 => 源或目标必须匹配一条 allow
// Allow 为空 & deny 非空 => 若匹配 deny 则拒绝
func (p *UDPPeer) PeerACLAllowsPacket(src, dst net.IP) bool {
	if len(p.AllowNets) == 0 && len(p.DenyNets) == 0 {
		return true
	}
	match := func(nets []*net.IPNet, ip net.IP) bool {
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if len(p.AllowNets) > 0 {
		if match(p.AllowNets, src) || match(p.AllowNets, dst) {
			return true
		}
		return false
	}
	// allow 为空，有 deny
	if match(p.DenyNets, src) || match(p.DenyNets, dst) {
		return false
	}
	return true
}

// UDPTransport UDP 加密传输层
//
// nonce 构造: 8 字节随机前缀(实例级) + 4 字节原子 counter，
// 保证同实例 nonce 绝不重复，避免高丢包重传下的生日碰撞。
type UDPTransport struct {
	conn    *net.UDPConn
	aead    cipher.AEAD
	peers   sync.Map // virtualIP(string) -> *UDPPeer
	prefix  [8]byte
	counter atomic.Uint64

	localIP net.IP // 本节点虚拟 IP
}

// NewUDPTransport 创建 UDP 传输层
func NewUDPTransport(listenPort int, token string, localVirtualIP net.IP) (*UDPTransport, error) {
	// 从 token 派生 AES-256 密钥（与 TCP 控制通道相同算法）
	hkdfReader := hkdf.New(sha256.New, []byte(token), []byte("nlink-vpn-salt"), []byte("nlink-vpn-aes-gcm"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("密钥派生失败: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("AES初始化失败: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM初始化失败: %w", err)
	}

	addr := &net.UDPAddr{Port: listenPort}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("UDP监听 :%d 失败: %w", listenPort, err)
	}

	t := &UDPTransport{
		conn:    conn,
		aead:    aead,
		localIP: localVirtualIP,
	}
	if _, err := io.ReadFull(rand.Reader, t.prefix[:]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("生成nonce前缀失败: %w", err)
	}
	return t, nil
}

// AddPeer 注册一个对端节点（保留简单版本，兼容旧代码）
func (t *UDPTransport) AddPeer(virtualIP net.IP, endpoint *net.UDPAddr) {
	t.AddPeerWithOptions(virtualIP, endpoint, nil, nil, nil)
}

// AddPeerWithOptions 注册对端节点并配置 routes / ACL
func (t *UDPTransport) AddPeerWithOptions(virtualIP net.IP, endpoint *net.UDPAddr, routes, allow, deny []*net.IPNet) {
	key := virtualIP.String()
	if existing, ok := t.peers.Load(key); ok {
		// 更新现有对端的地址 + 策略，保留指标
		p := existing.(*UDPPeer)
		p.Endpoint = endpoint
		p.LastSeen = time.Now()
		p.Routes = routes
		p.AllowNets = allow
		p.DenyNets = deny
		logger.Info("[VPN] 更新对端: %s -> %s (routes=%d)", key, endpoint.String(), len(routes))
		return
	}
	peer := &UDPPeer{
		VirtualIP: virtualIP,
		Endpoint:  endpoint,
		LastSeen:  time.Now(),
		Routes:    routes,
		AllowNets: allow,
		DenyNets:  deny,
	}
	t.peers.Store(key, peer)
	logger.Info("[VPN] 添加对端: %s -> %s (routes=%d allow=%d deny=%d)", key, endpoint.String(), len(routes), len(allow), len(deny))
}

// LookupPeerForDst 按目标 IP 查找对端：先看直连 VirtualIP，再遍历 routes
func (t *UDPTransport) LookupPeerForDst(dst net.IP) (*UDPPeer, bool) {
	if p, ok := t.GetPeer(dst); ok {
		return p, true
	}
	var matched *UDPPeer
	var matchedPrefix int = -1
	t.peers.Range(func(_, v interface{}) bool {
		p := v.(*UDPPeer)
		for _, n := range p.Routes {
			if n.Contains(dst) {
				ones, _ := n.Mask.Size()
				if ones > matchedPrefix {
					matchedPrefix = ones
					matched = p
				}
			}
		}
		return true
	})
	return matched, matched != nil
}

// RemovePeer 移除一个对端节点
func (t *UDPTransport) RemovePeer(virtualIP net.IP) {
	t.peers.Delete(virtualIP.String())
}

// GetPeer 根据虚拟 IP 获取对端信息
func (t *UDPTransport) GetPeer(virtualIP net.IP) (*UDPPeer, bool) {
	v, ok := t.peers.Load(virtualIP.String())
	if !ok {
		return nil, false
	}
	return v.(*UDPPeer), true
}

// ListPeers 返回所有已知对端
func (t *UDPTransport) ListPeers() []*UDPPeer {
	var result []*UDPPeer
	t.peers.Range(func(_, v interface{}) bool {
		result = append(result, v.(*UDPPeer))
		return true
	})
	return result
}

// SendTo 加密并发送 IP 包到指定对端
// 支持按路由表匹配：如果 dstVirtualIP 不是任何对端的 VirtualIP，
// 则查找 Routes 中包含该 IP 的对端。
func (t *UDPTransport) SendTo(packet []byte, dstVirtualIP net.IP) error {
	peer, ok := t.LookupPeerForDst(dstVirtualIP)
	if !ok {
		return fmt.Errorf("未知对端: %s", dstVirtualIP.String())
	}

	// ACL 过滤（按包的源/目标 IP）
	if len(packet) >= 20 {
		srcIP := net.IP(packet[12:16])
		dstIP := net.IP(packet[16:20])
		if !peer.PeerACLAllowsPacket(srcIP, dstIP) {
			peer.TxDropped.Add(1)
			return fmt.Errorf("ACL 拒绝: src=%s dst=%s peer=%s", srcIP, dstIP, peer.VirtualIP)
		}
	}

	encrypted, err := t.encrypt(packet)
	if err != nil {
		peer.TxDropped.Add(1)
		return fmt.Errorf("加密失败: %w", err)
	}

	n, err := t.conn.WriteToUDP(encrypted, peer.Endpoint)
	if err != nil {
		peer.TxDropped.Add(1)
		return err
	}
	peer.TxBytes.Add(uint64(n))
	peer.TxPackets.Add(1)
	return nil
}

// RecvFrom 从 UDP 接收并解密一个 IP 包，返回明文和来源地址
func (t *UDPTransport) RecvFrom(buf []byte) (int, *net.UDPAddr, error) {
	// 临时缓冲区接收加密数据
	encBuf := make([]byte, len(buf)+t.aead.NonceSize()+t.aead.Overhead())
	n, addr, err := t.conn.ReadFromUDP(encBuf)
	if err != nil {
		return 0, nil, err
	}

	plaintext, err := t.decrypt(encBuf[:n])
	if err != nil {
		return 0, addr, fmt.Errorf("解密失败 (from %s): %w", addr.String(), err)
	}

	copy(buf, plaintext)

	// 更新对端的最后活跃时间和地址（支持漫游）+ 指标 + ACL
	if len(plaintext) >= 20 {
		srcIP := net.IP(plaintext[12:16])
		dstIP := net.IP(plaintext[16:20])
		if v, ok := t.peers.Load(srcIP.String()); ok {
			peer := v.(*UDPPeer)
			if !peer.PeerACLAllowsPacket(srcIP, dstIP) {
				peer.RxDropped.Add(1)
				return 0, addr, fmt.Errorf("ACL 拒绝 src=%s dst=%s", srcIP, dstIP)
			}
			peer.RxBytes.Add(uint64(n))
			peer.RxPackets.Add(1)
		}
	}
	t.updatePeerEndpoint(addr, plaintext)

	return len(plaintext), addr, nil
}

// Close 关闭 UDP 连接
func (t *UDPTransport) Close() error {
	return t.conn.Close()
}

// Conn 返回底层 UDP 连接（用于 STUN 探测）
func (t *UDPTransport) Conn() *net.UDPConn {
	return t.conn
}

// encrypt 使用 AES-256-GCM 加密，返回 nonce + ciphertext
// nonce = prefix(8B) + counter(4B)，保证同实例不重复
func (t *UDPTransport) encrypt(plaintext []byte) ([]byte, error) {
	n := t.counter.Add(1)
	if n > (1<<32)-1 {
		return nil, fmt.Errorf("nonce计数器溢出")
	}
	nonce := make([]byte, t.aead.NonceSize())
	copy(nonce[:8], t.prefix[:])
	binary.BigEndian.PutUint32(nonce[8:12], uint32(n))
	return t.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt 解密 nonce + ciphertext
func (t *UDPTransport) decrypt(data []byte) ([]byte, error) {
	nonceSize := t.aead.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("数据太短")
	}
	return t.aead.Open(nil, data[:nonceSize], data[nonceSize:], nil)
}

// updatePeerEndpoint 根据收到的包更新或自动注册对端（支持 NAT 漫游和自动发现）
func (t *UDPTransport) updatePeerEndpoint(addr *net.UDPAddr, packet []byte) {
	if len(packet) < 20 {
		return
	}
	// IPv4 源地址在偏移 12-16 字节
	srcIP := net.IP(packet[12:16])
	key := srcIP.String()

	v, ok := t.peers.Load(key)
	if ok {
		// 已知对端：更新地址和活跃时间
		peer := v.(*UDPPeer)
		peer.Endpoint = addr
		peer.LastSeen = time.Now()
	} else {
		// 未知对端但能解密 = 持有相同 token，自动注册
		t.peers.Store(key, &UDPPeer{
			VirtualIP: srcIP,
			Endpoint:  addr,
			LastSeen:  time.Now(),
		})
		logger.Info("[VPN] 自动发现对端: %s -> %s", key, addr.String())
	}
}
