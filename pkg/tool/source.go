package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Source is the interface for loading manifests from some backing store.
type Source interface {
	Load(ctx context.Context) ([]*Manifest, error)
}

// LocalSource loads manifests from a directory on disk (dev / seed use).
type LocalSource struct {
	dir string
}

// NewLocalSource returns a Source backed by a local directory.
func NewLocalSource(dir string) *LocalSource { return &LocalSource{dir: dir} }

func (s *LocalSource) Load(_ context.Context) ([]*Manifest, error) {
	return loadFromDir(s.dir)
}

// GitSource clones / pulls a git repository and loads manifests from it.
type GitSource struct {
	gitURL string
	branch string
	// cloneDir is populated after the first clone.
	cloneDir string
}

// NewGitSource returns a Source backed by a remote git repository.
func NewGitSource(gitURL, branch string) *GitSource {
	return &GitSource{gitURL: gitURL, branch: branch}
}

func (s *GitSource) Load(ctx context.Context) ([]*Manifest, error) {
	if s.cloneDir == "" {
		dir, err := os.MkdirTemp("", "tools-registry-*")
		if err != nil {
			return nil, err
		}
		s.cloneDir = dir
		if err := gitClone(ctx, s.gitURL, s.branch, dir); err != nil {
			return nil, fmt.Errorf("git clone: %w", err)
		}
	} else {
		if err := gitPull(ctx, s.cloneDir); err != nil {
			return nil, fmt.Errorf("git pull: %w", err)
		}
	}
	return loadFromDir(filepath.Join(s.cloneDir, "tools"))
}

func loadFromDir(root string) ([]*Manifest, error) {
	var manifests []*Manifest

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "manifest.yaml" {
			return nil
		}
		m, err := ParseManifest(path)
		if err != nil {
			// Log and skip bad manifests rather than aborting the whole load.
			fmt.Fprintf(os.Stderr, "WARN: skipping %s: %v\n", path, err)
			return nil
		}
		manifests = append(manifests, m)
		return nil
	})
	return manifests, err
}

func gitClone(ctx context.Context, url, branch, dir string) error {
	return runGit(ctx, "clone", "--depth=1", "--branch", branch, url, dir)
}

func gitPull(ctx context.Context, dir string) error {
	return runGit(ctx, "-C", dir, "pull", "--ff-only")
}

func runGit(ctx context.Context, args ...string) error {
	return runCmd(ctx, "git", args...)
}
