package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics gom toàn bộ Prometheus collector theo tên/label chốt ở tài
// liệu 09 mục 3 (Job/Proxmox/Resource/PGW metrics). Dùng registry riêng
// (không phải prometheus.DefaultRegisterer) để vmf-api/vmf-worker mỗi
// process chỉ có đúng một bộ series của chính nó, không lẫn collector
// mặc định của thư viện.
//
// Mọi method nhận receiver nil an toàn (return sớm) — cho phép truyền
// Metrics: nil vào struct test/handler mà không cần wiring metrics giả.
type Metrics struct {
	registry *prometheus.Registry

	// Job metrics (Phần 3.1) — nguồn: cmd/worker job-lease loop.
	jobsTotal       *prometheus.CounterVec
	jobsActive      *prometheus.GaugeVec
	jobDuration     *prometheus.HistogramVec
	stateDuration   *prometheus.HistogramVec
	jobRetriesTotal *prometheus.CounterVec
	jobLeaseExpired prometheus.Counter
	jobBacklog      prometheus.Gauge
	// jobStateAge/rollbackIncompleteTotal KHÔNG có trong tài liệu 09 mục
	// 3.1 — bổ sung để 2 alert JobStuckInState/RollbackIncomplete (mục
	// 4) có metric thật để dựa vào thay vì mãi mãi không emit được gì:
	// vmf_state_duration_seconds chỉ ghi transition ĐÃ XONG (không biết
	// job RUNNING hiện đứng ở checkpoint bao lâu); vmf_instances không
	// phân biệt được QUARANTINED vì rollback tự nó thất bại (Phần V mục
	// 6) với QUARANTINED vì validation fail.
	jobStateAge             *prometheus.GaugeVec
	rollbackIncompleteTotal prometheus.Counter

	// Proxmox metrics (Phần 3.2) — nguồn: internal/proxmox.Client qua
	// MetricsRecorder interface (proxmox không import package này ngược
	// lại). vmf_pve_tasks_active/vmf_pve_task_duration_seconds VÀ
	// vmf_pve_capacity KHÔNG có ở đây — cần sửa signature WaitForTask
	// (19 call site xuyên 6 file, gồm cả code P0-06/P0-07 đã verify
	// thật) hoặc method adapter mới đọc node/storage status chưa từng
	// verify trên cluster thật — rủi ro/chi phí vượt giá trị tăng thêm
	// so với vmf_pve_api_requests_total{operation="get_task"} đã có sẵn
	// làm proxy hợp lý, để lại follow-up riêng thay vì sửa rộng vội.
	pveAPIRequestsTotal *prometheus.CounterVec
	pveAPILatency       *prometheus.HistogramVec

	// Resource metrics (Phần 3.3) — nguồn: periodic gauge refresh
	// (cmd/worker maintenance loop) + stateengine validation handlers.
	ipPoolAddresses    *prometheus.GaugeVec
	instances          *prometheus.GaugeVec
	orphansTotal       *prometheus.CounterVec
	identityDuplicates prometheus.Counter
	validationTotal    *prometheus.CounterVec

	// PGW/egress metrics (Phần 3.4) — ĐĂNG KÝ nhưng CHƯA có nơi emit
	// thật (P0-04 PGW Adapter thật chưa triển khai, pgw.NoopAdapter
	// không gọi hệ thống ngoài nào nên số liệu sẽ luôn hằng/giả nếu ép
	// đo — cố tình để 0, không emit, thay vì tạo telemetry trông như
	// thật nhưng vô nghĩa).
	pgwRequestsTotal *prometheus.CounterVec
	pgwBindingState  *prometheus.GaugeVec
	egressProofTotal *prometheus.CounterVec
	egressProofAge   prometheus.Gauge
}

