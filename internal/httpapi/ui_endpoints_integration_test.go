package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// ui_endpoints_integration_test.go — smoke test cho 6 endpoint mới nối
// UI console (API_UI_Gap_Register v1.0): xác nhận route→handler→
// repository→response thật chạy đúng, không lặp lại toàn bộ test theo
// filter đã có ở tầng repository (internal/jobs, internal/storage,
// internal/audit).

func TestJobHandlers_List(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedActiveTemplateForHTTP(ctx, t, db)
	instanceID := seedInstanceForHTTP(ctx, t, db, templateID)
	jobID := seedFailedJobForHTTP(ctx, t, db, instanceID)

	h := &JobHandlers{Jobs: jobs.NewRepository(db), AuditR: audit.NewReader(db), DB: db, Idem: storage.NewIdempotencyRepository(db)}

	req := httptest.NewRequest("GET", "/v1/jobs?instance_id="+instanceID, nil)
	req = withPrincipal(req, RoleViewer)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("List() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp listEnvelope[jobResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != jobID {
		t.Fatalf("List() items = %+v, want exactly job %s", resp.Items, jobID)
	}
}

func TestIPPoolHandlers_List(t *testing.T) {
	db := openTestDB(t)
	segmentID := seedSegmentForHTTP(context.Background(), t, db)

	h := &IPPoolHandlers{Segments: ipam.NewSegmentRepository(db)}
	req := httptest.NewRequest("GET", "/v1/ip-pools?segment_id="+segmentID, nil)
	req = withPrincipal(req, RoleViewer)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("List() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp listEnvelope[ipPoolResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].SegmentID != segmentID {
		t.Fatalf("List() items = %+v, want exactly segment %s", resp.Items, segmentID)
	}
}

func TestValidationHandlers_List(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedActiveTemplateForHTTP(ctx, t, db)
	instanceID := seedInstanceForHTTP(ctx, t, db, templateID)

	runsRepo := storage.NewValidationRunRepository(db)
	created, err := runsRepo.Create(ctx, db, domain.ValidationRun{
		InstanceID: instanceID, Type: "identity", Result: domain.ValidationPass, RulesetVersion: "v1",
	})
	if err != nil {
		t.Fatalf("seed validation run: %v", err)
	}

	h := &ValidationHandlers{Runs: runsRepo}
	req := httptest.NewRequest("GET", "/v1/validations?instance_id="+instanceID, nil)
	req = withPrincipal(req, RoleViewer)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("List() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp listEnvelope[validationRunResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != created.ID {
		t.Fatalf("List() items = %+v, want exactly run %s", resp.Items, created.ID)
	}
}

func TestAuditEventHandlers_List(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	resourceID := uniqueKey(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM audit_events WHERE resource_id = $1`, resourceID)
	})

	writer := audit.NewWriter()
	if err := writer.Append(ctx, db, domain.AuditEvent{
		ActorType: "operator", ActorID: "alice", Action: "quarantine", ResourceType: "vm_instance", ResourceID: resourceID,
	}); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}

	h := &AuditEventHandlers{AuditR: audit.NewReader(db)}
	req := httptest.NewRequest("GET", "/v1/audit-events?resource_id="+resourceID, nil)
	req = withPrincipal(req, RoleViewer)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("List() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp listEnvelope[auditEventResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Action != "quarantine" {
		t.Fatalf("List() items = %+v, want exactly the quarantine event", resp.Items)
	}
}

func TestEgressBindingHandlers_List(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	templateID := seedActiveTemplateForHTTP(ctx, t, db)
	instanceID := seedInstanceForHTTP(ctx, t, db, templateID)

	cpData, _ := json.Marshal(map[string]any{"pgw_client_id": "client-1", "pgw_mapping_id": "mapping-1", "desired_generation": 3})
	jobsRepo := jobs.NewRepository(db)
	job, err := jobsRepo.Create(ctx, db, instanceID, domain.JobOpProvision, domain.InstanceNetworkBinding)
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := jobsRepo.UpdateCheckpoint(ctx, db, job.ID, domain.InstanceNetworkBinding, cpData); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	h := &EgressBindingHandlers{DB: db, Runs: storage.NewValidationRunRepository(db)}
	req := httptest.NewRequest("GET", "/v1/egress-bindings?instance_id="+instanceID, nil)
	req = withPrincipal(req, RoleViewer)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("List() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp listEnvelope[egressBindingResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].PGWMappingID != "mapping-1" {
		t.Fatalf("List() items = %+v, want exactly mapping-1", resp.Items)
	}
	if resp.Items[0].State != "PENDING" {
		t.Errorf("State = %s, want PENDING (chua co validation run egress nao)", resp.Items[0].State)
	}
}

func TestAlertHandlers_ListAndAcknowledge(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	fp := uniqueKey(t)
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM alerts WHERE fingerprint = $1`, fp) })

	alertsRepo := storage.NewAlertRepository(db)
	if err := alertsRepo.Upsert(ctx, domain.Alert{
		Fingerprint: fp, Severity: "warning", ResourceType: "system", ResourceID: "backlog", Title: "test",
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	h := &AlertHandlers{Alerts: alertsRepo}
	req := httptest.NewRequest("GET", "/v1/alerts?resource_type=system", nil)
	req = withPrincipal(req, RoleViewer)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != 200 {
		t.Fatalf("List() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listResp listEnvelope[alertResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var id string
	for _, a := range listResp.Items {
		if a.Status == "firing" && a.ResourceType == "system" && a.Title == "test" {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatalf("List() did not include seeded alert among %+v", listResp.Items)
	}

	ackReq := newJSONRequest("POST", "/v1/alerts/"+id+"/acknowledge", []byte(`{"note":"investigating"}`))
	ackReq.SetPathValue("id", id)
	ackReq = withPrincipal(ackReq, RoleOperator)
	ackRec := httptest.NewRecorder()
	h.Acknowledge(ackRec, ackReq)
	if ackRec.Code != 200 {
		t.Fatalf("Acknowledge() status = %d, body = %s", ackRec.Code, ackRec.Body.String())
	}
	var ackResp alertResponse
	if err := json.Unmarshal(ackRec.Body.Bytes(), &ackResp); err != nil {
		t.Fatalf("decode acknowledge: %v", err)
	}
	if ackResp.Status != "acknowledged" {
		t.Errorf("Status after Acknowledge() = %s, want acknowledged", ackResp.Status)
	}
	if ackResp.AcknowledgedNote == nil || *ackResp.AcknowledgedNote != "investigating" {
		t.Errorf("AcknowledgedNote = %v, want \"investigating\"", ackResp.AcknowledgedNote)
	}
}
