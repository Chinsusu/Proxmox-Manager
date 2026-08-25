# Observability (P0-10)

Metrics expose Prometheus text format tại `observability.metrics_listen`
(`configs/vm-factory.example.yaml`) trên cả `vmf-api` và `vmf-worker`.
Job/Proxmox/Resource/Validation metric thật chỉ có dữ liệu từ `vmf-worker`
(nơi job-lease loop chạy) — `vmf-api` phục vụ `/metrics` để scrape target
lên (`up{job="vmf-api"}`) nhưng không tự sinh các series này.

## Alert rules

`prometheus-rules.yml` — nạp vào Prometheus qua `rule_files:`. 3 alert
đánh dấu `[CHUA CO DU LIEU THAT]` tham chiếu metric đã đăng ký nhưng
chưa có nguồn emit thật (PGW Adapter thật — P0-04; orphan scanner —
P0-11; Proxmox node/storage capacity — chưa có Adapter method, chưa
verify trên cluster thật). Alert vẫn để nguyên trong file — không cần
sửa lại khi các epic đó hoàn tất, chỉ cần bỏ comment "chưa emit".

## Dashboards

Import `grafana/*.json` vào Grafana, chọn datasource Prometheus cho biến
`DS_PROMETHEUS`.

- `fleet-executive.json` — tổng quan fleet (Phần 5 tài liệu 09, mục
  "Executive/Fleet"): instance theo state, SLO thành công provisioning,
  IP pool, top failure code, phân bố template, active alert.
- `operations.json` — vận hành hàng ngày (mục "Operations"): job
  waterfall, latency Proxmox API, lease/backlog, rollback/orphan queue.
  Panel "Proxmox node/storage pressure" và "PGW binding/proof state"
  dùng proxy tạm (Proxmox API error rate) hoặc sẽ trống cho tới khi các
  epic phụ thuộc hoàn tất — xem description trong từng panel.

**Không có `instance-detail.json`.** Panel doc 09 liệt kê cho view này
("desired vs observed", "state timeline", "external references",
"identity/network/egress evidence", "audit trail", "allowed actions")
là dữ liệu ứng dụng theo TỪNG instance, không phải time-series tổng hợp
— gắn instance_id làm label vào metric Prometheus sẽ tạo cardinality
không kiểm soát được (đúng lý do `vmf_pve_api_requests_total` không
label theo vmid/path thô, xem `internal/observability/metrics.go`).
View này đã có sẵn và đúng công cụ hơn qua:

```bash
vmf instance get <id>
vmf instance evidence <id>
vmf job get <id> --events
```

hoặc `GET /v1/instances/{id}`, `GET /v1/instances/{id}/evidence`,
`GET /v1/jobs/{id}/events` (đã triển khai ở P0-09).
