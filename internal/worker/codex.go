package worker

import (
	"os"
	"path/filepath"
)

// Codex runs codex exec. In write mode the workspace-write sandbox also keeps
// it from committing, since a worktree's git dir lives outside the workspace.
type Codex struct{}

func (Codex) Name() string { return "codex" }

func (Codex) Command(req Request, tmp string) []string {
	sandbox := "read-only"
	if req.Write {
		sandbox = "workspace-write"
	}
	argv := []string{"codex", "exec", "-s", sandbox, "-C", req.Dir,
		"--skip-git-repo-check", "--output-last-message", filepath.Join(tmp, "last.txt")}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	argv = append(argv, req.Args...)
	return append(argv, "-") // prompt from stdin
}

func (Codex) Parse(_ []byte, tmp string) (string, error) {
	b, err := os.ReadFile(filepath.Join(tmp, "last.txt"))
	return string(b), err
}
