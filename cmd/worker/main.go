// Command vmf-worker là entrypoint của worker service. P0-00 chỉ wiring
// foundation (config, logger); job lease loop thật thuộc epic P0-05
// (State Engine) sau khi P0-01 (Domain & PostgreSQL) hoàn tất.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/Chinsusu/vm-factory/internal/config"
	"github.com/Chinsusu/vm-factory/internal/observability"
)

var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", envOr("VMF_CONFIG", "configs/local.yaml"), "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Default().Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := observability.New("vmf-worker", parseLevel(cfg.Observability.LogLevel))
	logger.Info("starting vmf-worker", "version", version, "commit", commit, "build_time", buildTime,
		"worker_concurrency", cfg.Provisioning.WorkerConcurrency)

	logger.Warn("job lease loop chưa implement — chờ epic P0-01 (Domain & PostgreSQL) và P0-05 (State Engine)")
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
