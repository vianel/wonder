// Package config loads and validates wonder.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	wonder "github.com/vianel/wonder"
)

// File is the config file name at the repo root.
const File = "wonder.toml"

type Config struct {
	Factory  Factory  `toml:"factory"`
	Router   Router   `toml:"router"`
	Stations Stations `toml:"stations"`
	Eval     Eval     `toml:"eval"`
	// Pricing gives dollar figures to workers whose CLI reports tokens only
	// (codex). Keyed by worker name; USD per million tokens.
	Pricing map[string]Price `toml:"pricing"`
}

type Price struct {
	Input       float64 `toml:"input"`
	CachedInput float64 `toml:"cached_input"`
	Output      float64 `toml:"output"`
}

// Cost prices token counts.
func (p Price) Cost(input, cached, output int64) float64 {
	return (float64(input)*p.Input + float64(cached)*p.CachedInput + float64(output)*p.Output) / 1e6
}

type Factory struct {
	MaxPanes   int    `toml:"max_panes"`
	BaseBranch string `toml:"base_branch"`
}

type Router struct {
	AllowAuto    bool `toml:"allow_auto"`
	MaxDiffLines int  `toml:"max_diff_lines"`
}

// Worker picks the tool and model for one agent invocation.
type Worker struct {
	Worker string   `toml:"worker"`
	Model  string   `toml:"model,omitempty"`
	Args   []string `toml:"args,omitempty"` // extra CLI args passed to the worker
}

type Station struct {
	Worker
	Prompt      string `toml:"prompt"`
	MaxAttempts int    `toml:"max_attempts,omitempty"`
}

type Review struct {
	Prompt  string   `toml:"prompt"`
	Workers []Worker `toml:"workers"`
}

type Stations struct {
	Spec   Station `toml:"spec"`
	Build  Station `toml:"build"`
	Fix    Station `toml:"fix"`
	Review Review  `toml:"review"`
}

type Eval struct {
	Judge Station `toml:"judge"`
	Runs  int     `toml:"runs"`
}

// Default returns the config for the given installed workers ("claude", "codex",
// "copilot"). With claude and codex it splits the work; with one worker, every
// station uses it. Copilot is only picked when neither of the others is there.
func Default(installed []string) Config {
	has := map[string]bool{}
	for _, w := range installed {
		has[w] = true
	}
	claude := Worker{Worker: "claude", Model: "sonnet"}
	codex := Worker{Worker: "codex"}
	reviewClaude := Worker{Worker: "claude", Model: "opus"}
	writer, reader := codex, claude
	reviewers := []Worker{codex, reviewClaude}
	switch {
	case has["claude"] && !has["codex"]:
		writer, reviewers = claude, []Worker{reviewClaude}
	case has["codex"] && !has["claude"]:
		reader, reviewers = codex, []Worker{codex}
	case has["copilot"] && !has["claude"]:
		copilot := Worker{Worker: "copilot"}
		writer, reader, reviewers = copilot, copilot, []Worker{copilot}
	}
	return Config{
		Factory: Factory{MaxPanes: 3, BaseBranch: "main"},
		Router:  Router{MaxDiffLines: 400},
		Stations: Stations{
			Spec:   Station{Worker: reader, Prompt: ".wonder/prompts/spec.md"},
			Build:  Station{Worker: writer, Prompt: ".wonder/prompts/build.md"},
			Fix:    Station{Worker: writer, Prompt: ".wonder/prompts/fix.md", MaxAttempts: 3},
			Review: Review{Prompt: ".wonder/prompts/review.md", Workers: reviewers},
		},
		Eval: Eval{Judge: Station{Worker: reader, Prompt: ".wonder/prompts/judges/miss.md"}, Runs: 3},
	}
}

// Load reads <root>/wonder.toml on top of the two-worker defaults.
func Load(root string) (*Config, error) {
	c := Default([]string{"claude", "codex"})
	// Arrays are replaced, not merged, so clear the default reviewer list first.
	c.Stations.Review.Workers = nil
	md, err := toml.DecodeFile(filepath.Join(root, File), &c)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no %s here; run `wonder init` first", File)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("%s: unknown keys %v", File, u)
	}
	return &c, c.Validate()
}

var knownWorkers = map[string]bool{"claude": true, "codex": true, "copilot": true}

func (c *Config) Validate() error {
	var errs []string
	if c.Factory.MaxPanes < 1 {
		errs = append(errs, "factory.max_panes must be >= 1")
	}
	if c.Factory.BaseBranch == "" {
		errs = append(errs, "factory.base_branch is empty")
	}
	if c.Router.MaxDiffLines < 1 {
		errs = append(errs, "router.max_diff_lines must be >= 1")
	}
	check := func(name string, w Worker) {
		if !knownWorkers[w.Worker] {
			errs = append(errs, fmt.Sprintf("%s.worker %q is not one of claude, codex, copilot", name, w.Worker))
		}
	}
	for name, s := range map[string]Station{"stations.spec": c.Stations.Spec, "stations.build": c.Stations.Build, "stations.fix": c.Stations.Fix, "eval.judge": c.Eval.Judge} {
		check(name, s.Worker)
		if s.Prompt == "" {
			errs = append(errs, name+".prompt is empty")
		}
	}
	if len(c.Stations.Review.Workers) == 0 {
		errs = append(errs, "stations.review needs at least one [[stations.review.workers]]")
	}
	for i, w := range c.Stations.Review.Workers {
		check(fmt.Sprintf("stations.review.workers[%d]", i), w)
	}
	for w, p := range c.Pricing {
		if !knownWorkers[w] {
			errs = append(errs, fmt.Sprintf("pricing.%s: unknown worker", w))
		}
		if p.Input < 0 || p.CachedInput < 0 || p.Output < 0 {
			errs = append(errs, fmt.Sprintf("pricing.%s: prices can't be negative", w))
		}
	}
	if c.Stations.Fix.MaxAttempts < 1 {
		errs = append(errs, "stations.fix.max_attempts must be >= 1")
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", File, strings.Join(errs, "; "))
	}
	return nil
}

// Prompt reads a prompt file relative to root, falling back to the embedded
// default with the same base path (".wonder/prompts/x.md" -> "prompts/x.md").
func Prompt(root, path string) (string, error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, path)
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		if e, eerr := wonder.Assets.ReadFile(strings.TrimPrefix(filepath.ToSlash(path), ".wonder/")); eerr == nil {
			return string(e), nil
		}
	}
	return string(b), err
}
