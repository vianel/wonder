// Package gate asks the human for a ship/changes verdict: a Bubble Tea TUI on
// a terminal, a plain prompt otherwise.
package gate

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/store"
)

// Item is one finding, with every reviewer that raised something in its area.
type Item struct {
	store.Finding
	By []string
}

// Merge groups findings by area. Areas flagged by more than one reviewer come
// first (agreed), then areas only one reviewer flagged (disagreements).
// Known limit: "same area" stands in for "same issue"; fuzzy matching if it misleads.
func Merge(reviews map[string]store.Review) (agreed, solo []Item) {
	byArea := map[string][]Item{}
	var order []string
	names := make([]string, 0, len(reviews))
	for n := range reviews {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, f := range reviews[n].Findings {
			a := strings.ToLower(strings.TrimSpace(f.Area))
			if _, ok := byArea[a]; !ok {
				order = append(order, a)
			}
			byArea[a] = append(byArea[a], Item{Finding: f, By: []string{n}})
		}
	}
	for _, a := range order {
		items := byArea[a]
		reviewers := map[string]bool{}
		for _, it := range items {
			reviewers[it.By[0]] = true
		}
		if len(reviewers) > 1 {
			agreed = append(agreed, items...)
		} else {
			solo = append(solo, items...)
		}
	}
	return agreed, solo
}

var (
	accent = lipgloss.Color("212")
	bold   = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	good   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	bad    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	key    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	box    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)
	sev    = map[string]lipgloss.Style{
		"high":   bad.Bold(true),
		"medium": lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"low":    dim,
	}
)

// maxCriteria keeps long specs from pushing the actions off screen.
const maxCriteria = 6

// hang wraps s to width, indenting every line after the first by indent.
func hang(s string, indent, width int) string {
	w := lipgloss.NewStyle().Width(max(width-indent, 20)).Render(s)
	return strings.ReplaceAll(w, "\n", "\n"+strings.Repeat(" ", indent))
}

func sortedNames(m map[string]store.Review) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// headline is the one-line answer to "what did the reviewers think?".
func headline(r *store.Run) string {
	var approved, changes []string
	findings := 0
	for _, n := range sortedNames(r.Reviews) {
		findings += len(r.Reviews[n].Findings)
		if r.Reviews[n].Verdict == "changes" {
			changes = append(changes, n)
		} else {
			approved = append(approved, n)
		}
	}
	if len(changes) > 0 {
		s := bad.Render("✗ " + strings.Join(changes, " and ") + " asked for changes")
		if len(approved) > 0 {
			s += "; " + strings.Join(approved, " and ") + " approved"
		}
		return s + "."
	}
	who := fmt.Sprintf("All %d reviewers", len(approved))
	switch len(approved) {
	case 1:
		who = approved[0]
	case 2:
		who = "Both reviewers"
	}
	tail := "no findings."
	if findings > 0 {
		tail = fmt.Sprintf("%d finding(s) below.", findings)
	}
	return good.Render("✓ "+who+" approved") + ", " + tail
}

// why says in plain words why the PR is waiting for a human.
func why(r *store.Run) string {
	if r.Router == nil {
		return ""
	}
	if r.Router.RulesRoute == "auto" {
		return "Waiting for you because auto-merge is off (router.allow_auto in wonder.toml)."
	}
	var rest []string // reviewer verdicts are already in the headline
	for _, x := range strings.Split(r.Router.Reason, "; ") {
		if !strings.HasSuffix(x, "asked for changes") {
			rest = append(rest, x)
		}
	}
	if len(rest) == 0 {
		return ""
	}
	return "Waiting for you because: " + strings.Join(rest, "; ") + "."
}

