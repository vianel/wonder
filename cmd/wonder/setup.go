package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	wonder "github.com/vianel/wonder"
	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gate"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/runner"
)

var hints = map[string]string{
	"git":     "install git: https://git-scm.com/downloads",
	"gh":      "install the GitHub CLI: https://cli.github.com, then `gh auth login`",
	"claude":  "install Claude Code: `curl -fsSL https://claude.ai/install.sh | bash`",
	"codex":   "install Codex CLI: `brew install --cask codex` or `npm install -g @openai/codex`",
	"copilot": "install GitHub Copilot CLI: `npm install -g @github/copilot` (needs Copilot CLI enabled at https://github.com/settings/copilot)",
	"herdr":   "optional, to watch workers in panes: https://herdr.dev",
}

// installedWorkers returns the worker CLIs found on PATH.
func installedWorkers() []string {
	var ws []string
	for _, w := range []string{"claude", "codex", "copilot"} {
		if found(w) && (w != "copilot" || githubCopilot()) {
			ws = append(ws, w)
		}
	}
	return ws
}

// githubCopilot tells GitHub's Copilot CLI apart from AWS Copilot, which
// installs a binary with the same name.
func githubCopilot() bool {
	out, _ := exec.Command("copilot", "--version").CombinedOutput()
	return strings.Contains(string(out), "GitHub Copilot")
}

// doctor prints one line per check and reports whether wonder can run.
func doctor(w io.Writer, dir string) bool {
	ok := true
	line := func(mark, name, msg string) { fmt.Fprintf(w, "%s %-10s %s\n", mark, name, msg) }

	if !found("git") {
		line("✗", "git", "not found — "+hints["git"])
		ok = false
	} else if root, err := gitx.Root(dir); err != nil {
		line("✗", "git", "not inside a git repository")
		ok = false
	} else if _, err := gitx.Run(root, "remote", "get-url", "origin"); err != nil {
		line("✗", "git", "no `origin` remote")
		ok = false
	} else {
		line("✓", "git", root)
		dir = root
	}

	if !found("gh") {
		line("✗", "gh", "not found — "+hints["gh"])
		ok = false
	} else if out, err := exec.Command("gh", "auth", "status").CombinedOutput(); err != nil {
		line("✗", "gh", "not logged in — run `gh auth login`: "+firstLine(string(out)))
		ok = false
	} else {
		line("✓", "gh", "logged in")
	}

	workers := installedWorkers()
	for _, wk := range []string{"claude", "codex", "copilot"} {
		switch {
		case contains(workers, wk):
			line("✓", wk, "found")
		case found(wk):
			line("·", wk, "this `copilot` is not GitHub Copilot CLI (AWS Copilot?) — "+hints[wk])
		default:
			line("·", wk, "not found — "+hints[wk])
		}
	}
	if len(workers) == 0 {
		line("✗", "workers", "need at least one of claude, codex or copilot")
		ok = false
	}

	switch {
	case !found("herdr"):
		line("·", "herdr", "not found (workers run as subprocesses) — "+hints["herdr"])
	case runner.InHerdr():
		line("✓", "herdr", "workers will run in herdr panes")
	default:
		line("·", "herdr", "installed, but not running inside it (HERDR_ENV != 1); workers run as subprocesses")
	}

	if wf, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml")); len(wf) > 0 {
		line("✓", "ci", fmt.Sprintf("%d GitHub Actions workflow(s)", len(wf)))
	} else {
		line("!", "ci", "no .github/workflows: PRs will merge without CI (the babysit station has nothing to fix)")
	}

	if _, err := os.Stat(filepath.Join(dir, config.File)); errors.Is(err, fs.ErrNotExist) {
		line("!", "config", "no "+config.File+" — run `wonder init`")
	} else if cfg, err := config.Load(dir); err != nil {
		line("✗", "config", err.Error())
		ok = false
	} else {
		missing := map[string]bool{}
		check := func(c config.Worker) {
			if !found(c.Worker) {
				missing[c.Worker] = true
			}
		}
		check(cfg.Stations.Spec.Worker)
		check(cfg.Stations.Build.Worker)
		check(cfg.Stations.Fix.Worker)
		check(cfg.Eval.Judge.Worker)
		for _, rw := range cfg.Stations.Review.Workers {
			check(rw)
		}
		if len(missing) > 0 {
			for m := range missing {
				line("✗", "config", config.File+" uses "+m+", which is not installed — "+hints[m])
			}
			ok = false
		} else {
			line("✓", "config", config.File+" is valid")
		}
	}
	if ok {
		fmt.Fprintln(w, "\n🍫 The factory is ready.")
	} else {
		fmt.Fprintln(w, "\nFix the ✗ lines above, then run `wonder doctor` again.")
	}
	for _, t := range []string{"gh", "claude", "codex", "herdr"} {
		if !found(t) {
			fmt.Fprintln(w, "Run `wonder doctor --fix` to install missing tools (it asks before each one).")
			break
		}
	}
	return ok
}

