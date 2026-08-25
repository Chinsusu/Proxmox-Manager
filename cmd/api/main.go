// Command vmf-api là entrypoint của API service — implement /v1/*
// routes (templates/instances/jobs/network-segments/findings) theo
// api/openapi.yaml, thuộc epic P0-09.
package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/config"
	"github.com/Chinsusu/vm-factory/internal/httpapi"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/observability"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
)

// version/commit/buildTime được set qua -ldflags khi build release,
// theo Phần II mục 16 và tài liệu 11 mục 8.
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

// RBAC theo Phần IX mục 9 (viewer/operator/admin/service) — docs không
// chốt mapping chi tiết theo từng action, áp dụng quy ước hợp lý:
// mọi role đã xác thực đọc được; operator/service thực hiện hành động
// vận hành ngày thường (create/retry/rebuild/quarantine/decommission
// instance, retry job); admin riêng cho thay đổi hạ tầng bậc cao hơn
// (template lifecycle, network segment topology).
var (
	anyRole    = []httpapi.Role{httpapi.RoleViewer, httpapi.RoleOperator, httpapi.RoleAdmin, httpapi.RoleService}
	writeRoles = []httpapi.Role{httpapi.RoleOperator, httpapi.RoleAdmin, httpapi.RoleService}
	adminRoles = []httpapi.Role{httpapi.RoleAdmin}
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

	dsn, err := loadDSN(cfg.Database.DSNFile)
	if err != nil {
		logger.Error("failed to load database DSN", "error", err, "path", cfg.Database.DSNFile)
		os.Exit(1)
	}
	db, err := storage.Open(dsn, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	mux := buildMux(db, authn)
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

	// vmf-api không tự sinh job/proxmox/resource metric (đó là việc của
	// vmf-worker) — vẫn phục vụ /metrics rỗng-nhưng-hợp-lệ để Prometheus
	// scrape target này lên (up{job="vmf-api"}) mà không lỗi, nhất quán
	// với observability.metrics_listen đã có trong config.
	var metricsSrv *http.Server
	if cfg.Observability.MetricsListen != "" {
		metricsSrv = &http.Server{Addr: cfg.Observability.MetricsListen, Handler: observability.NewMetrics().Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			logger.Info("metrics listening", "addr", cfg.Observability.MetricsListen)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics server error", "error", err)
			}
		}()
	}

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
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
}

// buildMux đăng ký toàn bộ route /v1/* theo api/openapi.yaml, wiring
// repository thật (thay placeholder P0-00 alwaysReady bằng
// db.Ready() thật).
func buildMux(db *storage.DB, authn httpapi.Authenticator) *http.ServeMux {
	templatesRepo := template.NewRepository(db)
	instancesRepo := instance.NewRepository(db)
	jobsRepo := jobs.NewRepository(db)
	segmentsRepo := ipam.NewSegmentRepository(db)
	ipamRepo := ipam.NewRepository(db)
	hostnamesRepo := ipam.NewHostnameRepository(db)
	runsRepo := storage.NewValidationRunRepository(db)
	findingsRepo := storage.NewFindingRepository(db)
	idemRepo := storage.NewIdempotencyRepository(db)
	alertsRepo := storage.NewAlertRepository(db)
	auditReader := audit.NewReader(db)
	auditWriter := audit.NewWriter()

	templateH := &httpapi.TemplateHandlers{Templates: templatesRepo, DB: db, Idem: idemRepo}
	instanceH := &httpapi.InstanceHandlers{
		Instances: instancesRepo, Templates: templatesRepo, Segments: segmentsRepo, IPAM: ipamRepo,
		Hostnames: hostnamesRepo, Jobs: jobsRepo, Runs: runsRepo, AuditR: auditReader, AuditW: auditWriter,
		DB: db, Idem: idemRepo,
	}
	jobH := &httpapi.JobHandlers{Jobs: jobsRepo, AuditR: auditReader, DB: db, Idem: idemRepo}
	segmentH := &httpapi.SegmentHandlers{Segments: segmentsRepo, DB: db, Idem: idemRepo}
	findingH := &httpapi.FindingHandlers{Findings: findingsRepo}
	ipPoolH := &httpapi.IPPoolHandlers{Segments: segmentsRepo}
	validationH := &httpapi.ValidationHandlers{Runs: runsRepo}
	auditEventH := &httpapi.AuditEventHandlers{AuditR: auditReader}
	egressH := &httpapi.EgressBindingHandlers{DB: db, Runs: runsRepo}
	alertH := &httpapi.AlertHandlers{Alerts: alertsRepo}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", httpapi.HealthHandler)
	mux.HandleFunc("GET /v1/ready", httpapi.ReadyHandler(db))

	route := func(pattern string, roles []httpapi.Role, h http.HandlerFunc) {
		mux.Handle(pattern, httpapi.AuthMiddleware(authn)(httpapi.RequireRole(roles...)(h)))
	}

	route("GET /v1/templates", anyRole, templateH.List)
	route("POST /v1/templates", adminRoles, templateH.Create)
	route("GET /v1/templates/{id}", anyRole, templateH.Get)
	route("POST /v1/templates/{id}/promote", adminRoles, templateH.Promote)

	route("GET /v1/instances", anyRole, instanceH.List)
	route("POST /v1/instances", writeRoles, instanceH.Create)
	route("GET /v1/instances/{id}", anyRole, instanceH.Get)
	route("GET /v1/instances/{id}/evidence", anyRole, instanceH.Evidence)
	route("POST /v1/instances/{id}/retry", writeRoles, instanceH.Retry)
	route("POST /v1/instances/{id}/quarantine", writeRoles, instanceH.Quarantine)
	route("POST /v1/instances/{id}/rebuild", writeRoles, instanceH.Rebuild)
	route("POST /v1/instances/{id}/decommission", writeRoles, instanceH.Decommission)

	route("GET /v1/jobs", anyRole, jobH.List)
	route("GET /v1/jobs/{id}", anyRole, jobH.Get)
	route("POST /v1/jobs/{id}/retry", writeRoles, jobH.Retry)
	route("GET /v1/jobs/{id}/events", anyRole, jobH.Events)

	route("GET /v1/network-segments", anyRole, segmentH.List)
	route("POST /v1/network-segments", adminRoles, segmentH.Create)

	route("GET /v1/findings", anyRole, findingH.List)

	// Bo sung cho UI console (API_UI_Gap_Register v1.0) - xem
	// vm-factory-ui/docs/API_UI_Gap_Register_v1.0.md.
	route("GET /v1/ip-pools", anyRole, ipPoolH.List)
	route("GET /v1/validations", anyRole, validationH.List)
	route("GET /v1/audit-events", anyRole, auditEventH.List)
	route("GET /v1/audit-events/export", anyRole, auditEventH.Export)
	route("GET /v1/egress-bindings", anyRole, egressH.List)
	route("GET /v1/alerts", anyRole, alertH.List)
	route("POST /v1/alerts/{id}/acknowledge", writeRoles, alertH.Acknowledge)

	return mux
}

func loadDSN(path string) (string, error) {
	// path đến từ config.Database.DSNFile do operator chỉ định lúc
	// deploy, không phải request input.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path là config field do operator kiểm soát, không phải request input
	if err != nil {
		return "", err
	}
	dsn := strings.TrimSpace(string(raw))
	if dsn == "" {
		return "", errors.New("dsn file is empty")
	}
	return dsn, nil
}

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
