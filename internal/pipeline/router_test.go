package pipeline

import (
	"strings"
	"testing"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/store"
)

func TestRoute(t *testing.T) {
	ok := map[string]store.Review{"codex": {Verdict: "approve"}, "claude": {Verdict: "approve"}}
	low := store.Spec{Risk: "low"}
	small := gitx.Stats{Added: 10}
	auto := config.Router{AllowAuto: true, MaxDiffLines: 400}

	if r := Route(auto, low, ok, small); r.Route != "auto" {
		t.Fatalf("clean PR should auto-merge: %+v", r)
	}
	if r := Route(config.Router{MaxDiffLines: 400}, low, ok, small); r.Route != "gate" || r.RulesRoute != "auto" {
		t.Fatalf("allow_auto off must gate: %+v", r)
	}
	cases := []struct {
		spec    store.Spec
		reviews map[string]store.Review
		stats   gitx.Stats
		want    string
	}{
		{low, map[string]store.Review{"codex": {Verdict: "changes"}, "claude": {Verdict: "approve"}}, small, "codex asked for changes"},
		{low, ok, gitx.Stats{Added: 300, Deleted: 101}, "diff is 401 lines"},
		{low, ok, gitx.Stats{Added: 1, TestsDeleted: true}, "tests deleted"},
		{low, ok, gitx.Stats{Added: 1, TestsWeakened: true}, "tests weakened"},
		{store.Spec{Risk: "high"}, ok, small, "risk is high"},
	}
	for _, c := range cases {
		r := Route(auto, c.spec, c.reviews, c.stats)
		if r.Route != "gate" || !strings.Contains(r.Reason, c.want) || r.RuleOverrode {
			t.Errorf("want gate %q, got %+v", c.want, r)
		}
	}
}
