package agent

import (
	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/nithinkuma/opsmate/pkg/tool"
)

// Resolver performs a pure deterministic lookup of a tool for a given Intent.
type Resolver struct {
	registry *tool.Registry
}

// NewResolver constructs a Resolver backed by the given registry.
func NewResolver(registry *tool.Registry) *Resolver {
	return &Resolver{registry: registry}
}

// ResolveResult bundles the matched tool with how it was found.
type ResolveResult struct {
	Entry  *tool.Entry
	Status tool.ResolveStatus
}

// Resolve finds the best tool for the Intent's (repo, verb) key.
// It never calls an LLM; it is always deterministic.
func (r *Resolver) Resolve(i *intent.Intent) ResolveResult {
	entry, status := r.registry.Resolve(i.Action.Target.Repo, i.Action.Verb)
	return ResolveResult{Entry: entry, Status: status}
}
