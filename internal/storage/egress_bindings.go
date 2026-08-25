package storage

import (
	"context"
	"fmt"
	"time"
)

// EgressBinding là VIEW SUY RA (không phải bảng riêng) từ
// provisioning_jobs.checkpoint_data — NetworkBindingHandler
// (internal/stateengine) chưa ghi bảng egress_bindings riêng (gap đã
// biết từ P0-05, để lại tới khi có pgw.Adapter thật ở P0-04), nên đây
// là cách tốt nhất hiện có để phục vụ GET /v1/egress-bindings (UI
// integration, API_UI_Gap_Register mục 3.3) mà không cần thêm bảng
// mới/sửa handler đã verify thật trước khi có PGW thật để biết đúng
// schema cần.
type EgressBinding struct {
	InstanceID   string
	LogicalName  string
	Hostname     string
	PGWClientID  string
	PGWMappingID string
	// Generation là desired_generation ActivateMapping trả về lúc bind
	// (Phần VII mục 4) — 0 nếu chưa từng activate.
	Generation int64
	BoundAt    time.Time // job.created_at, thời điểm job tạo binding này
}

// ListEgressBindings trả mọi instance có checkpoint_data chứa
// pgw_mapping_id (đã qua NETWORK_BINDING) — một dòng cho MỖI instance,
// lấy job MỚI NHẤT của instance đó có mapping (DISTINCT ON), lọc theo
// instanceID nếu khác rỗng.
func (db *DB) ListEgressBindings(ctx context.Context, instanceID string) ([]EgressBinding, error) {
	query := `
		SELECT DISTINCT ON (i.id)
			i.id, i.logical_name, i.hostname,
			COALESCE(j.checkpoint_data->>'pgw_client_id', ''),
			COALESCE(j.checkpoint_data->>'pgw_mapping_id', ''),
			COALESCE((j.checkpoint_data->>'desired_generation')::bigint, 0),
			j.created_at
		FROM vm_instances i
		JOIN provisioning_jobs j ON j.instance_id = i.id
		WHERE j.checkpoint_data ? 'pgw_mapping_id'
		  AND j.checkpoint_data->>'pgw_mapping_id' != ''
	`
	args := []any{}
	if instanceID != "" {
		args = append(args, instanceID)
		query += fmt.Sprintf(" AND i.id = $%d", len(args))
	}
	query += ` ORDER BY i.id, j.created_at DESC`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list egress bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EgressBinding
	for rows.Next() {
		var b EgressBinding
		if err := rows.Scan(&b.InstanceID, &b.LogicalName, &b.Hostname, &b.PGWClientID, &b.PGWMappingID, &b.Generation, &b.BoundAt); err != nil {
			return nil, fmt.Errorf("storage: scan egress binding: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate egress bindings: %w", err)
	}
	return out, nil
}
