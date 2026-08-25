// Package config load cấu hình YAML theo schema đã chốt ở
// appendices/vm-factory-config.example.yaml (docs/appendices), hỗ trợ
// override một số field bằng environment variable theo Phần II mục 15
// ("YAML + environment overrides").
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration bọc time.Duration để yaml.v3 parse được chuỗi kiểu "30s", "15m".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q: %w", node.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) AsTimeDuration() time.Duration { return time.Duration(d) }

type ServerConfig struct {
	Listen          string   `yaml:"listen"`
	PublicBaseURL   string   `yaml:"public_base_url"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
}

type DatabaseConfig struct {
	DSNFile          string   `yaml:"dsn_file"`
	MaxOpenConns     int      `yaml:"max_open_conns"`
	MaxIdleConns     int      `yaml:"max_idle_conns"`
	StatementTimeout Duration `yaml:"statement_timeout"`
}

type AuthConfig struct {
	Issuer           string `yaml:"issuer"`
	JWTPublicKeyFile string `yaml:"jwt_public_key_file"`
	RequiredAudience string `yaml:"required_audience"`
}

type ProxmoxClusterConfig struct {
	ID              string   `yaml:"id"`
	BaseURL         string   `yaml:"base_url"`
	TokenID         string   `yaml:"token_id"`
	TokenSecretFile string   `yaml:"token_secret_file"`
	CAFile          string   `yaml:"ca_file"`
	RequestTimeout  Duration `yaml:"request_timeout"`
	TaskTimeout     Duration `yaml:"task_timeout"`
}

type ProxmoxConfig struct {
	Clusters []ProxmoxClusterConfig `yaml:"clusters"`
}

type PGWConfig struct {
	BaseURL           string   `yaml:"base_url"`
	TokenFile         string   `yaml:"token_file"`
	RequestTimeout    Duration `yaml:"request_timeout"`
	ActivationTimeout Duration `yaml:"activation_timeout"`
}

type ProvisioningConfig struct {
	DefaultCloneMode      string   `yaml:"default_clone_mode"`
	WorkerConcurrency     int      `yaml:"worker_concurrency"`
	PerPVENodeConcurrency int      `yaml:"per_pve_node_concurrency"`
	PerStorageConcurrency int      `yaml:"per_storage_concurrency"`
	JobLease              Duration `yaml:"job_lease"`
	JobHeartbeat          Duration `yaml:"job_heartbeat"`
	MaxAttempts           int      `yaml:"max_attempts"`
}

type GuestConfig struct {
	QGATimeout        Duration `yaml:"qga_timeout"`
	SSHFallback       bool     `yaml:"ssh_fallback"`
	SSHUser           string   `yaml:"ssh_user"`
	SSHPrivateKeyFile string   `yaml:"ssh_private_key_file"`
	CloudInitTimeout  Duration `yaml:"cloud_init_timeout"`
}

type DuplicatePolicyConfig struct {
	ActiveFleet    string `yaml:"active_fleet"`
	RetiredHistory string `yaml:"retired_history"`
}

type IdentityConfig struct {
	HMACKeyFile     string                `yaml:"hmac_key_file"`
	DuplicatePolicy DuplicatePolicyConfig `yaml:"duplicate_policy"`
}

type NetworkConfig struct {
	IPv6Policy                string `yaml:"ipv6_policy"`
	RequireSingleNIC          bool   `yaml:"require_single_nic"`
	RequireSingleDefaultRoute bool   `yaml:"require_single_default_route"`
}

type ObservabilityConfig struct {
	MetricsListen string `yaml:"metrics_listen"`
	LogFormat     string `yaml:"log_format"`
	LogLevel      string `yaml:"log_level"`
}

// Config là root object khớp 1:1 với vm-factory-config.example.yaml.
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Database      DatabaseConfig      `yaml:"database"`
	Auth          AuthConfig          `yaml:"auth"`
	Proxmox       ProxmoxConfig       `yaml:"proxmox"`
	PGW           PGWConfig           `yaml:"pgw"`
	Provisioning  ProvisioningConfig  `yaml:"provisioning"`
	Guest         GuestConfig         `yaml:"guest"`
	Identity      IdentityConfig      `yaml:"identity"`
	Network       NetworkConfig       `yaml:"network"`
	Observability ObservabilityConfig `yaml:"observability"`
}

// Load đọc file YAML tại path, áp env override, rồi validate field bắt buộc.
// Không log nội dung config vì có thể chứa path tới secret file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	applyEnvOverrides(&cfg)

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: invalid: %w", err)
	}
	return &cfg, nil
}

// applyEnvOverrides cho phép override một số field vận hành phổ biến qua
// biến môi trường mà không cần sửa file YAML — hữu ích khi chạy trong
// systemd unit hoặc container. Danh sách override liệt kê tường minh,
// không dùng reflection để tránh override nhầm field nhạy cảm.
func applyEnvOverrides(cfg *Config) {
	if v, ok := os.LookupEnv("VMF_SERVER_LISTEN"); ok {
		cfg.Server.Listen = v
	}
	if v, ok := os.LookupEnv("VMF_DATABASE_DSN_FILE"); ok {
		cfg.Database.DSNFile = v
	}
	if v, ok := os.LookupEnv("VMF_OBSERVABILITY_LOG_LEVEL"); ok {
		cfg.Observability.LogLevel = v
	}
	if v, ok := os.LookupEnv("VMF_OBSERVABILITY_METRICS_LISTEN"); ok {
		cfg.Observability.MetricsListen = v
	}
}

func (c *Config) validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Database.DSNFile == "" {
		return fmt.Errorf("database.dsn_file is required")
	}
	if c.Auth.JWTPublicKeyFile == "" {
		return fmt.Errorf("auth.jwt_public_key_file is required")
	}
	for i, cl := range c.Proxmox.Clusters {
		if cl.ID == "" || cl.BaseURL == "" {
			return fmt.Errorf("proxmox.clusters[%d]: id và base_url là bắt buộc", i)
		}
	}
	return nil
}
