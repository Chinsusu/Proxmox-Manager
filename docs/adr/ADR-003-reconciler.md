# ADR-003: State machine và reconciler thay script tuyến tính

**Status:** Accepted

External systems async và partial failure là bình thường. Mỗi step persist intent/reference/evidence; retry luôn reconcile trước khi reissue mutation.
