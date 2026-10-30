// Package pipeline chains the stations for one issue and persists state after
// every transition, so any run can be killed and resumed.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gh"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/schema"
	"github.com/vianel/wonder/internal/store"
	"github.com/vianel/wonder/internal/worker"
)

// Stations, in order. pr, ci and fix loop until CI passes.
const (
	StSpec   = "spec"
	StAsk    = "ask" // the spec's questions get answered, once, up front
	StBuild  = "build"
	StPR     = "pr"
	StCI     = "ci"
	StFix    = "fix"
	StReview = "review"
	StRoute  = "route"
	StGate   = "gate"
	StMerge  = "merge"
	StDone   = "done"
)

// Label is the factory-floor name shown to humans.
var Label = map[string]string{
	StSpec:   "🏭 Planning Room",
	StAsk:    "🏭 Planning Room",
	StBuild:  "⚙️  Invention Room",
	StPR:     "🍬 Production Line",
	StCI:     "🍬 Production Line",
	StFix:    "🍬 Production Line",
	StReview: "🔍 Quality Control",
	StRoute:  "🎫 Golden Ticket",
	StGate:   "🎫 Golden Ticket",
	StMerge:  "✨ Delivery",
	StDone:   "✨ Delivery",
}

// maxDiff caps the diff put in a review prompt.
// Known limit: plain truncation; summarise per file if large PRs become common.
const maxDiff = 200_000

type Engine struct {
	Root  string
	Cfg   *config.Config
	Inv   Invoker
	Store store.Store
	GH    gh.Client
	Log   io.Writer

	// Answer asks the human the spec questions of several runs at once.
	// AutoAnswer takes the suggested answers instead (wonder run --yes).
	Answer     AnswerFunc
	AutoAnswer bool

	logMu  sync.Mutex
	paused atomic.Bool // a usage limit was hit: start no new stations
}

func (e *Engine) logf(r *store.Run, format string, a ...any) {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	fmt.Fprintf(e.Log, "#%-4d %-20s %s\n", r.Issue, Label[r.Station], fmt.Sprintf(format, a...))
}

// Open loads the run for issue, or creates a new one at the spec station.
func (e *Engine) Open(issue int) (*store.Run, error) {
	r, err := e.Store.Load(issue)
	if errors.Is(err, fs.ErrNotExist) {
		r = &store.Run{Issue: issue, Station: StSpec, Status: store.Running, Created: time.Now().UTC()}
		return r, e.Store.Save(r)
	}
	return r, err
}

// Wake makes a paused, blocked or failed run runnable again.
func Wake(r *store.Run) {
	switch r.Status {
	case store.Paused, store.Blocked, store.Failed, store.WaitingAnswers:
		r.Status, r.Error, r.ResetAt = store.Running, "", nil
	}
}

// Advance runs stations until the run waits on a human, finishes or stops.
func (e *Engine) Advance(ctx context.Context, r *store.Run) error { return e.advance(ctx, r, nil) }

// advance is Advance with an optional early stop, checked before each station.
func (e *Engine) advance(ctx context.Context, r *store.Run, stop func(*store.Run) bool) error {
	for r.Status == store.Running && (stop == nil || !stop(r)) {
		if e.paused.Load() {
			r.Status, r.Error = store.Paused, "another station hit a usage limit"
			break
		}
		err := e.step(ctx, r)
		var rl *worker.RateLimitError
		var bl *BlockedError
		switch {
		case err == nil:
		case ctx.Err() != nil:
			// Interrupted: leave it running so `wonder resume` picks it up.
			_ = e.Store.Save(r)
			return ctx.Err()
		case errors.As(err, &rl):
			e.paused.Store(true)
			r.Status, r.Error, r.ResetAt = store.Paused, rl.Error(), rl.Reset
		case errors.As(err, &bl):
			r.Status, r.Error = store.Blocked, err.Error()
		default:
			r.Status, r.Error = store.Failed, err.Error()
		}
		if err != nil {
			e.logf(r, "%s: %s", r.Status, r.Error)
		}
		if serr := e.Store.Save(r); serr != nil {
			return serr
		}
	}
	return nil
}

func (e *Engine) step(ctx context.Context, r *store.Run) error {
	switch r.Station {
	case StSpec:
		return e.spec(ctx, r)
	case StAsk:
		return e.ask(r)
	case StBuild:
		return e.build(ctx, r)
	case StPR:
		return e.pr(r)
	case StCI:
		return e.ci(ctx, r)
	case StFix:
		return e.fix(ctx, r)
	case StReview:
		return e.review(ctx, r)
	case StRoute:
		return e.route(r)
	case StGate:
		r.Status = store.WaitingGate
		return nil
	case StMerge:
		return e.merge(ctx, r)
	}
	return fmt.Errorf("unknown station %q", r.Station)
}

