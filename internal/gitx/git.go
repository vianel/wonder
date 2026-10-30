// Package gitx wraps the git CLI: worktrees, commits, pushes and diffs.
package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Known limit: one lock for every git call. Worktrees share .git/config, refs and
// objects, and parallel runs race on their lock files; git calls are short
// next to agent runs. Narrow it if git ever shows up in profiles.
var mu sync.Mutex

// Run runs git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return strings.TrimSpace(string(out)), err
}

// Root returns the top-level directory of the repo containing dir.
func Root(dir string) (string, error) { return Run(dir, "rev-parse", "--show-toplevel") }

// WorktreeAdd creates path on a new branch from origin/base. An existing
// worktree at path is reused, so a resumed run keeps its work.
func WorktreeAdd(repo, path, branch, base string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if _, err := Run(repo, "fetch", "origin", base); err != nil {
		return err
	}
	_, err := Run(repo, "worktree", "add", "-B", branch, path, "origin/"+base)
	return err
}

// WorktreeAddDetached checks out sha in a throwaway worktree.
func WorktreeAddDetached(repo, path, sha string) error {
	_, err := Run(repo, "worktree", "add", "--detach", path, sha)
	return err
}

func WorktreeRemove(repo, path string) error {
	_, err := Run(repo, "worktree", "remove", "--force", path)
	return err
}

// CommitAll stages everything and commits. It reports false when there was
// nothing to commit.
func CommitAll(dir, msg string) (bool, error) {
	if _, err := Run(dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := Run(dir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	_, err := Run(dir, "commit", "-q", "-m", msg)
	return err == nil, err
}

func Push(dir, branch string) error {
	_, err := Run(dir, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	return err
}

func HeadSHA(dir string) (string, error) { return Run(dir, "rev-parse", "HEAD") }

// Diff returns the diff between two commits.
func Diff(dir, from, to string) (string, error) { return Run(dir, "diff", from, to) }

// Stats summarises a diff for the router.
type Stats struct {
	Files         int  `json:"files"`
	Added         int  `json:"added"`
	Deleted       int  `json:"deleted"`
	TestsDeleted  bool `json:"tests_deleted"`
	TestsWeakened bool `json:"tests_weakened"`
}

func (s Stats) Lines() int { return s.Added + s.Deleted }

var testFile = regexp.MustCompile(`(_test\.go|_test\.py|_spec\.rb|Test\.java|\.(test|spec)\.[jt]sx?)$|(^|/)tests?/|(^|/)test_[^/]*\.py$`)

// IsTest reports whether path looks like a test file.
func IsTest(path string) bool { return testFile.MatchString(path) }

func DiffStats(dir, from, to string) (Stats, error) {
	var s Stats
	num, err := Run(dir, "diff", "--numstat", "--no-renames", from, to)
	if err != nil {
		return s, err
	}
	for _, l := range lines(num) {
		f := strings.SplitN(l, "\t", 3)
		if len(f) < 3 {
			continue
		}
		a, _ := strconv.Atoi(f[0]) // "-" for binary files counts as 0
		d, _ := strconv.Atoi(f[1])
		s.Files++
		s.Added += a
		s.Deleted += d
		// Known limit: "weakened" = a test file lost more lines than it gained;
		// misses assertion swaps that keep the line count.
		if IsTest(f[2]) && d > a {
			s.TestsWeakened = true
		}
	}
	names, err := Run(dir, "diff", "--name-status", "--no-renames", "--diff-filter=D", from, to)
	if err != nil {
		return s, err
	}
	for _, l := range lines(names) {
		if f := strings.SplitN(l, "\t", 2); len(f) == 2 && IsTest(f[1]) {
			s.TestsDeleted = true
		}
	}
	return s, nil
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
