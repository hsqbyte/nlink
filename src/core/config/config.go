package config

import (
	"fmt"
	"os"

	"github.com/fastgox/utils/logger"
	"github.com/hsqbyte/nlink/src/models/config"
	"gopkg.in/yaml.v3"
)

var GlobalConfig *config.Config

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
	logger.Info("配置加载成功: %s", cfgFile)
	return nil
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
