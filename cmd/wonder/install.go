package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// method is one way to install a tool; it applies when needs is on PATH.
type method struct{ needs, cmd string }

type tool struct {
	name    string
	why     string
	methods []method
	login   []string // argv of the CLI's own login command, if any
	manual  string   // shown when no method applies
}

// tools wonder can offer to install, in the order it offers them. Commands are
// the vendors' documented ones; wonder always shows them and asks first.
var tools = []tool{
	{name: "gh", why: "required: wonder opens and merges PRs with it",
		methods: []method{{"brew", "brew install gh"}},
		login:   []string{"gh", "auth", "login"},
		manual:  "see https://github.com/cli/cli#installation (Linux packages need sudo)"},
	{name: "claude", why: "a worker: Claude Code",
		methods: []method{{"curl", "curl -fsSL https://claude.ai/install.sh | bash"}, {"npm", "npm install -g @anthropic-ai/claude-code"}},
		login:   []string{"claude", "auth", "login"},
		manual:  "see https://code.claude.com/docs/en/setup"},
	{name: "codex", why: "a worker: OpenAI Codex",
		methods: []method{{"brew", "brew install --cask codex"}, {"npm", "npm install -g @openai/codex"}},
		login:   []string{"codex", "login"},
		manual:  "see https://github.com/openai/codex"},
	{name: "herdr", why: "optional: watch each worker in its own pane",
		methods: []method{{"brew", "brew install herdr"}, {"curl", "curl -fsSL https://herdr.dev/install.sh | sh"}},
		manual:  "see https://herdr.dev"},
}

// installer offers to install missing tools. Every command is shown and
// needs an explicit "y"; the default is no.
type installer struct {
	in   *bufio.Scanner
	out  io.Writer
	look func(string) (string, error)
	// run executes a command attached to the terminal, so installers and
	// logins can prompt (browser logins, sudo).
	run func(name string, argv ...string) error
	os  string
	// dirs are where installers drop binaries that may not be on PATH yet.
	dirs []string
}

func newInstaller() installer {
	home, _ := os.UserHomeDir()
	return installer{
		in: bufio.NewScanner(os.Stdin), out: os.Stdout, look: exec.LookPath, os: runtime.GOOS,
		dirs: []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin"},
		run: func(name string, argv ...string) error {
			cmd := exec.Command(name, argv...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		},
	}
}

func (i installer) yes(q string) bool {
	fmt.Fprintf(i.out, "%s [y/N] ", q)
	return i.in.Scan() && strings.EqualFold(strings.TrimSpace(i.in.Text()), "y")
}

// pick returns the first install command whose prerequisite is on PATH.
func (i installer) pick(t tool) string {
	if i.os == "windows" {
		return ""
	}
	for _, m := range t.methods {
		if _, err := i.look(m.needs); err == nil {
			return m.cmd
		}
	}
	return ""
}

// Missing returns the names of tools (from names) that are not on PATH.
func (i installer) missing(names ...string) []string {
	var out []string
	for _, n := range names {
		if _, err := i.look(n); err != nil {
			out = append(out, n)
		}
	}
	return out
}

// offer walks through each named missing tool and installs the ones the user
// approves. It returns the tools that are on PATH afterwards.
func (i installer) offer(names ...string) (installed []string) {
	for _, t := range tools {
		if !contains(names, t.name) {
			continue
		}
		if _, err := i.look(t.name); err == nil {
			continue
		}
		cmd := i.pick(t)
		fmt.Fprintf(i.out, "\n%s is not installed (%s).\n", t.name, t.why)
		if cmd == "" {
			fmt.Fprintf(i.out, "  Install it yourself: %s\n", t.manual)
			continue
		}
		fmt.Fprintf(i.out, "  wonder can run:  %s\n", cmd)
		if !i.yes("  Run it now?") {
			fmt.Fprintln(i.out, "  skipped")
			continue
		}
		if err := i.run("sh", "-c", cmd); err != nil {
			fmt.Fprintf(i.out, "  ✗ install failed: %v\n", err)
			continue
		}
		if !i.found(t.name) {
			fmt.Fprintf(i.out, "  ! installed, but %s is not on your PATH yet; open a new terminal\n", t.name)
			continue
		}
		fmt.Fprintf(i.out, "  ✓ %s installed\n", t.name)
		installed = append(installed, t.name)
		if len(t.login) > 0 && i.yes(fmt.Sprintf("  Log in now (%s)?", strings.Join(t.login, " "))) {
			if err := i.run(t.login[0], t.login[1:]...); err != nil {
				fmt.Fprintf(i.out, "  ✗ login failed: %v (run `%s` later)\n", err, strings.Join(t.login, " "))
			}
		}
	}
	return installed
}

// found looks for name on PATH, then in the places installers put binaries
// that may not be on PATH yet; a hit there is added to PATH for this process.
func (i installer) found(name string) bool {
	if _, err := i.look(name); err == nil {
		return true
	}
	for _, dir := range i.dirs {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			fmt.Fprintf(i.out, "  ! %s is in %s, which is not on your PATH; add it to your shell profile\n", name, dir)
			return true
		}
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
