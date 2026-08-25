// Command vmf-api là entrypoint của API service. P0-00 chỉ wiring
// foundation (config, logger, auth baseline, health/ready); route
// nghiệp vụ (templates/instances/jobs...) thuộc epic P0-09.
package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Chinsusu/vm-factory/internal/config"
	"github.com/Chinsusu/vm-factory/internal/httpapi"
	"github.com/Chinsusu/vm-factory/internal/observability"

	"crypto/rsa"
)

// version/commit/buildTime được set qua -ldflags khi build release,
// theo Phần II mục 16 và tài liệu 11 mục 8.
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

	logger := observability.New("vmf-api", parseLevel(cfg.Observability.LogLevel))
	logger.Info("starting vmf-api", "version", version, "commit", commit, "build_time", buildTime)

	pubKey, err := loadRSAPublicKey(cfg.Auth.JWTPublicKeyFile)
	if err != nil {
		logger.Error("failed to load JWT public key", "error", err, "path", cfg.Auth.JWTPublicKeyFile)
		os.Exit(1)
	}

	authn := &httpapi.JWTAuthenticator{
		PublicKey:        pubKey,
		ExpectedIssuer:   cfg.Auth.Issuer,
		ExpectedAudience: cfg.Auth.RequiredAudience,
	}
	_ = authn // dùng khi route nghiệp vụ được thêm ở P0-09; health/ready là public.

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", httpapi.HealthHandler)
	mux.HandleFunc("GET /v1/ready", httpapi.ReadyHandler(alwaysReady{}))

	handler := observability.RequestIDMiddleware(mux)

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", cfg.Server.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.AsTimeDuration())
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

// alwaysReady là placeholder cho P0-00; P0-01 thay bằng ready checker
// thật ping PostgreSQL trước khi báo ready.
type alwaysReady struct{}

func (alwaysReady) Ready() error { return nil }

func loadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	// path đến từ config.Auth.JWTPublicKeyFile do operator chỉ định lúc
	// deploy, không phải request input — không phải path traversal qua
	// user input mà gosec G304 cảnh báo.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path là config field do operator kiểm soát, không phải request input
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("invalid PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return rsaPub, nil
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