// spentLine says what the run has cost so far, then per station.
func spentLine(r *store.Run) string {
	t := store.TotalUsage(r.Usage)
	s := fmt.Sprintf("Spent so far: %s (API prices) in %.0f min of agent time.", t.Short(), t.Seconds/60)
	by := map[string]store.Usage{}
	for k, u := range r.Usage {
		st := strings.SplitN(k, ".", 2)[0]
		by[st] = by[st].Plus(u)
	}
	var parts []string
	for _, st := range []string{"spec", "build", "fix", "review"} {
		if u, ok := by[st]; ok {
			parts = append(parts, st+" "+u.Short())
		}
	}
	return s + " " + strings.Join(parts, " · ")
}

func ciText(ci string) string {
	switch ci {
	case "pass":
		return good.Render("CI ✓ passed")
	case "fail":
		return bad.Render("CI ✗ failing")
	case "none":
		return "no CI checks"
	}
	return "CI —"
}

// Summary renders what the human needs to decide, wrapped to width.
func Summary(r *store.Run, width int) string {
	if width <= 0 {
		width = 80
	}
	width = min(width, 100)
	var b strings.Builder

	info := []string{fmt.Sprintf("PR #%d", r.PR)}
	st := r.DiffStats
	if r.Worktree != "" && r.BaseSHA != "" { // fresh numbers: fixes may have changed the PR
		if fresh, err := gitx.DiffStats(r.Worktree, r.BaseSHA, "HEAD"); err == nil {
			st = &fresh
		}
	}
	if st != nil {
		info = append(info, fmt.Sprintf("%d files · +%d −%d", st.Files, st.Added, st.Deleted))
	}
	info = append(info, ciText(r.CI))
	card := key.Render("🎫 Golden Ticket") + "\n" +
		bold.Render(hang(fmt.Sprintf("#%d %s", r.Issue, r.Title), 0, width-4)) + "\n" +
		strings.Join(info, " · ")
	fmt.Fprintln(&b, box.Render(card))

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+hang(headline(r), 2, width))
	if w := why(r); w != "" {
		fmt.Fprintln(&b, "  "+dim.Render(hang(w, 2, width)))
	}
	if len(r.Usage) > 0 {
		fmt.Fprintln(&b, "  "+dim.Render(hang(spentLine(r), 2, width)))
	}
	if r.GateRounds > 0 {
		fmt.Fprintln(&b, "  "+hang(fmt.Sprintf("Round %d. Last time you asked: %q. The fix agent has changed the PR since; the reviews below are from round 1.", r.GateRounds+1, r.GateNotes), 2, width))
	}

	if r.Spec != nil && len(r.Spec.AcceptanceCriteria) > 0 {
		cs := r.Spec.AcceptanceCriteria
		fmt.Fprintf(&b, "\n  %s\n", bold.Render(fmt.Sprintf("What it should do (%d)", len(cs))))
		shown := cs
		if len(cs) > maxCriteria {
			shown = cs[:maxCriteria-1]
		}
		for _, c := range shown {
			fmt.Fprintf(&b, "    • %s\n", hang(c, 6, width))
		}
		if len(shown) < len(cs) {
			fmt.Fprintf(&b, "    %s\n", dim.Render(fmt.Sprintf("… and %d more (press o to see the PR)", len(cs)-len(shown))))
		}
	}

	if r.Spec != nil && len(r.Spec.Questions) > 0 {
		fmt.Fprintf(&b, "\n  %s\n", bold.Render("Answered before the build"))
		for _, q := range r.Spec.Questions {
			by := ""
			if q.AnsweredBy == "suggested" {
				by = dim.Render(" (suggested; nobody was asked)")
			}
			fmt.Fprintf(&b, "    • %s\n", hang(q.Question+" → "+q.Answer+by, 6, width))
		}
	}

	if len(r.Reviews) > 0 {
		fmt.Fprintf(&b, "\n  %s\n", bold.Render("Reviewers"))
		for _, n := range sortedNames(r.Reviews) {
			rv := r.Reviews[n]
			mark := good.Render("✓ approve")
			if rv.Verdict == "changes" {
				mark = bad.Render("✗ changes")
			}
			fmt.Fprintf(&b, "    %-7s %s  %s\n", n, mark, hang(rv.Summary, 23, width))
		}
	}

	agreed, solo := Merge(r.Reviews)
	section := func(name string, items []Item) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n  %s\n", bold.Render(name))
		for _, it := range items {
			s, ok := sev[it.Severity]
			if !ok {
				s = dim
			}
			line := fmt.Sprintf("%s — %s %s", it.Area, it.Issue, dim.Render("("+strings.Join(it.By, ", ")+")"))
			fmt.Fprintf(&b, "    %s %s\n", s.Render(fmt.Sprintf("%-6s", it.Severity)), hang(line, 11, width))
		}
	}
	section("Both reviewers flagged", agreed)
	section("Flagged by one reviewer", solo)
	return b.String()
}

