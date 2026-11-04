package cilog

// githubapi.go — minimal GitHub REST client for the consolidated log
// collector. Standard library only: the collector must stay runnable
// even when the application dependency graph is broken.
//
// The GitHub job/step conclusions are the AUTHORITATIVE status layer
// for the consolidated report; text scanning of captured logs is only
// supplemental (a failing job does not necessarily emit a `::error::`
// line, and custom command capture may be truncated before the failing
// command). This client therefore fetches, for the exact run attempt:
//
//   - every job of the workflow run (name, conclusion, steps);
//   - the complete raw log of every job (including post steps the
//     custom capture never sees);
//   - the open code scanning alerts for the analyzed commit (CodeQL
//     evidence for the same commit).
//
// Every call is best-effort: on failure the collector records the exact
// reason in the report and continues with the artifact-based evidence.
// It never fabricates job, step, alert or exit-code values.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// StepConclusion is one workflow step as reported by the API.
type StepConclusion struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// JobConclusion is one workflow job as reported by the API.
type JobConclusion struct {
	ID          int              `json:"id"`
	Name        string           `json:"name"`
	Status      string           `json:"status"`
	Conclusion  string           `json:"conclusion"`
	StartedAt   string           `json:"started_at"`
	CompletedAt string           `json:"completed_at"`
	RunnerName  string           `json:"runner_name"`
	RunnerGroup string           `json:"runner_group_name"`
	Labels      []string         `json:"labels"`
	Steps       []StepConclusion `json:"steps"`
}

// CodeScanningAlert is one open code scanning alert.
type CodeScanningAlert struct {
	Number    int    `json:"number"`
	RuleID    string `json:"rule_id"`
	RuleDesc  string `json:"rule_description"`
	Severity  string `json:"rule_severity"`
	Tool      string `json:"tool"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url"`
	FilePath  string `json:"path"`
	StartLine int    `json:"start_line"`
}

// GitHubClient queries the GitHub REST API for authoritative run data.
type GitHubClient struct {
	BaseURL string // e.g. https://api.github.com (override in tests)
	Repo    string // "owner/name"
	Token   string // may be empty (public, unauthenticated; rate limits apply)
	HTTP    *http.Client
	Timeout time.Duration
}

// NewGitHubClient builds a client; baseURL empty means the public API.
func NewGitHubClient(baseURL, repo, token string) *GitHubClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Repo:    repo,
		Token:   token,
		HTTP:    &http.Client{},
		Timeout: 20 * time.Second,
	}
}

func (c *GitHubClient) get(ctx context.Context, path string, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found (HTTP 404) for %s", path)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("access denied (HTTP %d) for %s", resp.StatusCode, path)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected HTTP %d for %s", resp.StatusCode, path)
	}
	return body, nil
}

// FetchRunJobs returns every job of the given run attempt (up to 3
// pages of 100 jobs, enough for this workflow and its matrix).
func (c *GitHubClient) FetchRunJobs(ctx context.Context, runID, attempt string) ([]JobConclusion, error) {
	var all []JobConclusion
	for page := 1; page <= 3; page++ {
		q := fmt.Sprintf("/repos/%s/actions/runs/%s/attempts/%s/jobs?per_page=100&page=%d",
			c.Repo, url.PathEscape(runID), url.PathEscape(attempt), page)
		body, err := c.get(ctx, q, "application/vnd.github+json")
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		var decoded struct {
			Jobs []JobConclusion `json:"jobs"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			if page == 1 {
				return nil, fmt.Errorf("decoding jobs response: %w", err)
			}
			break
		}
		all = append(all, decoded.Jobs...)
		if len(decoded.Jobs) < 100 {
			break
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no jobs returned for run %s attempt %s", runID, attempt)
	}
	return all, nil
}

// FetchJobLog returns the complete raw log text of one job. GitHub
// serves this as a redirect to a signed temporary URL; net/http
// follows it automatically.
func (c *GitHubClient) FetchJobLog(ctx context.Context, jobID int) (string, error) {
	body, err := c.get(ctx,
		fmt.Sprintf("/repos/%s/actions/jobs/%d/logs", c.Repo, jobID),
		"application/vnd.github+json")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// FetchOpenAlerts returns the open code scanning alerts for a ref
// (commit SHA). A 403/404 is a real access limitation and is returned
// as an error for the caller to record — never silently ignored.
func (c *GitHubClient) FetchOpenAlerts(ctx context.Context, ref string) ([]CodeScanningAlert, error) {
	var all []CodeScanningAlert
	for page := 1; page <= 5; page++ {
		q := fmt.Sprintf("/repos/%s/code-scanning/alerts?state=open&ref=%s&per_page=100&page=%d",
			c.Repo, url.QueryEscape(ref), page)
		body, err := c.get(ctx, q, "application/vnd.github+json")
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		var decoded []CodeScanningAlert
		if err := json.Unmarshal(body, &decoded); err != nil {
			if page == 1 {
				return nil, fmt.Errorf("decoding alerts response: %w", err)
			}
			break
		}
		all = append(all, decoded...)
		if len(decoded) < 100 {
			break
		}
	}
	return all, nil
}

// ParseJobID converts a string job id if needed elsewhere.
func ParseJobID(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
