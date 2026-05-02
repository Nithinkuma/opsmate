package policy_test

import (
	"testing"

	"github.com/nithinkuma/opsmate/pkg/policy"
	"github.com/stretchr/testify/require"
)

func TestResolve_ReviewState(t *testing.T) {
	p := policy.Resolve(policy.StateReview, false, nil)
	require.False(t, p.AutoMerge)
	require.True(t, p.RequireHumanApproval)
	require.Equal(t, policy.StateReview, p.State)
}

func TestResolve_AutoMergeEligible(t *testing.T) {
	p := policy.Resolve(policy.StateAutoMergeEligible, false, nil)
	require.False(t, p.AutoMerge)
	require.True(t, p.RequireHumanApproval)
}

func TestResolve_AutoMergeActive(t *testing.T) {
	p := policy.Resolve(policy.StateAutoMergeActive, false, nil)
	require.True(t, p.AutoMerge)
	require.False(t, p.RequireHumanApproval)
}

func TestResolve_TicketCanTighten(t *testing.T) {
	// Even auto_merge_active is tightened if ticket says require human
	p := policy.Resolve(policy.StateAutoMergeActive, true, nil)
	require.False(t, p.AutoMerge)
	require.True(t, p.RequireHumanApproval)
}

func TestResolve_ApproversPassedThrough(t *testing.T) {
	approvers := []string{"@alice", "@bob"}
	p := policy.Resolve(policy.StateReview, false, approvers)
	require.Equal(t, approvers, p.Approvers)
}

func TestCanDemote_FromAutoMergeActive(t *testing.T) {
	next, ok := policy.CanDemote(policy.StateAutoMergeActive)
	require.True(t, ok)
	require.Equal(t, policy.StateAutoMergeEligible, next)
}

func TestCanDemote_FromAutoMergeEligible(t *testing.T) {
	next, ok := policy.CanDemote(policy.StateAutoMergeEligible)
	require.True(t, ok)
	require.Equal(t, policy.StateReview, next)
}

func TestCanDemote_FromReview(t *testing.T) {
	next, ok := policy.CanDemote(policy.StateReview)
	require.False(t, ok)
	require.Equal(t, policy.StateReview, next)
}
