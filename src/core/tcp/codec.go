package tcp

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/panjf2000/gnet/v2"
)

const (
	headerLen      = 4     // 4字节大端长度头
	defaultMaxSize = 65536 // 默认最大消息体
)

// Codec 长度头协议编解码器
type Codec struct {
	maxMessageSize int
	crypto         *Crypto // 可选加密层
}

// NewCodec 创建编解码器
func NewCodec(maxMessageSize int) *Codec {
	if maxMessageSize <= 0 {
		maxMessageSize = defaultMaxSize
	}
	return &Codec{maxMessageSize: maxMessageSize}
}

// SetCrypto 设置加密器（启用后所有消息加解密）
func (c *Codec) SetCrypto(cr *Crypto) {
	c.crypto = cr
}

// Decode 从 gnet.Conn 解码一条完整消息
// 返回 nil 表示数据不足，需要等待更多数据
func (c *Codec) Decode(conn gnet.Conn) (*Message, error) {
	// 读取长度头
	if conn.InboundBuffered() < headerLen {
		return nil, nil
	}

	// Peek 不消费数据
	lenBuf, _ := conn.Peek(headerLen)
	msgLen := int(binary.BigEndian.Uint32(lenBuf))

	if msgLen <= 0 {
		return nil, fmt.Errorf("消息长度无效: %d", msgLen)
	}
	maxSize := c.maxMessageSize
	if c.crypto != nil {
		maxSize += c.crypto.Overhead()
	}
	if msgLen > maxSize {
		return nil, fmt.Errorf("消息长度超限: %d (最大: %d)", msgLen, maxSize)
	}

	// 等待完整消息
	totalLen := headerLen + msgLen
	if conn.InboundBuffered() < totalLen {
		return nil, nil
	}

	// 消费数据
	buf, _ := conn.Peek(totalLen)
	payload := make([]byte, msgLen)
	copy(payload, buf[headerLen:totalLen])
	if _, err := conn.Discard(totalLen); err != nil {
		// 底层缓冲区消费失败 → 协议状态已损坏，必须断连
		return nil, fmt.Errorf("Discard底层缓冲区失败: %w", err)
	}

	// 解密（如果启用）
	if c.crypto != nil {
		decrypted, err := c.crypto.Decrypt(payload)
		if err != nil {
			return nil, fmt.Errorf("解密失败: %w", err)
		}
		payload = decrypted
	}

	// 解析 JSON
	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}

	return &msg, nil
}

// Encode 编码响应消息为字节
func (c *Codec) Encode(resp *Response) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("JSON编码失败: %w", err)
	}

	// 加密（如果启用）
	if c.crypto != nil {
		payload, err = c.crypto.Encrypt(payload)
		if err != nil {
			return nil, fmt.Errorf("加密失败: %w", err)
		}
	}

	buf := make([]byte, headerLen+len(payload))
	binary.BigEndian.PutUint32(buf[:headerLen], uint32(len(payload)))
	copy(buf[headerLen:], payload)
	return buf, nil
}

// EncodeMessage 编码请求消息为字节（客户端用）
func (c *Codec) EncodeMessage(msg *Message) ([]byte, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("JSON编码失败: %w", err)
	}

	// 加密（如果启用）
	if c.crypto != nil {
		payload, err = c.crypto.Encrypt(payload)
		if err != nil {
			return nil, fmt.Errorf("加密失败: %w", err)
		}
	}

	buf := make([]byte, headerLen+len(payload))
	binary.BigEndian.PutUint32(buf[:headerLen], uint32(len(payload)))
	copy(buf[headerLen:], payload)
	return buf, nil
}

// EncodeMessage 包级别函数：编码 Message 为 [4字节长度头 + JSON]
// 注意: 此函数不经过加密层，仅用于工作连接（port+1）注册消息。
// 控制通道消息请使用 Codec.EncodeMessage 方法。
func EncodeMessage(msg *Message) []byte {
	payload, _ := json.Marshal(msg)
	buf := make([]byte, headerLen+len(payload))
	binary.BigEndian.PutUint32(buf[:headerLen], uint32(len(payload)))
	copy(buf[headerLen:], payload)
	return buf
}
