package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gh"
	"github.com/vianel/wonder/internal/runner"
	"github.com/vianel/wonder/internal/store"
)

type env struct {
	t     *testing.T
	root  string
	ghDir string
	state string
}

// setup makes a repo with a bare origin, puts the fake CLIs first on PATH and
// returns an engine wired to them.
func setup(t *testing.T) (*Engine, env) {
	t.Helper()
	bin, _ := filepath.Abs("../../testdata/bin")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := env{t: t, root: t.TempDir(), ghDir: t.TempDir(), state: t.TempDir()}
	t.Setenv("FAKE_GH_DIR", e.ghDir)
	t.Setenv("FAKE_STATE", e.state)
	for _, k := range []string{"FAKE_CI", "FAKE_REVIEW_CODEX", "FAKE_REVIEW_CLAUDE", "FAKE_INVALID_ONCE", "FAKE_RATE_LIMIT", "FAKE_BUILD_STATUS", "FAKE_MERGEABLE", "FAKE_BUILD_HOOK", "FAKE_SPEC_QUESTIONS", "FAKE_EXPECT_IN_BUILD"} {
		t.Setenv(k, "")
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	origin := filepath.Join(t.TempDir(), "origin.git")
	e.git("", "init", "-q", "--bare", "-b", "main", origin)
	e.git("", "clone", "-q", origin, e.root)
	_ = os.WriteFile(filepath.Join(e.root, "README"), []byte("hi\n"), 0o644)
	_ = os.WriteFile(filepath.Join(e.root, ".gitignore"), []byte(".wonder/\n"), 0o644)
	e.git(e.root, "add", "-A")
	e.git(e.root, "commit", "-q", "-m", "init")
	e.git(e.root, "push", "-q", "origin", "HEAD:main")

	gh.PollInterval = time.Millisecond
	cfg := config.Default([]string{"claude", "codex"})
	cfg.Router.AllowAuto = false
	st := store.Store{Dir: filepath.Join(e.root, ".wonder")}
	return &Engine{
		Root: e.root, Cfg: &cfg, Store: st, GH: gh.Client{Dir: e.root}, Log: io.Discard,
		Inv: Invoker{Runner: runner.NewLimit(runner.Exec{}, 3), TmpDir: filepath.Join(st.Dir, "tmp")},
	}, e
}

func (e env) git(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (e env) ghCalls() string {
	b, _ := os.ReadFile(filepath.Join(e.ghDir, "calls.log"))
	return string(b)
}

func gateAlways(ship bool, notes string) GateFunc {
	return func(*store.Run) (bool, string, bool) { return ship, notes, true }
}

func run(t *testing.T, en *Engine, gate GateFunc, issues ...int) []*store.Run {
	t.Helper()
	var runs []*store.Run
	for _, n := range issues {
		r, err := en.Open(n)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	if err := en.RunAll(context.Background(), runs, gate); err != nil {
		t.Fatal(err)
	}
	return runs
}

func TestHappyPathWritesVerdict(t *testing.T) {
	en, e := setup(t)
	t.Setenv("FAKE_REVIEW_CLAUDE", "changes")
	r := run(t, en, gateAlways(true, ""), 7)[0]
	if r.Status != store.Done || r.PR != 101 {
		t.Fatalf("run: %+v", r)
	}
	if !strings.Contains(e.ghCalls(), "pr merge 101 --squash") {
		t.Fatalf("not merged:\n%s", e.ghCalls())
	}
	if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree not removed")
	}
	vs, err := en.Store.Verdicts()
	if err != nil || len(vs) != 1 {
		t.Fatalf("verdicts: %+v %v", vs, err)
	}
	v := vs[0]
	if v.V != 1 || v.Labels["codex"] != store.Agree || v.Labels["claude"] != store.FalseAlarm || v.GateFirst != "ship" {
		t.Fatalf("labels: %+v", v)
	}
	for _, k := range []string{"spec", "build", "review"} {
		if len(v.Prompts[k]) != 8 {
			t.Fatalf("prompt hash %s missing: %+v", k, v.Prompts)
		}
	}
	if v.Models["review.claude"] != "opus" || v.Models["spec"] != "sonnet" || v.Router.Route != "gate" || v.HeadSHA == "" || v.BaseSHA == "" {
		t.Fatalf("verdict: %+v", v)
	}
}

func TestCIBabysitAndGateChangesLoop(t *testing.T) {
	en, e := setup(t)
	t.Setenv("FAKE_CI", "fail pass")
	calls := 0
	gate := func(r *store.Run) (bool, string, bool) {
		calls++
		if calls == 1 {
			return false, "rename greet", true
		}
		return true, "", true
	}
	r := run(t, en, gate, 3)[0]
	if r.Status != store.Done || r.BabysitCount != 1 || r.GateRounds != 2 || r.GateFirst != "changes" {
		t.Fatalf("run: %+v", r)
	}
	// One spec, one build, one CI fix, one gate fix, and review only once.
	agents, _ := os.ReadFile(filepath.Join(e.state, "agent_calls.log"))
	if got := strings.Count(string(agents), "\n"); got != 6 {
		t.Fatalf("want 6 agent calls (spec, build, fix, 2 reviews, fix), got %d:\n%s", got, agents)
	}
	if vs, _ := en.Store.Verdicts(); len(vs) != 1 || vs[0].Labels["codex"] != store.Miss || vs[0].BabysitFixes != 1 {
		t.Fatalf("verdicts: %+v", vs)
	}
	if !strings.Contains(e.ghCalls(), "run view 123 --log-failed") {
		t.Fatal("CI logs not fetched")
	}
}

func TestInvalidJSONRetriesOnce(t *testing.T) {
	en, _ := setup(t)
	t.Setenv("FAKE_INVALID_ONCE", "1")
	if r := run(t, en, gateAlways(true, ""), 1)[0]; r.Status != store.Done {
		t.Fatalf("run: %+v", r)
	}
}

func TestRateLimitPausesThenResumes(t *testing.T) {
	en, _ := setup(t)
	t.Setenv("FAKE_RATE_LIMIT", "codex")
	r := run(t, en, nil, 1)[0]
	if r.Status != store.Paused || r.Station != StBuild || r.ResetAt == nil {
		t.Fatalf("want paused at build with reset time: %+v", r)
	}
	t.Setenv("FAKE_RATE_LIMIT", "")
	en2 := &Engine{Root: en.Root, Cfg: en.Cfg, Store: en.Store, GH: en.GH, Log: io.Discard, Inv: en.Inv}
	r, _ = en2.Open(1)
	Wake(r)
	if err := en2.RunAll(context.Background(), []*store.Run{r}, gateAlways(true, "")); err != nil || r.Status != store.Done {
		t.Fatalf("resume: %+v %v", r, err)
	}
}

func TestBlockedIsSurfaced(t *testing.T) {
	en, _ := setup(t)
	t.Setenv("FAKE_BUILD_STATUS", "blocked")
	r := run(t, en, nil, 1)[0]
	if r.Status != store.Blocked || !strings.Contains(r.Error, "needs a decision") {
		t.Fatalf("run: %+v", r)
	}
}

func TestResumeAtGateFromDisk(t *testing.T) {
	en, _ := setup(t)
	r := run(t, en, nil, 5)[0] // no gate: stops waiting
	if r.Status != store.WaitingGate {
		t.Fatalf("run: %+v", r)
	}
	// A new process: load from disk, answer the gate, finish.
	en2 := &Engine{Root: en.Root, Cfg: en.Cfg, Store: en.Store, GH: en.GH, Log: io.Discard, Inv: en.Inv}
	r2, _ := en2.Open(5)
	if err := en2.RunAll(context.Background(), []*store.Run{r2}, gateAlways(true, "")); err != nil || r2.Status != store.Done {
		t.Fatalf("resume: %+v %v", r2, err)
	}
}

func TestParallelIssuesGetOwnWorktrees(t *testing.T) {
	en, _ := setup(t)
	runs := run(t, en, gateAlways(true, ""), 1, 2, 3)
	seen := map[string]bool{}
	for _, r := range runs {
		if r.Status != store.Done || seen[r.Worktree] {
			t.Fatalf("run: %+v", r)
		}
		seen[r.Worktree] = true
	}
}

func TestConflictWithBaseGoesToFixer(t *testing.T) {
	en, e := setup(t)
	// Another PR lands on main, touching the same file, while we build.
	origin, _ := exec.Command("git", "-C", e.root, "remote", "get-url", "origin").Output()
	other := t.TempDir()
	t.Setenv("FAKE_BUILD_HOOK", fmt.Sprintf("git clone -q %s %s && cd %s && echo 'from main' > greet.txt && git add -A && git commit -qm other && git push -q origin HEAD:main",
		strings.TrimSpace(string(origin)), other, other))
	t.Setenv("FAKE_MERGEABLE", "CONFLICTING MERGEABLE")

	r := run(t, en, gateAlways(true, ""), 4)[0]
	if r.Status != store.Done || r.BabysitCount != 1 {
		t.Fatalf("run: %+v", r)
	}
	if parents, _ := exec.Command("git", "-C", e.root, "log", "-1", "--format=%P", r.HeadSHA).Output(); len(strings.Fields(string(parents))) != 2 {
		t.Fatalf("head %s is not a merge commit", r.HeadSHA)
	}
	agents, _ := os.ReadFile(filepath.Join(e.state, "agent_calls.log"))
	if strings.Count(string(agents), "codex") != 3 { // build, conflict fix, review
		t.Fatalf("agent calls:\n%s", agents)
	}
}

func TestQuestionsAskedOnceUpFrontForAllIssues(t *testing.T) {
	en, e := setup(t)
	t.Setenv("FAKE_SPEC_QUESTIONS", "1")
	t.Setenv("FAKE_EXPECT_IN_BUILD", "tabs, always")
	calls := 0
	en.Answer = func(runs []*store.Run) bool {
		calls++
		agents, _ := os.ReadFile(filepath.Join(e.state, "agent_calls.log"))
		if len(runs) != 2 || strings.Count(string(agents), "\n") != 2 {
			t.Errorf("want both specs and nothing else before asking; runs=%d calls:\n%s", len(runs), agents)
		}
		for _, r := range runs {
			for _, i := range r.Spec.Unanswered() {
				r.Spec.Questions[i].Answer, r.Spec.Questions[i].AnsweredBy = "tabs, always", "you"
			}
		}
		return true
	}
	for _, r := range run(t, en, gateAlways(true, ""), 1, 2) {
		if r.Status != store.Done || r.Spec.Questions[0].AnsweredBy != "you" {
			t.Fatalf("run: %+v", r)
		}
	}
	if calls != 1 {
		t.Fatalf("want one batch of questions, got %d", calls)
	}
	if vs, _ := en.Store.Verdicts(); vs[0].Spec.Questions[0].Answer != "tabs, always" {
		t.Fatalf("answer not in verdict spec: %+v", vs[0].Spec)
	}
}

func TestAutoAnswerTakesSuggestions(t *testing.T) {
	en, _ := setup(t)
	t.Setenv("FAKE_SPEC_QUESTIONS", "1")
	t.Setenv("FAKE_EXPECT_IN_BUILD", "spaces")
	en.AutoAnswer = true
	en.Answer = func([]*store.Run) bool { t.Fatal("must not ask with --yes"); return false }
	r := run(t, en, gateAlways(true, ""), 1)[0]
	if r.Status != store.Done || r.Spec.Questions[0].AnsweredBy != "suggested" {
		t.Fatalf("run: %+v", r)
	}
}

func TestUnansweredQuestionsParkTheRun(t *testing.T) {
	en, _ := setup(t)
	t.Setenv("FAKE_SPEC_QUESTIONS", "1")
	en.Answer = func([]*store.Run) bool { return false } // human walked off
	r := run(t, en, nil, 1)[0]
	if r.Status != store.WaitingAnswers || r.Station != StAsk {
		t.Fatalf("run: %+v", r)
	}
	Wake(r)
	en.Answer = func(runs []*store.Run) bool {
		runs[0].Spec.Questions[0].Answer, runs[0].Spec.Questions[0].AnsweredBy = "spaces", "you"
		return true
	}
	if err := en.RunAll(context.Background(), []*store.Run{r}, gateAlways(true, "")); err != nil || r.Status != store.Done {
		t.Fatalf("resume: %+v %v", r, err)
	}
}

func TestUsageIsRecordedPerStation(t *testing.T) {
	en, _ := setup(t)
	r := run(t, en, gateAlways(true, ""), 1)[0]
	// claude: $0.01 and 1300 tokens per call; codex: 2300 tokens, no price.
	if u := r.Usage["spec"]; u.Calls != 1 || u.CostUSD != 0.01 || u.Tokens() != 1300 || u.UnpricedTokens != 0 {
		t.Fatalf("spec: %+v", u)
	}
	if u := r.Usage["build"]; u.Calls != 1 || u.CostUSD != 0 || u.UnpricedTokens != 2300 || u.CachedInput != 500 {
		t.Fatalf("build: %+v", u)
	}
	if _, ok := r.Usage["review.codex"]; !ok {
		t.Fatalf("usage keys: %v", r.Usage)
	}
	if got := store.TotalUsage(r.Usage).Short(); got != "$0.02 + 5k unpriced tok" {
		t.Fatalf("total: %q", got)
	}
	vs, _ := en.Store.Verdicts()
	if len(vs[0].Usage) != 4 {
		t.Fatalf("verdict usage: %v", vs[0].Usage)
	}
}

func TestPricingGivesCodexADollarFigure(t *testing.T) {
	en, _ := setup(t)
	en.Cfg.Pricing = map[string]config.Price{"codex": {Input: 1, CachedInput: 0.1, Output: 10}}
	r := run(t, en, gateAlways(true, ""), 1)[0]
	// 1500 uncached * $1/M + 500 cached * $0.1/M + 300 out * $10/M = $0.00455
	if u := r.Usage["build"]; u.UnpricedTokens != 0 || u.CostUSD < 0.004549 || u.CostUSD > 0.004551 {
		t.Fatalf("build: %+v", u)
	}
}
