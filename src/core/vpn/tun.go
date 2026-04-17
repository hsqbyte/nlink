package vpn

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

const defaultMTU = 1400

// TunPacketOffset 是 TUN 设备 Read/Write 所需的最小缓冲区偏移量。
// macOS utun header 需要 4 字节，Linux virtio_net_hdr 需要 10 字节。
// 使用两者的最大值以保证跨平台兼容。
const TunPacketOffset = 10

// TunDevice 封装 TUN 设备的创建、配置和读写
type TunDevice struct {
	device    tun.Device
	name      string
	virtualIP net.IP
	cidr      int
	mtu       int
}

// NewTunDevice 创建并配置 TUN 设备
func NewTunDevice(virtualCIDR string, mtu int) (*TunDevice, error) {
	ip, ipNet, err := net.ParseCIDR(virtualCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid virtual_ip CIDR %q: %w", virtualCIDR, err)
	}
	cidr, _ := ipNet.Mask.Size()

	if mtu <= 0 {
		mtu = defaultMTU
	}

	dev, err := tun.CreateTUN("utun", mtu)
	if err != nil {
		return nil, fmt.Errorf("create TUN device: %w", err)
	}

	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("get TUN device name: %w", err)
	}

	td := &TunDevice{
		device:    dev,
		name:      name,
		virtualIP: ip.To4(),
		cidr:      cidr,
		mtu:       mtu,
	}

	if err := td.configureInterface(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure TUN interface: %w", err)
	}

	return td, nil
}

// Name 返回 TUN 设备名称
func (t *TunDevice) Name() string {
	return t.name
}

// Read 从 TUN 设备读取一个 IP 包
// buf 的前 offset 字节为 wireguard/tun 库保留，实际 IP 包从 buf[offset:offset+n] 读取
func (t *TunDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	return t.device.Read(bufs, sizes, offset)
}

// Write 向 TUN 设备写入 IP 包
func (t *TunDevice) Write(bufs [][]byte, offset int) (int, error) {
	return t.device.Write(bufs, offset)
}

// Close 关闭 TUN 设备并清理路由
func (t *TunDevice) Close() error {
	// 主动清理路由，防止残留路由指向已销毁的 utun
	if runtime.GOOS == "darwin" {
		_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", t.virtualIP.String(), t.cidr))
		if err == nil {
			if rerr := runCmd("route", "delete", "-net", ipNet.String()); rerr != nil {
				// 非关键错误，仅记录（路由可能本就已不存在）
				fmt.Printf("  [TUN] 清理路由失败 %s: %v\n", ipNet.String(), rerr)
			}
		}
	}
	return t.device.Close()
}

// BatchSize 返回批量读写大小
func (t *TunDevice) BatchSize() int {
	return t.device.BatchSize()
}

// configureInterface 配置 TUN 接口的 IP 地址和路由
func (t *TunDevice) configureInterface() error {
	switch runtime.GOOS {
	case "darwin":
		return t.configureDarwin()
	case "linux":
		return t.configureLinux()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func (t *TunDevice) configureDarwin() error {
	// 计算对端地址（点对点接口需要）
	peerIP := nextIP(t.virtualIP)

	// ifconfig utunX inet 10.0.0.1 10.0.0.2 netmask 255.255.255.0
	mask := net.CIDRMask(t.cidr, 32)
	maskStr := fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])

	if err := runCmd("ifconfig", t.name, "inet", t.virtualIP.String(), peerIP.String(), "netmask", maskStr); err != nil {
		return err
	}

	// 添加子网路由，使用点对点对端 IP 作为网关
	// 先清理可能存在的残留路由（上次退出时未清理或其他 utun 接管了接口号）
	_, ipNet, _ := net.ParseCIDR(fmt.Sprintf("%s/%d", t.virtualIP.String(), t.cidr))
	_ = runCmd("route", "delete", "-net", ipNet.String()) // 忽略错误（可能不存在）
	return runCmd("route", "add", "-net", ipNet.String(), "-interface", t.name)
}

func (t *TunDevice) configureLinux() error {
	// ip addr add 10.0.0.1/24 dev tunX
	cidrStr := fmt.Sprintf("%s/%d", t.virtualIP.String(), t.cidr)
	if err := runCmd("ip", "addr", "add", cidrStr, "dev", t.name); err != nil {
		return err
	}
	// ip link set tunX up
	if err := runCmd("ip", "link", "set", t.name, "up"); err != nil {
		return err
	}
	// ip link set tunX mtu 1400
	return runCmd("ip", "link", "set", t.name, "mtu", fmt.Sprintf("%d", t.mtu))
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s (%w)", name, strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	fmt.Printf("  [TUN] %s %s\n", name, strings.Join(args, " "))
	return nil
}

// nextIP 返回下一个 IP 地址（用于 macOS 点对点接口的对端地址）
func nextIP(ip net.IP) net.IP {
	next := make(net.IP, len(ip))
	copy(next, ip)
	for i := len(next) - 1; i >= 0; i-- {
		next[i]++
		if next[i] != 0 {
			break
		}
	}
	return next
}
