package stateengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/guest"
	"github.com/Chinsusu/vm-factory/internal/ipam"
	"github.com/Chinsusu/vm-factory/internal/observability"
	"github.com/Chinsusu/vm-factory/internal/pgw"
	"github.com/Chinsusu/vm-factory/internal/proxmox"
	"github.com/Chinsusu/vm-factory/internal/storage"
	"github.com/Chinsusu/vm-factory/internal/validation"
)

// ValidatingIdentityHandler thực hiện 4.8 VALIDATING_IDENTITY →
// VALIDATING_EGRESS (Phần V) — chạy ID-xxx/NET-xxx rules (Phần VIII
// mục 4-5) trên guest facts thu thập qua QGA exec, lưu evidence
// (identity_observations + validation_runs) ATOMIC với transition qua
// TransitionResult.PersistEvidence.
//
// Đăng ký cho domain.InstanceWaitingGuest (Engine.Register nhận state
// NGUỒN handler đưa instance RA KHỎI, không phải state đích — xem
// engine.go) vì đây là bước tiếp theo sau WAITING_GUEST.
//
// FAIL (bất kỳ BLOCK hoặc WARN rule nào fail) chuyển thẳng QUARANTINED
// trong CHÍNH transition này (không trả error) — evidence của một lần
// FAIL chính là thứ cần audit nhất (Phần VIII mục 1), nên phải được ghi
// trong nhánh THÀNH CÔNG của Execute (Engine.Step chỉ chạy
// PersistEvidence khi Execute không lỗi). PGW mapping được suspend +
// IP allocation được đánh dấu quarantined best-effort trước khi trả về
// (Phần VIII mục 9: "suspend PGW mapping" là bước đầu của quarantine
// action) — không dừng VM (Quarantine.Execute cũng mặc định stopVM=false).
type ValidatingIdentityHandler struct {
	PGW      pgw.Adapter
	Facts    *guest.FactsCollector
	Digester *validation.IdentityDigester
	Identity *storage.IdentityRepository
	Runs     *storage.ValidationRunRepository
	IPAM     *ipam.Repository
	Segments *ipam.SegmentRepository
	// Metrics, khi khác nil, ghi vmf_validation_total/vmf_identity_duplicates_total
	// (tài liệu 09 mục 3.3) — optional, nil an toàn.
	Metrics *observability.Metrics

	FactsTimeout              time.Duration
	BlockRetiredDuplicate     bool
	RequireSingleNIC          bool
	RequireSingleDefaultRoute bool
}

