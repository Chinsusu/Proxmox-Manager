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

// UnmarshalYAML implement yaml.Unmarshaler cho Duration.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q: %w", node.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

// AsTimeDuration trả Duration dạng time.Duration chuẩn để dùng trực tiếp
// với time.After/context.WithTimeout.
func (d Duration) AsTimeDuration() time.Duration { return time.Duration(d) }

// ServerConfig cấu hình HTTP server của vmf-api.
type ServerConfig struct {
	Listen          string   `yaml:"listen"`
	PublicBaseURL   string   `yaml:"public_base_url"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
}

// DatabaseConfig cấu hình kết nối PostgreSQL.
type DatabaseConfig struct {
	DSNFile          string   `yaml:"dsn_file"`
	MaxOpenConns     int      `yaml:"max_open_conns"`
	MaxIdleConns     int      `yaml:"max_idle_conns"`
	StatementTimeout Duration `yaml:"statement_timeout"`
}

// AuthConfig cấu hình verify JWT bearer token.
type AuthConfig struct {
	Issuer           string `yaml:"issuer"`
	JWTPublicKeyFile string `yaml:"jwt_public_key_file"`
	RequiredAudience string `yaml:"required_audience"`
}

// ProxmoxClusterConfig cấu hình một Proxmox cluster mà vmf-worker kết nối tới.
type ProxmoxClusterConfig struct {
	ID              string   `yaml:"id"`
	BaseURL         string   `yaml:"base_url"`
	TokenID         string   `yaml:"token_id"`
	TokenSecretFile string   `yaml:"token_secret_file"`
	CAFile          string   `yaml:"ca_file"`
	RequestTimeout  Duration `yaml:"request_timeout"`
	TaskTimeout     Duration `yaml:"task_timeout"`
	// InsecureSkipVerify chỉ dùng cho lab/dev self-signed cert (Phần III
	// mục 2 yêu cầu CA validation bắt buộc ở production — internal/proxmox
	// chưa consume CAFile, gap đã biết). Mặc định false.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// ProxmoxConfig liệt kê mọi Proxmox cluster đã đăng ký.
type ProxmoxConfig struct {
	Clusters []ProxmoxClusterConfig `yaml:"clusters"`
}

// PGWConfig cấu hình kết nối tới PGW API (external dependency, ADR-006).
type PGWConfig struct {
	BaseURL           string   `yaml:"base_url"`
	TokenFile         string   `yaml:"token_file"`
	RequestTimeout    Duration `yaml:"request_timeout"`
	ActivationTimeout Duration `yaml:"activation_timeout"`
}

// ProvisioningConfig cấu hình concurrency và retry cho provisioning worker.
type ProvisioningConfig struct {
	DefaultCloneMode      string                     `yaml:"default_clone_mode"`
	WorkerConcurrency     int                        `yaml:"worker_concurrency"`
	PerPVENodeConcurrency int                        `yaml:"per_pve_node_concurrency"`
	PerStorageConcurrency int                        `yaml:"per_storage_concurrency"`
	JobLease              Duration                   `yaml:"job_lease"`
	JobHeartbeat          Duration                   `yaml:"job_heartbeat"`
	MaxAttempts           int                        `yaml:"max_attempts"`
	Defaults              ProvisioningDefaultsConfig `yaml:"defaults"`
}

// ProvisioningDefaultsConfig chốt profile mạng/tài nguyên dùng chung cho
// MỌI instance — vm_instances.desired_config đã ghi network_segment_id/
// egress_policy_id/resources riêng theo từng request (P0-09), nhưng
// stateengine handlers (P0-05) chưa đọc field đó (gap đã biết, xem
// comment ở ConfiguringHandler) nên worker cần MỘT profile mặc định để
// chạy được, cho tới khi có bản củng cố đọc per-instance.
type ProvisioningDefaultsConfig struct {
	NetworkSegmentID string   `yaml:"network_segment_id"`
	EgressPolicyID   string   `yaml:"egress_policy_id"`
	Pool             string   `yaml:"pool"`
	Bridge           string   `yaml:"bridge"`
	IPConfig0        string   `yaml:"ipconfig0"`
	Cores            int      `yaml:"cores"`
	MemoryMB         int      `yaml:"memory_mb"`
	ReservationTTL   Duration `yaml:"reservation_ttl"`
}

// GuestConfig cấu hình cách vmf-worker chờ và giao tiếp với guest OS.
type GuestConfig struct {
	QGATimeout        Duration `yaml:"qga_timeout"`
	SSHFallback       bool     `yaml:"ssh_fallback"`
	SSHUser           string   `yaml:"ssh_user"`
	SSHPrivateKeyFile string   `yaml:"ssh_private_key_file"`
	CloudInitTimeout  Duration `yaml:"cloud_init_timeout"`
}

// DuplicatePolicyConfig chốt hành vi khi phát hiện machine-id digest trùng,
// theo Phần VIII mục 10 (active fleet luôn block, lịch sử retired configurable).
type DuplicatePolicyConfig struct {
	ActiveFleet    string `yaml:"active_fleet"`
	RetiredHistory string `yaml:"retired_history"`
}

// IdentityConfig cấu hình HMAC digest và duplicate policy cho identity validation.
type IdentityConfig struct {
	HMACKeyFile     string                `yaml:"hmac_key_file"`
	DuplicatePolicy DuplicatePolicyConfig `yaml:"duplicate_policy"`
}

// NetworkConfig chốt policy IPv6/NIC/route áp dụng cho mọi instance.
type NetworkConfig struct {
	IPv6Policy                string `yaml:"ipv6_policy"`
	RequireSingleNIC          bool   `yaml:"require_single_nic"`
	RequireSingleDefaultRoute bool   `yaml:"require_single_default_route"`
}

// ObservabilityConfig cấu hình metrics endpoint và log format/level.
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
	// path đến từ --config flag/env do operator triển khai chỉ định lúc
	// khởi động process, không phải input theo từng request — không phải
	// path traversal qua user input mà gosec G304 cảnh báo.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path là startup flag do operator kiểm soát, không phải request input
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
