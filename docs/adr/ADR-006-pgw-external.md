# ADR-006: PGW là external dependency

**Status:** Accepted

VM Factory gọi PGW API; không import code, truy DB hay quản lý nftables. Hai repo có version/release/lifecycle độc lập.
