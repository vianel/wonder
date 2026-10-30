// Package eval replays past gate verdicts against a candidate prompt, decides
// promotion, reports stats and harvests post-merge facts from GitHub.
package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gh"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/pipeline"
	"github.com/vianel/wonder/internal/schema"
	"github.com/vianel/wonder/internal/store"
)

// MinExamples is the dataset size below which results are called noisy.
const MinExamples = 15

type Eval struct {
	Root  string
	Cfg   *config.Config
	Store store.Store
	Inv   pipeline.Invoker
	GH    gh.Client
	Out   io.Writer
}

// Outcome is what the gate effectively decided: a revert harvested later
// turns a ship into changes.
func Outcome(v store.Verdict) (gate, notes string) {
	if v.Harvest != nil && v.Harvest.Reverted {
		return "changes", strings.TrimSpace(v.GateNotes + "\nThe PR was reverted within 7 days of merging.")
	}
	return v.GateFirst, v.GateNotes
}

// Score counts reviewer mistakes over all runs of one prompt.
type Score struct {
	Runs        int `json:"runs"`
	FalseAlarms int `json:"false_alarms"`
	Misses      int `json:"misses"`
}

func (s *Score) add(o Score) { s.Runs += o.Runs; s.FalseAlarms += o.FalseAlarms; s.Misses += o.Misses }

// Passes is the promotion rule: fewer misses and no more false alarms.
func Passes(cur, cand Score) bool {
	return cand.Misses < cur.Misses && cand.FalseAlarms <= cur.FalseAlarms
}

type Row struct {
	Issue     int    `json:"issue"`
	Gate      string `json:"gate"`
	Current   Score  `json:"current"`
	Candidate Score  `json:"candidate"`
	Skipped   string `json:"skipped,omitempty"`
}

// Result of a review replay, saved to .wonder/replays/ for promote.
type Result struct {
	TS            time.Time `json:"ts"`
	Station       string    `json:"station"`
	CurrentPath   string    `json:"current_path"`
	CurrentHash   string    `json:"current_hash"`
	CandidatePath string    `json:"candidate_path"`
	CandidateHash string    `json:"candidate_hash"`
	RunsPerPrompt int       `json:"runs_per_prompt"`
	Current       Score     `json:"current"`
	Candidate     Score     `json:"candidate"`
	Passed        bool      `json:"passed"`
	Rows          []Row     `json:"rows"`
}

func resultName(station, hash string) string {
	return filepath.Join("replays", station+"-"+hash+".json")
}

// ReplayReview runs the current and candidate review prompts n times on every
// verdict and scores both.
func (e *Eval) ReplayReview(ctx context.Context, candidate string, n int) (*Result, error) {
	vs, err := e.Store.Verdicts()
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, errors.New("no verdicts yet: answer some gates first")
	}
	if len(vs) < MinExamples {
		fmt.Fprintf(e.Out, "! only %d examples (< %d): results are noisy and in-sample\n", len(vs), MinExamples)
	}
	rc := e.Cfg.Stations.Review
	cur, err := config.Prompt(e.Root, rc.Prompt)
	if err != nil {
		return nil, err
	}
	candB, err := os.ReadFile(candidate)
	if err != nil {
		return nil, err
	}
	cand := string(candB)
	judge, err := config.Prompt(e.Root, e.Cfg.Eval.Judge.Prompt)
	if err != nil {
		return nil, err
	}
	res := &Result{TS: time.Now().UTC(), Station: "review", CurrentPath: rc.Prompt, CurrentHash: pipeline.Hash(cur),
		CandidatePath: candidate, CandidateHash: pipeline.Hash(cand), RunsPerPrompt: n}

	for _, v := range vs {
		row, err := e.replayOne(ctx, v, cur, cand, judge, n)
		if err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, row)
		res.Current.add(row.Current)
		res.Candidate.add(row.Candidate)
	}
	res.Passed = Passes(res.Current, res.Candidate)
	e.printReplay(res)
	return res, e.Store.WriteJSON(resultName("review", res.CandidateHash), res)
}

