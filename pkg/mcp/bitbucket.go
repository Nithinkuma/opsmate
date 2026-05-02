package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// BitbucketClient is a typed wrapper for the Bitbucket MCP server.
type BitbucketClient struct {
	base *BaseClient
}

// NewBitbucketClient creates a Bitbucket MCP client.
func NewBitbucketClient(serverURL, authToken string) (*BitbucketClient, error) {
	b, err := NewBaseClient(serverURL, authToken)
	if err != nil {
		return nil, err
	}
	return &BitbucketClient{base: b}, nil
}

// PR holds a minimal Bitbucket pull-request record.
type PR struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"links_self_href"`
	State       string `json:"state"`
}

// CreatePRRequest carries parameters for opening a pull request.
type CreatePRRequest struct {
	Repo        string
	Title       string
	Description string
	SourceBranch string
	TargetBranch string
	Reviewers   []string
}

// CreatePR opens a pull request on Bitbucket via MCP.
func (b *BitbucketClient) CreatePR(ctx context.Context, req CreatePRRequest) (*PR, error) {
	args := map[string]interface{}{
		"workspace":     repoWorkspace(req.Repo),
		"repo_slug":     repoSlug(req.Repo),
		"title":         req.Title,
		"description":   req.Description,
		"source_branch": req.SourceBranch,
		"target_branch": req.TargetBranch,
	}
	if len(req.Reviewers) > 0 {
		args["reviewers"] = req.Reviewers
	}

	raw, err := b.base.CallTool(ctx, "create_pull_request", args)
	if err != nil {
		return nil, err
	}

	var pr PR
	if err := json.Unmarshal([]byte(raw), &pr); err != nil {
		return nil, fmt.Errorf("bitbucket: parse PR response: %w", err)
	}
	return &pr, nil
}

// GetFile retrieves a file from a repository at the given ref.
func (b *BitbucketClient) GetFile(ctx context.Context, repo, path, ref string) (string, error) {
	return b.base.CallTool(ctx, "get_file_content", map[string]interface{}{
		"workspace": repoWorkspace(repo),
		"repo_slug": repoSlug(repo),
		"path":      path,
		"ref":       ref,
	})
}

// ListFiles lists files in a directory of a repository.
func (b *BitbucketClient) ListFiles(ctx context.Context, repo, path string) (string, error) {
	return b.base.CallTool(ctx, "list_directory", map[string]interface{}{
		"workspace": repoWorkspace(repo),
		"repo_slug": repoSlug(repo),
		"path":      path,
	})
}

// SearchPRs searches for pull requests matching a query string.
func (b *BitbucketClient) SearchPRs(ctx context.Context, repo, query string) (string, error) {
	return b.base.CallTool(ctx, "search_pull_requests", map[string]interface{}{
		"workspace": repoWorkspace(repo),
		"repo_slug": repoSlug(repo),
		"query":     query,
	})
}

// GetPRDiff retrieves the diff for a pull request.
func (b *BitbucketClient) GetPRDiff(ctx context.Context, repo string, prID int) (string, error) {
	return b.base.CallTool(ctx, "get_pull_request_diff", map[string]interface{}{
		"workspace": repoWorkspace(repo),
		"repo_slug": repoSlug(repo),
		"pr_id":     prID,
	})
}

// PushBranch pushes a branch to Bitbucket.
func (b *BitbucketClient) PushBranch(ctx context.Context, repo, branch, commitMsg string, files map[string]string) error {
	_, err := b.base.CallTool(ctx, "push_branch", map[string]interface{}{
		"workspace":      repoWorkspace(repo),
		"repo_slug":      repoSlug(repo),
		"branch":         branch,
		"commit_message": commitMsg,
		"files":          files,
	})
	return err
}

func repoWorkspace(repo string) string {
	for i, c := range repo {
		if c == '/' {
			return repo[:i]
		}
	}
	return repo
}

func repoSlug(repo string) string {
	for i, c := range repo {
		if c == '/' {
			return repo[i+1:]
		}
	}
	return repo
}
