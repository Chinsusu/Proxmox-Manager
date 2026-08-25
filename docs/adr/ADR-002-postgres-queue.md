# ADR-002: PostgreSQL là source of truth và queue P0

**Status:** Accepted

Dùng PostgreSQL cho state, unique constraints, job lease (`FOR UPDATE SKIP LOCKED`), audit và outbox. Không thêm Redis/NATS trong P0 để giảm dependency; có thể tách queue ở P2 nếu evidence yêu cầu.
