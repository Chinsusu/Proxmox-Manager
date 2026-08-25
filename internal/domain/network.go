package domain

import "time"

// NetworkSegment là một dải mạng IPv4 đăng ký để cấp phát — bản ghi
// network_segments, theo Phần VI mục 2.3.
type NetworkSegment struct {
	ID                 string
	Name               string
	CIDR               string
	Gateway            string
	Bridge             string
	DNSServers         []string
	IPv6Policy         string
	AllocationStrategy string
	State              string
}

// IPAllocation là một địa chỉ IPv4 trong vòng đời IPAM — bản ghi
// ip_allocations, theo Phần VI mục 2.4 và mục 3.
//
// State chỉ được chuyển theo đúng chuỗi FREE → RESERVED → ASSIGNED →
// QUARANTINED → RELEASED (Phần VI mục 3.1); domain không tự enforce
// transition, đó là trách nhiệm của IPAM repository/state engine.
type IPAllocation struct {
	ID            string
	SegmentID     string
	Address       string
	InstanceID    *string
	State         AllocationState
	ReservedUntil *time.Time
	AssignedAt    *time.Time
	ReleasedAt    *time.Time
	CreatedAt     time.Time
}

// IsExpiredReservation báo hiệu một RESERVED allocation đã quá TTL và đủ
// điều kiện để reaper thu hồi (Phần VI mục 3.3), miễn là job liên quan
// không còn active — điều kiện đó reaper phải tự kiểm tra riêng.
func (a IPAllocation) IsExpiredReservation(now time.Time) bool {
	return a.State == AllocationReserved && a.ReservedUntil != nil && now.After(*a.ReservedUntil)
}
