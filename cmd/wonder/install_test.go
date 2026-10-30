package main

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeInstaller has only the tools in onPath; run "installs" by adding the
// tool named in the command to onPath and records every command.
func fakeInstaller(answers string, onPath ...string) (*installer, *[]string, *bytes.Buffer) {
	path := map[string]bool{}
	for _, p := range onPath {
		path[p] = true
	}
	var ran []string
	var out bytes.Buffer
	i := &installer{
		in: bufio.NewScanner(strings.NewReader(answers)), out: &out, os: "darwin",
		look: func(n string) (string, error) {
			if path[n] {
				return "/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		run: func(name string, argv ...string) error {
			cmd := strings.Join(append([]string{name}, argv...), " ")
			ran = append(ran, cmd)
			for _, t := range tools {
				if strings.Contains(cmd, t.methods[0].cmd) || (len(t.methods) > 1 && strings.Contains(cmd, t.methods[1].cmd)) {
					path[t.name] = true
				}
			}
			return nil
		},
	}
	return i, &ran, &out
}

func TestInstallerAsksBeforeEachTool(t *testing.T) {
	// claude: yes, then decline login; codex: no.
	i, ran, out := fakeInstaller("y\nn\nn\n", "curl", "brew")
	got := i.offer("claude", "codex")
	if len(got) != 1 || got[0] != "claude" {
		t.Fatalf("installed %v\n%s", got, out)
	}
	if len(*ran) != 1 || !strings.Contains((*ran)[0], "https://claude.ai/install.sh") {
		t.Fatalf("ran %v", *ran)
	}
	if !strings.Contains(out.String(), "brew install --cask codex") || !strings.Contains(out.String(), "skipped") {
		t.Fatalf("codex must be shown and skipped:\n%s", out)
	}
}

func TestInstallerPicksAvailableMethodAndLogsIn(t *testing.T) {
	i, ran, _ := fakeInstaller("y\ny\n", "npm") // no brew: codex via npm
	if got := i.offer("codex"); len(got) != 1 {
		t.Fatalf("installed %v", got)
	}
	if strings.Join(*ran, "|") != "sh -c npm install -g @openai/codex|codex login" {
		t.Fatalf("ran %v", *ran)
	}
}

func TestInstallerDefaultsToNoAndSkipsWhatExists(t *testing.T) {
	i, ran, out := fakeInstaller("\n", "brew", "gh")
	i.offer("gh", "herdr")
	if len(*ran) != 0 || strings.Contains(out.String(), "gh is not installed") {
		t.Fatalf("ran %v\n%s", *ran, out)
	}
	i, _, out = fakeInstaller("", "brew")
	i.os = "windows"
	i.offer("herdr")
	if !strings.Contains(out.String(), "Install it yourself") {
		t.Fatalf("windows should get manual instructions:\n%s", out)
	}
}
