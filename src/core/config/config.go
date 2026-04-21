package config

import (
	"fmt"
	"os"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src/models/config"
	"gopkg.in/yaml.v3"
)

var GlobalConfig *config.Config

// ConfigFilePath 记录当前加载的配置文件路径（供导入/导出 API 回写使用）
var ConfigFilePath string

func InitConfig(cfgFile string) error {
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	if cfg.Node.Name == "" {
		return fmt.Errorf("node.name 不能为空")
	}
	if cfg.Node.Token == "" {
		return fmt.Errorf("node.token 不能为空")
	}
	if cfg.Node.Listen == nil && len(cfg.Peers) == 0 {
		return fmt.Errorf("配置文件必须包含 node.listen 或 peers 至少一个部分")
	}

	if cfg.Node.Listen != nil {
		if err := validateListen(cfg.Node.Listen); err != nil {
			return fmt.Errorf("listen 配置验证失败: %w", err)
		}
	}
	if cfg.Node.Dashboard != nil {
		if err := validateDashboard(cfg.Node.Dashboard); err != nil {
			return fmt.Errorf("dashboard 配置验证失败: %w", err)
		}
	}

	for i := range cfg.Peers {
		if err := validatePeer(&cfg.Peers[i]); err != nil {
			return fmt.Errorf("peers[%d] 配置验证失败: %w", i, err)
		}
	}

	GlobalConfig = &cfg
	ConfigFilePath = cfgFile
	logger.Info("配置加载成功: %s", cfgFile)
	return nil
}

// ReloadConfig 重新读取并校验配置文件，不修改 GlobalConfig。
// 返回新配置供调用方决定差异化应用策略。
func ReloadConfig(cfgFile string) (*config.Config, error) {
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	return ParseAndValidate(data)
}

// ParseAndValidate 从 YAML 字节流解析并校验配置 (供 API 导入使用)
func ParseAndValidate(data []byte) (*config.Config, error) {
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Node.Name == "" {
		return nil, fmt.Errorf("node.name 不能为空")
	}
	if cfg.Node.Token == "" {
		return nil, fmt.Errorf("node.token 不能为空")
	}
	if cfg.Node.Listen != nil {
		if err := validateListen(cfg.Node.Listen); err != nil {
			return nil, fmt.Errorf("listen 配置验证失败: %w", err)
		}
	}
	if cfg.Node.Dashboard != nil {
		if err := validateDashboard(cfg.Node.Dashboard); err != nil {
			return nil, fmt.Errorf("dashboard 配置验证失败: %w", err)
		}
	}
	for i := range cfg.Peers {
		if err := validatePeer(&cfg.Peers[i]); err != nil {
			return nil, fmt.Errorf("peers[%d] 配置验证失败: %w", i, err)
		}
	}
	return &cfg, nil
}

// ExportYAML 将 GlobalConfig 序列化为 YAML
func ExportYAML() ([]byte, error) {
	if GlobalConfig == nil {
		return nil, fmt.Errorf("GlobalConfig 为空")
	}
	return yaml.Marshal(GlobalConfig)
}

// SaveConfigFile 原子写入 YAML 到 ConfigFilePath
func SaveConfigFile(data []byte) error {
	if ConfigFilePath == "" {
		return fmt.Errorf("ConfigFilePath 未初始化")
	}
	tmp := ConfigFilePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, ConfigFilePath); err != nil {
		return fmt.Errorf("rename 失败: %w", err)
	}
	return nil
}

// ApplyReload 仅在运行时安全的字段上应用差异（token 轮换、token_prev）。
// 其余字段会被记录为 "需要重启" 日志提示。
func ApplyReload(newCfg *config.Config) {
	if GlobalConfig == nil {
		GlobalConfig = newCfg
		return
	}
	old := GlobalConfig

	// 安全可热更的字段
	if old.Node.Token != newCfg.Node.Token {
		logger.Info("[reload] node.token 已更新")
		old.Node.Token = newCfg.Node.Token
	}
	if old.Node.TokenPrev != newCfg.Node.TokenPrev {
		logger.Info("[reload] node.token_prev 已更新")
		old.Node.TokenPrev = newCfg.Node.TokenPrev
	}

	// 结构性字段 —— 记录但不应用
	if old.Node.Listen != nil && newCfg.Node.Listen != nil && old.Node.Listen.Port != newCfg.Node.Listen.Port {
		logger.Warn("[reload] listen.port 变更需要重启才能生效")
	}
	if old.Node.VPN != nil && newCfg.Node.VPN != nil && old.Node.VPN.IsEnabled() != newCfg.Node.VPN.IsEnabled() {
		logger.Warn("[reload] vpn.enabled 变更需要重启才能生效")
	}
	if len(old.Peers) != len(newCfg.Peers) {
		logger.Warn("[reload] peers 数量变化需要重启才能生效（当前=%d，新=%d）", len(old.Peers), len(newCfg.Peers))
	}
}

func validateListen(l *config.ListenConfig) error {
	if l.Port <= 0 || l.Port > 65535 {
		return fmt.Errorf("port 必须在 1-65535 之间")
	}
	if l.MaxMessageSize <= 0 {
		l.MaxMessageSize = 65536
	}
	if l.WorkConnTimeout <= 0 {
		l.WorkConnTimeout = 10
	}
	if l.MaxProxiesPerPeer <= 0 {
		l.MaxProxiesPerPeer = 10
	}
	if l.HeartbeatTimeout <= 0 {
		l.HeartbeatTimeout = 90
	}
	return nil
}

func validateDashboard(d *config.DashboardConfig) error {
	if d.Port <= 0 || d.Port > 65535 {
		return fmt.Errorf("port 必须在 1-65535 之间")
	}
	if d.ShutdownTimeout <= 0 {
		d.ShutdownTimeout = 30
	}
	return nil
}

func validatePeer(p *config.PeerConfig) error {
	if p.Addr == "" {
		return fmt.Errorf("addr 不能为空")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return fmt.Errorf("port 必须在 1-65535 之间")
	}
	if p.Token == "" {
		return fmt.Errorf("token 不能为空")
	}
	if len(p.Proxies) == 0 {
		return fmt.Errorf("至少需要配置一个 proxy")
	}
	return nil
}
