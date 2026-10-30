package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gh"
	"github.com/vianel/wonder/internal/runner"
	"github.com/vianel/wonder/internal/schema"
	"github.com/vianel/wonder/internal/worker"
)

// PromptData is what prompt templates can reference. Documented in docs/prompts.md.
type PromptData struct {
	Repo      string
	Issue     gh.Issue
	Spec      string // JSON
	Diff      string
	Reason    string
	Input     string
	Review    string // JSON, judge only
	GateNotes string // judge only
}

// Render executes a prompt template.
func Render(tpl string, d PromptData) (string, error) {
	t, err := template.New("prompt").Option("missingkey=error").Parse(tpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, d)
	return b.String(), err
}

// Hash is the short content hash recorded for every prompt.
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

// ModelName is how a worker's model is recorded.
func ModelName(w config.Worker) string {
	if w.Model == "" {
		return w.Worker + ":default"
	}
	return w.Model
}

// BlockedError means the worker needs a human decision.
type BlockedError struct{ Msg string }

func (e *BlockedError) Error() string { return "blocked: " + e.Msg }

// Invoker runs one worker and returns its schema-valid JSON.
type Invoker struct {
	Runner runner.Runner
	TmpDir string
}

// Invoke runs w with prompt in dir. Invalid JSON gets one retry with the
// validation error appended, then fails.
func (iv Invoker) Invoke(ctx context.Context, name string, w config.Worker, prompt, dir string, write bool, schemaName string) (json.RawMessage, error) {
	wk, err := worker.Get(w.Worker)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(iv.TmpDir, 0o755); err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		tmp, err := os.MkdirTemp(iv.TmpDir, name+"-*")
		if err != nil {
			return nil, err
		}
		req := worker.Request{Prompt: prompt, Model: w.Model, Dir: dir, Write: write, Args: w.Args}
		res, err := iv.Runner.Run(ctx, runner.Job{Name: name, Argv: wk.Command(req, tmp), Dir: dir, Stdin: prompt, Tmp: tmp})
		if err != nil {
			return nil, err
		}
		text, perr := wk.Parse(res.Stdout, tmp)
		if res.ExitCode != 0 || perr != nil {
			all := append(append([]byte{}, res.Stdout...), res.Stderr...)
			if rl := worker.DetectRateLimit(w.Worker, all, time.Now()); rl != nil {
				return nil, rl
			}
			if perr == nil {
				perr = fmt.Errorf("exit %d", res.ExitCode)
			}
			return nil, fmt.Errorf("%s (%s) failed: %v; stderr: %s (scratch: %s)", name, w.Worker, perr, tail(res.Stderr, 800), tmp)
		}
		out, verr := schema.Parse(schemaName, text)
		if verr == nil {
			os.RemoveAll(tmp)
			return out, nil
		}
		lastErr = verr
		prompt += fmt.Sprintf("\n\nYour previous reply was rejected: %v\nPrevious reply:\n%s\n\nReply again with ONLY the JSON object.", verr, tail([]byte(text), 4000))
	}
	return nil, fmt.Errorf("%s: invalid JSON after retry: %w", name, lastErr)
}

func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}