// Execute implement TransitionHandler.
func (h *ValidatingIdentityHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp networkBindingCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.VMID == 0 {
		return TransitionResult{}, fmt.Errorf("validating_identity: missing placement checkpoint")
	}
	ref := proxmox.VMRef{Node: cp.Node, VMID: cp.VMID}

	timeout := h.FactsTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	facts, err := h.Facts.Collect(ctx, ref, timeout)
	if err != nil {
		// Collector loi = UNKNOWN (Phan VIII muc 8: "UNKNOWN khong duoc
		// chuyen READY") - tra error de job retry theo backoff thay vi
		// tu quyet dinh QUARANTINED chi vi mot lan collect that bai tam
		// thoi (vd QGA chua san sang).
		return TransitionResult{}, fmt.Errorf("validating_identity: collect guest facts: %w", err)
	}

	alloc, err := h.IPAM.Get(ctx, cp.IPAllocationID)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("validating_identity: load ip allocation: %w", err)
	}
	segment, err := h.Segments.Get(ctx, alloc.SegmentID)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("validating_identity: load network segment: %w", err)
	}

	digest := h.Digester.Digest(facts.MachineID)
	machineDup, err := h.Identity.FindDuplicateMachineDigest(ctx, digest, tctx.Instance.ID)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("validating_identity: find duplicate machine digest: %w", err)
	}

	canonicalFP := facts.CanonicalSSHFingerprint()
	var sshDup []storage.DuplicateMatch
	if canonicalFP != "" {
		sshDup, err = h.Identity.FindDuplicateSSHFingerprint(ctx, canonicalFP, tctx.Instance.ID)
		if err != nil {
			return TransitionResult{}, fmt.Errorf("validating_identity: find duplicate ssh fingerprint: %w", err)
		}
	}

	var expectedMACs []string
	if cp.NIC0MAC != "" {
		expectedMACs = []string{cp.NIC0MAC}
	}

	checks := validation.EvaluateIdentityAndNetwork(validation.IdentityInput{
		Facts:                     facts,
		MachineIDDigest:           digest,
		MachineIDDuplicates:       machineDup,
		SSHFingerprintDuplicates:  sshDup,
		BlockRetiredDuplicate:     h.BlockRetiredDuplicate,
		ExpectedHostname:          tctx.Instance.Hostname,
		ExpectedMACAddresses:      expectedMACs,
		ExpectedIPv4:              alloc.Address,
		ExpectedGatewayV4:         segment.Gateway,
		RequireSingleNIC:          h.RequireSingleNIC,
		RequireSingleDefaultRoute: h.RequireSingleDefaultRoute,
		DenyIPv6DefaultRoute:      segment.IPv6Policy == "deny",
	})

	evidence, result, err := validation.BuildEvidence(tctx.Instance.ID, checks)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("validating_identity: build evidence: %w", err)
	}
	for _, c := range checks {
		h.Metrics.ObserveValidation("identity", c.Result, c.RuleID)
	}
	if len(machineDup) > 0 || len(sshDup) > 0 {
		h.Metrics.IncIdentityDuplicate()
	}

	safeFacts, err := json.Marshal(redactFacts(facts))
	if err != nil {
		return TransitionResult{}, fmt.Errorf("validating_identity: marshal redacted facts: %w", err)
	}

	nextState := domain.InstanceValidatingEgress
	if result != domain.ValidationPass {
		nextState = domain.InstanceQuarantined
		if cp.PGWMappingID != "" {
			_ = h.PGW.SuspendMapping(ctx, cp.PGWMappingID) // best-effort, xem doc comment struct
		}
		if cp.IPAllocationID != "" {
			_ = h.IPAM.MarkQuarantined(ctx, cp.IPAllocationID)
		}
	}

	startedAt := facts.CollectedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	data, _ := json.Marshal(cp)
	jobID := tctx.Job.ID
	instanceID := tctx.Instance.ID
	instanceGeneration := tctx.Instance.Generation
	return TransitionResult{
		NextState:      nextState,
		CheckpointData: data,
		PersistEvidence: func(pctx context.Context, tx *sql.Tx) error {
			if _, err := h.Identity.Create(pctx, tx, domain.IdentityObservation{
				InstanceID:          instanceID,
				Generation:          instanceGeneration,
				MachineIDDigest:     digest,
				SSHHostFingerprint:  canonicalFP,
				CloudInitInstanceID: stringPtrIfNonEmpty(facts.CloudInitInstanceID),
				Hostname:            facts.Hostname,
				MACAddresses:        facts.MACAddresses,
				IPAddresses:         facts.IPv4Addresses,
				BootID:              stringPtrIfNonEmpty(facts.BootID),
				Facts:               safeFacts,
			}); err != nil {
				return fmt.Errorf("create identity observation: %w", err)
			}
			if _, err := h.Runs.Create(pctx, tx, domain.ValidationRun{
				InstanceID:     instanceID,
				JobID:          &jobID,
				Type:           "identity",
				Result:         result,
				RulesetVersion: validation.RulesetVersion,
				Evidence:       evidence,
				StartedAt:      startedAt,
			}); err != nil {
				return fmt.Errorf("create validation run: %w", err)
			}
			return nil
		},
	}, nil
}

// ValidatingEgressHandler thực hiện 4.9 VALIDATING_EGRESS →
// APPLYING_WORKLOAD (Phần V) — chạy EGR-xxx rules (Phần VIII mục 6)
// trên egress proof đọc qua pgw.Adapter.EgressProof.
//
// KHÔNG có handler đăng ký cho domain.InstanceApplyingWorkload — bước
// đó thuộc epic P0-08 (Workload Adapter) chưa triển khai. Engine.Step
// sẽ lỗi ErrInvalidTransition nếu một job tới APPLYING_WORKLOAD trước
// khi P0-08 đăng ký handler cho state đó — gap đã biết, chấp nhận được
// vì P0-08 là epic kế tiếp theo lộ trình "làm lần lượt".
type ValidatingEgressHandler struct {
	PGW     pgw.Adapter
	IPAM    *ipam.Repository
	Runs    *storage.ValidationRunRepository
	Metrics *observability.Metrics

	DenyIPv6    bool
	ProofMaxAge time.Duration
}

