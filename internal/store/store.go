// Package store persists run state (.wonder/runs/<issue>.json) and the verdict
// log (.wonder/verdicts.jsonl). Formats are documented in docs/.
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/vianel/wonder/internal/gitx"
)

// Version of the run-state and verdict formats.
const V = 1

type Spec struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Assumptions        []string `json:"assumptions"`
	Risk               string   `json:"risk"`
	FilesLikelyTouched []string `json:"files_likely_touched"`
	// Questions the spec writer could not settle from the issue. Answers are
	// filled in before the build, so every later station sees them.
	Questions []Question `json:"questions,omitempty"`
}

type Question struct {
	Question   string `json:"question"`
	Suggested  string `json:"suggested"`
	Answer     string `json:"answer,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"` // you | suggested
}

// Unanswered returns the indexes of questions that still need an answer.
func (s *Spec) Unanswered() []int {
	var idx []int
	for i, q := range s.Questions {
		if q.AnsweredBy == "" {
			idx = append(idx, i)
		}
	}
	return idx
}

// WorkerOut is the worker JSON contract shared by write stations.
type WorkerOut struct {
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed"`
	Notes        string   `json:"notes"`
}

type Finding struct {
	Severity string `json:"severity"`
	Area     string `json:"area"`
	Issue    string `json:"issue"`
}

type Review struct {
	Verdict  string    `json:"verdict"` // approve | changes
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

type Route struct {
	Route        string `json:"route"` // auto | gate
	Reason       string `json:"reason"`
	RuleOverrode bool   `json:"rule_overrode"` // a hard rule overrode the router agent (always false until there is one)
	AgentRoute   string `json:"agent_route,omitempty"`
	// RulesRoute is where the rules alone would send the PR, ignoring
	// allow_auto. Used by stats and router replay.
	RulesRoute string `json:"rules_route"`
}

// Run statuses.
const (
	Running        = "running"
	WaitingGate    = "waiting_gate"
	WaitingAnswers = "waiting_answers"
	Blocked        = "blocked"
	Paused         = "paused_rate_limit"
	Failed         = "failed"
	Done           = "done"
)

type Run struct {
	V        int        `json:"v"`
	Issue    int        `json:"issue"`
	Title    string     `json:"title"`
	Body     string     `json:"body"`
	Station  string     `json:"station"`
	Status   string     `json:"status"`
	Error    string     `json:"error,omitempty"`
	ResetAt  *time.Time `json:"reset_at,omitempty"`
	Branch   string     `json:"branch,omitempty"`
	Worktree string     `json:"worktree,omitempty"`
	BaseSHA  string     `json:"base_sha,omitempty"`
	HeadSHA  string     `json:"head_sha,omitempty"`
	PR       int        `json:"pr,omitempty"`
	PRURL    string     `json:"pr_url,omitempty"`

	Spec         *Spec             `json:"spec,omitempty"`
	Build        *WorkerOut        `json:"build,omitempty"`
	FixReason    string            `json:"fix_reason,omitempty"` // ci | gate
	FixInput     string            `json:"fix_input,omitempty"`
	BabysitCount int               `json:"babysit_count"`
	CI           string            `json:"ci,omitempty"`
	Reviews      map[string]Review `json:"reviews,omitempty"`
	DiffStats    *gitx.Stats       `json:"diff_stats,omitempty"`
	Router       *Route            `json:"router,omitempty"`
	GateRounds   int               `json:"gate_rounds"`
	GateFirst    string            `json:"gate_first,omitempty"`
	GateNotes    string            `json:"gate_notes,omitempty"`
	Models       map[string]string `json:"models"`
	Prompts      map[string]string `json:"prompts"`

	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Store is rooted at <repo>/.wonder.
type Store struct{ Dir string }

func (s Store) runPath(issue int) string {
	return filepath.Join(s.Dir, "runs", strconv.Itoa(issue)+".json")
}

// Save writes run state atomically (temp file + rename).
func (s Store) Save(r *Run) error {
	r.V, r.Updated = V, time.Now().UTC()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.runPath(r.Issue), b)
}

func (s Store) Load(issue int) (*Run, error) {
	b, err := os.ReadFile(s.runPath(issue))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	var r Run
	return &r, json.Unmarshal(b, &r)
}

// List returns all runs ordered by issue number.
func (s Store) List() ([]*Run, error) {
	paths, _ := filepath.Glob(filepath.Join(s.Dir, "runs", "*.json"))
	var runs []*Run
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r Run
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		runs = append(runs, &r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Issue < runs[j].Issue })
	return runs, nil
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}

// Verdict is one line of verdicts.jsonl: the first gate verdict of a run.
type Verdict struct {
	V            int               `json:"v"`
	TS           time.Time         `json:"ts"`
	Issue        int               `json:"issue"`
	PR           int               `json:"pr"`
	BaseSHA      string            `json:"base_sha"`
	HeadSHA      string            `json:"head_sha"`
	GateFirst    string            `json:"gate_first"` // ship | changes
	GateNotes    string            `json:"gate_notes"`
	Reviews      map[string]Review `json:"reviews"`
	Labels       map[string]string `json:"labels"` // agree | miss | false_alarm
	Router       Route             `json:"router"`
	DiffStats    gitx.Stats        `json:"diff_stats"`
	BabysitFixes int               `json:"babysit_fixes"`
	Spec         Spec              `json:"spec"`
	Title        string            `json:"title"`
	Models       map[string]string `json:"models"`
	Prompts      map[string]string `json:"prompts"`
	Harvest      *Harvest          `json:"harvest,omitempty"`
}

// Harvest holds facts pulled from GitHub after the gate by `wonder harvest`.
type Harvest struct {
	TS       time.Time `json:"ts"`
	Merged   bool      `json:"merged"`
	MergedAt time.Time `json:"merged_at,omitempty"`
	MergeSHA string    `json:"merge_sha,omitempty"`
	Reverted bool      `json:"reverted"`
}

// Labels.
const (
	Agree      = "agree"
	Miss       = "miss"
	FalseAlarm = "false_alarm"
)

// Label compares a reviewer's verdict with the gate's.
func Label(review, gate string) string {
	switch {
	case review == "approve" && gate == "changes":
		return Miss
	case review == "changes" && gate == "ship":
		return FalseAlarm
	}
	return Agree
}

func (s Store) verdictPath() string { return filepath.Join(s.Dir, "verdicts.jsonl") }

func (s Store) AppendVerdict(v Verdict) error {
	v.V = V
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.verdictPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s Store) Verdicts() ([]Verdict, error) { return ReadVerdicts(s.verdictPath()) }

// ReadVerdicts reads a verdicts.jsonl file; a missing file is an empty log.
func ReadVerdicts(path string) ([]Verdict, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var vs []Verdict
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v Verdict
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, errors.New(path + ":" + strconv.Itoa(n) + ": " + err.Error())
		}
		vs = append(vs, v)
	}
	return vs, sc.Err()
}

// WriteVerdicts rewrites the whole log atomically (used by harvest).
func (s Store) WriteVerdicts(vs []Verdict) error {
	var b bytes.Buffer
	for _, v := range vs {
		line, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(append(line, '\n'))
	}
	return writeAtomic(s.verdictPath(), b.Bytes())
}

// AppendJSONL appends one JSON line to a file under the store.
func (s Store) AppendJSONL(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteJSON writes v as indented JSON to a file under the store, atomically.
func (s Store) WriteJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, name), b)
}
