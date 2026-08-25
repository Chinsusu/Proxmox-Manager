package pgw

import (
	"context"
	"strings"
	"testing"
)

func TestNoopAdapter_MarksEverythingSimulated(t *testing.T) {
	ctx := context.Background()
	n := NewNoopAdapter()

	client, err := n.CreateClient(ctx, ClientRequest{Name: "test-host"})
	if err != nil || client.ID == "" {
		t.Fatalf("CreateClient() = %+v, %v", client, err)
	}

	mapping, err := n.CreateMapping(ctx, MappingRequest{ClientID: client.ID})
	if err != nil || mapping.ID == "" {
		t.Fatalf("CreateMapping() = %+v, %v", mapping, err)
	}

	gen, err := n.ActivateMapping(ctx, mapping.ID)
	if err != nil || gen == 0 {
		t.Fatalf("ActivateMapping() = %v, %v", gen, err)
	}

	if err := n.SuspendMapping(ctx, mapping.ID); err != nil {
		t.Fatalf("SuspendMapping() error: %v", err)
	}
	if err := n.DeleteMapping(ctx, mapping.ID); err != nil {
		t.Fatalf("DeleteMapping() error: %v", err)
	}

	proof, err := n.EgressProof(ctx, client.ID)
	if err != nil {
		t.Fatalf("EgressProof() error: %v", err)
	}
	if !strings.Contains(proof.Result, "SIMULATED") || !strings.Contains(proof.Policy, "SIMULATED") {
		t.Fatalf("EgressProof() = %+v, ky vong Result/Policy chua ro 'SIMULATED' de khong nham voi proof that", proof)
	}
}
