package tcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Crypto AES-256-GCM 加解密器
type Crypto struct {
	aead cipher.AEAD
}

// NewCrypto 从 token 派生 AES-256 密钥，创建加解密器
func NewCrypto(token string) (*Crypto, error) {
	// HKDF 派生 32 字节密钥
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

	return &Crypto{aead: aead}, nil
}

// Encrypt 加密明文，返回 nonce + 密文
func (cr *Crypto) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, cr.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("生成nonce失败: %w", err)
	}
	// nonce + ciphertext+tag
	return cr.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 解密 nonce + 密文
func (cr *Crypto) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := cr.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("密文太短")
	}
	nonce := ciphertext[:nonceSize]
	return cr.aead.Open(nil, nonce, ciphertext[nonceSize:], nil)
}

// Overhead 返回加密额外开销（nonce + tag）
func (cr *Crypto) Overhead() int {
	return cr.aead.NonceSize() + cr.aead.Overhead()
}
