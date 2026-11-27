package remotesource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"GoWork/internal/netfetch"
)

type treeResponse struct {
	SHA       string      `json:"sha"`
	Tree      []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

func (r *Resolver) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	requestURL := r.apiURL("repos", owner, repo)
	response, err := r.getGitHub(ctx, requestURL, githubAcceptJSON)
	if err != nil {
		return "", r.classifyError(err, fmt.Sprintf("read repository %s/%s", owner, repo))
	}

	var metadata struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(response.Body, &metadata); err != nil {
		return "", fmt.Errorf("remotesource: decode repository %s/%s metadata: %w", owner, repo, err)
	}
	branch := strings.TrimSpace(metadata.DefaultBranch)
	if branch == "" {
		return "", fmt.Errorf("remotesource: repository %s/%s has no default branch", owner, repo)
	}
	return branch, nil
}

func (r *Resolver) discoverGitHub(ctx context.Context, owner, repo, ref, prefix string) ([]Candidate, error) {
	root, err := r.fetchTree(ctx, owner, repo, ref, true)
	if err != nil {
		return nil, err
	}

	entries := root.Tree
	if root.Truncated {
		// GitHub truncates the recursive tree for very large repositories.
		// Walk one directory at a time instead so discovery stays complete.
		entries, err = r.walkTrees(ctx, owner, repo, ref)
		if err != nil {
			return nil, err
		}
	}

	prefix = strings.Trim(prefix, "/")
	seen := make(map[string]struct{})
	candidates := make([]Candidate, 0)
	for _, entry := range entries {
		if entry.Type != "blob" {
			continue
		}
		entryPath := strings.TrimPrefix(strings.ReplaceAll(entry.Path, `\`, "/"), "/")
		if !pathUnderPrefix(entryPath, prefix) || ignoredTreePath(entryPath) {
			continue
		}
		kind, ok := ClassifyPath(entryPath)
		if !ok {
			continue
		}
		if _, duplicate := seen[entryPath]; duplicate {
			continue
		}
		seen[entryPath] = struct{}{}
		candidates = append(candidates, Candidate{
			Path: entryPath,
			Kind: kind,
			URL:  r.contentsURL(owner, repo, ref, entryPath),
			SHA:  entry.SHA,
			Size: entry.Size,
		})
	}
	sortCandidates(candidates)
	return candidates, nil
}

// walkTrees performs a breadth-first, per-directory tree walk. It is used
// when the recursive tree API reports truncation.
func (r *Resolver) walkTrees(ctx context.Context, owner, repo, ref string) ([]treeEntry, error) {
	type node struct {
		sha  string
		path string
	}

	queue := []node{{sha: ref}}
	visited := make(map[string]struct{})
	entries := make([]treeEntry, 0)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.sha == "" {
			continue
		}
		key := current.sha + "\x00" + current.path
		if _, seen := visited[key]; seen {
			continue
		}
		visited[key] = struct{}{}

		tree, err := r.fetchTree(ctx, owner, repo, current.sha, false)
		if err != nil {
			return nil, err
		}
		if tree.Truncated {
			return nil, fmt.Errorf("%w: directory %q", ErrTreeTruncated, current.path)
		}

		directories := make([]node, 0)
		for _, entry := range tree.Tree {
			entryPath := strings.TrimPrefix(strings.ReplaceAll(entry.Path, `\`, "/"), "/")
			if current.path != "" {
				entryPath = current.path + "/" + entryPath
			}
			switch entry.Type {
			case "tree":
				if ignoredTreePath(entryPath) {
					continue
				}
				directories = append(directories, node{sha: entry.SHA, path: entryPath})
			case "blob":
				entry.Path = entryPath
				entries = append(entries, entry)
			}
		}
		// Visit directories in path order so request order is deterministic.
		sort.Slice(directories, func(i, j int) bool { return directories[i].path < directories[j].path })
		queue = append(queue, directories...)
	}
	return entries, nil
}

func (r *Resolver) fetchTree(ctx context.Context, owner, repo, ref string, recursive bool) (*treeResponse, error) {
	requestURL := r.apiURL("repos", owner, repo, "git", "trees", ref)
	if recursive {
		requestURL += "?recursive=1"
	}
	response, err := r.getGitHub(ctx, requestURL, githubAcceptJSON)
	if err != nil {
		return nil, r.classifyError(err, fmt.Sprintf("discover repository %s/%s", owner, repo))
	}

	var tree treeResponse
	if err := json.Unmarshal(response.Body, &tree); err != nil {
		return nil, fmt.Errorf("remotesource: decode repository %s/%s tree: %w", owner, repo, err)
	}
	return &tree, nil
}

func (r *Resolver) getGitHub(ctx context.Context, rawURL, accept string) (*netfetch.Response, error) {
	if r.fetcher == nil {
		return nil, fmt.Errorf("remotesource: fetcher is not initialized")
	}
	headers := make(http.Header)
	headers.Set("Accept", accept)
	headers.Set("X-GitHub-Api-Version", githubAPIVersion)
	if r.shouldSendToken(rawURL) {
		netfetch.SetGitHubAuthHeader(headers, r.token)
	}
	return r.fetcher.Fetch(ctx, rawURL, headers)
}

func (r *Resolver) apiURL(segments ...string) string {
	builder := strings.TrimRight(r.apiBase, "/")
	for _, segment := range segments {
		builder += "/" + url.PathEscape(segment)
	}
	return builder
}

func (r *Resolver) contentsURL(owner, repo, ref, filePath string) string {
	builder := r.apiURL("repos", owner, repo, "contents")
	for _, segment := range strings.Split(strings.Trim(filePath, "/"), "/") {
		if segment == "" {
			continue
		}
		builder += "/" + url.PathEscape(segment)
	}
	return builder + "?ref=" + url.QueryEscape(ref)
}

func pathUnderPrefix(filePath, prefix string) bool {
	if prefix == "" {
		return true
	}
	return filePath == prefix || strings.HasPrefix(filePath, prefix+"/")
}

func ignoredTreePath(filePath string) bool {
	for _, segment := range strings.Split(filePath, "/") {
		if skipDirName(segment) {
			return true
		}
	}
	return false
}
