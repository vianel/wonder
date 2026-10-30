package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vianel/wonder/internal/config"
)

// pathWith builds a PATH dir holding only the named tools: fakes from
// testdata/bin, real git.
func pathWith(t *testing.T, tools ...string) {
	t.Helper()
	dir := t.TempDir()
	fakes, _ := filepath.Abs("../../testdata/bin")
	for _, tool := range tools {
		src := filepath.Join(fakes, tool)
		if tool == "git" {
			src, _ = exec.LookPath("git")
		}
		if err := os.Symlink(src, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Symlink(filepath.Join(fakes, "_agent.sh"), filepath.Join(dir, "_agent.sh"))
	// Basics the fake scripts need.
	for _, b := range []string{"bash", "env", "cat", "dirname", "tr", "grep", "mv", "python3"} {
		if p, err := exec.LookPath(b); err == nil {
			_ = os.Symlink(p, filepath.Join(dir, b))
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("HERDR_ENV", "")
	t.Setenv("FAKE_GH_DIR", t.TempDir())
}

func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git, _ := exec.LookPath("git")
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://example.com/o/r.git"}} {
		cmd := exec.Command(git, args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
	}
	return root
}

func TestDoctorReportsMissingTools(t *testing.T) {
	root := repo(t)
	pathWith(t, "git", "gh", "claude")
	var out bytes.Buffer
	if !doctor(&out, root) {
		t.Fatalf("claude alone should be enough:\n%s", out.String())
	}
	for _, want := range []string{"✓ claude", "· codex", "npm install -g @openai/codex", "· herdr", "! ci", "! config"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	pathWith(t, "git", "claude")
	out.Reset()
	if doctor(&out, root) || !strings.Contains(out.String(), "✗ gh") {
		t.Fatalf("missing gh must fail:\n%s", out.String())
	}

	pathWith(t, "git", "gh")
	out.Reset()
	if doctor(&out, root) || !strings.Contains(out.String(), "need at least one of claude or codex") {
		t.Fatalf("no workers must fail:\n%s", out.String())
	}
}

func TestInitWithOnlyCodex(t *testing.T) {
	root := repo(t)
	pathWith(t, "git", "gh", "codex")
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules"), 0o644)
	var out bytes.Buffer
	if err := initRepo(&out, root, false, false, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Stations.Spec.Worker.Worker != "codex" || len(cfg.Stations.Review.Workers) != 1 || cfg.Stations.Review.Workers[0].Worker != "codex" {
		t.Fatalf("codex-only init uses claude: %+v", cfg.Stations)
	}
	if _, err := os.Stat(filepath.Join(root, ".wonder/prompts/judges/miss.md")); err != nil {
		t.Fatal("prompts not copied")
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if !strings.HasPrefix(string(gi), "node_modules\n") || !strings.Contains(string(gi), ".wonder/*\n!.wonder/prompts/\n") || strings.Contains(string(gi), "!.wonder/verdicts.jsonl") {
		t.Fatalf(".gitignore:\n%s", gi)
	}
	if err := initRepo(&out, root, false, true, nil); err == nil {
		t.Fatal("second init without --force must refuse")
	}
	if err := initRepo(&out, root, true, true, nil); err != nil {
		t.Fatal(err)
	}
	gi, _ = os.ReadFile(filepath.Join(root, ".gitignore"))
	if strings.Count(string(gi), ".wonder/*") != 1 || !strings.Contains(string(gi), "!.wonder/verdicts.jsonl") {
		t.Fatalf(".gitignore after --commit-verdicts:\n%s", gi)
	}
}

func TestRenderedDefaultConfigLoads(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, config.File), []byte(renderConfig(config.Default([]string{"claude", "codex"}))), 0o644)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Stations.Review.Workers) != 2 || cfg.Stations.Review.Workers[1].Model != "opus" || cfg.Eval.Judge.Prompt == "" {
		t.Fatalf("%+v", cfg)
	}
}

func TestIssueArgs(t *testing.T) {
	got, err := issues([]string{"7", "#8", "https://github.com/o/r/issues/9"})
	if err != nil || len(got) != 3 || got[0] != 7 || got[1] != 8 || got[2] != 9 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := issues([]string{"https://github.com/o/r/pull/9"}); err == nil {
		t.Fatal("a PR URL is not an issue")
	}
}
