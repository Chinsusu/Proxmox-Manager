package observability

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetrics_NilSafe(t *testing.T) {
	var m *Metrics
	// Không panic khi Metrics chưa wiring (struct test/handler dùng
	// zero-value nil) — mọi hàm ghi metric phải nhận receiver nil an toàn.
	m.ObserveJobFinished("PROVISION", "success", time.Second)
	m.ObserveStateTransition("CLONING", time.Second)
	m.IncJobRetry("STEP_FAILED")
	m.AddJobLeaseExpired(1)
	m.SetJobBacklog(1)
	m.SetJobsActive("QUEUED", 1)
	m.ResetJobsActive()
	m.ObserveProxmoxRequest("clone", "ok", time.Second)
	m.SetIPPoolAddresses("seg-a", "FREE", 1)
	m.ResetIPPoolAddresses()
	m.SetInstances("READY", "2026.08.1", "pve01", 1)
	m.ResetInstances()
	m.IncIdentityDuplicate()
	m.ObserveValidation("identity", "PASS", "ID-001")
	if m.Handler() == nil {
		t.Fatal("Handler() on nil Metrics returned nil")
	}
}

func TestMetrics_ExposesExpectedSeries(t *testing.T) {
	m := NewMetrics()
	m.ObserveJobFinished("PROVISION", "success", 2*time.Second)
	m.ObserveStateTransition("CLONING", time.Second)
	m.IncJobRetry("STEP_FAILED")
	m.AddJobLeaseExpired(3)
	m.SetJobBacklog(5)
	m.SetJobsActive("QUEUED", 2)
	m.ObserveProxmoxRequest("clone", "ok", 500*time.Millisecond)
	m.SetIPPoolAddresses("seg-a", "FREE", 10)
	m.SetInstances("READY", "2026.08.1", "pve01", 4)
	m.IncIdentityDuplicate()
	m.ObserveValidation("identity", "PASS", "ID-001")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Handler() status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(body)

	wantSubstrings := []string{
		`vmf_jobs_total{operation="PROVISION",result="success"} 1`,
		`vmf_state_duration_seconds_count{state="CLONING"} 1`,
		`vmf_job_retries_total{error_code="STEP_FAILED"} 1`,
		`vmf_job_lease_expired_total 3`,
		`vmf_job_backlog 5`,
		`vmf_jobs_active{state="QUEUED"} 2`,
		`vmf_pve_api_requests_total{operation="clone",status="ok"} 1`,
		`vmf_ip_pool_addresses{segment="seg-a",state="FREE"} 10`,
		`vmf_instances{pve_node="pve01",state="READY",template_version="2026.08.1"} 4`,
		`vmf_identity_duplicates_total 1`,
		`vmf_validation_total{result="PASS",rule_id="ID-001",type="identity"} 1`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(text, want) {
			t.Errorf("metrics output missing %q\nfull output:\n%s", want, text)
		}
	}
}
