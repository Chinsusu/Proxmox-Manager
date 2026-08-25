# PHẦN XI - ENGINEERING STANDARDS & GIT WORKFLOW

# 1. Mục đích và phạm vi

Tài liệu này chốt cách viết code, cách tổ chức branch/commit/PR, pipeline CI và cách build/release cho repo `vm-factory`. Nó là contract vận hành hàng ngày cho dev, bổ sung cho [02_System_Architecture_and_Technical_Design_v1.0.md](02_System_Architecture_and_Technical_Design_v1.0.md) (Implementation Guardrails, mục 18) và [10_Operations_Runbook_and_Implementation_Roadmap_v1.0.md](10_Operations_Runbook_and_Implementation_Roadmap_v1.0.md) (Repository Layout, mục 1).

Không tuân thủ tài liệu này không tự động chặn merge nếu có lý do chính đáng, nhưng ngoại lệ phải ghi trong PR description, không âm thầm bỏ qua.

# 2. Coding Standards (Go)

## 2.1 Toolchain

- Go version pin trong `go.mod` (`go 1.22` trở lên) và trong CI image; không dùng `latest` trôi nổi.
- `gofmt` và `goimports` bắt buộc, không thương lượng. CI fail nếu diff sau khi format khác 0 dòng.
- Không có linter nào bị tắt toàn cục để né lỗi; muốn tắt một rule ở một dòng cụ thể phải có `//nolint:<rule> // lý do`.

`.golangci.yml` khởi điểm:

```yaml
linters:
  enable:
    - govet
    - staticcheck
    - errcheck
    - unused
    - ineffassign
    - revive
    - gosec
    - bodyclose
    - sqlclosecheck
    - noctx
    - errorlint
    - contextcheck
run:
  timeout: 5m
issues:
  exclude-use-default: false
```

## 2.2 Package boundaries

- Tuân thủ layout đã chốt ở Phần X mục 1 (`cmd/`, `internal/domain`, `internal/stateengine`, `internal/proxmox`, `internal/pgw`, ...).
- `internal/domain` không được import package SDK Proxmox/PGW cụ thể; domain chỉ phụ thuộc interface (port) định nghĩa trong Phần II mục 3.5. Adapter implement interface, không ngược lại.
- Không có import cycle giữa `internal/*`; nếu hai package cần dùng chung type, tách sang `internal/domain` hoặc một package `internal/shared` nhỏ, không tạo dependency vòng.
- CLI (`cmd/cli`) gọi API qua HTTP client, không import thẳng `internal/stateengine` hay DB layer.

## 2.3 Error handling

- Wrap lỗi với `%w`, không nuốt lỗi (`_ = err` chỉ chấp nhận khi có comment giải thích tại sao an toàn).
- Mọi lỗi trả ra API phải map về `error_code` ổn định theo error envelope (Phần II mục 10) và bảng Error Mapping (Phần III mục 11). Không để lỗi Go thô (`err.Error()`) lộ ra response.
- Không `panic` trên request path. `panic` chỉ chấp nhận ở `init()`/startup khi config sai không thể chạy tiếp, và phải `recover` ở tầng HTTP middleware để trả `500` có `request_id` thay vì crash process.
- Không dùng `sleep` cố định để chờ external state — đây là guardrail cấp kiến trúc (Phần II mục 18), vi phạm bị reject ở review bất kể lý do.

## 2.4 Context và concurrency

- Mọi hàm I/O (DB, HTTP, exec) nhận `context.Context` làm tham số đầu tiên; không dùng `context.Background()` ngoài `main()`/test.
- Goroutine phải có đường thoát rõ ràng (`ctx.Done()`, `WaitGroup`, hoặc job hoàn tất); không "fire and forget" side effect có thể mutate resource.
- Semaphore per-cluster/per-storage/per-network-segment theo Phần II mục 3.2 implement bằng buffered channel hoặc `golang.org/x/sync/semaphore`, không tự chế cơ chế khoá song song khác trong cùng service.

## 2.5 Logging

- Một logger structured JSON duy nhất (interface nội bộ), không gọi `fmt.Println`/`log.Print` trong code nghiệp vụ.
- Field bắt buộc theo Phần II mục 12: `request_id`, `job_id`, `instance_id` (khi có), `component`, `event`.
- Không log secret, token, raw machine-id, cloud-init user-data thô — theo redaction rule Phần IX mục 2. Có middleware/hook redact tự động, không dựa vào dev tự nhớ.