// NewMetrics tạo và đăng ký toàn bộ collector vào một registry riêng.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{registry: reg}

	m.jobsTotal = registerCounterVec(reg, "vmf_jobs_total", "Tong so provisioning job da ket thuc, theo operation va result.", "operation", "result")
	m.jobsActive = registerGaugeVec(reg, "vmf_jobs_active", "So provisioning job dang o moi job state.", "state")
	m.jobDuration = registerHistogramVec(reg, "vmf_job_duration_seconds", "Thoi gian tu luc job duoc tao toi luc ket thuc (thanh cong hoac fail han).", jobDurationBuckets, "operation")
	m.stateDuration = registerHistogramVec(reg, "vmf_state_duration_seconds", "Thoi gian Engine.Step() xu ly MOT state nguon (khong tinh thoi gian cho trong hang doi).", jobDurationBuckets, "state")
	m.jobRetriesTotal = registerCounterVec(reg, "vmf_job_retries_total", "So lan job duoc dua ve RETRY_WAIT, theo error_code.", "error_code")
	m.jobLeaseExpired = registerCounter(reg, "vmf_job_lease_expired_total", "So lan job lease het han bi reclaim (worker chet giua chung khong kip Fail/Complete).")
	m.jobBacklog = registerGauge(reg, "vmf_job_backlog", "So job dang QUEUED hoac RETRY_WAIT, cho worker xu ly.")
	m.jobStateAge = registerGaugeVec(reg, "vmf_job_state_age_seconds", "Tuoi (giay) cua job RUNNING lau nhat theo checkpoint - dung cho JobStuckInState alert.", "checkpoint")
	m.rollbackIncompleteTotal = registerCounter(reg, "vmf_rollback_incomplete_total", "So lan Rollback tu no that bai (con resource leftover, instance chuyen QUARANTINED thay vi FAILED) - Phan V muc 6.")

	m.pveAPIRequestsTotal = registerCounterVec(reg, "vmf_pve_api_requests_total", "Tong so request toi Proxmox API, theo operation va status.", "operation", "status")
	m.pveAPILatency = registerHistogramVec(reg, "vmf_pve_api_latency_seconds", "Do tre request toi Proxmox API, theo operation.", apiLatencyBuckets, "operation")

	m.ipPoolAddresses = registerGaugeVec(reg, "vmf_ip_pool_addresses", "So dia chi IP theo segment va state.", "segment", "state")
	m.instances = registerGaugeVec(reg, "vmf_instances", "So VM instance theo state/template_version/pve_node.", "state", "template_version", "pve_node")
	m.orphansTotal = registerCounterVec(reg, "vmf_orphans_total", "So orphan resource phat hien, theo system va type. Chua co orphan scanner (P0-11) - luon 0.", "system", "type")
	m.identityDuplicates = registerCounter(reg, "vmf_identity_duplicates_total", "So lan phat hien machine-id/ssh-fingerprint trung lap.")
	m.validationTotal = registerCounterVec(reg, "vmf_validation_total", "Tong so lan chay validation rule, theo type/result/rule_id.", "type", "result", "rule_id")

	m.pgwRequestsTotal = registerCounterVec(reg, "vmf_pgw_requests_total", "Tong so request toi PGW API, theo operation va status. Chua co PGW adapter that (P0-04) - luon 0.", "operation", "status")
	m.pgwBindingState = registerGaugeVec(reg, "vmf_pgw_binding_state", "So PGW mapping theo state. Chua co PGW adapter that (P0-04) - luon 0.", "state")
	m.egressProofTotal = registerCounterVec(reg, "vmf_egress_proof_total", "Tong so lan doc egress proof, theo result. Chua co PGW adapter that (P0-04) - luon 0.", "result")
	m.egressProofAge = registerGauge(reg, "vmf_egress_proof_age_seconds", "Tuoi cua egress proof gan nhat. Chua co PGW adapter that (P0-04) - luon 0.")

	return m
}

// Handler trả http.Handler phục vụ /metrics (Prometheus text exposition format).
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// --- Job metrics ---