// invoke renders the station prompt, runs the worker and records the prompt
// hash and model. key is the models/prompts key ("spec", "review.codex").
func (e *Engine) invoke(ctx context.Context, r *store.Run, station, key string, w config.Worker, promptPath string, d PromptData, write bool, schemaName string, out any) (hash string, err error) {
	tpl, err := config.Prompt(e.Root, promptPath)
	if err != nil {
		return "", err
	}
	if d.Issue.Number == 0 {
		d.Issue = gh.Issue{Number: r.Issue, Title: r.Title, Body: r.Body}
	}
	if d.Spec == "" && r.Spec != nil {
		b, _ := json.MarshalIndent(r.Spec, "", "  ")
		d.Spec = string(b)
	}
	prompt, err := Render(tpl, d)
	if err != nil {
		return "", fmt.Errorf("%s: %w", promptPath, err)
	}
	name := fmt.Sprintf("f%d-%s", r.Issue, strings.ReplaceAll(key, ".", "-"))
	raw, err := e.Inv.Invoke(ctx, name, w, prompt, r.Worktree, write, schemaName)
	if err != nil {
		return "", err
	}
	return Hash(tpl), json.Unmarshal(raw, out)
}

func (e *Engine) record(r *store.Run, station, key, hash string, w config.Worker) {
	if r.Prompts == nil {
		r.Prompts, r.Models = map[string]string{}, map[string]string{}
	}
	r.Prompts[station], r.Models[key] = hash, ModelName(w)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

func (e *Engine) spec(ctx context.Context, r *store.Run) error {
	iss, err := e.GH.Issue(r.Issue)
	if err != nil {
		return err
	}
	r.Title, r.Body = iss.Title, iss.Body
	if r.Branch == "" {
		r.Branch = fmt.Sprintf("wonder/%d-%s", r.Issue, slug(iss.Title))
		r.Worktree = filepath.Join(e.Root, ".wonder", "worktrees", strconv.Itoa(r.Issue))
	}
	if err := gitx.WorktreeAdd(e.Root, r.Worktree, r.Branch, e.Cfg.Factory.BaseBranch); err != nil {
		return err
	}
	if r.BaseSHA == "" {
		if r.BaseSHA, err = gitx.HeadSHA(r.Worktree); err != nil {
			return err
		}
	}
	repo, _ := e.GH.Repo()
	e.logf(r, "writing spec for %q", iss.Title)
	var spec store.Spec
	w := e.Cfg.Stations.Spec
	hash, err := e.invoke(ctx, r, StSpec, StSpec, w.Worker, w.Prompt, PromptData{Repo: repo, Issue: iss}, false, schema.Spec, &spec)
	if err != nil {
		return err
	}
	e.record(r, StSpec, StSpec, hash, w.Worker)
	e.logf(r, "✓ spec ready (risk %s, %d criteria)", spec.Risk, len(spec.AcceptanceCriteria))
	r.Spec, r.Station = &spec, StAsk
	return nil
}

func (e *Engine) ask(r *store.Run) error {
	if r.Spec == nil || len(r.Spec.Unanswered()) == 0 {
		r.Station = StBuild
		return nil
	}
	if e.AutoAnswer {
		for _, i := range r.Spec.Unanswered() {
			q := &r.Spec.Questions[i]
			q.Answer, q.AnsweredBy = q.Suggested, "suggested"
		}
		e.logf(r, "took the suggested answers to %d question(s)", len(r.Spec.Questions))
		r.Station = StBuild
		return nil
	}
	r.Status = store.WaitingAnswers
	e.logf(r, "%d question(s) for you: wonder resume %d", len(r.Spec.Unanswered()), r.Issue)
	return nil
}

// write runs a write station (build or fix) and commits what it changed.
func (e *Engine) write(ctx context.Context, r *store.Run, station string, w config.Station, d PromptData, msg string) error {
	var out store.WorkerOut
	hash, err := e.invoke(ctx, r, station, station, w.Worker, w.Prompt, d, true, schema.Worker, &out)
	if err != nil {
		return err
	}
	e.record(r, station, station, hash, w.Worker)
	r.Build = &out
	switch out.Status {
	case "blocked":
		return &BlockedError{Msg: firstNonEmpty(out.Notes, out.Summary)}
	case "failed":
		return fmt.Errorf("%s failed: %s %s", station, out.Summary, out.Notes)
	}
	committed, err := gitx.CommitAll(r.Worktree, msg)
	if err != nil {
		return err
	}
	if !committed {
		head, _ := gitx.HeadSHA(r.Worktree)
		// A resumed build may have committed before the crash.
		if station == StFix || head == r.BaseSHA {
			return fmt.Errorf("%s reported done but changed no files", station)
		}
	}
	e.logf(r, "✓ %s", out.Summary)
	r.Station = StPR
	return nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func (e *Engine) build(ctx context.Context, r *store.Run) error {
	e.logf(r, "building")
	return e.write(ctx, r, StBuild, e.Cfg.Stations.Build, PromptData{}, fmt.Sprintf("%s (#%d)", r.Title, r.Issue))
}

func (e *Engine) fix(ctx context.Context, r *store.Run) error {
	reason, msg := "CI is failing. The failing logs are below.", "fix: make CI pass"
	switch r.FixReason {
	case "gate":
		reason, msg = "A human reviewer asked for changes. Their notes are below.", "fix: address review notes"
	case "conflict":
		reason, msg = "The branch conflicts with the base branch. The conflicted files are listed below.", "fix: resolve merge conflicts"
	}
	e.logf(r, "fixing (%s)", r.FixReason)
	if err := e.write(ctx, r, StFix, e.Cfg.Stations.Fix, PromptData{Reason: reason, Input: r.FixInput}, fmt.Sprintf("%s (#%d)", msg, r.Issue)); err != nil {
		return err
	}
	r.FixInput = ""
	return nil
}

func (e *Engine) pr(r *store.Run) error {
	if err := gitx.Push(r.Worktree, r.Branch); err != nil {
		return err
	}
	var body strings.Builder
	if r.Spec != nil {
		fmt.Fprintf(&body, "%s\n\n**Acceptance criteria**\n", r.Spec.Goal)
		for _, c := range r.Spec.AcceptanceCriteria {
			fmt.Fprintf(&body, "- [ ] %s\n", c)
		}
	}
	fmt.Fprintf(&body, "\nCloses #%d\n\n_Made by [wonder](https://github.com/vianel/wonder)._\n", r.Issue)
	pr, err := e.GH.EnsurePR(e.Cfg.Factory.BaseBranch, r.Branch, r.Title, body.String())
	if err != nil {
		return err
	}
	if r.HeadSHA, err = gitx.HeadSHA(r.Worktree); err != nil {
		return err
	}
	e.logf(r, "✓ pushed %s", pr.URL)
	r.PR, r.PRURL, r.Station = pr.Number, pr.URL, StCI
	return nil
}

// syncBase merges the latest base into a PR that conflicts with it. GitHub runs
// no CI on conflicting PRs, so this has to happen before waiting on checks.
// The factory does the git work; only conflicted files go to the fixer.
// It reports whether anything changed (the run moved to pr or fix).
func (e *Engine) syncBase(ctx context.Context, r *store.Run) (bool, error) {
	conflict, err := e.GH.Conflicting(ctx, r.PR)
	if err != nil || !conflict {
		return false, err
	}
	base := e.Cfg.Factory.BaseBranch
	e.logf(r, "PR #%d conflicts with %s; merging %s in", r.PR, base, base)
	if _, err := gitx.Run(r.Worktree, "fetch", "-q", "origin", base); err != nil {
		return false, err
	}
	if _, err := gitx.Run(r.Worktree, "merge", "--no-edit", "origin/"+base); err == nil {
		r.Station = StPR
		return true, nil
	}
	files, err := gitx.Run(r.Worktree, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err
	}
	if r.BabysitCount >= e.Cfg.Stations.Fix.MaxAttempts {
		return false, fmt.Errorf("merge conflicts with %s after %d fix attempts", base, r.BabysitCount)
	}
	r.BabysitCount++
	r.FixReason, r.Station = "conflict", StFix
	r.FixInput = fmt.Sprintf("Merging the latest %s into this branch conflicted in:\n%s\n\nResolve every conflict marker. Keep both sides' changes where they are compatible, and keep the tests passing.", base, files)
	return true, nil
}

func (e *Engine) ci(ctx context.Context, r *store.Run) error {
	if moved, err := e.syncBase(ctx, r); err != nil || moved {
		return err
	}
	e.logf(r, "waiting for CI on PR #%d", r.PR)
	res, failed, err := e.GH.WaitChecks(ctx, r.PR)
	if err != nil {
		return err
	}
	r.CI = res
	switch res {
	case gh.CIFail:
		if r.BabysitCount >= e.Cfg.Stations.Fix.MaxAttempts {
			return fmt.Errorf("CI still failing after %d fix attempts", r.BabysitCount)
		}
		r.BabysitCount++
		r.FixReason, r.FixInput, r.Station = "ci", e.GH.FailedLogs(failed), StFix
		e.logf(r, "✗ CI failed, fix attempt %d/%d", r.BabysitCount, e.Cfg.Stations.Fix.MaxAttempts)
		return nil
	case gh.CINone:
		e.logf(r, "! no CI checks reported; continuing without CI")
	default:
		e.logf(r, "✓ CI passed")
	}
	// Only the first gate verdict labels the reviewers; later rounds skip review.
	r.Station = StReview
	if r.GateRounds > 0 {
		r.Station = StGate
	}
	return nil
}

// ReviewKeys names each configured reviewer: "codex", "claude", or "claude-2"
// if a worker appears twice.
func ReviewKeys(ws []config.Worker) []string {
	keys, seen := make([]string, len(ws)), map[string]int{}
	for i, w := range ws {
		seen[w.Worker]++
		keys[i] = w.Worker
		if seen[w.Worker] > 1 {
			keys[i] = fmt.Sprintf("%s-%d", w.Worker, seen[w.Worker])
		}
	}
	return keys
}

func (e *Engine) review(ctx context.Context, r *store.Run) error {
	diff, err := gitx.Diff(r.Worktree, r.BaseSHA, "HEAD")
	if err != nil {
		return err
	}
	stats, err := gitx.DiffStats(r.Worktree, r.BaseSHA, "HEAD")
	if err != nil {
		return err
	}
	if len(diff) > maxDiff {
		diff = diff[:maxDiff] + "\n… diff truncated …"
	}
	rc := e.Cfg.Stations.Review
	keys := ReviewKeys(rc.Workers)
	e.logf(r, "reviewing with %s", strings.Join(keys, " + "))

	reviews := make([]store.Review, len(keys))
	hashes := make([]string, len(keys))
	errs := make([]error, len(keys))
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hashes[i], errs[i] = e.invoke(ctx, r, StReview, "review."+keys[i], rc.Workers[i], rc.Prompt, PromptData{Diff: diff}, false, schema.Review, &reviews[i])
		}(i)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		var rl *worker.RateLimitError
		if errors.As(err, &rl) {
			return rl
		}
		return err
	}
	r.Reviews = map[string]store.Review{}
	for i, k := range keys {
		r.Reviews[k] = reviews[i]
		e.record(r, StReview, "review."+k, hashes[i], rc.Workers[i])
		e.logf(r, "%s: %s (%d findings)", k, reviews[i].Verdict, len(reviews[i].Findings))
	}
	r.DiffStats, r.Station = &stats, StRoute
	return nil
}

