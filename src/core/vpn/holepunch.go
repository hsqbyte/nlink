package vpn

import (
	"fmt"
	"net"
	"time"

	"github.com/fastgox/utils/logger"
)

const (
	punchAttempts  = 10              // 打洞尝试次数
	punchInterval  = 200 * time.Millisecond // 每次发送间隔
	punchTimeout   = 5 * time.Second        // 总超时
)

// PunchResult 打洞结果
type PunchResult struct {
	Success  bool
	PeerAddr *net.UDPAddr // 对端真实地址
}

// HolePunch 尝试 UDP 打洞
// transport: 本地 VPN 的 UDP 传输层（复用同一个 socket）
// peerPublicAddr: 对端的公网地址 (STUN 探测到的)
// peerVirtualIP: 对端的虚拟 IP
func HolePunch(transport *UDPTransport, peerPublicAddr string, peerVirtualIP string) (*PunchResult, error) {
	addr, err := net.ResolveUDPAddr("udp4", peerPublicAddr)
	if err != nil {
		return nil, fmt.Errorf("解析对端地址失败: %w", err)
	}

	vip := net.ParseIP(peerVirtualIP)
	if vip == nil {
		return nil, fmt.Errorf("无效虚拟 IP: %s", peerVirtualIP)
	}

	logger.Info("[VPN] 开始打洞: %s -> %s (VIP: %s)", transport.conn.LocalAddr(), peerPublicAddr, peerVirtualIP)

	// 构造一个小的探测包（伪造一个最小 IP 头，只为了能通过解密验证和自动注册）
	// 使用本地虚拟 IP 作为源，对端虚拟 IP 作为目标
	probePacket := makePunchProbe(transport.localIP, vip)

	// 先注册对端（用公网地址），这样收到回应时能识别
	transport.AddPeer(vip, addr)

	// 连续发送多个打洞包
	for i := 0; i < punchAttempts; i++ {
		encrypted, err := transport.encrypt(probePacket)
		if err != nil {
			continue
		}
		transport.conn.WriteToUDP(encrypted, addr)
		time.Sleep(punchInterval)
	}

	// 检查是否收到对端的包（endpoint 会被 updatePeerEndpoint 自动更新）
	time.Sleep(punchTimeout - time.Duration(punchAttempts)*punchInterval)

	peer, ok := transport.GetPeer(vip)
	if ok && peer.LastSeen.After(time.Now().Add(-punchTimeout)) {
		logger.Info("[VPN] 打洞成功: %s -> %s", peerVirtualIP, peer.Endpoint)
		return &PunchResult{Success: true, PeerAddr: peer.Endpoint}, nil
	}

	logger.Warn("[VPN] 打洞失败: %s (对端 %s 无响应)", peerVirtualIP, peerPublicAddr)
	return &PunchResult{Success: false}, nil
}

// makePunchProbe 构造一个最小的 IPv4 探测包
// 20 字节 IP 头，无 payload
func makePunchProbe(srcIP, dstIP net.IP) []byte {
	src := srcIP.To4()
	dst := dstIP.To4()
	if src == nil || dst == nil {
		return nil
	}

	packet := make([]byte, 20)
	packet[0] = 0x45       // Version=4, IHL=5 (20 bytes)
	packet[1] = 0          // DSCP/ECN
	packet[2] = 0          // Total length = 20
	packet[3] = 20
	packet[4] = 0          // Identification
	packet[5] = 0
	packet[6] = 0x40       // Flags: Don't Fragment
	packet[7] = 0
	packet[8] = 64         // TTL
	packet[9] = 0          // Protocol: reserved (probe only)
	// packet[10:12] = checksum (skip for probe)
	copy(packet[12:16], src)
	copy(packet[16:20], dst)

	return packet
}
