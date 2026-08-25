// Package pgw định nghĩa Adapter (PGWAdapter theo Phần II mục 3.5) và
// NoopAdapter dùng chung giữa stateengine (P0-05) và validation (P0-07).
// PGW là external dependency (ADR-006); implementation thật gọi PGW API
// (Phần VII) — endpoint cần verify với PGW staging trước khi coi là
// dev-ready — thuộc epic P0-04, chưa triển khai.
package pgw
