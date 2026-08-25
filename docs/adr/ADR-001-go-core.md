# ADR-001: Go cho core services

**Status:** Accepted

Core `vmf-api`, `vmf-worker`, `vmf-cli` dùng Go để thống nhất với nền hạ tầng hiện có, tạo binary độc lập, concurrency/context rõ và giảm runtime dependency. Python chỉ dùng cho tooling/test khi có lợi.
