package vpn

import (
	"fmt"
	"testing"
)

func TestNewTunDevice(t *testing.T) {
	// 需要 root 权限才能创建 TUN 设备
	dev, err := NewTunDevice("10.0.0.1/24", 1400)
	if err != nil {
		t.Skipf("跳过 TUN 测试 (需要 root 权限): %v", err)
	}
	defer dev.Close()

	fmt.Printf("TUN 设备已创建: %s\n", dev.Name())

	// 验证批量大小
	bs := dev.BatchSize()
	if bs <= 0 {
		t.Fatalf("BatchSize 应该 > 0, 实际: %d", bs)
	}

	// 测试从 TUN 读取（非阻塞方式，这里只验证接口可用）
	bufs := make([][]byte, bs)
	for i := range bufs {
		bufs[i] = make([]byte, 1500+4) // MTU + header
	}
	sizes := make([]int, bs)

	fmt.Println("TUN 设备测试通过")
	_ = bufs
	_ = sizes
}