## 2.6 Testing

- Table-driven test là mặc định cho unit test.
- Không test nào được phép dùng `time.Sleep` để chờ async — dùng `context.WithTimeout` + polling có backoff hoặc channel signal (nhất quán với guardrail "không sleep cố định").
- Coverage tối thiểu: `internal/domain` và `internal/stateengine` >= 80%; `internal/*adapter` (Proxmox/PGW/guest) ưu tiên contract test qua mock hơn là coverage % thô.
- Race detector bắt buộc trong CI: `go test -race ./...`.

## 2.7 Migration tool

- Dùng `golang-migrate` (hoặc tương đương forward-only) đặt trong `migrations/`, đúng nguyên tắc "forward-only" đã chốt ở Phần VI mục 7.
- Tên file: `NNNNNN_description.up.sql` / `.down.sql`. `.down.sql` vẫn viết để dev rollback local, nhưng production chỉ apply `up` (theo mục 7: "Backup bắt buộc trước destructive migration").

## 2.8 Naming và style

- Go convention chuẩn: `MixedCaps`, không dùng underscore trong tên biến/hàm Go (chỉ dùng trong tên file test `_test.go` và SQL).
- Comment chỉ viết khi giải thích lý do (why), không mô tả lại code đã tự rõ nghĩa qua tên định danh.
- Exported type/function trong package dùng công khai (đặc biệt `internal/domain`) có doc comment một dòng.

# 3. Repository & Branching Model

Trunk-based development, nhánh `main` là nguồn triển khai, nhánh feature sống ngắn ngày.

```text
main                    protected, luôn deployable
├─ feat/p0-01-domain-schema
├─ fix/p0-02-clone-task-poll
├─ chore/ci-lint-setup
```

## 3.1 Branch protection trên `main`

- Không push trực tiếp; mọi thay đổi qua PR.
- Yêu cầu CI xanh trước khi merge (mục 7).
- Yêu cầu tối thiểu 1 approval nếu có từ 2 dev trở lên; nếu làm solo, thay approval bằng self-review checklist (mục 5.3) — CI xanh là gate bắt buộc không thể thay thế trong cả hai trường hợp.
- Không force-push vào `main`. Force-push vào nhánh feature của chính mình được phép trước khi mở PR.
- Xoá branch tự động sau khi merge.

## 3.2 Quy ước tên nhánh

```text
<type>/<epic-id>-<short-slug>
```

| Type | Dùng khi |
|---|---|
| `feat` | Tính năng mới, một epic hoặc một phần epic (P0-00..P0-12) |
| `fix` | Sửa lỗi hành vi |
| `refactor` | Tái cấu trúc không đổi hành vi |
| `test` | Thêm/sửa test không đổi code sản phẩm |
| `docs` | Thay đổi tài liệu (bao gồm bộ hồ sơ này) |
| `chore` | Tooling, CI, dependency bump |
| `perf` | Tối ưu hiệu năng có đo lường |

Ví dụ: `feat/p0-03-ipam-reservation`, `fix/p0-05-rollback-idempotent`, `docs/p0-04-pgw-contract-clarify`.

Nhánh không gắn epic (spike, thử nghiệm) dùng `spike/<slug>` và không merge thẳng vào `main` — phải rebase thành `feat/...` sạch trước khi mở PR.

# 4. Commit Convention

Conventional Commits, bắt buộc cho mọi commit vào `main` (qua squash merge nên message cuối cùng nhìn thấy trên `main` là message của PR, xem mục 6).

```text
<type>(<scope>): <subject ngắn gọn, thì hiện tại, không viết hoa đầu, không dấu chấm cuối>

<body — giải thích WHY nếu không hiển nhiên, tham chiếu ADR nếu có>

Refs: P0-0X
```

- `type`: giống bảng branch type ở mục 3.2.
- `scope`: tên package hoặc epic ngắn, ví dụ `ipam`, `stateengine`, `proxmox-adapter`, `api`.
- Một commit nên tự build và test được (không commit "wip", "fix typo" rời rạc trong lịch sử cuối cùng trên `main` — dọn bằng squash).
- Không commit binary, file build, secret, `.env` thật.

