package store

import "testing"

func TestSaveLoadList(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, n := range []int{7, 3} {
		if err := s.Save(&Run{Issue: n, Station: "spec", Status: Running}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Load(7)
	if err != nil || r.Issue != 7 || r.V != V {
		t.Fatalf("%+v %v", r, err)
	}
	runs, _ := s.List()
	if len(runs) != 2 || runs[0].Issue != 3 {
		t.Fatalf("list: %+v", runs)
	}
}

func TestVerdictLog(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	vs, err := s.Verdicts()
	if err != nil || len(vs) != 0 {
		t.Fatal("missing log should be empty")
	}
	_ = s.AppendVerdict(Verdict{Issue: 1, GateFirst: "ship"})
	_ = s.AppendVerdict(Verdict{Issue: 2, GateFirst: "changes"})
	vs, _ = s.Verdicts()
	if len(vs) != 2 || vs[1].Issue != 2 || vs[0].V != V {
		t.Fatalf("%+v", vs)
	}
}

func TestLabel(t *testing.T) {
	for _, c := range [][3]string{
		{"approve", "ship", Agree}, {"changes", "changes", Agree},
		{"approve", "changes", Miss}, {"changes", "ship", FalseAlarm},
	} {
		if got := Label(c[0], c[1]); got != c[2] {
			t.Errorf("Label(%s,%s)=%s want %s", c[0], c[1], got, c[2])
		}
	}
}

func TestUsageShort(t *testing.T) {
	priced := Usage{Calls: 1, Input: 400_000, Output: 12_000, CostUSD: 1.844}
	if got := priced.Short(); got != "$1.84 · 412k tok" {
		t.Fatal(got)
	}
	tokensOnly := Usage{Calls: 1, Input: 1_500_000, UnpricedTokens: 1_500_000}
	if got := tokensOnly.Short(); got != "1.5M tok" {
		t.Fatal(got)
	}
	if got := priced.Plus(tokensOnly).Short(); got != "$1.84 + 1.5M unpriced tok" {
		t.Fatal(got)
	}
	if got := (Usage{}).Short(); got != "—" {
		t.Fatal(got)
	}
}
