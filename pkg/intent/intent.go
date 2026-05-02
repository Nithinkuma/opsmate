// Package intent defines the central contract between all system components.
// The Intent schema is the highest-stability schema in the system; treat it
// as an API — every change is a breaking change downstream.
package intent

import (
	"time"
)

const SchemaVersion = "1"

// Intent is the structured representation of a Jira ticket action.
// It is produced by the Intent Agent and consumed by all downstream pipelines.
type Intent struct {
	IntentID      string  `json:"intent_id"`
	SchemaVersion string  `json:"schema_version"`
	Source        Source  `json:"source"`
	Action        Action  `json:"action"`
	Context       Context `json:"context"`
	Policy        Policy  `json:"policy"`
}

// Source describes where the intent originated.
type Source struct {
	System               string    `json:"system"`
	TicketID             string    `json:"ticket_id"`
	URL                  string    `json:"url"`
	Reporter             string    `json:"reporter"`
	FetchedAt            time.Time `json:"fetched_at"`
	// RepoResolutionMethod is set for auditability when the repo was
	// resolved via the third-tier (LLM extraction) rather than explicitly.
	RepoResolutionMethod string `json:"repo_resolution_method,omitempty"`
}

// Action describes what should be done.
type Action struct {
	Verb       string                 `json:"verb"`
	Target     Target                 `json:"target"`
	Parameters map[string]interface{} `json:"parameters"`
}

// Target identifies the git repository and scope.
type Target struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Scope  string `json:"scope,omitempty"`
}

// Context carries metadata from the original ticket.
type Context struct {
	DescriptionRaw string   `json:"description_raw"`
	LinkedTickets  []string `json:"linked_tickets,omitempty"`
	Priority       string   `json:"priority,omitempty"`
}

// Policy controls merge and approval behaviour for the produced PR.
type Policy struct {
	AutoMerge            bool     `json:"auto_merge"`
	RequireHumanApproval bool     `json:"require_human_approval"`
	Approvers            []string `json:"approvers,omitempty"`
}
