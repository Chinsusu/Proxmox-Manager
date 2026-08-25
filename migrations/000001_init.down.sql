-- Down migration cho local dev only (tài liệu 11 mục 2.7 — production chỉ
-- apply "up", forward-only theo Phần VI mục 7 của bộ tài liệu thiết kế).
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS secrets;
DROP TABLE IF EXISTS hostname_sequences;
DROP TABLE IF EXISTS resource_locks;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS validation_runs;
DROP TABLE IF EXISTS identity_observations;
DROP TABLE IF EXISTS external_tasks;
DROP TABLE IF EXISTS provisioning_jobs;
DROP TABLE IF EXISTS egress_bindings;
DROP TABLE IF EXISTS ip_allocations;
DROP TABLE IF EXISTS vm_instances;
DROP TABLE IF EXISTS network_segments;
DROP TABLE IF EXISTS vm_templates;
DROP TABLE IF EXISTS capacity_snapshots;
DROP TABLE IF EXISTS pve_storages;
DROP TABLE IF EXISTS pve_nodes;
DROP TABLE IF EXISTS pve_clusters;

DROP TYPE IF EXISTS validation_result;
DROP TYPE IF EXISTS allocation_state;
DROP TYPE IF EXISTS job_state;
DROP TYPE IF EXISTS job_operation;
DROP TYPE IF EXISTS instance_state;
DROP TYPE IF EXISTS template_state;
