package tcp

import (
	"encoding/json"
	"sync"

	"github.com/panjf2000/gnet/v2"
)

// Context TCP请求上下文，类比 gin.Context
type Context struct {
	Conn    gnet.Conn      // 原始连接
	ConnID  string         // 连接ID
	Msg     *Message       // 当前消息
	codec   *Codec         // 编解码器
	keys    map[string]any // 上下文存储
	mu      sync.RWMutex   // keys 锁
	aborted bool           // 是否中断
}

// NewContext 创建请求上下文
func NewContext(conn gnet.Conn, connID string, msg *Message, codec *Codec) *Context {
	return &Context{
		Conn:   conn,
		ConnID: connID,
		Msg:    msg,
		codec:  codec,
		keys:   make(map[string]any),
	}
}

// Set 存储键值对
func (c *Context) Set(key string, value any) {
	c.mu.Lock()
	c.keys[key] = value
	c.mu.Unlock()
}

// Get 获取值
func (c *Context) Get(key string) (any, bool) {
	c.mu.RLock()
	val, ok := c.keys[key]
	c.mu.RUnlock()
	return val, ok
}

// GetString 获取字符串值
func (c *Context) GetString(key string) string {
	if val, ok := c.Get(key); ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt64 获取int64值
func (c *Context) GetInt64(key string) int64 {
	if val, ok := c.Get(key); ok {
		switch v := val.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case float64:
			return int64(v)
		}
	}
	return 0
}

// Bind 将消息Data解析到目标结构体
func (c *Context) Bind(v any) error {
	if c.Msg.Data == nil {
		return json.Unmarshal([]byte("{}"), v)
	}
	return json.Unmarshal(c.Msg.Data, v)
}

// Reply 发送成功响应
func (c *Context) Reply(data any) error {
	return c.send(&Response{
		Cmd:     c.Msg.Cmd,
		Seq:     c.Msg.Seq,
		Code:    200,
		Message: "success",
		Data:    data,
	})
}

// ReplyMsg 发送自定义消息响应
func (c *Context) ReplyMsg(code int, message string, data any) error {
	return c.send(&Response{
		Cmd:     c.Msg.Cmd,
		Seq:     c.Msg.Seq,
		Code:    code,
		Message: message,
		Data:    data,
	})
}

// Error 发送错误响应
func (c *Context) Error(code int, message string) error {
	return c.send(&Response{
		Cmd:     c.Msg.Cmd,
		Seq:     c.Msg.Seq,
		Code:    code,
		Message: message,
	})
}

// Abort 中断后续中间件/handler执行
func (c *Context) Abort() {
	c.aborted = true
}

// IsAborted 是否已中断
func (c *Context) IsAborted() bool {
	return c.aborted
}

func (c *Context) send(resp *Response) error {
	buf, err := c.codec.Encode(resp)
	if err != nil {
		return err
	}
	return c.Conn.AsyncWrite(buf, nil)
}
