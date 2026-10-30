package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Herdr runs each job in its own herdr pane so the human can watch it. The
// command is the same headless CLI the Exec runner uses; output goes to files
// in the job's tmp dir, so nothing is scraped from the screen.
// See docs/herdr.md.
type Herdr struct {
	mu    sync.Mutex
	panes []string // live panes this runner created, newest last
}

func (h *Herdr) Run(ctx context.Context, j Job) (Result, error) {
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	marker := "WONDER_DONE_" + hex.EncodeToString(nonce)
	if err := os.WriteFile(filepath.Join(j.Tmp, "prompt.txt"), []byte(j.Stdin), 0o600); err != nil {
		return Result{}, err
	}
	script := filepath.Join(j.Tmp, "run.sh")
	if err := os.WriteFile(script, []byte(h.script(j, marker)), 0o700); err != nil {
		return Result{}, err
	}

	pane, err := h.split(ctx, j.Dir)
	if err != nil {
		return Result{}, err
	}
	defer h.close(pane)
	_, _ = herdr(ctx, "pane", "rename", pane, j.Name)
	if _, err := herdr(ctx, "pane", "run", pane, "bash "+shq(script)); err != nil {
		return Result{}, err
	}
	// Blocks inside herdr until the marker appears; no sleeping or polling here.
	if _, err := herdr(ctx, "pane", "wait-output", pane, "--match", marker, "--source", "recent"); err != nil {
		return Result{}, fmt.Errorf("waiting for pane %s (%s): %w", pane, j.Name, err)
	}

	var r Result
	r.Stdout, _ = os.ReadFile(filepath.Join(j.Tmp, "stdout.txt"))
	r.Stderr, _ = os.ReadFile(filepath.Join(j.Tmp, "stderr.txt"))
	code, err := os.ReadFile(filepath.Join(j.Tmp, "exit.txt"))
	if err != nil {
		return r, fmt.Errorf("pane %s finished without an exit code: %w", j.Name, err)
	}
	r.ExitCode, err = strconv.Atoi(strings.TrimSpace(string(code)))
	return r, err
}

// script tees both stdout and stderr to files and to the pane, so progress is
// visible either way (codex --json streams its events on stdout). Plain bash
// 3.2 syntax so it runs on stock macOS.
func (h *Herdr) script(j Job, marker string) string {
	q := make([]string, len(j.Argv))
	for i, a := range j.Argv {
		q[i] = shq(a)
	}
	t := func(f string) string { return shq(filepath.Join(j.Tmp, f)) }
	return fmt.Sprintf(`cd %s || exit 1
printf 'wonder: %%s\n' %s
exec 4>&1
{ %s < %s 2>&1 1>&3 3>&- | tee %s >&4; echo "${PIPESTATUS[0]}" > %s; } 3>&1 | tee %s
printf '\n%%s\n' %s
`, shq(j.Dir), shq(j.Name), strings.Join(q, " "), t("prompt.txt"), t("stderr.txt"), t("exit.txt"), t("stdout.txt"), shq(marker))
}

// split opens a pane without stealing focus: the first one to the right of the
// caller, later ones below the newest pane we own, so columns stay usable.
func (h *Herdr) split(ctx context.Context, dir string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := []string{"pane", "split", "--current", "--direction", "right"}
	if n := len(h.panes); n > 0 {
		args = []string{"pane", "split", h.panes[n-1], "--direction", "down"}
	}
	out, err := herdr(ctx, append(args, "--cwd", dir, "--no-focus")...)
	if err != nil {
		return "", err
	}
	var resp struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("herdr pane split: unexpected output %q", out)
	}
	h.panes = append(h.panes, resp.Result.Pane.PaneID)
	return resp.Result.Pane.PaneID, nil
}

// close closes a pane this runner created, and only those.
func (h *Herdr) close(pane string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, p := range h.panes {
		if p == pane {
			h.panes = append(h.panes[:i], h.panes[i+1:]...)
			_, _ = herdr(context.Background(), "pane", "close", pane)
			return
		}
	}
}

func herdr(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "herdr", args...).Output()
	if ee, ok := err.(*exec.ExitError); ok {
		err = fmt.Errorf("herdr %s: %s", strings.Join(args[:2], " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
