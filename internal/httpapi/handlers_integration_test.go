package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/template"
)

// withPrincipal gắn principal đã "xác thực" vào context của request —
// bỏ qua AuthMiddleware (đã có unit test riêng ở auth_test.go), test
// thẳng handler+repository thật ở đây.
func withPrincipal(r *http.Request, role Role) *http.Request {
	ctx := context.WithValue(r.Context(), principalCtxKey{}, Principal{Subject: "test-user", Role: role})
	return r.WithContext(ctx)
}

func newJSONRequest(method, target string, body []byte) *http.Request {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestTemplateHandlers_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	clusterID := seedClusterForHTTP(ctx, t, db)
	repo := template.NewRepository(db)
	h := &TemplateHandlers{Templates: repo, DB: db, Idem: storage.NewIdempotencyRepository(db)}

	family := uniqueKey(t) + "-family"
	createBody, _ := json.Marshal(map[string]any{
		"name": family + "-tpl", "family": family, "version": "2026.08.1",
		"pve_cluster_id": clusterID, "pve_node": "pve01", "pve_template_vmid": 9101,
		"source_checksum": "deadbeef",
	})
	req := newJSONRequest("POST", "/v1/templates", createBody)
	req.Header.Set("Idempotency-Key", uniqueKey(t))
	req = withPrincipal(req, RoleAdmin)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created templateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.State != "DRAFT" {
		t.Errorf("State = %s, want DRAFT", created.State)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM vm_templates WHERE id = $1`, created.ID)
	})

	getReq := httptest.NewRequest("GET", "/v1/templates/"+created.ID, nil)
	getReq.SetPathValue("id", created.ID)
	getReq = withPrincipal(getReq, RoleViewer)
	getRec := httptest.NewRecorder()
	h.Get(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("Get() status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	promoteReq := httptest.NewRequest("POST", "/v1/templates/"+created.ID+"/promote", nil)
	promoteReq.SetPathValue("id", created.ID)
	promoteReq.Header.Set("Idempotency-Key", uniqueKey(t))
	promoteReq = withPrincipal(promoteReq, RoleAdmin)
	promoteRec := httptest.NewRecorder()
	h.Promote(promoteRec, promoteReq)
	if promoteRec.Code != http.StatusAccepted {
		t.Fatalf("Promote() status = %d, body = %s", promoteRec.Code, promoteRec.Body.String())
	}
	var promoted templateResponse
	if err := json.Unmarshal(promoteRec.Body.Bytes(), &promoted); err != nil {
		t.Fatalf("decode promote response: %v", err)
	}
	if promoted.State != "CANDIDATE" {
		t.Errorf("State after promote = %s, want CANDIDATE", promoted.State)
	}

	listReq := httptest.NewRequest("GET", "/v1/templates?family="+family, nil)
	listReq = withPrincipal(listReq, RoleViewer)
	listRec := httptest.NewRecorder()
	h.List(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("List() status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listResp listEnvelope[templateResponse]
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID != created.ID {
		t.Fatalf("List() items = %+v, want exactly the created template", listResp.Items)
	}
}

func TestInstanceHandlers_CreateGetEvidence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedActiveTemplateForHTTP(ctx, t, db)
	segmentID := seedSegmentForHTTP(ctx, t, db)

	h := &InstanceHandlers{
		Instances: instance.NewRepository(db), Templates: template.NewRepository(db),
		Segments: ipam.NewSegmentRepository(db), IPAM: ipam.NewRepository(db),
		Hostnames: ipam.NewHostnameRepository(db), Jobs: jobs.NewRepository(db),
		Runs: storage.NewValidationRunRepository(db), AuditR: audit.NewReader(db), AuditW: audit.NewWriter(),
		DB: db, Idem: storage.NewIdempotencyRepository(db),
	}

	createBody, _ := json.Marshal(map[string]any{
		"template_id": templateID, "network_segment_id": segmentID, "egress_policy_id": "policy-test",
	})
	req := newJSONRequest("POST", "/v1/instances", createBody)
	req.Header.Set("Idempotency-Key", uniqueKey(t))
	req = withPrincipal(req, RoleOperator)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Create() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var accepted acceptedJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if accepted.State != "REQUESTED" {
		t.Errorf("State = %s, want REQUESTED", accepted.State)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM provisioning_jobs WHERE instance_id = $1`, accepted.InstanceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM vm_instances WHERE id = $1`, accepted.InstanceID)
	})

	getReq := httptest.NewRequest("GET", "/v1/instances/"+accepted.InstanceID, nil)
	getReq.SetPathValue("id", accepted.InstanceID)
	getReq = withPrincipal(getReq, RoleViewer)
	getRec := httptest.NewRecorder()
	h.Get(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("Get() status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	var got instanceResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.CurrentJobID == nil || *got.CurrentJobID != accepted.JobID {
		t.Errorf("CurrentJobID = %v, want %s", got.CurrentJobID, accepted.JobID)
	}

	evReq := httptest.NewRequest("GET", "/v1/instances/"+accepted.InstanceID+"/evidence", nil)
	evReq.SetPathValue("id", accepted.InstanceID)
	evReq = withPrincipal(evReq, RoleViewer)
	evRec := httptest.NewRecorder()
	h.Evidence(evRec, evReq)
	if evRec.Code != http.StatusOK {
		t.Fatalf("Evidence() status = %d, body = %s", evRec.Code, evRec.Body.String())
	}
	var evResp listEnvelope[validationRunResponse]
	if err := json.Unmarshal(evRec.Body.Bytes(), &evResp); err != nil {
		t.Fatalf("decode evidence response: %v", err)
	}
	if len(evResp.Items) != 0 {
		t.Errorf("Evidence() items = %+v, want empty (chua qua validation nao)", evResp.Items)
	}
}

func TestInstanceHandlers_Quarantine_RequiresReason(t *testing.T) {
	db := openTestDB(t)
	h := &InstanceHandlers{
		Instances: instance.NewRepository(db), DB: db, Idem: storage.NewIdempotencyRepository(db),
		AuditW: audit.NewWriter(),
	}

	req := newJSONRequest("POST", "/v1/instances/x/quarantine", []byte(`{"reason":"ab"}`))
	req.Header.Set("Idempotency-Key", uniqueKey(t))
	req.SetPathValue("id", "00000000-0000-0000-0000-000000000000")
	req = withPrincipal(req, RoleOperator)
	rec := httptest.NewRecorder()
	h.Quarantine(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Quarantine() with too-short reason status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestJobHandlers_Retry(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedActiveTemplateForHTTP(ctx, t, db)
	instanceID := seedInstanceForHTTP(ctx, t, db, templateID)
	jobID := seedFailedJobForHTTP(ctx, t, db, instanceID)

	h := &JobHandlers{Jobs: jobs.NewRepository(db), AuditR: audit.NewReader(db), DB: db, Idem: storage.NewIdempotencyRepository(db)}

	req := newJSONRequest("POST", "/v1/jobs/"+jobID+"/retry", []byte(`{"reason":"transient PVE timeout"}`))
	req.Header.Set("Idempotency-Key", uniqueKey(t))
	req.SetPathValue("id", jobID)
	req = withPrincipal(req, RoleOperator)
	rec := httptest.NewRecorder()
	h.Retry(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Retry() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result acceptedJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if result.State != "QUEUED" {
		t.Errorf("State = %s, want QUEUED", result.State)
	}
}

func TestSegmentHandlers_CreateAndList(t *testing.T) {
	db := openTestDB(t)
	h := &SegmentHandlers{Segments: ipam.NewSegmentRepository(db), DB: db, Idem: storage.NewIdempotencyRepository(db)}

	name := uniqueKey(t) + "-segment"
	body, _ := json.Marshal(map[string]any{"name": name, "cidr": "10.61.0.0/24", "gateway": "10.61.0.1", "bridge": "vmbr1"})
	req := newJSONRequest("POST", "/v1/network-segments", body)
	req.Header.Set("Idempotency-Key", uniqueKey(t))
	req = withPrincipal(req, RoleAdmin)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created segmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM network_segments WHERE id = $1`, created.ID)
	})

	listReq := httptest.NewRequest("GET", "/v1/network-segments", nil)
	listReq = withPrincipal(listReq, RoleViewer)
	listRec := httptest.NewRecorder()
	h.List(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("List() status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listResp listEnvelope[segmentResponse]
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	found := false
	for _, s := range listResp.Items {
		if s.ID == created.ID {
			found = true
			if s.Capacity.Total != 0 {
				t.Errorf("Capacity.Total = %d, want 0 (khong co ip_allocations nao)", s.Capacity.Total)
			}
		}
	}
	if !found {
		t.Fatalf("List() did not include created segment %s", created.ID)
	}
}

// --- shared seed helpers cho httpapi integration tests ---
// openTestDB/uniqueKey đã định nghĩa ở idempotency_integration_test.go
// (cùng package), dùng lại ở đây thay vì khai báo trùng.

func seedClusterForHTTP(ctx context.Context, t *testing.T, db *storage.DB) string {
	t.Helper()
	var clusterID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO pve_clusters (name, base_url, secret_ref) VALUES ($1, $2, $3) RETURNING id
	`, uniqueKey(t)+"-cluster", "https://pve.test:8006/api2/json", "secret_ref_test").Scan(&clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_templates WHERE pve_cluster_id = $1`, clusterID)
		_, _ = db.ExecContext(ctx, `DELETE FROM pve_clusters WHERE id = $1`, clusterID)
	})
	return clusterID
}