func (e *Engine) route(r *store.Run) error {
	spec := store.Spec{}
	if r.Spec != nil {
		spec = *r.Spec
	}
	rt := Route(e.Cfg.Router, spec, r.Reviews, *r.DiffStats)
	r.Router = &rt
	if rt.Route == "auto" {
		e.logf(r, "auto-merging: %s", rt.Reason)
		r.Station = StMerge
		return nil
	}
	r.Station, r.Status = StGate, store.WaitingGate
	e.logf(r, "\awaiting for you: %s  (wonder gate %d)", rt.Reason, r.Issue)
	return nil
}

func (e *Engine) merge(ctx context.Context, r *store.Run) error {
	// The base may have moved since the gate; resolve, re-run CI and re-gate.
	if conflict, err := e.GH.Conflicting(ctx, r.PR); err != nil || conflict {
		if conflict {
			e.logf(r, "PR #%d now conflicts with %s; back through CI and the gate", r.PR, e.Cfg.Factory.BaseBranch)
			r.Station = StCI
		}
		return err
	}
	if err := e.GH.Merge(r.PR); err != nil {
		return err
	}
	_, _ = gitx.Run(e.Root, "push", "-q", "origin", "--delete", r.Branch)
	_ = gitx.WorktreeRemove(e.Root, r.Worktree)
	_, _ = gitx.Run(e.Root, "branch", "-D", r.Branch)
	e.logf(r, "✓ merged PR #%d", r.PR)
	r.Station, r.Status = StDone, store.Done
	return nil
}

