# ADR-007: Lưu HMAC digest thay raw machine-id

**Status:** Accepted

Uniqueness cần stable comparison nhưng không cần lưu identifier raw. Dùng HMAC-SHA256 với key riêng, rotate theo controlled migration.