Ví dụ:

```text
feat(ipam): reserve IPv4 trong transaction FOR UPDATE SKIP LOCKED

Tránh race giữa hai worker cùng xin IP trong segment, khớp
RES-001 trong acceptance_test_matrix.csv.

Refs: P0-03
```

# 5. Pull Request Workflow

## 5.1 Kích thước PR

- Một PR tương ứng một epic con hoặc một concern rõ ràng; nhắm review trong một lần đọc (khuyến nghị < 400 dòng diff trừ khi là migration/generated code).
- PR đổi domain/API/DB schema tách riêng khỏi PR đổi tooling/CI.

## 5.2 Template mô tả PR

```markdown
## Mục tiêu
(Vấn đề gì, epic nào — P0-0X)

## Thay đổi chính
-

## Testing đã chạy
- [ ] go test -race ./...
- [ ] golangci-lint run
- [ ] migration up/down thử local
- [ ] contract test adapter liên quan (nếu có)

## Rủi ro / rollback
(Ảnh hưởng gì nếu revert, có cần thao tác thủ công không)

## Liên kết
Epic: P0-0X · ADR liên quan (nếu có) · Acceptance case: (mã trong acceptance_test_matrix.csv nếu áp dụng)
```

## 5.3 Checklist trước khi mark Ready for review

```text
[ ] Build pass, lint pass, test pass local
[ ] Không có secret/token/credential trong diff
[ ] Migration forward-only, có down script cho local dev
[ ] Đổi OpenAPI/SQL schema thì đã đồng bộ appendices tương ứng
[ ] Đổi hành vi thì đã cập nhật tài liệu 01-11 liên quan
[ ] Không vi phạm Implementation Guardrails (Phần II mục 18)
[ ] PR mô tả đủ theo template mục 5.2
```

## 5.4 Review

- Có từ 2 dev: bắt buộc 1 approval trước khi merge, ưu tiên người hiểu domain/epic liên quan.
- Solo dev: tự chạy đủ checklist mục 5.3, để CI làm gate khách quan thay cho reviewer người.
- Reviewer từ chối PR vi phạm guardrail kiến trúc dù test có pass — guardrail không thể "test pass là đủ".

# 6. Merge Strategy

- **Squash merge** mặc định vào `main` — một PR tương ứng đúng một commit trên lịch sử `main`, message theo Conventional Commits (mục 4).
- Không dùng merge commit thường (tránh lịch sử rối); không rebase lại lịch sử đã có trên `main`.
- Trước khi merge, rebase nhánh feature lên `main` mới nhất (`git rebase main`), không merge `main` liên tục vào feature branch làm nhiễu diff.
- Conflict giải quyết trên nhánh feature, không giải quyết bằng merge commit hai chiều trên `main`.
- Sau merge: xoá nhánh, đóng liên kết issue/epic tracking nếu có.

# 7. CI Pipeline

Stage chạy tuần tự, PR không merge được nếu bất kỳ stage nào fail. Thứ tự map trực tiếp với Release Gates đã chốt ở [09_Observability_Alerting_and_Test_Plan_v1.0.md](09_Observability_Alerting_and_Test_Plan_v1.0.md) mục 11.

```text
1. Lint          gofmt -l, goimports -l, golangci-lint run
2. Build         go build ./...
3. Unit test     go test -race -coverprofile=... ./...
4. Contract test Proxmox/PGW/workload adapter qua mock server
5. Migration     apply lên Postgres tạm (docker service), kiểm tra idempotent
6. Secret scan   gitleaks detect --no-git -v
7. OpenAPI check spectral lint + breaking-change diff so với main
8. Vuln scan     govulncheck ./...
```

Nightly/manual (không chặn từng PR vì cần Proxmox/PGW staging thật):

```text
9.  Integration lab   theo Phần IX Test Pyramid — System lab
10. Chaos suite       theo Phần IX mục 8 (worker/Proxmox/PGW/guest chaos)
11. Soak/wave test    chỉ chạy trước khi mở wave (Phần X mục 11)
```

GitHub Actions khung tham khảo:

