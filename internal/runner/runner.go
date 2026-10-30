// Package runner decides where a worker process runs: a plain subprocess or a
// herdr pane.
package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Job is one worker process.
type Job struct {
	Name  string // human label, also the herdr pane name (e.g. f42-build)
	Argv  []string
	Dir   string
	Stdin string
	Tmp   string // scratch dir owned by this job
}

type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner starts a job and waits for it. A non-zero exit is not an error;
// errors mean the job could not be run at all.
type Runner interface {
	Run(ctx context.Context, j Job) (Result, error)
}

// Exec runs jobs as direct subprocesses.
type Exec struct{}

func (Exec) Run(ctx context.Context, j Job) (Result, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, j.Argv[0], j.Argv[1:]...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = j.Dir, strings.NewReader(j.Stdin), &out, &errb
	err := cmd.Run()
	r := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode, err = ee.ExitCode(), nil
	}
	return r, err
}

// Limit caps how many jobs run at once across all issues (max_panes).
type Limit struct {
	R   Runner
	sem chan struct{}
}

func NewLimit(r Runner, n int) *Limit { return &Limit{R: r, sem: make(chan struct{}, n)} }

func (l *Limit) Run(ctx context.Context, j Job) (Result, error) {
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	defer func() { <-l.sem }()
	return l.R.Run(ctx, j)
}

// InHerdr reports whether we run inside a herdr pane.
func InHerdr() bool { return os.Getenv("HERDR_ENV") == "1" }
