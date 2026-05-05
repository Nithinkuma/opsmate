package tool

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nithinkuma/opsmate/pkg/policy"
)

// ResolveStatus describes how a tool was found.
type ResolveStatus int

const (
	ResolveExact    ResolveStatus = iota // exact (repo, verb) match
	ResolvePattern                       // repo matched by glob pattern
	ResolveTemplate                      // cross-repo template for same verb
	ResolveNotFound                      // no match
)

func (s ResolveStatus) String() string {
	switch s {
	case ResolveExact:
		return "exact"
	case ResolvePattern:
		return "pattern"
	case ResolveTemplate:
		return "template"
	default:
		return "not_found"
	}
}

// Entry is an indexed tool with its policy state.
type Entry struct {
	Manifest    *Manifest
	PolicyState policy.State
}

// Registry is the in-memory tool index. Thread-safe.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry // key: "normalised_repo::verb"
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*Entry)}
}

// Register adds or replaces a manifest entry after verifying its hashes.
func (r *Registry) Register(m *Manifest) error {
	if err := m.VerifyHashes(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[entryKey(m.Repo.Pattern, m.Verb)] = &Entry{
		Manifest:    m,
		PolicyState: policy.StateReview,
	}
	return nil
}

// Resolve finds the best tool for (repo, verb) using three tiers.
func (r *Registry) Resolve(repo, verb string) (*Entry, ResolveStatus) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	norm := normalizeRepo(repo)

	// Tier 1: exact match
	if e, ok := r.entries[entryKey(norm, verb)]; ok {
		return e, ResolveExact
	}

	// Tier 2: glob pattern match
	for _, e := range r.entries {
		if e.Manifest.Verb != verb {
			continue
		}
		pat := normalizeRepo(e.Manifest.Repo.Pattern)
		if matched, _ := filepath.Match(pat, norm); matched {
			return e, ResolvePattern
		}
	}

	// Tier 3: any tool with the same verb (cross-repo template)
	for _, e := range r.entries {
		if e.Manifest.Verb == verb {
			return e, ResolveTemplate
		}
	}

	return nil, ResolveNotFound
}

// Get returns an entry by tool ID.
func (r *Registry) Get(toolID string) (*Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		if e.Manifest.ID == toolID {
			return e, true
		}
	}
	return nil, false
}

// AllForVerb returns all entries that implement a given verb.
func (r *Registry) AllForVerb(verb string) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Entry
	for _, e := range r.entries {
		if e.Manifest.Verb == verb {
			out = append(out, e)
		}
	}
	return out
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// SetPolicyState updates the policy state for a tool.
func (r *Registry) SetPolicyState(toolID string, state policy.State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.Manifest.ID == toolID {
			e.PolicyState = state
			return nil
		}
	}
	return fmt.Errorf("registry: tool %q not found", toolID)
}

// All returns every registered entry.
func (r *Registry) All() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	return out
}

func entryKey(repo, verb string) string {
	return normalizeRepo(repo) + "::" + verb
}

func normalizeRepo(repo string) string {
	return strings.ToLower(strings.ReplaceAll(repo, "/", "__"))
}
