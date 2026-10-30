package worker

import (
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
	got, err := Claude{}.Parse([]byte(`{"type":"result","is_error":false,"result":"{\"a\":1}"}`), "")
	if err != nil || got != `{"a":1}` {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := (Claude{}).Parse([]byte(`{"is_error":true,"subtype":"error_max_turns","result":""}`), ""); err == nil {
		t.Fatal("want error")
	}
}
