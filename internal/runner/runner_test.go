package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExec(t *testing.T) {
	r, err := Exec{}.Run(context.Background(), Job{Argv: []string{"sh", "-c", "cat; echo err >&2; exit 3"}, Stdin: "hi"})
	if err != nil || string(r.Stdout) != "hi" || string(r.Stderr) != "err\n" || r.ExitCode != 3 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHerdrRunsInPaneAndClosesIt(t *testing.T) {
	bin, _ := filepath.Abs("../../testdata/bin")
	state := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_HERDR_DIR", state)

	h := &Herdr{}
	for i := 0; i < 2; i++ {
		tmp := t.TempDir()
		r, err := h.Run(context.Background(), Job{Name: "f1-build", Argv: []string{"sh", "-c", "cat; echo \"it's progress\" >&2; exit 4"}, Dir: tmp, Stdin: "in'put", Tmp: tmp})
		if err != nil || string(r.Stdout) != "in'put" || !strings.Contains(string(r.Stderr), "it's progress") || r.ExitCode != 4 {
			t.Fatalf("%+v %v", r, err)
		}
	}
	open, _ := os.ReadFile(filepath.Join(state, "open"))
	if strings.TrimSpace(string(open)) != "" {
		t.Fatalf("panes left open: %q", open)
	}
	calls, _ := os.ReadFile(filepath.Join(state, "calls.log"))
	if !strings.Contains(string(calls), "--no-focus") || !strings.Contains(string(calls), "pane rename w1:p1 f1-build") {
		t.Fatalf("calls: %s", calls)
	}
}