func (e *Eval) replayOne(ctx context.Context, v store.Verdict, cur, cand, judge string, n int) (Row, error) {
	gate, notes := Outcome(v)
	row := Row{Issue: v.Issue, Gate: gate}
	if !e.haveCommit(v) {
		row.Skipped = "head_sha not found locally or on origin"
		return row, nil
	}
	wt := filepath.Join(e.Store.Dir, "replay", fmt.Sprintf("%d-%s", v.Issue, short(v.HeadSHA)))
	_ = gitx.WorktreeRemove(e.Root, wt)
	if err := gitx.WorktreeAddDetached(e.Root, wt, v.HeadSHA); err != nil {
		return row, err
	}
	defer func() { _ = gitx.WorktreeRemove(e.Root, wt) }()
	diff, err := gitx.Diff(wt, v.BaseSHA, v.HeadSHA)
	if err != nil {
		return row, err
	}
	specJSON, _ := json.MarshalIndent(v.Spec, "", "  ")
	data := pipeline.PromptData{Issue: gh.Issue{Number: v.Issue, Title: v.Title}, Spec: string(specJSON), Diff: diff}

	ws := e.Cfg.Stations.Review.Workers
	keys := pipeline.ReviewKeys(ws)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var errs []error
	for pi, tpl := range []string{cur, cand} {
		prompt, err := pipeline.Render(tpl, data)
		if err != nil {
			return row, err
		}
		for wi := range ws {
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(pi, wi, i int, prompt string) {
					defer wg.Done()
					name := fmt.Sprintf("r%d-%s-%s%d", v.Issue, keys[wi], []string{"cur", "cand"}[pi], i+1)
					s, err := e.scoreOnce(ctx, name, ws[wi], prompt, wt, gate, notes, judge)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						errs = append(errs, err)
						return
					}
					if pi == 0 {
						row.Current.add(s)
					} else {
						row.Candidate.add(s)
					}
				}(pi, wi, i, prompt)
			}
		}
	}
	wg.Wait()
	return row, errors.Join(errs...)
}

// scoreOnce runs one review and scores it against the gate. A reviewer that
// approved a change the gate rejected missed it by definition; one that asked
// for changes is checked by the judge against the gate's notes.
func (e *Eval) scoreOnce(ctx context.Context, name string, w config.Worker, prompt, dir, gate, notes, judge string) (Score, error) {
	raw, err := e.Inv.Invoke(ctx, name, w, prompt, dir, false, schema.Review)
	if err != nil {
		return Score{}, err
	}
	var rv store.Review
	if err := json.Unmarshal(raw, &rv); err != nil {
		return Score{}, err
	}
	s := Score{Runs: 1}
	switch {
	case gate == "ship" && rv.Verdict == "changes":
		s.FalseAlarms = 1
	case gate == "changes" && rv.Verdict == "approve":
		s.Misses = 1
	case gate == "changes":
		jp, err := pipeline.Render(judge, pipeline.PromptData{GateNotes: notes, Review: string(raw)})
		if err != nil {
			return s, err
		}
		jraw, err := e.Inv.Invoke(ctx, name+"-judge", e.Cfg.Eval.Judge.Worker, jp, dir, false, schema.Judge)
		if err != nil {
			return s, err
		}
		var j struct {
			Missed bool `json:"missed"`
		}
		if err := json.Unmarshal(jraw, &j); err != nil {
			return s, err
		}
		if j.Missed {
			s.Misses = 1
		}
	}
	return s, nil
}

func (e *Eval) haveCommit(v store.Verdict) bool {
	has := func() bool { _, err := gitx.Run(e.Root, "cat-file", "-e", v.HeadSHA+"^{commit}"); return err == nil }
	if v.HeadSHA == "" {
		return false
	}
	if has() {
		return true
	}
	// Merged branches get deleted; GitHub keeps the PR head ref.
	_, _ = gitx.Run(e.Root, "fetch", "-q", "origin", fmt.Sprintf("refs/pull/%d/head", v.PR))
	return has()
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func (e *Eval) printReplay(r *Result) {
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "issue\tgate\tcurrent FA/miss\tcandidate FA/miss\t\n")
	for _, row := range r.Rows {
		if row.Skipped != "" {
			fmt.Fprintf(tw, "#%d\t%s\tskipped: %s\t\t\n", row.Issue, row.Gate, row.Skipped)
			continue
		}
		fmt.Fprintf(tw, "#%d\t%s\t%d/%d\t%d/%d\t\n", row.Issue, row.Gate, row.Current.FalseAlarms, row.Current.Misses, row.Candidate.FalseAlarms, row.Candidate.Misses)
	}
	fmt.Fprintf(tw, "total\t\t%d/%d\t%d/%d\t(over %d runs each)\n", r.Current.FalseAlarms, r.Current.Misses, r.Candidate.FalseAlarms, r.Candidate.Misses, r.Candidate.Runs)
	tw.Flush()
	if r.Passed {
		fmt.Fprintf(e.Out, "✓ candidate %s passes: fewer misses, no more false alarms. `wonder promote --station review --prompt %s`\n", r.CandidateHash, r.CandidatePath)
	} else {
		fmt.Fprintf(e.Out, "✗ candidate %s does not pass (needs fewer misses and no more false alarms)\n", r.CandidateHash)
	}
}

