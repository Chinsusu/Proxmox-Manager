# vm-factory

Control plane tạo, cấu hình, xác minh, vận hành và thu hồi Linux VM trên Proxmox, với PGW là external dependency. Xem [docs/README.md](docs/README.md) để đọc toàn bộ bộ hồ sơ thiết kế (10 tài liệu chính + tài liệu 11 Engineering Standards + appendices + ADR).

## Trạng thái

Đang ở epic **P0-00 Foundation**: repo skeleton, config loader, structured logger, request ID middleware, auth/RBAC baseline, health/ready endpoint, CI. Route nghiệp vụ (`/v1/instances`, `/v1/jobs`, ...) và domain logic thật thuộc các epic P0-01 trở đi — xem [docs/10_Operations_Runbook_and_Implementation_Roadmap_v1.0.md](docs/10_Operations_Runbook_and_Implementation_Roadmap_v1.0.md).

## Quan hệ giữa `docs/` và các file "sống"

`docs/appendices/vm-factory-openapi-v1.0.yaml` và `vm-factory-schema-v1.0.sql` là snapshot đã chốt của bộ hồ sơ thiết kế (đóng băng ở v1.2). Kể từ khi scaffold repo, hai file dưới đây là **bản sống, nguồn thật cho code**:

- [api/openapi.yaml](api/openapi.yaml) — contract API, sẽ tiếp tục cập nhật khi thêm route ở P0-09.
- [migrations/000001_init.up.sql](migrations/000001_init.up.sql) — chuyển thể từ schema blueprint, đã bỏ bảng `schema_migrations` tự định nghĩa vì `golang-migrate` tự quản lý bảng cùng tên (xem comment đầu file).

Khi domain contract đổi, sửa ở `api/`/`migrations/` trước, rồi đồng bộ ngược lại `docs/appendices/` nếu là thay đổi kiến trúc quan trọng (kèm ADR mới theo tài liệu 11 mục 11).

## Cấu trúc

```text
cmd/api/       vmf-api — HTTP service
cmd/worker/    vmf-worker — job lease + provisioning side effects
cmd/cli/       vmf — operator CLI
internal/      domain, stateengine, proxmox, pgw, guest, workload, ipam,
               validation, storage, jobs, audit, observability, config, httpapi
api/           OpenAPI 3.1 contract
migrations/    PostgreSQL schema, golang-migrate format
deploy/        docker-compose (local Postgres), systemd units (P0-12)
configs/       config example — copy thành configs/local.yaml, không commit bản thật
docs/          toàn bộ bộ hồ sơ thiết kế + coding/git standard
```

## Yêu cầu môi trường

- Go 1.26+ (bump từ 1.22 sau khi `govulncheck` phát hiện nhiều CVE stdlib chưa vá trên toolchain 1.22.x đã EOL — xem commit `fix(ci): bump go 1.22 -> 1.26`; máy dev hiện tại: `go1.26.3`)
- PostgreSQL 16 (qua `deploy/docker-compose.yml` hoặc instance có sẵn)
- `golangci-lint` v2.13.1+ (bắt buộc dùng `golangci-lint-action@v7` trong CI — `@v6` không hỗ trợ config schema v2), `goimports`, `golang-migrate` cho build/migration đầy đủ
- `make` — **không có sẵn trên máy Windows đã dùng để scaffold**; dùng lệnh `go`/`gofmt` trực tiếp nếu chưa cài `make` (xem tương đương từng target trong [Makefile](Makefile))

## Local dev

```bash
go build ./...
go test ./...              # -race cần cgo (cần gcc); CI Linux có sẵn, máy scaffold ban đầu thì không
gofmt -l .                 # phải rỗng
cp configs/vm-factory.example.yaml configs/local.yaml   # rồi chỉnh path secret cho môi trường local
go run ./cmd/api --config configs/local.yaml
```

`cmd/api` cần `auth.jwt_public_key_file` trỏ tới một RSA public key PEM hợp lệ để start (fail-fast theo guardrail "không log secret / không disable auth âm thầm"). Sinh cặp khoá dev nhanh:

```bash
openssl genrsa -out /tmp/vmf-dev.key 2048
openssl rsa -in /tmp/vmf-dev.key -pubout -out /tmp/vmf-dev.pub
```

rồi trỏ `auth.jwt_public_key_file` trong `configs/local.yaml` tới `/tmp/vmf-dev.pub`.

> **Windows dev**: binary Go là native Windows, không hiểu path kiểu MSYS/Git Bash (`/tmp/...`). Dùng path kiểu Windows với `/` xuôi trong YAML, ví dụ `D:/dev/vmf/dev.pub`, không dùng `/tmp/...` hay `\` ngược (YAML hiểu `\` là escape).

## Known gaps (trung thực về những gì chưa verify)

- Migration `migrations/000001_init.*.sql` chưa được chạy thật trên PostgreSQL (máy scaffold không có Docker/psql) — thứ tự DROP trong `.down.sql` được suy ra bằng cách soát ngược dependency FK trong `.up.sql`, chưa execute-verify.
- `go test -race` không chạy được trên máy scaffold (thiếu gcc/cgo trên Windows); CI (Ubuntu runner) có gcc nên chạy được ở đó — đã verify xanh trên CI.
- Auth middleware verify JWT chữ ký RS256 đã có unit test (valid/expired/wrong-issuer/wrong-key/unknown-role), nhưng chưa có integration test end-to-end qua `cmd/api` thật.
