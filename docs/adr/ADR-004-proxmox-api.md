# ADR-004: Dùng Proxmox REST API, không shell `qm` trong service

**Status:** Accepted

API có auth/audit/error contract tốt hơn shell. CLI `qm` chỉ được phép trong runbook/manual diagnostic hoặc test fixture, không nằm trong đường production của service.