// Decide applies a human gate verdict. The first one is written to the
// verdict log with a label per reviewer.
func (e *Engine) Decide(r *store.Run, ship bool, notes string) error {
	if r.Status != store.WaitingGate {
		return fmt.Errorf("#%d is not waiting on the gate (status %s)", r.Issue, r.Status)
	}
	verdict := "changes"
	if ship {
		verdict = "ship"
	}
	if !ship && strings.TrimSpace(notes) == "" {
		return errors.New("changes need notes for the fixer")
	}
	if r.GateRounds == 0 {
		r.GateFirst, r.GateNotes = verdict, notes
		if err := e.Store.AppendVerdict(VerdictFor(r)); err != nil {
			return err
		}
	}
	r.GateRounds++
	if notes != "" && r.PR != 0 {
		_ = e.GH.Comment(r.PR, fmt.Sprintf("🎫 **Gate: %s**\n\n%s", verdict, notes))
	}
	r.Status, r.Station = store.Running, StMerge
	if !ship {
		r.Station, r.FixReason, r.FixInput = StFix, "gate", notes
	}
	return e.Store.Save(r)
}

// VerdictFor builds the verdict line for a run's first gate verdict.
func VerdictFor(r *store.Run) store.Verdict {
	v := store.Verdict{
		TS: time.Now().UTC(), Issue: r.Issue, PR: r.PR, Title: r.Title,
		BaseSHA: r.BaseSHA, HeadSHA: r.HeadSHA,
		GateFirst: r.GateFirst, GateNotes: r.GateNotes,
		Reviews: r.Reviews, Labels: map[string]string{},
		BabysitFixes: r.BabysitCount, Models: r.Models, Prompts: r.Prompts,
	}
	for k, rv := range r.Reviews {
		v.Labels[k] = store.Label(rv.Verdict, r.GateFirst)
	}
	if r.Router != nil {
		v.Router = *r.Router
	}
	if r.DiffStats != nil {
		v.DiffStats = *r.DiffStats
	}
	if r.Spec != nil {
		v.Spec = *r.Spec
	}
	return v
}