// Interactive reports whether stdin and stdout are terminals.
func Interactive() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

// Ask asks about one run with the TUI on a terminal, a plain prompt otherwise.
func Ask(r *store.Run) (ship bool, notes string, ok bool) {
	if Interactive() {
		return TUI(r)
	}
	if stdin == nil {
		stdin = bufio.NewScanner(os.Stdin)
	}
	return Plain(r, stdin, os.Stdout)
}

// Questions asks the open spec questions of all runs in one go, on stdin.
func Questions(runs []*store.Run) bool {
	if stdin == nil {
		stdin = bufio.NewScanner(os.Stdin)
	}
	width := 80
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		width = w
	}
	return AskQuestions(runs, stdin, os.Stdout, width)
}

// AskQuestions asks every unanswered question; an empty line accepts the
// suggestion. It returns false if input ends before all are answered.
func AskQuestions(runs []*store.Run, sc *bufio.Scanner, out io.Writer, width int) bool {
	width = min(width, 100) - 1
	n := 0
	for _, r := range runs {
		n += len(r.Spec.Unanswered())
	}
	fmt.Fprintf(out, "\a\n%s\n%s\n", hang(key.Render("🏭 Planning Room ")+
		bold.Render(fmt.Sprintf("has %d question(s) before building. Answer now, then you can walk away.", n)), 0, width),
		dim.Render("Press enter to take the suggested answer."))
	for _, r := range runs {
		fmt.Fprintf(out, "\n  %s\n", bold.Render(fmt.Sprintf("#%d %s", r.Issue, r.Title)))
		for k, i := range r.Spec.Unanswered() {
			q := &r.Spec.Questions[i]
			fmt.Fprintf(out, "  %d. %s\n     %s\n     › ", k+1, hang(q.Question, 5, width), hang(dim.Render("suggested: ")+q.Suggested, 5, width))
			if !sc.Scan() {
				fmt.Fprintln(out)
				return false
			}
			q.Answer, q.AnsweredBy = q.Suggested, "you"
			if a := strings.TrimSpace(sc.Text()); a != "" {
				q.Answer = a
			}
		}
	}
	fmt.Fprintf(out, "\n%s\n\n", good.Render(hang("✓ Got it. Nothing else needs you until the golden ticket. Go get a chocolate. 🍫", 0, width)))
	return true
}

// stdin is shared by every plain gate prompt in the process, so lines piped
// in for later rounds are not lost in a discarded buffer.
var stdin *bufio.Scanner

// Plain is the line-based gate: s = ship, c = changes (next line is notes),
// anything else skips.
func Plain(r *store.Run, sc *bufio.Scanner, out io.Writer) (bool, string, bool) {
	fmt.Fprint(out, "\a"+Summary(r, 80)+"\nMerge this PR? [s] ship it · [c] request changes · anything else: decide later > ")
	if !sc.Scan() {
		return false, "", false
	}
	switch strings.TrimSpace(sc.Text()) {
	case "s":
		return true, "", true
	case "c":
		fmt.Fprint(out, "What should change? (one line, sent to the fix agent) > ")
		if sc.Scan() && strings.TrimSpace(sc.Text()) != "" {
			return false, strings.TrimSpace(sc.Text()), true
		}
	}
	return false, "", false
}