```yaml
name: ci
on:
  pull_request:
  push:
    branches: [main]
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: {go-version: '1.22'}
      - run: gofmt -l . && test -z "$(gofmt -l .)"
      - run: goimports -l . && test -z "$(goimports -l .)"
      - uses: golangci/golangci-lint-action@v6
  test:
    needs: lint
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env: {POSTGRES_PASSWORD: vmf}
        ports: ['5432:5432']
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: {go-version: '1.22'}
      - run: go build ./...
      - run: go test -race -coverprofile=coverage.out ./...
      - run: migrate -path migrations -database "$DATABASE_URL" up
  security:
    needs: lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: gitleaks/gitleaks-action@v2
      - run: go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...
  openapi:
    needs: lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: npx @stoplight/spectral-cli lint appendices/vm-factory-openapi-v1.0.yaml
```

Gate cứng — không merge nếu:

```text
migration dry-run fail
state-machine golden test fail
idempotency test fail
rollback test fail
duplicate identity test fail
secret scan fail
OpenAPI breaking change không bump version (info.version)
```

(nguyên văn từ Phần IX mục 11, nhắc lại tại đây vì đây là nơi CI thật sự enforce nó)

# 8. Build & Release

- Semantic Versioning cho binary/release (Phần II mục 16 đã chốt), độc lập với version tài liệu.
- Embed version/commit/build-time qua `-ldflags`:

```bash
go build -trimpath -ldflags "\
  -X main.version=$(git describe --tags --always) \
  -X main.commit=$(git rev-parse --short HEAD) \
  -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/vmf-api ./cmd/api
```

- `CGO_ENABLED=0` trừ khi một dependency bắt buộc cgo; ưu tiên static binary cho triển khai systemd (Phần X mục 3).
- Mỗi release đính kèm SHA-256 checksum và SBOM (`syft` hoặc tương đương) — theo yêu cầu Phần II mục 16.
- Container image (nếu dùng để chạy lab/CI, không bắt buộc cho production P0 vốn deploy qua systemd theo Phần X): multi-stage build, base image pin theo digest, chạy non-root.
- Changelog release theo nhóm Conventional Commit type (`feat`/`fix`/`perf` lên changelog user-facing; `chore`/`docs`/`test` không).

# 9. Local Dev Workflow

```bash
make lint          # gofmt + goimports + golangci-lint
make test           # go test -race ./...
make build           # build 3 binary: api, worker, cli
make db-up            # docker compose lên Postgres local
make migrate-up         # apply migrations lên DB local
make lab-mocks            # chạy Proxmox/PGW mock server cho dev không cần cluster thật
```

- Config local dựa trên [vm-factory-config.example.yaml](appendices/vm-factory-config.example.yaml), copy thành `configs/local.yaml`, không commit file có giá trị thật.
- Pre-commit hook khuyến nghị (không bắt buộc, nhưng đỡ fail CI oan):

```bash
#!/bin/sh
gofmt -l . | grep . && echo "run gofmt -w ." && exit 1
goimports -l . | grep . && echo "run goimports -w ." && exit 1
golangci-lint run --fast
gitleaks protect --staged -v
```

# 10. Definition of Done cấp Pull Request

Khác với Definition of Done cấp dự án (Phần I mục 12, áp dụng cho toàn hệ thống P0), đây là gate áp dụng cho từng PR riêng lẻ:

```text
Code + test + doc liên quan nằm trong cùng một PR
→ CI (mục 7) xanh toàn bộ, không skip step
→ Coverage domain/state-engine không giảm dưới ngưỡng mục 2.6
→ Acceptance case liên quan trong acceptance_test_matrix.csv (nếu có) chuyển PASS
→ Không vi phạm Implementation Guardrails (Phần II mục 18)
→ Checklist mục 5.3 đã tick đủ
```

# 11. Governance

- Thay đổi convention trong tài liệu này đi qua PR riêng (`docs/...`), không sửa "tiện tay" trong lúc làm epic khác.
- Vi phạm guardrail kiến trúc (Phần II mục 18) không được hợp thức hoá bằng cách sửa tài liệu này — phải qua ADR mới (Phần II mục 17) vì đó là quyết định kiến trúc, không phải quy ước coding.
- Khi convention ở đây và một tài liệu 01-10 mâu thuẫn về mặt kiến trúc/domain, tài liệu 01-10 thắng; tài liệu này chỉ chốt "cách làm", không tự ý đổi "làm cái gì".
