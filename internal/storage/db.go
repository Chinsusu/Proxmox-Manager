// Package storage chứa PostgreSQL repository layer theo Phần VI — job
// lease, IPAM reservation, idempotency store. Source of truth cho
// desired/lifecycle state, theo nguyên tắc ở Phần VI mục 1.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // đăng ký driver "pgx" cho database/sql
)

// Execer là tập method tối thiểu để chạy statement mutation, thoả mãn cả
// *sql.DB và *sql.Tx — cho phép repository nhận một trong hai mà không
// ép caller phải tự quản lý transaction khi không cần.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// QueryRower là tập method tối thiểu để chạy query trả một row, thoả mãn
// cả *sql.DB và *sql.Tx.
type QueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB bọc *sql.DB, cấu hình pool theo config.DatabaseConfig
// (Phần II mục 15, appendices/vm-factory-config.example.yaml).
type DB struct {
	*sql.DB
}

// Open kết nối PostgreSQL qua driver pgx, áp dụng max_open_conns/
// max_idle_conns theo config. Không tự động chạy migration — migration
// là bước riêng trước khi start service (Phần VI mục 7, tài liệu 11 mục 2.7).
func Open(dsn string, maxOpenConns, maxIdleConns int) (*DB, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	return &DB{DB: sqlDB}, nil
}

// Ready implement httpapi.ReadyChecker (Ready() error) qua structural
// typing — storage không import internal/httpapi để tránh dependency
// ngược package layer.
func (d *DB) Ready() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return d.PingContext(ctx)
}

// WithTx chạy fn trong một transaction: begin, rollback nếu fn lỗi hoặc
// panic, commit nếu fn thành công. Dùng cho mọi mutation cần atomic
// nhiều statement (vd: reserve resource + audit event).
func WithTx(ctx context.Context, db *DB, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	err = fn(tx)
	return err
}