// Router outcomes.
const (
	Correct     = "correct"
	Unnecessary = "unnecessary review"
	Unsafe      = "UNSAFE auto-merge"
)

// RouterOutcome compares a route with what the gate decided.
func RouterOutcome(route, gate string) string {
	switch {
	case route == "auto" && gate == "changes":
		return Unsafe
	case route == "gate" && gate == "ship":
		return Unnecessary
	}
	return Correct
}

// ReplayRouter routes every verdict with the current and candidate router
// rules and compares both with the gate. No agents involved.
func (e *Eval) ReplayRouter(cand config.Router) map[string]int {
	vs, err := e.Store.Verdicts()
	if err != nil || len(vs) == 0 {
		fmt.Fprintln(e.Out, "no verdicts yet")
		return nil
	}
	if len(vs) < MinExamples {
		fmt.Fprintf(e.Out, "! only %d examples (< %d): results are noisy and in-sample\n", len(vs), MinExamples)
	}
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "issue\tgate\tcurrent\tcandidate\t\n")
	totals := map[string]int{}
	for _, v := range vs {
		gate, _ := Outcome(v)
		c := pipeline.Route(e.Cfg.Router, v.Spec, v.Reviews, v.DiffStats)
		k := pipeline.Route(cand, v.Spec, v.Reviews, v.DiffStats)
		co, ko := RouterOutcome(c.Route, gate), RouterOutcome(k.Route, gate)
		totals["current "+co]++
		totals["candidate "+ko]++
		fmt.Fprintf(tw, "#%d\t%s\t%s → %s\t%s → %s\t\n", v.Issue, gate, c.Route, co, k.Route, ko)
	}
	tw.Flush()
	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(e.Out, "%-36s %d\n", k, totals[k])
	}
	return totals
}

// Promote switches the review prompt to candidate if its saved replay passed
// against the prompt that is current now.
func (e *Eval) Promote(candidate string) error {
	b, err := os.ReadFile(candidate)
	if err != nil {
		return err
	}
	hash := pipeline.Hash(string(b))
	var res Result
	rb, err := os.ReadFile(filepath.Join(e.Store.Dir, resultName("review", hash)))
	if err != nil {
		return fmt.Errorf("no replay for %s (%s): run `wonder replay --station review --prompt %s` first", candidate, hash, candidate)
	}
	if err := json.Unmarshal(rb, &res); err != nil {
		return err
	}
	cur, err := config.Prompt(e.Root, e.Cfg.Stations.Review.Prompt)
	if err != nil {
		return err
	}
	if res.CurrentHash != pipeline.Hash(cur) {
		return fmt.Errorf("the current review prompt changed since the replay (%s → %s); replay again", res.CurrentHash, pipeline.Hash(cur))
	}
	if !res.Passed {
		return fmt.Errorf("replay did not pass: current FA/miss %d/%d, candidate %d/%d",
			res.Current.FalseAlarms, res.Current.Misses, res.Candidate.FalseAlarms, res.Candidate.Misses)
	}
	rel := candidate
	if abs, err := filepath.Abs(candidate); err == nil {
		if r, err := filepath.Rel(e.Root, abs); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
	}
	path := filepath.Join(e.Root, config.File)
	tb, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := SetPrompt(string(tb), "stations.review", rel)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return err
	}
	if _, err := config.Load(e.Root); err != nil {
		_ = os.WriteFile(path, tb, 0o644)
		return fmt.Errorf("promotion reverted, config invalid: %w", err)
	}
	fmt.Fprintf(e.Out, "✓ review prompt is now %s (%s)\n", rel, hash)
	return e.Store.AppendJSONL("promotions.jsonl", map[string]any{
		"ts": time.Now().UTC(), "station": "review",
		"from":    map[string]string{"path": res.CurrentPath, "hash": res.CurrentHash},
		"to":      map[string]string{"path": rel, "hash": hash},
		"current": res.Current, "candidate": res.Candidate,
	})
}

var sectionRe = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
var promptRe = regexp.MustCompile(`^(\s*prompt\s*=\s*)"[^"]*"(.*)$`)

