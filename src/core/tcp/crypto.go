package tcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sync/atomic"

	"golang.org/x/crypto/hkdf"
)

// Crypto AES-256-GCM 加解密器
//
// Nonce 构造采用 "随机 prefix(8B) + 原子递增 counter(4B)" 的固定策略，
// 保证同一 Crypto 实例生命周期内 nonce 绝不重复：
//   - 每个实例启动时生成 8 字节随机前缀（不同实例极难碰撞）
//   - counter 单调递增，2^32 - 1 条消息内不会回绕
//
// 这比纯随机 nonce 更安全，避免了高吞吐下的生日碰撞风险。
type Crypto struct {
	aead      cipher.AEAD
	prefix    [8]byte       // 随机 nonce 前缀（实例唯一）
	counter   atomic.Uint64 // 单调递增计数器
	fallbacks []cipher.AEAD // 仅用于解密的旧密钥（token 轮换过渡期）
}

// NewCrypto 从 token 派生 AES-256 密钥，创建加解密器
func NewCrypto(token string) (*Crypto, error) {
	aead, err := deriveAEAD(token)
	if err != nil {
		return nil, err
	}
	cr := &Crypto{aead: aead}
	if _, err := io.ReadFull(rand.Reader, cr.prefix[:]); err != nil {
		return nil, fmt.Errorf("生成nonce前缀失败: %w", err)
	}
	return cr, nil
}

// deriveAEAD 由 token 派生一个 AES-256-GCM AEAD
func deriveAEAD(token string) (cipher.AEAD, error) {
	hkdfReader := hkdf.New(sha256.New, []byte(token), []byte("nlink-salt"), []byte("nlink-aes-gcm"))
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
	return aead, nil
}

// nextNonce 生成下一个 nonce: prefix(8B) + counter(4B, 大端)
// GCM 标准 nonce 为 12 字节，此处约束 counter 上限为 2^32。
func (cr *Crypto) nextNonce() ([]byte, error) {
	n := cr.counter.Add(1)
	if n > (1<<32)-1 {
		return nil, fmt.Errorf("nonce计数器溢出，请重建加密实例")
	}
	nonce := make([]byte, cr.aead.NonceSize())
	copy(nonce[:8], cr.prefix[:])
	binary.BigEndian.PutUint32(nonce[8:12], uint32(n))
	return nonce, nil
}

// Encrypt 加密明文，返回 nonce + 密文
func (cr *Crypto) Encrypt(plaintext []byte) ([]byte, error) {
	nonce, err := cr.nextNonce()
	if err != nil {
		return nil, err
	}
	return cr.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 解密 nonce + 密文
// 若设置了 fallback 密钥，会在主密钥失败后依次尝试（用于 token 轮换过渡）
func (cr *Crypto) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := cr.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("密文太短")
	}
	nonce := ciphertext[:nonceSize]
	body := ciphertext[nonceSize:]
	if plain, err := cr.aead.Open(nil, nonce, body, nil); err == nil {
		return plain, nil
	}
	for _, fb := range cr.fallbacks {
		if plain, err := fb.Open(nil, nonce, body, nil); err == nil {
			return plain, nil
		}
	}
	return nil, fmt.Errorf("解密失败：所有密钥均不匹配")
}

// AddFallbackKey 添加一个仅用于解密的旧密钥（token 轮换过渡期用）
func (cr *Crypto) AddFallbackKey(token string) error {
	aead, err := deriveAEAD(token)
	if err != nil {
		return err
	}
	cr.fallbacks = append(cr.fallbacks, aead)
	return nil
}

// Overhead 返回加密额外开销（nonce + tag）
func (cr *Crypto) Overhead() int {
	return cr.aead.NonceSize() + cr.aead.Overhead()
}
