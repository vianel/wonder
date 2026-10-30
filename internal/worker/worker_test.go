package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectRateLimit(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	if e := DetectRateLimit("claude", []byte(`{"is_error":true,"result":"Claude AI usage limit reached|1700000000"}`), now); e == nil || e.Reset.Unix() != 1700000000 {
		t.Fatalf("claude epoch: %+v", e)
	}
	if e := DetectRateLimit("codex", []byte("ERROR: You've hit your usage limit. Try again in 2 hours."), now); e == nil || !e.Reset.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("codex: %+v", e)
	}
	if e := DetectRateLimit("codex", []byte("panic: nil map"), now); e != nil {
		t.Fatalf("false positive: %+v", e)
	}
}

func TestClaudeParse(t *testing.T) {
	got, u, err := Claude{}.Parse([]byte(`{"type":"result","is_error":false,"result":"{\"a\":1}","total_cost_usd":0.09,
		"usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":50,"output_tokens":9}}`), "")
	if err != nil || got != `{"a":1}` {
		t.Fatalf("%q %v", got, err)
	}
	if (u != Usage{Input: 102, CachedInput: 50, Output: 9, CostUSD: 0.09, HasCost: true}) {
		t.Fatalf("usage %+v", u)
	}
	if _, _, err := (Claude{}).Parse([]byte(`{"is_error":true,"subtype":"error_max_turns","result":""}`), ""); err == nil {
		t.Fatal("want error")
	}
}

func TestCodexParseSumsTurns(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "last.txt"), []byte("done"), 0o644)
	out := `{"type":"thread.started"}
{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":50,"reasoning_output_tokens":20}}
not json
{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":0,"output_tokens":10}}`
	text, u, err := Codex{}.Parse([]byte(out), tmp)
	if err != nil || text != "done" || (u != Usage{Input: 900, CachedInput: 600, Output: 60}) {
		t.Fatalf("%q %+v %v", text, u, err)
	}
}