// ObserveJobFinished ghi nhận MỘT job đã tới trạng thái kết thúc
// (SUCCEEDED hoặc FAILED terminal) — result là "success"/"failed"/
// "unsupported". duration tính từ job.CreatedAt (job.StartedAt có thể
// nil nếu job chưa từng được claim — dùng CreatedAt làm mốc ổn định hơn).
func (m *Metrics) ObserveJobFinished(operation, result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.jobsTotal.WithLabelValues(operation, result).Inc()
	m.jobDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// ObserveStateTransition ghi nhận Engine.Step() vừa xử lý xong MỘT
// state nguồn thành công, mất bao lâu.
func (m *Metrics) ObserveStateTransition(sourceState string, duration time.Duration) {
	if m == nil {
		return
	}
	m.stateDuration.WithLabelValues(sourceState).Observe(duration.Seconds())
}

// IncJobRetry ghi nhận một job vừa được đưa về RETRY_WAIT.
func (m *Metrics) IncJobRetry(errorCode string) {
	if m == nil {
		return
	}
	m.jobRetriesTotal.WithLabelValues(errorCode).Inc()
}

// AddJobLeaseExpired cộng dồn số lease bị reclaim trong một vòng bảo trì.
func (m *Metrics) AddJobLeaseExpired(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.jobLeaseExpired.Add(float64(n))
}

// SetJobBacklog set gauge backlog (QUEUED+RETRY_WAIT) tại thời điểm scrape gauge định kỳ.
func (m *Metrics) SetJobBacklog(n float64) {
	if m == nil {
		return
	}
	m.jobBacklog.Set(n)
}

// SetJobsActive set gauge số job đang ở một state cụ thể — caller tự
// Reset trước khi set lại toàn bộ tập state để không giữ series cũ đã
// về 0 (xem cmd/worker refreshResourceGauges).
func (m *Metrics) SetJobsActive(state string, n float64) {
	if m == nil {
		return
	}
	m.jobsActive.WithLabelValues(state).Set(n)
}

// ResetJobsActive xoá toàn bộ series cũ trước khi ghi lại một vòng scrape mới.
func (m *Metrics) ResetJobsActive() {
	if m == nil {
		return
	}
	m.jobsActive.Reset()
}

// SetJobStateAge set gauge tuổi job RUNNING lâu nhất tại checkpoint đó.
func (m *Metrics) SetJobStateAge(checkpoint string, ageSeconds float64) {
	if m == nil {
		return
	}
	m.jobStateAge.WithLabelValues(checkpoint).Set(ageSeconds)
}

// ResetJobStateAge xoá series cũ trước một vòng scrape mới.
func (m *Metrics) ResetJobStateAge() {
	if m == nil {
		return
	}
	m.jobStateAge.Reset()
}

// IncRollbackIncomplete ghi nhận một lần Rollback.Execute tự nó thất
// bại (compensating action lỗi, instance chuyển QUARANTINED thay vì FAILED).
func (m *Metrics) IncRollbackIncomplete() {
	if m == nil {
		return
	}
	m.rollbackIncompleteTotal.Inc()
}

// --- Proxmox metrics (implement proxmox.MetricsRecorder qua duck typing) ---

// ObserveProxmoxRequest implement proxmox.MetricsRecorder.
func (m *Metrics) ObserveProxmoxRequest(operation, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.pveAPIRequestsTotal.WithLabelValues(operation, status).Inc()
	m.pveAPILatency.WithLabelValues(operation).Observe(duration.Seconds())
}

// --- Resource metrics ---

// SetIPPoolAddresses set gauge số địa chỉ theo segment/state.
func (m *Metrics) SetIPPoolAddresses(segment, state string, n float64) {
	if m == nil {
		return
	}
	m.ipPoolAddresses.WithLabelValues(segment, state).Set(n)
}

// ResetIPPoolAddresses xoá series cũ trước một vòng scrape mới.
func (m *Metrics) ResetIPPoolAddresses() {
	if m == nil {
		return
	}
	m.ipPoolAddresses.Reset()
}

// SetInstances set gauge số instance theo state/template_version/pve_node.
func (m *Metrics) SetInstances(state, templateVersion, pveNode string, n float64) {
	if m == nil {
		return
	}
	m.instances.WithLabelValues(state, templateVersion, pveNode).Set(n)
}

// ResetInstances xoá series cũ trước một vòng scrape mới.
func (m *Metrics) ResetInstances() {
	if m == nil {
		return
	}
	m.instances.Reset()
}

// IncIdentityDuplicate ghi nhận một lần phát hiện machine-id/ssh-fingerprint trùng lặp.
func (m *Metrics) IncIdentityDuplicate() {
	if m == nil {
		return
	}
	m.identityDuplicates.Inc()
}

// ObserveValidation ghi nhận kết quả MỘT rule validation (ID-xxx/NET-xxx/EGR-xxx/workload).
func (m *Metrics) ObserveValidation(validationType, result, ruleID string) {
	if m == nil {
		return
	}
	m.validationTotal.WithLabelValues(validationType, result, ruleID).Inc()
}

var (
	jobDurationBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600}
	apiLatencyBuckets  = prometheus.DefBuckets
)

func registerCounter(reg *prometheus.Registry, name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	reg.MustRegister(c)
	return c
}

func registerCounterVec(reg *prometheus.Registry, name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	reg.MustRegister(c)
	return c
}

func registerGauge(reg *prometheus.Registry, name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	reg.MustRegister(g)
	return g
}

func registerGaugeVec(reg *prometheus.Registry, name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	reg.MustRegister(g)
	return g
}

func registerHistogramVec(reg *prometheus.Registry, name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	reg.MustRegister(h)
	return h
}
