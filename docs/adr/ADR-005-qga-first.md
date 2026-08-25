# ADR-005: QEMU Guest Agent trước, SSH fallback

**Status:** Accepted

QGA dùng cho readiness/facts qua Proxmox. SSH chỉ fallback có policy, host-key verification và allowlisted commands.