type model struct {
	run     *store.Run
	notes   textinput.Model
	width   int
	writing bool
	ship    bool
	done    bool
	quit    bool
	msg     string
}

// TUI runs the Bubble Tea gate. Keys: s ship, c changes, o open PR, d diff, q skip.
func TUI(r *store.Run) (bool, string, bool) {
	// The card is printed as plain output so it scrolls like any text even when
	// it is taller than the terminal; Bubble Tea only draws the actions below it.
	width := 80
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		width = w
	}
	fmt.Print("\a" + Summary(r, width) + "\n") // the bell: a run reached the gate
	m, err := tea.NewProgram(model{run: r, notes: newInput()}).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gate:", err)
		return false, "", false
	}
	res := m.(model)
	return res.ship, strings.TrimSpace(res.notes.Value()), res.done
}

func newInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "e.g. also handle empty input, and add a test for it"
	ti.CharLimit = 2000
	ti.Width = 80
	return ti
}

func (m model) Init() tea.Cmd { return nil }

type execDone struct{ err error }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.notes.Width = max(min(msg.Width, 100)-8, 20)
		return m, nil
	case execDone:
		if msg.err != nil {
			m.msg = msg.err.Error()
		}
		return m, nil
	case tea.KeyMsg:
		if m.writing {
			switch msg.Type {
			case tea.KeyEsc:
				m.writing = false
				m.notes.Blur()
				return m, nil
			case tea.KeyEnter:
				if strings.TrimSpace(m.notes.Value()) == "" {
					m.msg = "notes can't be empty"
					return m, nil
				}
				m.done, m.quit = true, true
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.notes, cmd = m.notes.Update(msg)
			return m, cmd
		}
		pr := strconv.Itoa(m.run.PR)
		switch msg.String() {
		case "s":
			m.ship, m.done, m.quit = true, true, true
			return m, tea.Quit
		case "c":
			m.writing = true
			return m, m.notes.Focus()
		case "o":
			return m, tea.ExecProcess(exec.Command("gh", "pr", "view", pr, "--web"), func(err error) tea.Msg { return execDone{err} })
		case "d":
			return m, tea.ExecProcess(exec.Command("gh", "pr", "diff", pr), func(err error) tea.Msg { return execDone{err} })
		case "q", "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	r := m.run
	// After a decision, leave one plain line in the scrollback.
	if m.quit {
		switch {
		case m.ship:
			return fmt.Sprintf("🎫 #%d: shipping, merging PR #%d…\n", r.Issue, r.PR)
		case m.done:
			return fmt.Sprintf("↺ #%d: your notes went to the fix agent. It comes back here after CI passes.\n", r.Issue)
		}
		return fmt.Sprintf("#%d is still waiting. Run `wonder gate %d` when you're ready.\n", r.Issue, r.Issue)
	}
	v := ""
	if m.writing {
		v += "  " + bold.Render(fmt.Sprintf("Request changes on PR #%d", r.PR)) + "\n"
		v += "  " + dim.Render("Tell the fix agent what to change. It gets this text as its instructions.") + "\n\n"
		v += "  " + m.notes.View() + "\n\n"
		v += fmt.Sprintf("   %s send    %s back\n", key.Render("enter"), key.Render("esc"))
	} else {
		v += "  " + bold.Render("Merge this PR?") + "\n\n"
		v += fmt.Sprintf("   %s  Ship it — merge PR #%d and close issue #%d\n", key.Render("s"), r.PR, r.Issue)
		v += fmt.Sprintf("   %s  Request changes — the fix agent makes them, CI reruns,\n      then it comes back here\n", key.Render("c"))
		v += fmt.Sprintf("   %s  View diff    %s  Open in browser\n", key.Render("d"), key.Render("o"))
		v += fmt.Sprintf("   %s  Decide later (wonder gate %d)\n", key.Render("q"), r.Issue)
	}
	if m.msg != "" {
		v += "\n  " + bad.Render(m.msg) + "\n"
	}
	return v
}
