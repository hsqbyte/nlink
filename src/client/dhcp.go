package client

import (
	"encoding/json"
	"fmt"
	"time"

	modelConfig "github.com/hsqbyte/nlink/src/models/config"
)

// RequestDHCP 向对端请求 VPN DHCP 分配虚拟 IP。
// 创建一次性控制连接：auth → vpn_dhcp_request → 关闭，返回 CIDR。
// 用于 vpn.virtual_ip 为空或 "auto" 时的启动引导。
func RequestDHCP(nodeName, hint string, peer *modelConfig.PeerConfig) (string, error) {
	c := &Client{
		cfg:      peer,
		nodeName: nodeName,
	}
	addr := fmt.Sprintf("%s:%d", peer.Addr, peer.Port)
	conn, err := c.dialPeer(addr, 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("DHCP 连接失败: %w", err)
	}
	defer conn.Close()
	c.conn = conn

	if err := c.auth(); err != nil {
		return "", fmt.Errorf("DHCP 认证失败: %w", err)
	}

	conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetDeadline(time.Time{})

	if err := c.sendMsg("vpn_dhcp_request", map[string]interface{}{
		"node_name": nodeName,
		"hint":      hint,
	}); err != nil {
		return "", fmt.Errorf("DHCP 请求发送失败: %w", err)
	}
	resp, err := c.readMsg()
	if err != nil {
		return "", fmt.Errorf("DHCP 响应读取失败: %w", err)
	}
	if resp.Cmd != "vpn_dhcp_request" {
		return "", fmt.Errorf("DHCP 非预期响应 cmd=%s", resp.Cmd)
	}
	raw, _ := json.Marshal(resp.Data)
	var data struct {
		VirtualIP string `json:"virtual_ip"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", fmt.Errorf("DHCP 解析失败: %w", err)
	}
	if data.Error != "" {
		return "", fmt.Errorf("DHCP 服务端错误: %s", data.Error)
	}
	if data.VirtualIP == "" {
		return "", fmt.Errorf("DHCP 返回空 IP")
	}
	return data.VirtualIP, nil
}
