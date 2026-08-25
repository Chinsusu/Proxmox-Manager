-- VM Factory PostgreSQL schema blueprint v1.1
-- v1.1: tach job_state khoi instance_state; bo sung secrets, pve_nodes,
--       pve_storages, capacity_snapshots, hostname_sequences.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE template_state AS ENUM ('DRAFT','CANDIDATE','ACTIVE','DEPRECATED','REVOKED');
CREATE TYPE instance_state AS ENUM (
  'REQUESTED','RESERVING','CLONING','CONFIGURING','NETWORK_BINDING','BOOTING',
  'WAITING_GUEST','VALIDATING_IDENTITY','VALIDATING_EGRESS','APPLYING_WORKLOAD',
  'READY','RETRY_WAIT','DEGRADED','QUARANTINED','ROLLING_BACK','FAILED',
  'DRAINING','DECOMMISSIONING','RELEASING_RESOURCES','RETIRED'
);
CREATE TYPE job_operation AS ENUM ('PROVISION','RETRY','REBUILD','QUARANTINE','DECOMMISSION','RECONCILE');
-- Job execution state la state machine rieng, khong dung chung instance_state.
-- Vi tri lifecycle cua instance duoc job theo doi qua cot checkpoint.
CREATE TYPE job_state AS ENUM ('QUEUED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','CANCELLED');
CREATE TYPE allocation_state AS ENUM ('FREE','RESERVED','ASSIGNED','QUARANTINED','RELEASED');
CREATE TYPE validation_result AS ENUM ('PASS','WARN','FAIL','UNKNOWN');

CREATE TABLE schema_migrations (
  version BIGINT PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  checksum TEXT NOT NULL
);

CREATE TABLE pve_clusters (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL UNIQUE,
  base_url TEXT NOT NULL,
  secret_ref TEXT NOT NULL,
  ca_ref TEXT,
  state TEXT NOT NULL DEFAULT 'ACTIVE',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE pve_nodes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES pve_clusters(id),
  name TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'ACTIVE',
  last_seen_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(cluster_id, name)
);

CREATE TABLE pve_storages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES pve_clusters(id),
  name TEXT NOT NULL,
  content_types TEXT[] NOT NULL DEFAULT '{}',
  nodes TEXT[] NOT NULL DEFAULT '{}',
  state TEXT NOT NULL DEFAULT 'ACTIVE',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(cluster_id, name)
);

-- Capacity snapshot la observation co TTL, khong phai hard guarantee (Phan VI muc 4).
CREATE TABLE capacity_snapshots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cluster_id UUID NOT NULL REFERENCES pve_clusters(id),
  scope_type TEXT NOT NULL,
  scope_key TEXT NOT NULL,
  metrics JSONB NOT NULL,
  collected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_capacity_scope ON capacity_snapshots(cluster_id, scope_type, scope_key, collected_at DESC);

CREATE TABLE vm_templates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  family TEXT NOT NULL,
  version TEXT NOT NULL,
  os_family TEXT NOT NULL,
  os_version TEXT NOT NULL,
  architecture TEXT NOT NULL,
  pve_cluster_id UUID NOT NULL REFERENCES pve_clusters(id),
  pve_node TEXT NOT NULL,
  pve_template_vmid INTEGER NOT NULL,
  storage TEXT,
  clone_mode_allowed TEXT[] NOT NULL DEFAULT ARRAY['full'],
  source_checksum TEXT NOT NULL,
  build_manifest JSONB NOT NULL DEFAULT '{}'::jsonb,
  state template_state NOT NULL DEFAULT 'DRAFT',
  validation_status validation_result NOT NULL DEFAULT 'UNKNOWN',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(family, version),
  UNIQUE(pve_cluster_id, pve_node, pve_template_vmid)
);

CREATE TABLE network_segments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL UNIQUE,
  cidr CIDR NOT NULL,
  gateway INET NOT NULL,
  bridge TEXT NOT NULL,
  dns_servers INET[] NOT NULL DEFAULT '{}',
  ipv6_policy TEXT NOT NULL DEFAULT 'deny',
  allocation_strategy TEXT NOT NULL DEFAULT 'sequential-lowest-free',
  exclusions JSONB NOT NULL DEFAULT '[]'::jsonb,
  state TEXT NOT NULL DEFAULT 'ACTIVE',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE vm_instances (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  logical_name TEXT NOT NULL,
  hostname TEXT NOT NULL,
  state instance_state NOT NULL DEFAULT 'REQUESTED',
  generation INTEGER NOT NULL DEFAULT 1,
  template_id UUID NOT NULL REFERENCES vm_templates(id),
  pve_cluster_id UUID REFERENCES pve_clusters(id),
  pve_node TEXT,
  vmid INTEGER,
  resource_pool TEXT,
  desired_config JSONB NOT NULL DEFAULT '{}'::jsonb,
  desired_config_hash TEXT,
  workload_adapter TEXT,
  workload_spec JSONB NOT NULL DEFAULT '{}'::jsonb,
  version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  retired_at TIMESTAMPTZ,
  UNIQUE(logical_name, generation)
);
CREATE UNIQUE INDEX uq_active_instance_hostname ON vm_instances(hostname) WHERE retired_at IS NULL;
CREATE UNIQUE INDEX uq_active_instance_vmid ON vm_instances(pve_cluster_id, vmid) WHERE retired_at IS NULL AND vmid IS NOT NULL;