// SetPrompt rewrites `prompt = "..."` in [section], keeping the rest of the
// file (comments included) as is.
func SetPrompt(toml, section, path string) (string, error) {
	lines := strings.Split(toml, "\n")
	in := false
	for i, l := range lines {
		if m := sectionRe.FindStringSubmatch(l); m != nil {
			in = m[1] == section && !strings.HasPrefix(strings.TrimSpace(l), "[[")
			continue
		}
		if in {
			if m := promptRe.FindStringSubmatch(l); m != nil {
				lines[i] = m[1] + strconv.Quote(path) + m[2]
				return strings.Join(lines, "\n"), nil
			}
		}
	}
	return "", fmt.Errorf("no prompt = \"...\" line under [%s] in %s", section, config.File)
}

// Stats prints reviewer, router, babysit and gate stats from the verdict log.
func (e *Eval) Stats() error {
	vs, err := e.Store.Verdicts()
	if err != nil {
		return err
	}
	if len(vs) == 0 {
		fmt.Fprintln(e.Out, "no verdicts yet")
		return nil
	}
	type counts struct{ n, agree, miss, fa int }
	rev := map[string]*counts{}
	router := map[string]int{}
	ships, fixes, maxFix := 0, 0, 0
	for _, v := range vs {
		for k, l := range v.Labels {
			c := rev[k]
			if c == nil {
				c = &counts{}
				rev[k] = c
			}
			c.n++
			switch l {
			case store.Agree:
				c.agree++
			case store.Miss:
				c.miss++
			case store.FalseAlarm:
				c.fa++
			}
		}
		gate, _ := Outcome(v)
		if gate == "ship" {
			ships++
		}
		router[RouterOutcome(v.Router.RulesRoute, gate)]++
		fixes += v.BabysitFixes
		if v.BabysitFixes > maxFix {
			maxFix = v.BabysitFixes
		}
	}
	pct := func(a, b int) string { return fmt.Sprintf("%3.0f%%", 100*float64(a)/float64(b)) }
	fmt.Fprintf(e.Out, "%d verdicts, %s shipped at the first gate\n\n", len(vs), pct(ships, len(vs)))
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "reviewer\tn\tagree\tmiss\tfalse alarm\t\n")
	keys := make([]string, 0, len(rev))
	for k := range rev {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := rev[k]
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t\n", k, c.n, pct(c.agree, c.n), pct(c.miss, c.n), pct(c.fa, c.n))
	}
	tw.Flush()
	fmt.Fprintf(e.Out, "\nrouter rules (ignoring allow_auto): %d correct, %d unnecessary reviews, %d unsafe auto-merges\n",
		router[Correct], router[Unnecessary], router[Unsafe])
	fmt.Fprintf(e.Out, "babysit: %.1f CI fixes per run on average, max %d\n", float64(fixes)/float64(len(vs)), maxFix)
	if len(vs) < MinExamples {
		fmt.Fprintf(e.Out, "! fewer than %d verdicts: rates are noisy\n", MinExamples)
	}
	return nil
}

// Harvest records merge and revert facts for verdicts. A revert within 7 days
// turns every approving reviewer's label into a miss.
func (e *Eval) Harvest() error {
	vs, err := e.Store.Verdicts()
	if err != nil {
		return err
	}
	now, changed := time.Now().UTC(), 0
	for i := range vs {
		v := &vs[i]
		// Done once a revert is found or the 7-day window was already checked in full.
		if h := v.Harvest; v.PR == 0 || (h != nil && (h.Reverted || (h.Merged && h.TS.Sub(h.MergedAt) > 7*24*time.Hour))) {
			continue
		}
		st, err := e.GH.PRState(v.PR)
		if err != nil {
			fmt.Fprintf(e.Out, "#%d: %v\n", v.Issue, err)
			continue
		}
		h := &store.Harvest{TS: now, Merged: st.State == "MERGED", MergedAt: st.MergedAt}
		if st.MergeCommit != nil {
			h.MergeSHA = st.MergeCommit.Oid
		}
		if h.Merged {
			if h.Reverted, err = e.GH.Reverted(v.PR, st.MergedAt); err != nil {
				return err
			}
		}
		if h.Reverted {
			for k, rv := range v.Reviews {
				if rv.Verdict == "approve" {
					v.Labels[k] = store.Miss
				}
			}
			fmt.Fprintf(e.Out, "#%d PR #%d was reverted: approving reviewers now count as misses\n", v.Issue, v.PR)
		}
		v.Harvest = h
		changed++
	}
	fmt.Fprintf(e.Out, "harvested %d verdicts\n", changed)
	if changed == 0 {
		return nil
	}
	return e.Store.WriteVerdicts(vs)
}