func seedActiveTemplateForHTTP(ctx context.Context, t *testing.T, db *storage.DB) string {
	t.Helper()
	clusterID := seedClusterForHTTP(ctx, t, db)
	var templateID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_templates
			(name, family, version, os_family, os_version, architecture,
			 pve_cluster_id, pve_node, pve_template_vmid, source_checksum, state)
		VALUES ($1, $2, '2026.01.1', 'ubuntu', '22.04', 'amd64', $3, 'pve01', 9200, 'deadbeef', 'ACTIVE')
		RETURNING id
	`, uniqueKey(t)+"-tpl", uniqueKey(t)+"-family", clusterID).Scan(&templateID); err != nil {
		t.Fatalf("seed active template: %v", err)
	}
	return templateID
}

func seedSegmentForHTTP(ctx context.Context, t *testing.T, db *storage.DB) string {
	t.Helper()
	var segmentID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO network_segments (name, cidr, gateway, bridge) VALUES ($1, '10.62.0.0/24', '10.62.0.1', 'vmbr1')
		RETURNING id
	`, uniqueKey(t)+"-segment").Scan(&segmentID); err != nil {
		t.Fatalf("seed segment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM network_segments WHERE id = $1`, segmentID)
	})
	return segmentID
}

func seedInstanceForHTTP(ctx context.Context, t *testing.T, db *storage.DB, templateID string) string {
	t.Helper()
	var instanceID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO vm_instances (logical_name, hostname, template_id) VALUES ($1, $2, $3) RETURNING id
	`, uniqueKey(t)+"-logical", uniqueKey(t)+"-host", templateID).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM provisioning_jobs WHERE instance_id = $1`, instanceID)
		_, _ = db.ExecContext(ctx, `DELETE FROM vm_instances WHERE id = $1`, instanceID)
	})
	return instanceID
}

func seedFailedJobForHTTP(ctx context.Context, t *testing.T, db *storage.DB, instanceID string) string {
	t.Helper()
	var jobID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO provisioning_jobs (instance_id, operation, state, checkpoint, error_code, error_message)
		VALUES ($1, 'PROVISION', 'FAILED', 'CLONING', 'TEST_ERROR', 'simulated')
		RETURNING id
	`, instanceID).Scan(&jobID); err != nil {
		t.Fatalf("seed failed job: %v", err)
	}
	return jobID
}
