// Package worker builds the command line for one coding-agent CLI and parses
// its output. Adding an agent means adding one Worker.
package worker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Request is one agent invocation. The prompt is fed on stdin.
type Request struct {
	Prompt string
	Model  string
	Dir    string   // working directory the agent operates in
	Write  bool     // false = read-only
	Args   []string // extra args from config
}

type Worker interface {
	Name() string
	// Command returns argv. tmp is a private scratch dir for this invocation.
	Command(req Request, tmp string) []string
	// Parse returns the agent's final message from its stdout (or files in tmp).
	Parse(stdout []byte, tmp string) (string, error)
}

func Get(name string) (Worker, error) {
	switch name {
	case "claude":
		return Claude{}, nil
	case "codex":
		return Codex{}, nil
	}
	return nil, fmt.Errorf("unknown worker %q", name)
}

// RateLimitError means the CLI refused work because of a usage limit.
type RateLimitError struct {
	Worker string
	Reset  *time.Time // nil when the CLI did not say
	Msg    string
}

func (e *RateLimitError) Error() string {
	if e.Reset != nil {
		return fmt.Sprintf("%s usage limit reached, resets %s", e.Worker, e.Reset.Local().Format(time.RFC1123))
	}
	return fmt.Sprintf("%s usage limit reached: %s", e.Worker, e.Msg)
}

var (
	limitRe = regexp.MustCompile(`(?i)usage limit|rate limit|rate_limit|too many requests|\b429\b|hit your limit|limit reached`)
	epochRe = regexp.MustCompile(`limit reached\|(\d{10})`)
	inRe    = regexp.MustCompile(`(?i)try again in (\d+) (second|minute|hour|day)s?`)
)

// DetectRateLimit inspects the output of a failed invocation. Call it only on
// failure: a successful agent may legitimately talk about rate limits.
func DetectRateLimit(worker string, out []byte, now time.Time) *RateLimitError {
	s := string(out)
	loc := limitRe.FindStringIndex(s)
	if loc == nil {
		return nil
	}
	e := &RateLimitError{Worker: worker, Msg: strings.TrimSpace(firstLine(s[loc[0]:]))}
	if m := epochRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		t := time.Unix(n, 0)
		e.Reset = &t
	} else if m := inRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"second": time.Second, "minute": time.Minute, "hour": time.Hour, "day": 24 * time.Hour}[strings.ToLower(m[2])]
		t := now.Add(time.Duration(n) * unit)
		e.Reset = &t
	}
	return e
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
