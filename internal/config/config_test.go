package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSingleWorker(t *testing.T) {
	c := Default([]string{"claude"})
	if c.Stations.Build.Worker.Worker != "claude" || len(c.Stations.Review.Workers) != 1 {
		t.Fatalf("claude-only config uses codex: %+v", c.Stations)
	}
	c = Default([]string{"codex"})
	if c.Stations.Spec.Worker.Worker != "codex" || c.Stations.Review.Workers[0].Worker != "codex" {
		t.Fatalf("codex-only config uses claude: %+v", c.Stations)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, File), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[router]\nmax_diff_lines = 50\n[[stations.review.workers]]\nworker = \"claude\"\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Router.MaxDiffLines != 50 || c.Factory.MaxPanes != 3 || len(c.Stations.Review.Workers) != 1 {
		t.Fatalf("bad merge: %+v", c)
	}
	write("[stations.build]\nworker = \"gemini\"\n[[stations.review.workers]]\nworker = \"claude\"\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "gemini") {
		t.Fatalf("want unknown worker error, got %v", err)
	}
	write("[pricing.codex]\ninput = 1.25\noutput = 10\n[[stations.review.workers]]\nworker = \"claude\"\n")
	if c, err := Load(dir); err != nil || c.Pricing["codex"].Output != 10 {
		t.Fatalf("pricing: %+v %v", c, err)
	}
	write("[pricing.gemini]\ninput = 1\n[[stations.review.workers]]\nworker = \"claude\"\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("want unknown pricing worker error")
	}
	write("[factory]\nmax_pane = 2\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("want unknown key error, got %v", err)
	}
}

func TestPromptFallsBackToEmbedded(t *testing.T) {
	p, err := Prompt(t.TempDir(), ".wonder/prompts/spec.md")
	if err != nil || !strings.Contains(p, "spec") {
		t.Fatalf("got %q, %v", p, err)
	}
}
