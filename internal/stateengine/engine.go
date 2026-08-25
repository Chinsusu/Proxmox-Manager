// Package stateengine implement transition registry, checkpoint,
// retry/backoff, rollback, quarantine theo Phần V (VM Lifecycle State
// Machine) — thay script tuyến tính bằng reconciler có contract rõ
// (ADR-003).
package stateengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Chinsusu/vm-factory/internal/audit"
	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/instance"
	"github.com/Chinsusu/vm-factory/internal/jobs"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// TransitionContext là dữ liệu một TransitionHandler cần để thực hiện
// một bước chuyển tiếp.
type TransitionContext struct {
	Instance *domain.VMInstance
	Job      *domain.ProvisioningJob
	// CheckpointData là dữ liệu handler trước đã stash (Phần II mục
	// 6.2 — vd pve.vmid/clone_task, pgw.client_id), đọc lại để tiếp
	// tục idempotent thay vì gọi lại side effect đã thành công.
	CheckpointData json.RawMessage
	// SaveCheckpoint cho phép handler tự persist checkpoint_data NGAY
	// sau khi gọi một external mutation (vd Clone, Configure), TRƯỚC
	// khi poll task — đúng External Side-effect Pattern 5 bước (Phần
	// II mục 7: "3. Persist external task/reference" phải xảy ra
	// trước "4. Poll", không gộp chung vào kết quả cuối cùng). Không
	// gọi SaveCheckpoint thì nếu worker crash giữa lúc gọi API và lúc
	// Execute return, task reference bị mất — retry có thể gọi lại
	// side effect, tạo VM/mapping thứ hai.
	SaveCheckpoint func(ctx context.Context, data json.RawMessage) error
}

// TransitionResult là kết quả một bước chuyển tiếp thành công.
type TransitionResult struct {
	NextState domain.InstanceState
	// CheckpointData thay thế checkpoint_data hiện tại của job — handler
	// tự merge với CheckpointData đầu vào nếu cần giữ lại field cũ.
	CheckpointData json.RawMessage
	// PVEPlacement, khi khác nil, được Engine ghi vào instance trong
	// cùng transaction với state transition (vd sau khi Clone thành
	// công). Handler không tự chạm DB — chỉ trả dữ liệu, Engine chịu
	// trách nhiệm persist, giữ toàn bộ ghi DB tập trung một chỗ.
	PVEPlacement *PVEPlacement
	// PersistEvidence, khi khác nil, được Engine gọi TRONG CÙNG
	// transaction với state transition (vd ghi identity_observations/
	// validation_runs ở ValidatingIdentityHandler/ValidatingEgressHandler,
	// P0-07) — evidence không được tồn tại tách rời khỏi transition mà
	// nó chứng minh (Phần V mục 1: "mọi transition có checkpoint + audit
	// event"; Phần VIII mục 1: evidence phải audit được).
	PersistEvidence func(ctx context.Context, tx *sql.Tx) error
}

// PVEPlacement là placement Proxmox quan sát được sau Clone.
type PVEPlacement struct {
	ClusterID string
	Node      string
	VMID      int
}

// TransitionHandler thực hiện MỘT bước chuyển tiếp từ state hiện tại
// của instance sang state kế tiếp — đúng nguyên tắc trung tâm ở Phần V
// mục 1: "Không cho phép code cập nhật state tùy ý. Mọi transition
// phải qua một transition handler".
type TransitionHandler interface {
	Execute(ctx context.Context, tctx *TransitionContext) (TransitionResult, error)
}

// Engine điều phối transition registry, ghi checkpoint + audit event
// trong cùng transaction cho mỗi bước (Phần V mục 1, mục 10).
type Engine struct {
	registry  map[domain.InstanceState]TransitionHandler
	instances *instance.Repository
	jobsRepo  *jobs.Repository
	db        *storage.DB
	audit     *audit.Writer
}

// NewEngine tạo Engine gắn với repository/DB cần thiết.
func NewEngine(db *storage.DB, instances *instance.Repository, jobsRepo *jobs.Repository, auditWriter *audit.Writer) *Engine {
	return &Engine{
		registry:  make(map[domain.InstanceState]TransitionHandler),
		instances: instances,
		jobsRepo:  jobsRepo,
		db:        db,
		audit:     auditWriter,
	}
}

// Register gắn handler chịu trách nhiệm đưa instance RA KHỎI state
// `from`. Handler cho state nào chưa Register thì Step trả lỗi rõ
// ràng thay vì im lặng đứng yên.
func (e *Engine) Register(from domain.InstanceState, h TransitionHandler) {
	e.registry[from] = h
}

// Step thực hiện MỘT bước chuyển tiếp cho job đã được worker lease
// (caller gọi jobs.Repository.Claim trước). Handler chạy NGOÀI
// transaction (vì có thể gọi external API mất thời gian — không giữ
// transaction DB mở qua network call); kết quả (instance state mới +
// checkpoint) được ghi trong MỘT transaction cùng audit event, đúng
// External Side-effect Pattern (Phần II mục 7: persist observed result
// + audit là bước cuối, atomic).
func (e *Engine) Step(ctx context.Context, job *domain.ProvisioningJob) (domain.InstanceState, error) {
	inst, err := e.instances.Get(ctx, job.InstanceID)
	if err != nil {
		return "", fmt.Errorf("stateengine: load instance: %w", err)
	}

	handler, ok := e.registry[inst.State]
	if !ok {
		return "", fmt.Errorf("%w: no transition handler registered for state %s (instance %s)", domain.ErrInvalidTransition, inst.State, inst.ID)
	}

	currentState := inst.State
	result, err := handler.Execute(ctx, &TransitionContext{
		Instance:       inst,
		Job:            job,
		CheckpointData: job.CheckpointData,
		SaveCheckpoint: func(saveCtx context.Context, data json.RawMessage) error {
			return e.jobsRepo.UpdateCheckpoint(saveCtx, e.db, job.ID, currentState, data)
		},
	})
	if err != nil {
		return "", err
	}

	err = storage.WithTx(ctx, e.db, func(tx *sql.Tx) error {
		if err := e.instances.UpdateState(ctx, tx, inst.ID, result.NextState); err != nil {
			return fmt.Errorf("update instance state: %w", err)
		}
		if result.PVEPlacement != nil {
			p := result.PVEPlacement
			if err := e.instances.SetPVEPlacement(ctx, tx, inst.ID, p.ClusterID, p.Node, p.VMID); err != nil {
				return fmt.Errorf("set pve placement: %w", err)
			}
		}
		if err := e.jobsRepo.UpdateCheckpoint(ctx, tx, job.ID, result.NextState, result.CheckpointData); err != nil {
			return fmt.Errorf("update checkpoint: %w", err)
		}
		if result.PersistEvidence != nil {
			if err := result.PersistEvidence(ctx, tx); err != nil {
				return fmt.Errorf("persist evidence: %w", err)
			}
		}
		metadata, _ := json.Marshal(map[string]string{"from": string(inst.State), "to": string(result.NextState)})
		if err := e.audit.Append(ctx, tx, domain.AuditEvent{
			ActorType:    "system",
			ActorID:      "state-engine",
			Action:       "state_transition",
			ResourceType: "vm_instance",
			ResourceID:   inst.ID,
			Metadata:     metadata,
		}); err != nil {
			return fmt.Errorf("audit: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("stateengine: persist transition: %w", err)
	}

	return result.NextState, nil
}
