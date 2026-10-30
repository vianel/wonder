package eval

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gh"
	"github.com/vianel/wonder/internal/pipeline"
	"github.com/vianel/wonder/internal/runner"
	"github.com/vianel/wonder/internal/store"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setup builds a repo plus a hand-made 5-line verdict log: issues 1-3 were
// sent back by the gate (their diffs add "bad"), 4-5 shipped.
func setup(t *testing.T) (*Eval, *bytes.Buffer) {
	bin, _ := filepath.Abs("../../testdata/bin")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_STATE", t.TempDir())
	t.Setenv("FAKE_JUDGE_MISSED", "false")
	for _, k := range []string{"FAKE_REVIEW_CODEX", "FAKE_REVIEW_CLAUDE", "FAKE_INVALID_ONCE", "FAKE_RATE_LIMIT"} {
		t.Setenv(k, "")
	}
	for _, k := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+k+"_NAME", "t")
		t.Setenv("GIT_"+k+"_EMAIL", "t@example.com")
	}
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(root, "f.txt"), []byte("base\n"), 0o644)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "base")
	base := git(t, root, "rev-parse", "HEAD")

	st := store.Store{Dir: filepath.Join(root, ".wonder")}
	for i := 1; i <= 5; i++ {
		gate, content := "changes", "bad\n"
		if i > 3 {
			gate, content = "ship", "good\n"
		}
		git(t, root, "checkout", "-q", "-B", "b", base)
		_ = os.WriteFile(filepath.Join(root, "f.txt"), []byte(content), 0o644)
		git(t, root, "commit", "-q", "-am", "change")
		head := git(t, root, "rev-parse", "HEAD")
		if err := st.AppendVerdict(store.Verdict{Issue: i, PR: 100 + i, BaseSHA: base, HeadSHA: head, GateFirst: gate, GateNotes: "the bad line is wrong",
			Reviews: map[string]store.Review{"codex": {Verdict: "approve"}}, Labels: map[string]string{"codex": store.Label("approve", gate)},
			Router: store.Route{Route: "gate", RulesRoute: "auto"}, Spec: store.Spec{Goal: "g", Risk: "low"}}); err != nil {
			t.Fatal(err)
		}
	}
	git(t, root, "checkout", "-q", "main")
	cfg := config.Default([]string{"claude", "codex"})
	toml := "[stations.review]\nprompt = \".wonder/prompts/review.md\" # current\n\n[[stations.review.workers]]\nworker = \"codex\"\n[[stations.review.workers]]\nworker = \"claude\"\nmodel = \"opus\"\n"
	_ = os.WriteFile(filepath.Join(root, config.File), []byte(toml), 0o644)
	var out bytes.Buffer
	return &Eval{Root: root, Cfg: &cfg, Store: st, Out: &out,
		Inv: pipeline.Invoker{Runner: runner.NewLimit(runner.Exec{}, 3), TmpDir: filepath.Join(st.Dir, "tmp")}}, &out
}

func candidate(t *testing.T, root, name, marker string) string {
	b, _ := config.Prompt(root, ".wonder/prompts/review.md")
	p := filepath.Join(root, name)
	_ = os.WriteFile(p, []byte(string(b)+"\n"+marker+"\n"), 0o644)
	return p
}

