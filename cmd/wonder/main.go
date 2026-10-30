// Command wonder turns GitHub issues into reviewed, merged PRs by running a
// fixed pipeline of coding agents.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/eval"
	"github.com/vianel/wonder/internal/gate"
	"github.com/vianel/wonder/internal/gh"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/pipeline"
	"github.com/vianel/wonder/internal/runner"
	"github.com/vianel/wonder/internal/store"
)

var version = "dev" // set by goreleaser

func main() {
	if err := root().Execute(); err != nil {
		os.Exit(1)
	}
}

func root() *cobra.Command {
	c := &cobra.Command{
		Use:           "wonder",
		Short:         "🎩 WonderWorks: turn GitHub issues into reviewed, merged PRs with coding agents",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	c.AddCommand(doctorCmd(), initCmd(), runCmd(), pickCmd(), resumeCmd(), gateCmd(), statusCmd(),
		harvestCmd(), replayCmd(), promoteCmd(), statsCmd())
	return c
}

// app is everything a command needs inside a configured repo.
type app struct {
	root  string
	cfg   *config.Config
	store store.Store
	inv   pipeline.Invoker
	gh    gh.Client
}

func load() (*app, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := gitx.Root(wd)
	if err != nil {
		return nil, errors.New("not inside a git repository")
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	st := store.Store{Dir: filepath.Join(root, ".wonder")}
	var r runner.Runner = runner.Exec{}
	if runner.InHerdr() {
		if _, err := exec.LookPath("herdr"); err == nil {
			r = &runner.Herdr{}
		}
	}
	return &app{
		root: root, cfg: cfg, store: st, gh: gh.Client{Dir: root},
		inv: pipeline.Invoker{Runner: runner.NewLimit(r, cfg.Factory.MaxPanes), TmpDir: filepath.Join(st.Dir, "tmp")},
	}, nil
}

func (a *app) engine() *pipeline.Engine {
	if _, ok := a.inv.Runner.(*runner.Limit).R.(*runner.Herdr); ok {
		fmt.Printf("🎩 workers run in herdr panes (max %d)\n", a.cfg.Factory.MaxPanes)
	} else {
		fmt.Printf("🎩 workers run as subprocesses (max %d); run inside herdr to watch them in panes\n", a.cfg.Factory.MaxPanes)
	}
	return &pipeline.Engine{Root: a.root, Cfg: a.cfg, Inv: a.inv, Store: a.store, GH: a.gh, Log: os.Stdout}
}

func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

var issueURL = regexp.MustCompile(`/issues/(\d+)/?$`)

// issues parses "7", "#7" or an issue URL like the one `gh issue create` prints.
func issues(args []string) ([]int, error) {
	var ns []int
	for _, a := range args {
		s := strings.TrimPrefix(a, "#")
		if m := issueURL.FindStringSubmatch(s); m != nil {
			s = m[1]
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%q is not an issue number", a)
		}
		ns = append(ns, n)
	}
	return ns, nil
}

// drive opens or resumes the runs and pushes them through the pipeline.
// yes takes the spec's suggested answers instead of asking (also when there
// is no terminal to ask on).
func drive(a *app, nums []int, askGate, yes bool) error {
	e := a.engine()
	e.AutoAnswer = yes || !gate.Interactive()
	e.Answer = gate.Questions
	var runs []*store.Run
	for _, n := range nums {
		r, err := e.Open(n)
		if err != nil {
			return err
		}
		if r.Status == store.Done {
			fmt.Printf("#%d is already done (PR %s)\n", n, r.PRURL)
			continue
		}
		pipeline.Wake(r)
		runs = append(runs, r)
	}
	ctx, stop := signalCtx()
	defer stop()
	var g pipeline.GateFunc
	if askGate {
		g = gate.Ask
	}
	err := e.RunAll(ctx, runs, g)
	if ctx.Err() != nil {
		fmt.Println("\ninterrupted; state saved. Continue with `wonder resume <issue>`.")
		return nil
	}
	summary(runs)
	return err
}

func summary(runs []*store.Run) {
	for _, r := range runs {
		switch r.Status {
		case store.WaitingGate:
			fmt.Printf("#%d waiting at the gate: wonder gate %d\n", r.Issue, r.Issue)
		case store.WaitingAnswers:
			fmt.Printf("#%d has questions for you: wonder resume %d\n", r.Issue, r.Issue)
		case store.Paused:
			fmt.Printf("#%d paused (%s): wonder resume %d\n", r.Issue, r.Error, r.Issue)
		case store.Blocked, store.Failed:
			fmt.Printf("#%d %s at %s: %s\n   then: wonder resume %d\n", r.Issue, r.Status, r.Station, r.Error, r.Issue)
		}
	}
}

func runCmd() *cobra.Command {
	var noGate, yes bool
	c := &cobra.Command{
		Use:   "run <issue>...",
		Short: "Run issues through the pipeline, up to max_panes workers in parallel",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			nums, err := issues(args)
			if err != nil {
				return err
			}
			a, err := load()
			if err != nil {
				return err
			}
			return drive(a, nums, !noGate, yes)
		},
	}
	c.Flags().BoolVar(&noGate, "no-gate", false, "leave runs waiting at the gate instead of asking now")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask the spec's questions; take the suggested answers")
	return c
}

func resumeCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "resume <issue>...",
		Short: "Continue runs from their saved state",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			nums, err := issues(args)
			if err != nil {
				return err
			}
			a, err := load()
			if err != nil {
				return err
			}
			for _, n := range nums {
				if _, err := a.store.Load(n); errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("no saved run for #%d; start it with `wonder run %d`", n, n)
				}
			}
			return drive(a, nums, true, yes)
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "take the suggested answers to open questions")
	return c
}

func gateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gate [issue]",
		Short: "Answer the gate for runs waiting on a human",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			nums, err := issues(args)
			if err != nil {
				return err
			}
			if len(nums) == 0 {
				runs, err := a.store.List()
				if err != nil {
					return err
				}
				for _, r := range runs {
					if r.Status == store.WaitingGate {
						nums = append(nums, r.Issue)
					}
				}
				if len(nums) == 0 {
					fmt.Println("nothing is waiting at the gate")
					return nil
				}
			}
			for _, n := range nums {
				r, err := a.store.Load(n)
				if err != nil {
					return fmt.Errorf("#%d: %w", n, err)
				}
				if r.Status != store.WaitingGate {
					return fmt.Errorf("#%d is not waiting at the gate (%s at %s)", n, r.Status, r.Station)
				}
			}
			return drive(a, nums, true, false)
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show runs and their current station",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			runs, err := a.store.List()
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				fmt.Println("no runs yet: wonder run <issue>")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ISSUE\tSTATION\tSTATUS\tPR\tUPDATED\tNOTE")
			for _, r := range runs {
				pr := "—"
				if r.PR != 0 {
					pr = "#" + strconv.Itoa(r.PR)
				}
				note := r.Error
				if r.ResetAt != nil {
					note = "resets " + r.ResetAt.Local().Format("Jan 2 15:04")
				}
				if len(note) > 70 {
					note = note[:70] + "…"
				}
				fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\n", r.Issue, pipeline.Label[r.Station], r.Status, pr, r.Updated.Local().Format(time.DateTime), note)
			}
			return tw.Flush()
		},
	}
}

func (a *app) eval() *eval.Eval {
	return &eval.Eval{Root: a.root, Cfg: a.cfg, Store: a.store, Inv: a.inv, GH: a.gh, Out: os.Stdout}
}

func replayCmd() *cobra.Command {
	var station, prompt string
	var runs, maxDiff int
	var allowAuto bool
	c := &cobra.Command{
		Use:   "replay --station review --prompt <file> [--runs 3]",
		Short: "Evaluate a candidate prompt (or router rules) on past gate verdicts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			switch station {
			case "review":
				if prompt == "" {
					return errors.New("--prompt is required for --station review")
				}
				if !cmd.Flags().Changed("runs") {
					runs = a.cfg.Eval.Runs
				}
				ctx, stop := signalCtx()
				defer stop()
				_, err := a.eval().ReplayReview(ctx, prompt, runs)
				return err
			case "router":
				cand := a.cfg.Router
				if cmd.Flags().Changed("max-diff-lines") {
					cand.MaxDiffLines = maxDiff
				}
				if cmd.Flags().Changed("allow-auto") {
					cand.AllowAuto = allowAuto
				}
				a.eval().ReplayRouter(cand)
				return nil
			}
			return fmt.Errorf("--station must be review or router, not %q", station)
		},
	}
	c.Flags().StringVar(&station, "station", "review", "review or router")
	c.Flags().StringVar(&prompt, "prompt", "", "candidate prompt file (review)")
	c.Flags().IntVar(&runs, "runs", 3, "runs per prompt per example (models are not deterministic)")
	c.Flags().IntVar(&maxDiff, "max-diff-lines", 0, "candidate router.max_diff_lines (router)")
	c.Flags().BoolVar(&allowAuto, "allow-auto", false, "candidate router.allow_auto (router)")
	return c
}

func promoteCmd() *cobra.Command {
	var station, prompt string
	c := &cobra.Command{
		Use:   "promote --station review --prompt <file>",
		Short: "Switch the config to a candidate prompt if its replay passed",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if station != "review" {
				return errors.New("only --station review can be promoted in v1")
			}
			if prompt == "" {
				return errors.New("--prompt is required")
			}
			a, err := load()
			if err != nil {
				return err
			}
			return a.eval().Promote(prompt)
		},
	}
	c.Flags().StringVar(&station, "station", "review", "station whose prompt to promote")
	c.Flags().StringVar(&prompt, "prompt", "", "candidate prompt file")
	return c
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Miss and false-alarm rates per reviewer, router accuracy, babysit counts",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			return a.eval().Stats()
		},
	}
}

func harvestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "harvest",
		Short: "Pull merge and revert facts from GitHub into the verdict log",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			return a.eval().Harvest()
		},
	}
}
