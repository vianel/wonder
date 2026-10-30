package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	// --json puts events (with token usage) on stdout; the final message
	// still goes to the --output-last-message file.
	argv := []string{"codex", "exec", "-s", sandbox, "-C", req.Dir, "--json",
		"--skip-git-repo-check", "--output-last-message", filepath.Join(tmp, "last.txt")}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	argv = append(argv, req.Args...)
	return append(argv, "-") // prompt from stdin
}

// Parse sums the usage of every turn.completed event. Codex reports tokens
// only; a cost comes from [pricing.codex] in wonder.toml, if set.
func (Codex) Parse(stdout []byte, tmp string) (string, Usage, error) {
	var u Usage
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type  string `json:"type"`
			Usage struct {
				Input  int64 `json:"input_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Output int64 `json:"output_tokens"` // includes reasoning tokens
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "turn.completed" {
			u.Input += ev.Usage.Input - ev.Usage.Cached // input_tokens includes cached ones
			u.CachedInput += ev.Usage.Cached
			u.Output += ev.Usage.Output
		}
	}
	b, err := os.ReadFile(filepath.Join(tmp, "last.txt"))
	return string(b), u, err
}