func TestReplayAndPromote(t *testing.T) {
	e, out := setup(t)
	ctx := context.Background()

	// Flags everything: no misses, but false alarms on the two shipped PRs.
	noisy := candidate(t, e.Root, "noisy.md", "CANDIDATE_FLAGS_ALL")
	res, err := e.ReplayReview(ctx, noisy, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.Current.Misses != 6 || res.Candidate.FalseAlarms != 4 || res.Candidate.Misses != 0 {
		t.Fatalf("noisy: %+v %+v", res.Current, res.Candidate)
	}
	if !strings.Contains(out.String(), "issue") || !strings.Contains(out.String(), "#5") || !strings.Contains(out.String(), "noisy and in-sample") {
		t.Fatalf("table:\n%s", out)
	}
	if err := e.Promote(noisy); err == nil {
		t.Fatal("promote must refuse a failed replay")
	}

	// Flags only the bad diffs: fewer misses, no false alarms.
	smart := candidate(t, e.Root, "smart.md", "CANDIDATE_SMART")
	if res, err = e.ReplayReview(ctx, smart, 1); err != nil || !res.Passed {
		t.Fatalf("smart: %+v %v", res, err)
	}
	if err := e.Promote(smart); err != nil {
		t.Fatal(err)
	}
	toml, _ := os.ReadFile(filepath.Join(e.Root, config.File))
	if !strings.Contains(string(toml), `prompt = "smart.md" # current`) {
		t.Fatalf("toml not updated:\n%s", toml)
	}
	if _, err := os.Stat(filepath.Join(e.Store.Dir, "promotions.jsonl")); err != nil {
		t.Fatal("promotion not recorded")
	}
	// The config now points at smart.md, so the old replay no longer matches.
	cfg, _ := config.Load(e.Root)
	e.Cfg = cfg
	if err := e.Promote(smart); err == nil || !strings.Contains(err.Error(), "changed since the replay") {
		t.Fatalf("stale replay must be refused: %v", err)
	}
}

func TestReplayRouterAndStats(t *testing.T) {
	e, out := setup(t)
	totals := e.ReplayRouter(config.Router{AllowAuto: true, MaxDiffLines: 400})
	// Rules say auto for all 5; the 3 sent back would have been unsafe.
	if totals["candidate "+Unsafe] != 3 || totals["current "+Unnecessary] != 2 {
		t.Fatalf("%v\n%s", totals, out)
	}
	out.Reset()
	if err := e.Stats(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "codex") || !strings.Contains(out.String(), "3 unsafe") {
		t.Fatalf("stats:\n%s", out)
	}
}

func TestSetPrompt(t *testing.T) {
	in := "[stations.spec]\nprompt = \"a\"\n\n[[stations.review.workers]]\nworker = \"codex\"\n\n[stations.review]\nprompt = \"b\"\n"
	got, err := SetPrompt(in, "stations.review", "c.md")
	if err != nil || !strings.Contains(got, "prompt = \"a\"") || !strings.Contains(got, "prompt = \"c.md\"") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestHarvestRevertMakesMiss(t *testing.T) {
	e, _ := setup(t)
	ghDir := t.TempDir()
	t.Setenv("FAKE_GH_DIR", ghDir)
	e.GH = gh.Client{Dir: e.Root}
	_ = os.WriteFile(filepath.Join(ghDir, "prview.json"), []byte(`{"state":"MERGED","mergedAt":"2026-01-01T00:00:00Z","mergeCommit":{"oid":"abc"}}`), 0o644)
	// Issue 4 (PR 104) shipped, then got reverted two days later.
	_ = os.WriteFile(filepath.Join(ghDir, "merged.json"), []byte(`[{"title":"Revert \"x\"","body":"Reverts o/r#104","mergedAt":"2026-01-03T00:00:00Z"}]`), 0o644)
	if err := e.Harvest(); err != nil {
		t.Fatal(err)
	}
	vs, _ := e.Store.Verdicts()
	for _, v := range vs {
		reverted := v.PR == 104
		if v.Harvest == nil || !v.Harvest.Merged || v.Harvest.Reverted != reverted {
			t.Fatalf("#%d harvest: %+v", v.Issue, v.Harvest)
		}
		if reverted && v.Labels["codex"] != store.Miss {
			t.Fatalf("reverted PR approved by codex must be a miss: %+v", v.Labels)
		}
		if v.Issue == 5 && v.Labels["codex"] != store.Agree {
			t.Fatalf("#5 must stay agree: %+v", v.Labels)
		}
	}
	if g, _ := Outcome(vs[3]); g != "changes" {
		t.Fatal("a reverted ship counts as changes")
	}
}
