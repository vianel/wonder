// Package gh is a thin wrapper over the gh CLI, so it reuses the user's auth.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PollInterval is how often CI status is checked. Tests shorten it.
var PollInterval = 15 * time.Second

type Client struct{ Dir string }

func (c Client) run(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = c.Dir
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return out, fmt.Errorf("gh %s: %s", strings.Join(args[:2], " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

func (c Client) json(v any, args ...string) error {
	out, err := c.run(args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

func (c Client) Issue(n int) (Issue, error) {
	var i Issue
	return i, c.json(&i, "issue", "view", strconv.Itoa(n), "--json", "number,title,body,url")
}

func (c Client) OpenIssues() ([]Issue, error) {
	var is []Issue
	return is, c.json(&is, "issue", "list", "--state", "open", "--limit", "200", "--json", "number,title,url")
}

func (c Client) Repo() (string, error) {
	var r struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	return r.NameWithOwner, c.json(&r, "repo", "view", "--json", "nameWithOwner")
}

type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

var prNum = regexp.MustCompile(`/pull/(\d+)`)

// EnsurePR returns the open PR for head, creating it if needed.
func (c Client) EnsurePR(base, head, title, body string) (PR, error) {
	var prs []PR
	if err := c.json(&prs, "pr", "list", "--head", head, "--state", "open", "--json", "number,url"); err != nil {
		return PR{}, err
	}
	if len(prs) > 0 {
		return prs[0], nil
	}
	out, err := c.run("pr", "create", "--base", base, "--head", head, "--title", title, "--body", body)
	if err != nil {
		return PR{}, err
	}
	url := strings.TrimSpace(string(out))
	m := prNum.FindStringSubmatch(url)
	if m == nil {
		return PR{}, fmt.Errorf("gh pr create: unexpected output %q", url)
	}
	n, _ := strconv.Atoi(m[1])
	return PR{Number: n, URL: url}, nil
}

type Check struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"` // pass, fail, pending, skipping, cancel
	Link   string `json:"link"`
}

// CI results.
const (
	CIPass = "pass"
	CIFail = "fail"
	CINone = "none"
)

// WaitChecks blocks until every check on pr finishes. It returns CINone if no
// checks show up within a minute of the push.
func (c Client) WaitChecks(ctx context.Context, pr int) (string, []Check, error) {
	for empty := 0; ; {
		var checks []Check
		// gh exits non-zero when checks fail or are pending; the JSON is still valid.
		out, err := c.run("pr", "checks", strconv.Itoa(pr), "--json", "name,bucket,link")
		if jerr := json.Unmarshal(out, &checks); jerr != nil {
			if err != nil && !strings.Contains(err.Error(), "no checks") {
				return "", nil, err
			}
			checks = nil
		}
		pending, failed := false, []Check(nil)
		for _, ch := range checks {
			switch ch.Bucket {
			case "pending":
				pending = true
			case "fail", "cancel":
				failed = append(failed, ch)
			}
		}
		switch {
		case len(checks) == 0 && empty >= 4:
			return CINone, nil, nil
		case len(checks) == 0:
			empty++
		case !pending && len(failed) > 0:
			return CIFail, failed, nil
		case !pending:
			return CIPass, nil, nil
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(PollInterval):
		}
	}
}

var runID = regexp.MustCompile(`/actions/runs/(\d+)`)

// FailedLogs returns the failed-step logs of the given checks, trimmed to the
// tail so they fit in a prompt.
func (c Client) FailedLogs(checks []Check) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, ch := range checks {
		fmt.Fprintf(&b, "Check failed: %s %s\n", ch.Name, ch.Link)
		m := runID.FindStringSubmatch(ch.Link)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out, err := c.run("run", "view", m[1], "--log-failed")
		if err != nil {
			continue
		}
		const max = 15000
		if len(out) > max {
			out = out[len(out)-max:]
		}
		b.Write(out)
		b.WriteString("\n")
	}
	return b.String()
}

// Conflicting reports whether pr conflicts with its base. GitHub computes
// mergeability lazily, so UNKNOWN is retried a few times and then treated as
// not conflicting.
func (c Client) Conflicting(ctx context.Context, pr int) (bool, error) {
	for i := 0; i < 5; i++ {
		var s struct {
			Mergeable string `json:"mergeable"`
		}
		if err := c.json(&s, "pr", "view", strconv.Itoa(pr), "--json", "mergeable"); err != nil {
			return false, err
		}
		if s.Mergeable != "UNKNOWN" {
			return s.Mergeable == "CONFLICTING", nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(PollInterval / 3):
		}
	}
	return false, nil
}

func (c Client) Comment(pr int, body string) error {
	_, err := c.run("pr", "comment", strconv.Itoa(pr), "--body", body)
	return err
}

func (c Client) Merge(pr int) error {
	_, err := c.run("pr", "merge", strconv.Itoa(pr), "--squash")
	return err
}

type PRState struct {
	State       string    `json:"state"`
	MergedAt    time.Time `json:"mergedAt"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

func (c Client) PRState(pr int) (PRState, error) {
	var s PRState
	return s, c.json(&s, "pr", "view", strconv.Itoa(pr), "--json", "state,mergedAt,mergeCommit")
}

// Reverted reports whether a merged PR has a merged revert PR ("Reverts owner/repo#N"
// in the body, as GitHub's Revert button writes) within 7 days of mergedAt.
func (c Client) Reverted(pr int, mergedAt time.Time) (bool, error) {
	var prs []struct {
		Body     string    `json:"body"`
		Title    string    `json:"title"`
		MergedAt time.Time `json:"mergedAt"`
	}
	if err := c.json(&prs, "pr", "list", "--state", "merged", "--search", "revert in:title", "--limit", "100", "--json", "body,title,mergedAt"); err != nil {
		return false, err
	}
	ref := regexp.MustCompile(`(?i)revert.*#` + strconv.Itoa(pr) + `\b`)
	for _, p := range prs {
		if ref.MatchString(p.Title+"\n"+p.Body) && p.MergedAt.After(mergedAt) && p.MergedAt.Sub(mergedAt) <= 7*24*time.Hour {
			return true, nil
		}
	}
	return false, nil
}
