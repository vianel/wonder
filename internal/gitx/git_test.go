package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffStats(t *testing.T) {
	for _, k := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+k+"_NAME", "t")
		t.Setenv("GIT_"+k+"_EMAIL", "t@example.com")
	}
	d := t.TempDir()
	write := func(p, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(d, p)), 0o755)
		_ = os.WriteFile(filepath.Join(d, p), []byte(s), 0o644)
	}
	must := func(_ string, err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Run(d, "init", "-q"))
	write("a_test.go", "1\n2\n3\n")
	write("tests/b.py", "x\n")
	must(Run(d, "add", "-A"))
	must(Run(d, "commit", "-q", "-m", "a"))
	base, _ := HeadSHA(d)

	write("a.go", "new\n")
	_, _ = CommitAll(d, "add code")
	s, err := DiffStats(d, base, "HEAD")
	if err != nil || s.Added != 1 || s.TestsDeleted || s.TestsWeakened {
		t.Fatalf("%+v %v", s, err)
	}

	write("a_test.go", "1\n")
	_ = os.Remove(filepath.Join(d, "tests/b.py"))
	_, _ = CommitAll(d, "weaken")
	if s, _ = DiffStats(d, base, "HEAD"); !s.TestsDeleted || !s.TestsWeakened {
		t.Fatalf("%+v", s)
	}
	if committed, _ := CommitAll(d, "nothing"); committed {
		t.Fatal("empty commit")
	}
	for p, want := range map[string]bool{"x_test.go": true, "src/test_x.py": true, "a.spec.ts": true, "testdata.go": false, "contest.go": false} {
		if IsTest(p) != want {
			t.Errorf("IsTest(%q) != %v", p, want)
		}
	}
}
