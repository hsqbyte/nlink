package vpn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/fastgox/utils/logger"
	"golang.org/x/crypto/hkdf"
)

// UDPPeer 表示一个虚拟局域网中的对端节点
type UDPPeer struct {
	VirtualIP net.IP       // 对端虚拟 IP
	Endpoint  *net.UDPAddr // 对端真实 UDP 地址
	LastSeen  time.Time    // 最后活跃时间
}

// UDPTransport UDP 加密传输层
type UDPTransport struct {
	conn  *net.UDPConn
	aead  cipher.AEAD
	peers sync.Map // virtualIP(string) -> *UDPPeer

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

	return &UDPTransport{
		conn:    conn,
		aead:    aead,
		localIP: localVirtualIP,
	}, nil
}

// AddPeer 注册一个对端节点
func (t *UDPTransport) AddPeer(virtualIP net.IP, endpoint *net.UDPAddr) {
	key := virtualIP.String()
	t.peers.Store(key, &UDPPeer{
		VirtualIP: virtualIP,
		Endpoint:  endpoint,
		LastSeen:  time.Now(),
	})
	logger.Info("[VPN] 添加对端: %s -> %s", key, endpoint.String())
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

// SendTo 加密并发送 IP 包到指定对端
func (t *UDPTransport) SendTo(packet []byte, dstVirtualIP net.IP) error {
	peer, ok := t.GetPeer(dstVirtualIP)
	if !ok {
		return fmt.Errorf("未知对端: %s", dstVirtualIP.String())
	}

	encrypted, err := t.encrypt(packet)
	if err != nil {
		return fmt.Errorf("加密失败: %w", err)
	}

	_, err = t.conn.WriteToUDP(encrypted, peer.Endpoint)
	return err
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

	// 更新对端的最后活跃时间和地址（支持漫游）
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
func (t *UDPTransport) encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, t.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
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