func found(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func doctorCmd() *cobra.Command {
	var fix bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check that git, gh, the agent CLIs and the repo are ready",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wd, _ := os.Getwd()
			ok := doctor(os.Stdout, wd)
			if fix {
				in := newInstaller()
				switch missing := in.missing("gh", "claude", "codex", "herdr"); {
				case len(missing) == 0:
					fmt.Println("\nNothing to install.")
				case !gate.Interactive():
					fmt.Println("\n--fix asks before installing anything, so it needs a terminal.")
				default:
					if len(in.offer(missing...)) > 0 {
						fmt.Print("\nChecking again…\n\n")
						ok = doctor(os.Stdout, wd)
					}
				}
			}
			if !ok {
				cmd.SilenceErrors = true
				return errors.New("doctor found problems")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&fix, "fix", false, "offer to install missing tools (asks before each one)")
	return c
}

func workerTOML(w config.Worker) string {
	s := fmt.Sprintf("worker = %q\n", w.Worker)
	if w.Model != "" {
		s += fmt.Sprintf("model = %q\n", w.Model)
	}
	return s
}

func stationTOML(name string, s config.Station) string {
	out := fmt.Sprintf("[%s]\n%s", name, workerTOML(s.Worker))
	out += fmt.Sprintf("prompt = %q\n", s.Prompt)
	if s.MaxAttempts > 0 {
		out += fmt.Sprintf("max_attempts = %d\n", s.MaxAttempts)
	}
	return out + "\n"
}

// renderConfig writes a commented wonder.toml.
func renderConfig(c config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# wonder configuration. Docs: https://github.com/vianel/wonder/blob/main/docs/config.md

[factory]
max_panes = %d          # max workers running at once, across all issues
base_branch = %q

[router]
allow_auto = %t        # true lets PRs that pass every rule merge without the gate
max_diff_lines = %d

`, c.Factory.MaxPanes, c.Factory.BaseBranch, c.Router.AllowAuto, c.Router.MaxDiffLines)
	b.WriteString(stationTOML("stations.spec", c.Stations.Spec))
	b.WriteString(stationTOML("stations.build", c.Stations.Build))
	b.WriteString(stationTOML("stations.fix", c.Stations.Fix))
	fmt.Fprintf(&b, "[stations.review]\nprompt = %q\n\n", c.Stations.Review.Prompt)
	for _, w := range c.Stations.Review.Workers {
		b.WriteString("[[stations.review.workers]]\n" + workerTOML(w) + "\n")
	}
	fmt.Fprintf(&b, "[eval]\nruns = %d               # replay runs per prompt per example\n\n", c.Eval.Runs)
	b.WriteString(stationTOML("eval.judge", c.Eval.Judge))
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// initRepo writes wonder.toml, copies the default prompts and updates .gitignore.
// inst, when set, is used to offer installing a worker if none is on PATH.
func initRepo(w io.Writer, root string, force, commitVerdicts bool, inst *installer) error {
	workers := installedWorkers()
	if len(workers) == 0 && inst != nil {
		fmt.Fprintln(w, "wonder needs at least one worker CLI. Install one or both:")
		inst.offer("claude", "codex")
		workers = installedWorkers()
	}
	if len(workers) == 0 {
		return errors.New("no worker CLI on PATH; install one of them:\n  " + hints["claude"] + "\n  " + hints["codex"] + "\n  " + hints["copilot"])
	}
	cfgPath := filepath.Join(root, config.File)
	if _, err := os.Stat(cfgPath); err == nil && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", config.File)
	}
	if err := os.WriteFile(cfgPath, []byte(renderConfig(config.Default(workers))), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(w, "✓ wrote %s (workers: %s)\n", config.File, strings.Join(workers, ", "))

	err := fs.WalkDir(wonder.Assets, "prompts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dst := filepath.Join(root, ".wonder", p)
		if _, err := os.Stat(dst); err == nil && !force {
			return nil
		}
		b, err := wonder.Assets.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "✓ default prompts in .wonder/prompts/ (commit them; they are versioned)")

	gi := filepath.Join(root, ".gitignore")
	existing, _ := os.ReadFile(gi)
	want := []string{".wonder/*", "!.wonder/prompts/"}
	if commitVerdicts {
		want = append(want, "!.wonder/verdicts.jsonl")
	}
	var add []string
	for _, l := range want {
		if !strings.Contains("\n"+string(existing)+"\n", "\n"+l+"\n") {
			add = append(add, l)
		}
	}
	if len(add) > 0 {
		block := "\n# wonder: run state, worktrees and verdicts stay local; prompts are versioned\n" + strings.Join(add, "\n") + "\n"
		if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
			block = "\n" + block
		}
		f, err := os.OpenFile(gi, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(block); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintln(w, "✓ updated .gitignore")
	}
	if commitVerdicts {
		fmt.Fprintln(w, "! verdicts.jsonl will be committed: it holds gate notes and reviews, so only do this for repos where that is fine")
	}
	fmt.Fprintln(w, "\nNext: `wonder doctor`, then `wonder run <issue>`.")
	return nil
}

func initCmd() *cobra.Command {
	var force, commitVerdicts bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write wonder.toml, copy the default prompts and update .gitignore",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			wd, _ := os.Getwd()
			root, err := gitx.Root(wd)
			if err != nil {
				return errors.New("not inside a git repository")
			}
			var inst *installer
			if gate.Interactive() {
				i := newInstaller()
				inst = &i
			}
			return initRepo(os.Stdout, root, force, commitVerdicts, inst)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite wonder.toml and the prompt files")
	c.Flags().BoolVar(&commitVerdicts, "commit-verdicts", false, "keep .wonder/verdicts.jsonl in git (it can hold private notes)")
	return c
}
