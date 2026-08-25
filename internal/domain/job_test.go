package domain

import (
	"testing"
	"time"
)

func TestProvisioningJob_IsClaimable(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	cases := []struct {
		name string
		job  ProvisioningJob
		want bool
	}{
		{"queued and due", ProvisioningJob{State: JobQueued, NextAttemptAt: past}, true},
		{"retry_wait and due", ProvisioningJob{State: JobRetryWait, NextAttemptAt: past}, true},
		{"queued but not due yet", ProvisioningJob{State: JobQueued, NextAttemptAt: future}, false},
		{"running is not claimable", ProvisioningJob{State: JobRunning, NextAttemptAt: past}, false},
		{"succeeded is not claimable", ProvisioningJob{State: JobSucceeded, NextAttemptAt: past}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.job.IsClaimable(now); got != c.want {
				t.Errorf("IsClaimable() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProvisioningJob_IsLeased(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)
	owner := "worker-1"

	cases := []struct {
		name string
		job  ProvisioningJob
		want bool
	}{
		{"no owner", ProvisioningJob{}, false},
		{"owner but expired", ProvisioningJob{LeaseOwner: &owner, LeaseExpiresAt: &past}, false},
		{"owner and valid", ProvisioningJob{LeaseOwner: &owner, LeaseExpiresAt: &future}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.job.IsLeased(now); got != c.want {
				t.Errorf("IsLeased() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTemplate_IsUsable(t *testing.T) {
	if (Template{State: TemplateActive}).IsUsable() != true {
		t.Error("ACTIVE template should be usable")
	}
	for _, s := range []TemplateState{TemplateDraft, TemplateCandidate, TemplateDeprecated, TemplateRevoked} {
		if (Template{State: s}).IsUsable() {
			t.Errorf("%s template should not be usable", s)
		}
	}
}

func TestIPAllocation_IsExpiredReservation(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	cases := []struct {
		name  string
		alloc IPAllocation
		want  bool
	}{
		{"reserved and expired", IPAllocation{State: AllocationReserved, ReservedUntil: &past}, true},
		{"reserved but not expired", IPAllocation{State: AllocationReserved, ReservedUntil: &future}, false},
		{"free state ignored", IPAllocation{State: AllocationFree, ReservedUntil: &past}, false},
		{"no reserved_until", IPAllocation{State: AllocationReserved}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.alloc.IsExpiredReservation(now); got != c.want {
				t.Errorf("IsExpiredReservation() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestVMInstance_IsRetired(t *testing.T) {
	now := time.Now()
	if (VMInstance{}).IsRetired() {
		t.Error("instance without RetiredAt should not be retired")
	}
	if !(VMInstance{RetiredAt: &now}).IsRetired() {
		t.Error("instance with RetiredAt should be retired")
	}
}
