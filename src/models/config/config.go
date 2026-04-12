package config

// Config 节点配置
type Config struct {
	Node  NodeConfig   `yaml:"node"`
	Peers []PeerConfig `yaml:"peers"`
}
