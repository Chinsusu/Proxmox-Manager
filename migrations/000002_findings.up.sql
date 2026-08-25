-- Migration 000002: bang findings cho invariant scanner (Phan VI muc 11
-- "Data Quality Checks" va OpenAPI GET /v1/findings) - mo ta trong doc
-- nhung 000001 chua co bang nay, vay o day.
CREATE TYPE finding_severity AS ENUM ('info', 'warning', 'critical');
CREATE TYPE finding_state AS ENUM ('OPEN', 'ACKNOWLEDGED', 'REMEDIATING', 'RESOLVED');

CREATE TABLE findings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  category TEXT NOT NULL,
  severity finding_severity NOT NULL,
  resource_type TEXT,
  resource_id TEXT,
  summary TEXT NOT NULL,
  details JSONB NOT NULL DEFAULT '{}'::jsonb,
  state finding_state NOT NULL DEFAULT 'OPEN',
  detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);
-- Unique de scanner insert idempotent: khong tao them finding trung
-- cho cung mot van de dang OPEN (dua vao ON CONFLICT DO NOTHING).
CREATE UNIQUE INDEX uq_findings_open ON findings(category, resource_type, resource_id) WHERE state = 'OPEN';
CREATE INDEX idx_findings_state ON findings(state, detected_at DESC);
