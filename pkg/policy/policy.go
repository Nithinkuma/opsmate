// Package policy resolves per-(tool_id, repo) merge and approval policy.
package policy

// State is the trust-ladder level for a registered tool.
type State string

const (
	// StateReview is the default: every produced PR requires human approval.
	StateReview State = "review"
	// StateAutoMergeEligible: tool has N clean runs; PR can be auto-merged
	// once CI is green, but the operator must explicitly promote.
	StateAutoMergeEligible State = "auto_merge_eligible"
	// StateAutoMergeActive: PRs are auto-merged after CI green.
	StateAutoMergeActive State = "auto_merge_active"
)

// Policy is the resolved policy for a single (tool_id, repo) pair.
type Policy struct {
	ToolID              string
	Repo                string
	State               State
	RequireHumanApproval bool
	AutoMerge           bool
	Approvers           []string
}

// Resolve returns the effective policy for a tool.
// IntentPolicy fields (from the ticket) can tighten but not relax the stored state.
func Resolve(stored State, requireHumanFromTicket bool, approvers []string) Policy {
	p := Policy{
		State:     stored,
		Approvers: approvers,
	}

	switch stored {
	case StateAutoMergeActive:
		p.AutoMerge = true
		p.RequireHumanApproval = requireHumanFromTicket
	case StateAutoMergeEligible:
		p.AutoMerge = false
		p.RequireHumanApproval = true
	default: // StateReview
		p.AutoMerge = false
		p.RequireHumanApproval = true
	}

	// Ticket can always tighten approval requirements.
	if requireHumanFromTicket {
		p.RequireHumanApproval = true
		p.AutoMerge = false
	}

	return p
}

// CanDemote returns true if a failed run should demote the tool one level.
func CanDemote(current State) (State, bool) {
	switch current {
	case StateAutoMergeActive:
		return StateAutoMergeEligible, true
	case StateAutoMergeEligible:
		return StateReview, true
	default:
		return StateReview, false
	}
}
