-- alerts: ban ghi persisted cho UI console (API_UI_Gap_Register muc
-- 3.5) — doi tac vm-factory-native cua mot phan alert rule Prometheus
-- (deploy/observability/prometheus-rules.yml: ProvisioningBacklogHigh/
-- JobStuckInState/RollbackIncomplete), khong thay the Alertmanager.
-- fingerprint dung de dedup/upsert khi cung mot dieu kien con firing
-- lien tuc qua nhieu vong scan.
CREATE TABLE alerts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fingerprint TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'firing',
  severity TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  acknowledged_at TIMESTAMPTZ,
  acknowledged_by TEXT,
  acknowledged_note TEXT,
  version INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX idx_alerts_status ON alerts(status, created_at DESC);
CREATE INDEX idx_alerts_resource ON alerts(resource_type, resource_id);
