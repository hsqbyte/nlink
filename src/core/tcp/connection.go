package tcp

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/fastgox/utils/logger"
	"github.com/panjf2000/gnet/v2"
)

// ConnManager 连接管理器
type ConnManager struct {
	conns   sync.Map // connID -> gnet.Conn
	count   atomic.Int64
	counter atomic.Uint64 // 自增ID生成
}

// NewConnManager 创建连接管理器
func NewConnManager() *ConnManager {
	return &ConnManager{}
}

// Add 添加连接，返回连接ID
func (cm *ConnManager) Add(conn gnet.Conn) string {
	id := cm.counter.Add(1)
	connID := connIDFromUint64(id)
	cm.conns.Store(connID, conn)
	cm.count.Add(1)
	conn.SetContext(connID) // 将connID存到gnet.Conn上下文
	return connID
}

// Remove 移除连接
func (cm *ConnManager) Remove(connID string) {
	if _, loaded := cm.conns.LoadAndDelete(connID); loaded {
		cm.count.Add(-1)
	}
}

// Get 获取连接
func (cm *ConnManager) Get(connID string) (gnet.Conn, bool) {
	val, ok := cm.conns.Load(connID)
	if !ok {
		return nil, false
	}
	return val.(gnet.Conn), true
}

// Count 在线连接数
func (cm *ConnManager) Count() int64 {
	return cm.count.Load()
}

// Broadcast 广播消息给所有连接
func (cm *ConnManager) Broadcast(codec *Codec, resp *Response) {
	buf, err := codec.Encode(resp)
	if err != nil {
		logger.Error("[TCP ConnManager] 广播编码失败: %v", err)
		return
	}
	cm.conns.Range(func(key, value any) bool {
		conn := value.(gnet.Conn)
		_ = conn.AsyncWrite(buf, nil)
		return true
	})
}

// Kick 踢出指定连接
func (cm *ConnManager) Kick(connID string) {
	if conn, ok := cm.Get(connID); ok {
		_ = conn.Close()
	}
}

// SendTo 向指定连接发送消息
func (cm *ConnManager) SendTo(connID string, codec *Codec, resp *Response) error {
	conn, ok := cm.Get(connID)
	if !ok {
		return fmt.Errorf("连接不存在: %s", connID)
	}
	buf, err := codec.Encode(resp)
	if err != nil {
		return err
	}
	return conn.AsyncWrite(buf, nil)
}

// ConnIDFromConn 从 gnet.Conn 上下文获取连接ID
func ConnIDFromConn(conn gnet.Conn) string {
	if ctx := conn.Context(); ctx != nil {
		if id, ok := ctx.(string); ok {
			return id
		}
	}
	return ""
}

func connIDFromUint64(id uint64) string {
	// 简单的数字ID，避免 uuid 依赖
	const digits = "0123456789"
	if id == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for id > 0 {
		buf = append(buf, digits[id%10])
		id /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
