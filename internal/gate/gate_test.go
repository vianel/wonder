package gate

import (
	"bufio"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vianel/wonder/internal/store"
)

func TestMerge(t *testing.T) {
	agreed, solo := Merge(map[string]store.Review{
		"codex":  {Findings: []store.Finding{{Area: "auth.go", Issue: "a"}, {Area: "db.go", Issue: "b"}}},
		"claude": {Findings: []store.Finding{{Area: "Auth.go ", Issue: "c"}}},
	})
	if len(agreed) != 2 || len(solo) != 1 || solo[0].Area != "db.go" {
		t.Fatalf("agreed=%+v solo=%+v", agreed, solo)
	}
}

func TestPlain(t *testing.T) {
	r := &store.Run{Issue: 1}
	if ship, _, ok := Plain(r, lines("s\n"), io.Discard); !ship || !ok {
		t.Fatal("s should ship")
	}
	if ship, notes, ok := Plain(r, lines("c\nrename it\n"), io.Discard); ship || !ok || notes != "rename it" {
		t.Fatalf("c: %v %q %v", ship, notes, ok)
	}
	if _, _, ok := Plain(r, lines("c\n\n"), io.Discard); ok {
		t.Fatal("changes without notes must not count")
	}
	if _, _, ok := Plain(r, lines(""), io.Discard); ok {
		t.Fatal("EOF skips")
	}
}

func lines(s string) *bufio.Scanner { return bufio.NewScanner(strings.NewReader(s)) }

func TestPlainSharesInputAcrossRounds(t *testing.T) {
	sc := lines("c\nadd docs\ns\n")
	r := &store.Run{Issue: 1}
	if ship, notes, ok := Plain(r, sc, io.Discard); ship || notes != "add docs" || !ok {
		t.Fatal("round 1")
	}
	if ship, _, ok := Plain(r, sc, io.Discard); !ship || !ok {
		t.Fatal("round 2 lost its input")
	}
}

func press(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestTUIKeys(t *testing.T) {
	m := model{run: &store.Run{Issue: 1}}
	got, _ := m.Update(press("s"))
	if mm := got.(model); !mm.ship || !mm.done {
		t.Fatal("s should ship")
	}
	m = model{run: &store.Run{Issue: 1}, notes: newInput()}
	got, _ = m.Update(press("c"))
	m = got.(model)
	got, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = got.(model); m.done {
		t.Fatal("empty notes must not submit")
	}
	for _, r := range "fix it" {
		got, _ = m.Update(press(string(r)))
		m = got.(model)
	}
	got, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = got.(model); !m.done || m.ship || m.notes.Value() != "fix it" {
		t.Fatalf("changes: %+v %q", m, m.notes.Value())
	}
}

func TestAskQuestions(t *testing.T) {
	mk := func(n int) *store.Run {
		return &store.Run{Issue: n, Spec: &store.Spec{Questions: []store.Question{{Question: "Tabs?", Suggested: "spaces"}}}}
	}
	a, b := mk(1), mk(2)
	if !AskQuestions([]*store.Run{a, b}, lines("tabs\n\n"), io.Discard, 80) {
		t.Fatal("want ok")
	}
	if q := a.Spec.Questions[0]; q.Answer != "tabs" || q.AnsweredBy != "you" {
		t.Fatalf("a: %+v", q)
	}
	if q := b.Spec.Questions[0]; q.Answer != "spaces" || q.AnsweredBy != "you" {
		t.Fatalf("enter must accept the suggestion: %+v", q)
	}
	c := mk(3)
	if AskQuestions([]*store.Run{c}, lines(""), io.Discard, 80) || len(c.Spec.Unanswered()) != 1 {
		t.Fatal("EOF must leave the question open")
	}
}
