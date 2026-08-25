// Package jobs implement job lease (FOR UPDATE SKIP LOCKED), heartbeat,
// lease takeover theo Phần II mục 6.1. Dùng job_state riêng khỏi
// instance_state (xem docs 02/06 mục job state, sửa ở doc v1.1).
// Triển khai ở epic P0-01/P0-05.
package jobs
