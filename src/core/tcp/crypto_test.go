package tcp

import (
	"bytes"
	"sync"
	"testing"
)

// TestCryptoRoundTrip 验证加密/解密闭环正确
func TestCryptoRoundTrip(t *testing.T) {
	cr, err := NewCrypto("test-token-xyz")
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	plaintext := []byte("hello nlink 你好世界")
	ct, err := cr.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := cr.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("roundtrip mismatch: got=%q want=%q", got, plaintext)
	}
}

// TestNonceMonotonic 验证 nonce 不会重复（同实例，前缀相同，counter 递增）
func TestNonceMonotonic(t *testing.T) {
	cr, err := NewCrypto("nonce-test")
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	const N = 2000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		ct, err := cr.Encrypt([]byte("x"))
		if err != nil {
			t.Fatalf("Encrypt #%d: %v", i, err)
		}
		nonce := string(ct[:cr.aead.NonceSize()])
		if _, dup := seen[nonce]; dup {
			t.Fatalf("duplicated nonce at #%d", i)
		}
		seen[nonce] = struct{}{}
	}
}

// TestNonceConcurrent 验证并发下 counter 仍然单调且不重
func TestNonceConcurrent(t *testing.T) {
	cr, err := NewCrypto("concurrent")
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	const (
		goroutines = 16
		perGo      = 256
	)
	var (
		mu   sync.Mutex
		seen = make(map[string]struct{}, goroutines*perGo)
		wg   sync.WaitGroup
	)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGo; i++ {
				ct, err := cr.Encrypt([]byte("y"))
				if err != nil {
					t.Errorf("Encrypt: %v", err)
					return
				}
				nonce := string(ct[:cr.aead.NonceSize()])
				mu.Lock()
				if _, dup := seen[nonce]; dup {
					mu.Unlock()
					t.Errorf("duplicated nonce under concurrency")
					return
				}
				seen[nonce] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != goroutines*perGo {
		t.Fatalf("expected %d unique nonces, got %d", goroutines*perGo, len(seen))
	}
}

// TestPrefixUniquePerInstance 验证不同实例拥有不同前缀（极高概率）
func TestPrefixUniquePerInstance(t *testing.T) {
	cr1, _ := NewCrypto("same-token")
	cr2, _ := NewCrypto("same-token")
	if bytes.Equal(cr1.prefix[:], cr2.prefix[:]) {
		t.Fatal("两个实例的 nonce 前缀居然相同，随机源可能异常")
	}
}