CREATE TABLE ip_allocations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  segment_id UUID NOT NULL REFERENCES network_segments(id),
  address INET NOT NULL,
  instance_id UUID REFERENCES vm_instances(id),
  state allocation_state NOT NULL DEFAULT 'FREE',
  reserved_until TIMESTAMPTZ,
  assigned_at TIMESTAMPTZ,
  released_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(segment_id, address)
);
CREATE INDEX idx_ip_allocations_free ON ip_allocations(segment_id, state, address);

CREATE TABLE egress_bindings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id UUID NOT NULL REFERENCES vm_instances(id),
  pgw_client_id TEXT,
  pgw_mapping_id TEXT,
  pgw_policy_id TEXT,
  state TEXT NOT NULL DEFAULT 'PENDING',
  expected_exit JSONB NOT NULL DEFAULT '{}'::jsonb,
  desired_generation BIGINT,
  last_proof_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_active_egress_binding ON egress_bindings(instance_id) WHERE state IN ('PENDING','ACTIVE','SUSPENDED');

CREATE TABLE provisioning_jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id UUID NOT NULL REFERENCES vm_instances(id),
  operation job_operation NOT NULL,
  state job_state NOT NULL DEFAULT 'QUEUED',
  checkpoint TEXT NOT NULL,
  checkpoint_data JSONB NOT NULL DEFAULT '{}'::jsonb,
  priority INTEGER NOT NULL DEFAULT 0,
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 8,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_owner TEXT,
  lease_expires_at TIMESTAMPTZ,
  error_code TEXT,
  error_message TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);
CREATE INDEX idx_jobs_claim ON provisioning_jobs(state, next_attempt_at, priority DESC, created_at);

CREATE TABLE external_tasks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id UUID NOT NULL REFERENCES provisioning_jobs(id),
  system TEXT NOT NULL,
  operation TEXT NOT NULL,
  external_id TEXT NOT NULL,
  status TEXT NOT NULL,
  request_hash TEXT,
  result JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_polled_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  UNIQUE(system, external_id)
);

CREATE TABLE identity_observations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id UUID NOT NULL REFERENCES vm_instances(id),
  generation INTEGER NOT NULL,
  machine_id_digest TEXT NOT NULL,
  ssh_host_fingerprint TEXT NOT NULL,
  cloud_init_instance_id TEXT,
  hostname TEXT NOT NULL,
  mac_addresses MACADDR[] NOT NULL DEFAULT '{}',
  ip_addresses INET[] NOT NULL DEFAULT '{}',
  boot_id TEXT,
  facts JSONB NOT NULL DEFAULT '{}'::jsonb,
  observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Chu y: khong dat UNIQUE tren digest. Duplicate policy (block/warn theo scope)
-- la application-level va configurable (Phan VIII muc 10); bang nay luu ca lich su.
CREATE INDEX idx_identity_machine_digest ON identity_observations(machine_id_digest);
CREATE INDEX idx_identity_ssh_fingerprint ON identity_observations(ssh_host_fingerprint);

CREATE TABLE validation_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id UUID NOT NULL REFERENCES vm_instances(id),
  job_id UUID REFERENCES provisioning_jobs(id),
  type TEXT NOT NULL,
  result validation_result NOT NULL,
  ruleset_version TEXT NOT NULL,
  evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ
);
CREATE INDEX idx_validation_instance ON validation_runs(instance_id, type, started_at DESC);

CREATE TABLE idempotency_keys (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  response_status INTEGER,
  response_body JSONB,
  resource_id UUID,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(scope, key)
);

-- Workload slot reservation (Phan V muc 4.2) dung resource_locks
-- voi resource_type = 'workload_slot'; khong can bang rieng trong P0.
CREATE TABLE resource_locks (
  resource_type TEXT NOT NULL,
  resource_key TEXT NOT NULL,
  owner_job_id UUID NOT NULL REFERENCES provisioning_jobs(id),
  lease_expires_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(resource_type, resource_key)
);

-- Hostname allocator {prefix}-{sequence:04d} (Phan II muc 8.3).
CREATE TABLE hostname_sequences (
  prefix TEXT PRIMARY KEY,
  next_value INTEGER NOT NULL DEFAULT 1,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Chi dung khi khong co external secret manager (Phan VI muc 9).
-- Envelope key lay tu systemd credential/secret manager, khong bao gio luu trong DB.
CREATE TABLE secrets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider TEXT NOT NULL,
  ciphertext BYTEA NOT NULL,
  nonce BYTEA NOT NULL,
  key_version INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  rotated_at TIMESTAMPTZ
);

CREATE TABLE audit_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  request_id TEXT,
  correlation_id TEXT,
  before_state JSONB,
  after_state JSONB,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_audit_resource ON audit_events(resource_type, resource_id, occurred_at DESC);
CREATE INDEX idx_audit_correlation ON audit_events(correlation_id, occurred_at);

CREATE TABLE outbox_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type TEXT NOT NULL,
  aggregate_type TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ,
  attempt INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_outbox_unpublished ON outbox_events(created_at) WHERE published_at IS NULL;
