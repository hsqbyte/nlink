package vpn

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"
)

// STUN message types (RFC 5389)
const (
	stunBindingRequest  = 0x0001
	stunBindingResponse = 0x0101
	stunMagicCookie     = 0x2112A442

	stunAttrMappedAddress    = 0x0001
	stunAttrXORMappedAddress = 0x0020
)

// 公共 STUN 服务器列表
var defaultSTUNServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun2.l.google.com:19302",
	"stun.cloudflare.com:3478",
}

// STUNResult STUN 探测结果
type STUNResult struct {
	PublicAddr *net.UDPAddr // 本机 NAT 映射后的公网地址
	LocalAddr  *net.UDPAddr // 本机本地地址
}

// STUNDiscover 通过已有 UDP 连接探测公网地址（复用 VPN 的 UDP socket）
// 使用已绑定的 conn 确保 STUN 返回的端口就是 VPN 使用的端口
func STUNDiscover(conn *net.UDPConn, timeout time.Duration) (*STUNResult, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	// 随机打乱服务器顺序
	servers := make([]string, len(defaultSTUNServers))
	copy(servers, defaultSTUNServers)
	rand.Shuffle(len(servers), func(i, j int) { servers[i], servers[j] = servers[j], servers[i] })

	var lastErr error
	for _, server := range servers {
		result, err := stunQuery(conn, server, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}
	return nil, fmt.Errorf("所有 STUN 服务器查询失败: %w", lastErr)
}

// stunQuery 向单个 STUN 服务器发送 Binding Request
func stunQuery(conn *net.UDPConn, server string, timeout time.Duration) (*STUNResult, error) {
	addr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return nil, fmt.Errorf("解析 STUN 地址 %s 失败: %w", server, err)
	}

	// 构造 STUN Binding Request
	// Header: Type(2) + Length(2) + Magic Cookie(4) + Transaction ID(12) = 20 bytes
	txID := make([]byte, 12)
	rand.Read(txID)

	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:2], stunBindingRequest)
	binary.BigEndian.PutUint16(req[2:4], 0) // No attributes
	binary.BigEndian.PutUint32(req[4:8], stunMagicCookie)
	copy(req[8:20], txID)

	// 发送
	if _, err := conn.WriteToUDP(req, addr); err != nil {
		return nil, fmt.Errorf("发送 STUN 请求失败: %w", err)
	}

	// 接收
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{}) // 清除 deadline

	buf := make([]byte, 512)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return nil, fmt.Errorf("接收 STUN 响应超时: %w", err)
	}

	if n < 20 {
		return nil, fmt.Errorf("STUN 响应太短: %d bytes", n)
	}

	// 验证响应
	msgType := binary.BigEndian.Uint16(buf[0:2])
	if msgType != stunBindingResponse {
		return nil, fmt.Errorf("非 Binding Response: 0x%04x", msgType)
	}
	cookie := binary.BigEndian.Uint32(buf[4:8])
	if cookie != stunMagicCookie {
		return nil, fmt.Errorf("Magic Cookie 不匹配")
	}
	// 验证 Transaction ID
	for i := 0; i < 12; i++ {
		if buf[8+i] != txID[i] {
			return nil, fmt.Errorf("Transaction ID 不匹配")
		}
	}

	// 解析属性
	attrLen := binary.BigEndian.Uint16(buf[2:4])
	attrs := buf[20 : 20+attrLen]

	publicAddr, err := parseSTUNAddress(attrs, buf[4:8])
	if err != nil {
		return nil, err
	}

	return &STUNResult{
		PublicAddr: publicAddr,
		LocalAddr:  conn.LocalAddr().(*net.UDPAddr),
	}, nil
}

// parseSTUNAddress 解析 STUN 响应中的 MAPPED-ADDRESS 或 XOR-MAPPED-ADDRESS
func parseSTUNAddress(attrs []byte, header []byte) (*net.UDPAddr, error) {
	offset := 0
	for offset+4 <= len(attrs) {
		attrType := binary.BigEndian.Uint16(attrs[offset : offset+2])
		attrLen := binary.BigEndian.Uint16(attrs[offset+2 : offset+4])
		attrValue := attrs[offset+4 : offset+4+int(attrLen)]

		switch attrType {
		case stunAttrXORMappedAddress:
			return parseXORMappedAddress(attrValue, header)
		case stunAttrMappedAddress:
			return parseMappedAddress(attrValue)
		}

		// Attributes are padded to 4-byte boundaries
		offset += 4 + int(attrLen)
		if offset%4 != 0 {
			offset += 4 - (offset % 4)
		}
	}
	return nil, fmt.Errorf("STUN 响应中未找到地址属性")
}

// parseXORMappedAddress 解析 XOR-MAPPED-ADDRESS (RFC 5389)
func parseXORMappedAddress(data []byte, header []byte) (*net.UDPAddr, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("XOR-MAPPED-ADDRESS 数据太短")
	}
	// data[0] = reserved, data[1] = family, data[2:4] = xor'd port, data[4:8] = xor'd IP
	family := data[1]
	if family != 0x01 { // IPv4
		return nil, fmt.Errorf("不支持 IPv6 STUN 地址")
	}

	port := binary.BigEndian.Uint16(data[2:4]) ^ uint16(stunMagicCookie>>16)
	ip := make(net.IP, 4)
	magicBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(magicBytes, stunMagicCookie)
	for i := 0; i < 4; i++ {
		ip[i] = data[4+i] ^ magicBytes[i]
	}

	return &net.UDPAddr{IP: ip, Port: int(port)}, nil
}

// parseMappedAddress 解析 MAPPED-ADDRESS (RFC 5389)
func parseMappedAddress(data []byte) (*net.UDPAddr, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("MAPPED-ADDRESS 数据太短")
	}
	family := data[1]
	if family != 0x01 {
		return nil, fmt.Errorf("不支持 IPv6 STUN 地址")
	}

	port := binary.BigEndian.Uint16(data[2:4])
	ip := net.IP(data[4:8])

	return &net.UDPAddr{IP: ip, Port: int(port)}, nil
}
