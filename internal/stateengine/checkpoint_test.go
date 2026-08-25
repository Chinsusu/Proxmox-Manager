package stateengine

import (
	"encoding/json"
	"testing"
)

// TestCheckpointEmbedding_FlattensAndRoundTrips xac nhan gia dinh kien
// truc quan trong: cac checkpoint struct (reservingCheckpoint ->
// cloningCheckpoint -> configuringCheckpoint -> networkBindingCheckpoint)
// dung Go struct embedding se marshal/unmarshal JSON PHANG (khong long
// nhau duoi key rieng), de handler sau doc lai duoc field cua handler
// truoc ma khong can biet ten struct trung gian.
func TestCheckpointEmbedding_FlattensAndRoundTrips(t *testing.T) {
	nb := networkBindingCheckpoint{
		configuringCheckpoint: configuringCheckpoint{
			cloningCheckpoint: cloningCheckpoint{
				reservingCheckpoint: reservingCheckpoint{
					IPAllocationID: "alloc-1",
					VMID:           9101,
					Node:           "us-ny",
				},
				CloneTaskUPID: "UPID:clone",
			},
			ConfigTaskUPID: "UPID:config",
		},
		PGWClientID:  "client-1",
		PGWMappingID: "mapping-1",
	}

	data, err := json.Marshal(nb)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var flat map[string]any
	if err := json.Unmarshal(data, &flat); err != nil {
		t.Fatalf("Unmarshal to map error: %v", err)
	}
	for _, key := range []string{"ip_allocation_id", "vmid", "node", "clone_task_upid", "config_task_upid", "pgw_client_id", "pgw_mapping_id"} {
		if _, ok := flat[key]; !ok {
			t.Errorf("expected flattened key %q in JSON, got keys: %v", key, flat)
		}
	}

	// Doc lai bang mot fullCheckpoint (dung boi rollback/quarantine) -
	// phai lay duoc dung field du la struct khac hoan toan.
	var full fullCheckpoint
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatalf("Unmarshal to fullCheckpoint error: %v", err)
	}
	if full.IPAllocationID != "alloc-1" || full.VMID != 9101 || full.Node != "us-ny" ||
		full.PGWClientID != "client-1" || full.PGWMappingID != "mapping-1" {
		t.Errorf("fullCheckpoint sau unmarshal = %+v, thieu hoac sai field", full)
	}

	// Doc lai bang chinh cloningCheckpoint (nhu CloningHandler se lam
	// khi retry) - phai lay duoc field cua no du JSON co them field
	// tu cac struct "con" phia sau.
	var cloning cloningCheckpoint
	if err := json.Unmarshal(data, &cloning); err != nil {
		t.Fatalf("Unmarshal to cloningCheckpoint error: %v", err)
	}
	if cloning.VMID != 9101 || cloning.CloneTaskUPID != "UPID:clone" {
		t.Errorf("cloningCheckpoint sau unmarshal = %+v", cloning)
	}
}