// Execute implement TransitionHandler.
func (h *ValidatingEgressHandler) Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error) {
	var cp networkBindingCheckpoint
	if err := json.Unmarshal(tctx.CheckpointData, &cp); err != nil || cp.PGWClientID == "" || cp.PGWMappingID == "" {
		return TransitionResult{}, fmt.Errorf("validating_egress: missing pgw binding checkpoint")
	}

	evidence, err := h.PGW.EgressProof(ctx, cp.PGWClientID)
	if err != nil {
		// Loi doc proof = UNKNOWN, tra error de retry (cung ly do voi
		// ValidatingIdentityHandler o tren).
		return TransitionResult{}, fmt.Errorf("validating_egress: read egress proof: %w", err)
	}

	proofMaxAge := h.ProofMaxAge
	if proofMaxAge <= 0 {
		proofMaxAge = 5 * time.Minute
	}

	checks := validation.EvaluateEgress(validation.EgressInput{
		Evidence:          evidence,
		ExpectedMappingID: cp.PGWMappingID,
		DesiredGeneration: cp.DesiredGeneration,
		DenyIPv6:          h.DenyIPv6,
		ProofMaxAge:       proofMaxAge,
	})

	evidenceJSON, result, err := validation.BuildEvidence(tctx.Instance.ID, checks)
	if err != nil {
		return TransitionResult{}, fmt.Errorf("validating_egress: build evidence: %w", err)
	}
	for _, c := range checks {
		h.Metrics.ObserveValidation("egress", c.Result, c.RuleID)
	}

	nextState := domain.InstanceApplyingWorkload
	if result != domain.ValidationPass {
		nextState = domain.InstanceQuarantined
		_ = h.PGW.SuspendMapping(ctx, cp.PGWMappingID)
		if cp.IPAllocationID != "" {
			_ = h.IPAM.MarkQuarantined(ctx, cp.IPAllocationID)
		}
	}

	data, _ := json.Marshal(cp)
	jobID := tctx.Job.ID
	instanceID := tctx.Instance.ID
	return TransitionResult{
		NextState:      nextState,
		CheckpointData: data,
		PersistEvidence: func(pctx context.Context, tx *sql.Tx) error {
			if _, err := h.Runs.Create(pctx, tx, domain.ValidationRun{
				InstanceID:     instanceID,
				JobID:          &jobID,
				Type:           "egress",
				Result:         result,
				RulesetVersion: validation.RulesetVersion,
				Evidence:       evidenceJSON,
				StartedAt:      time.Now(),
			}); err != nil {
				return fmt.Errorf("create validation run: %w", err)
			}
			return nil
		},
	}, nil
}

// redactedGuestFacts là bản sao guest.Facts AN TOÀN để persist vào
// identity_observations.facts JSONB — cố ý KHÔNG có field MachineID
// (raw machine-id không bao giờ được lưu, Phần III mục 3, ADR-007;
// digest riêng đã lưu ở cột machine_id_digest).
type redactedGuestFacts struct {
	BootID                 string            `json:"boot_id"`
	Hostname               string            `json:"hostname"`
	CloudInitInstanceID    string            `json:"cloud_init_instance_id"`
	SSHHostKeyFingerprints map[string]string `json:"ssh_host_key_fingerprints"`
	MACAddresses           []string          `json:"mac_addresses"`
	IPv4Addresses          []string          `json:"ipv4_addresses"`
	GlobalIPv6Addresses    []string          `json:"global_ipv6_addresses"`
	OSRelease              string            `json:"os_release"`
	KernelVersion          string            `json:"kernel_version"`
	NICCount               int               `json:"nic_count"`
	DefaultRouteV4Count    int               `json:"default_route_v4_count"`
	DefaultRouteV6Count    int               `json:"default_route_v6_count"`
	DefaultGatewayV4       string            `json:"default_gateway_v4"`
}

func redactFacts(f guest.Facts) redactedGuestFacts {
	return redactedGuestFacts{
		BootID:                 f.BootID,
		Hostname:               f.Hostname,
		CloudInitInstanceID:    f.CloudInitInstanceID,
		SSHHostKeyFingerprints: f.SSHHostKeyFingerprints,
		MACAddresses:           f.MACAddresses,
		IPv4Addresses:          f.IPv4Addresses,
		GlobalIPv6Addresses:    f.GlobalIPv6Addresses,
		OSRelease:              f.OSRelease,
		KernelVersion:          f.KernelVersion,
		NICCount:               f.NICCount,
		DefaultRouteV4Count:    f.DefaultRouteV4Count,
		DefaultRouteV6Count:    f.DefaultRouteV6Count,
		DefaultGatewayV4:       f.DefaultGatewayV4,
	}
}

func stringPtrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
