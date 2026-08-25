package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Chinsusu/vm-factory/internal/domain"
	"github.com/Chinsusu/vm-factory/internal/storage"
)

// IdempotencyTTL là thời gian giữ bản ghi idempotency trước khi hết hạn
// và cho phép tái sử dụng key (Phần VI mục 2.11).
const IdempotencyTTL = 24 * time.Hour

// RequireIdempotencyKey đọc header Idempotency-Key, trả 400 ErrorEnvelope
// nếu thiếu/sai độ dài (Phần II mục 10; OpenAPI IdempotencyKey param:
// minLength 8, maxLength 128).
func RequireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		WriteError(w, r, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key header must be 8-128 characters")
		return "", false
	}
	return key, true
}

// IdempotentWork là công việc thực sự cần làm một lần duy nhất cho mỗi
// (scope, key, request_hash) — chạy trong transaction, trả HTTP status +
// body để lưu lại cho lần replay sau, và resourceID (rỗng nếu không áp
// dụng) để trace idempotency_keys.resource_id.
type IdempotentWork func(ctx context.Context, tx *sql.Tx) (status int, body any, resourceID string, err error)

// RunIdempotent thực thi work đúng một lần cho mỗi (scope, key,
// request_hash); gọi lại với cùng key+hash sẽ replay response đã lưu
// thay vì chạy lại work (Phần II mục 10). Cùng key nhưng request_hash
// khác trả domain.ErrIdempotencyConflict — caller map về 409.
func RunIdempotent(ctx context.Context, db *storage.DB, idem *storage.IdempotencyRepository, scope, key string, requestBody []byte, work IdempotentWork) (status int, body any, replayed bool, err error) {
	hash := requestHash(requestBody)

	existing, getErr := idem.Get(ctx, scope, key)
	switch {
	case getErr == nil:
		if existing.RequestHash != hash {
			return 0, nil, false, domain.ErrIdempotencyConflict
		}
		var replayedBody any
		if len(existing.ResponseBody) > 0 {
			if err := json.Unmarshal(existing.ResponseBody, &replayedBody); err != nil {
				return 0, nil, false, fmt.Errorf("httpapi: unmarshal replayed idempotency body: %w", err)
			}
		}
		respStatus := http.StatusOK
		if existing.ResponseStatus != nil {
			respStatus = *existing.ResponseStatus
		}
		return respStatus, replayedBody, true, nil
	case errors.Is(getErr, domain.ErrNotFound):
		// chưa từng chạy - tiếp tục thực thi work bên dưới.
	default:
		return 0, nil, false, getErr
	}

	var respStatus int
	var respBody any
	var resourceID string
	txErr := storage.WithTx(ctx, db, func(tx *sql.Tx) error {
		var workErr error
		respStatus, respBody, resourceID, workErr = work(ctx, tx)
		if workErr != nil {
			return workErr
		}
		bodyJSON, marshalErr := json.Marshal(respBody)
		if marshalErr != nil {
			return fmt.Errorf("httpapi: marshal idempotent response: %w", marshalErr)
		}
		var resourceIDPtr *string
		if resourceID != "" {
			resourceIDPtr = &resourceID
		}
		return idem.Store(ctx, tx, domain.IdempotencyRecord{
			Scope: scope, Key: key, RequestHash: hash,
			ResponseStatus: &respStatus, ResponseBody: bodyJSON, ResourceID: resourceIDPtr,
			ExpiresAt: time.Now().Add(IdempotencyTTL),
		})
	})
	if txErr != nil {
		return 0, nil, false, txErr
	}
	return respStatus, respBody, false, nil
}

func requestHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
