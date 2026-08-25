package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoad_ValidConfig(t *testing.T) {
	cfg, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("Server.Listen = %q", cfg.Server.Listen)
	}
	if got, want := cfg.Server.ShutdownTimeout.AsTimeDuration(), 30*time.Second; got != want {
		t.Errorf("Server.ShutdownTimeout = %v, want %v", got, want)
	}
	if got, want := cfg.Proxmox.Clusters[0].TaskTimeout.AsTimeDuration(), 15*time.Minute; got != want {
		t.Errorf("Proxmox cluster TaskTimeout = %v, want %v", got, want)
	}
	if len(cfg.Proxmox.Clusters) != 1 || cfg.Proxmox.Clusters[0].ID != "pve-main" {
		t.Errorf("Proxmox.Clusters = %+v", cfg.Proxmox.Clusters)
	}
	if cfg.Identity.DuplicatePolicy.ActiveFleet != "block" || cfg.Identity.DuplicatePolicy.RetiredHistory != "warn" {
		t.Errorf("Identity.DuplicatePolicy = %+v", cfg.Identity.DuplicatePolicy)
	}
	if !cfg.Network.RequireSingleNIC || !cfg.Network.RequireSingleDefaultRoute {
		t.Errorf("Network flags not parsed correctly: %+v", cfg.Network)
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	_, err := Load("testdata/missing_required.yaml")
	if err == nil {
		t.Fatal("expected error for missing required fields, got nil")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("testdata/does-not-exist.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("VMF_SERVER_LISTEN", "127.0.0.1:9999")
	t.Setenv("VMF_OBSERVABILITY_LOG_LEVEL", "debug")

	cfg, err := Load("testdata/valid.yaml")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Errorf("Server.Listen override = %q, want 127.0.0.1:9999", cfg.Server.Listen)
	}
	if cfg.Observability.LogLevel != "debug" {
		t.Errorf("Observability.LogLevel override = %q, want debug", cfg.Observability.LogLevel)
	}
}

func TestDuration_InvalidValue(t *testing.T) {
	var wrapper struct {
		D Duration `yaml:"d"`
	}
	err := yaml.Unmarshal([]byte("d: not-a-duration"), &wrapper)
	if err == nil {
		t.Fatal("expected error for invalid duration string")
	}
}
