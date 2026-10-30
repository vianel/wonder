package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vianel/wonder/internal/config"
	"github.com/vianel/wonder/internal/gitx"
	"github.com/vianel/wonder/internal/store"
)

// Route applies the hard router rules. There is no router agent in v1, so
// RuleOverrode is always false; the field exists so the format won't change.
func Route(cfg config.Router, spec store.Spec, reviews map[string]store.Review, s gitx.Stats) store.Route {
	var reasons []string
	names := make([]string, 0, len(reviews))
	for n := range reviews {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if reviews[n].Verdict == "changes" {
			reasons = append(reasons, n+" asked for changes")
		}
	}
	if s.Lines() > cfg.MaxDiffLines {
		reasons = append(reasons, fmt.Sprintf("diff is %d lines (max %d)", s.Lines(), cfg.MaxDiffLines))
	}
	if s.TestsDeleted {
		reasons = append(reasons, "tests deleted")
	}
	if s.TestsWeakened {
		reasons = append(reasons, "tests weakened")
	}
	if spec.Risk == "high" {
		reasons = append(reasons, "spec risk is high")
	}
	r := store.Route{Route: "auto", RulesRoute: "auto", Reason: "reviewers approved, small diff, tests intact, risk not high"}
	if len(reasons) > 0 {
		r.Route, r.RulesRoute, r.Reason = "gate", "gate", strings.Join(reasons, "; ")
	} else if !cfg.AllowAuto {
		r.Route, r.Reason = "gate", "all rules pass, but router.allow_auto is off"
	}
	return r
}