// AnswerFunc asks the human the open spec questions of every run in runs,
// filling in Answer and AnsweredBy. It returns false if the human stopped.
type AnswerFunc func(runs []*store.Run) bool

// planFirst writes every spec before anything is built and asks all their
// questions in one batch, so after that the human can walk away until the gate.
func (e *Engine) planFirst(ctx context.Context, runs []*store.Run) error {
	var wg sync.WaitGroup
	errs := make([]error, len(runs))
	for i, r := range runs {
		if r.Status == store.Running && r.Station == StSpec {
			wg.Add(1)
			go func(i int, r *store.Run) {
				defer wg.Done()
				errs[i] = e.advance(ctx, r, func(r *store.Run) bool { return r.Station != StSpec })
			}(i, r)
		}
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil || e.AutoAnswer || e.Answer == nil {
		return err
	}
	var ask []*store.Run
	for _, r := range runs {
		if r.Status == store.Running && r.Station == StAsk && len(r.Spec.Unanswered()) > 0 {
			ask = append(ask, r)
		}
	}
	if len(ask) == 0 {
		return nil
	}
	sort.Slice(ask, func(i, j int) bool { return ask[i].Issue < ask[j].Issue })
	e.Answer(ask) // unanswered questions stay open; the ask station parks the run
	for _, r := range ask {
		if err := e.Store.Save(r); err != nil {
			return err
		}
	}
	return nil
}

// GateFunc asks a human about a run waiting on the gate. ok=false leaves the
// run waiting (the human skipped or quit).
type GateFunc func(r *store.Run) (ship bool, notes string, ok bool)

// RunAll advances runs in parallel (workers are capped by the runner), then
// asks gate about each run waiting on a human, and repeats until nothing moves.
func (e *Engine) RunAll(ctx context.Context, runs []*store.Run, gate GateFunc) error {
	if err := e.planFirst(ctx, runs); err != nil {
		return err
	}
	for {
		var wg sync.WaitGroup
		errs := make([]error, len(runs))
		for i, r := range runs {
			if r.Status == store.Running {
				wg.Add(1)
				go func(i int, r *store.Run) {
					defer wg.Done()
					errs[i] = e.Advance(ctx, r)
				}(i, r)
			}
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			return err
		}
		moved := false
		if gate != nil {
			waiting := []*store.Run{}
			for _, r := range runs {
				if r.Status == store.WaitingGate {
					waiting = append(waiting, r)
				}
			}
			sort.Slice(waiting, func(i, j int) bool { return waiting[i].Issue < waiting[j].Issue })
			for _, r := range waiting {
				ship, notes, ok := gate(r)
				if !ok {
					break
				}
				if err := e.Decide(r, ship, notes); err != nil {
					return err
				}
				moved = true
			}
		}
		if !moved {
			return nil
		}
	}
}
