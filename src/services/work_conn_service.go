package services

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src/core/tcp"
)

// WorkConnListener 工作连接监听器
type WorkConnListener struct {
	listener net.Listener
	tunnel   *TunnelService
}

func NewWorkConnListener(port int, tunnel *TunnelService) (*WorkConnListener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	return &WorkConnListener{
		listener: ln,
		tunnel:   tunnel,
	}, nil
}

func (w *WorkConnListener) Start() {
	logger.Info("[WorkConn] 工作连接监听: %s", w.listener.Addr())
	for {
		conn, err := w.listener.Accept()
		if err != nil {
			logger.Error("[WorkConn] accept: %v", err)
			return
		}
		go w.handleWorkConn(conn)
	}
}

func (w *WorkConnListener) handleWorkConn(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	header := make([]byte, 4)
	if _, err := readFull(conn, header); err != nil {
		logger.Error("[WorkConn] 读取头部失败: %v", err)
		conn.Close()
		return
	}

	msgLen := int(binary.BigEndian.Uint32(header))
	if msgLen <= 0 || msgLen > 4096 {
		logger.Error("[WorkConn] 消息长度无效: %d", msgLen)
		conn.Close()
		return
	}

	body := make([]byte, msgLen)
	if _, err := readFull(conn, body); err != nil {
		logger.Error("[WorkConn] 读取消息失败: %v", err)
		conn.Close()
		return
	}

	conn.SetReadDeadline(time.Time{})

	var msg struct {
		Cmd  string              `json:"cmd"`
		Data tcp.NewWorkConnData `json:"data"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		logger.Error("[WorkConn] JSON解析失败: %v", err)
		conn.Close()
		return
	}

	if msg.Cmd != "new_work_conn" {
		logger.Warn("[WorkConn] 非法命令: %s", msg.Cmd)
		conn.Close()
		return
	}

	// 全局池连接: ConnID 非空且 ProxyName 为空
	if msg.Data.ConnID != "" && msg.Data.ProxyName == "" {
		if !w.tunnel.DeliverPoolConn(msg.Data.ConnID, conn) {
			logger.Warn("[WorkConn] 全局池投递失败: connID=%s", msg.Data.ConnID)
			conn.Close()
			return
		}
		logger.Info("[WorkConn] 全局池已投递: connID=%s", msg.Data.ConnID)
		return
	}

	// 按需连接: ProxyName 非空
	if !w.tunnel.DeliverWorkConn(msg.Data.ProxyName, conn) {
		logger.Warn("[WorkConn] 代理不存在: %s", msg.Data.ProxyName)
		conn.Close()
		return
	}

	logger.Info("[WorkConn] 已投递: proxy=%s", msg.Data.ProxyName)
}

func (w *WorkConnListener) Stop() error {
	return w.listener.Close()
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
