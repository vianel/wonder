package schema

import "testing"

func TestExtract(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`:                                 `{"a":1}`,
		"Done!\n```json\n{\"a\":2}\n```\n":        `{"a":2}`,
		`I did it. {"a":{"b":3}} hope that helps`: `{"a":{"b":3}}`,
		`first {"x":1} then {"a":4}`:              `{"a":4}`,
	}
	for in, want := range cases {
		got, err := Extract(in)
		if err != nil || got != want {
			t.Errorf("Extract(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Extract("no json here"); err == nil {
		t.Error("want error for text without JSON")
	}
}

func TestParseValidates(t *testing.T) {
	if _, err := Parse(Worker, `{"status":"done","summary":"s","files_changed":[],"notes":""}`); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(Worker, `{"status":"maybe","summary":"s","files_changed":[],"notes":""}`); err == nil {
		t.Fatal("want enum error")
	}
	if _, err := Parse(Review, `{"verdict":"approve","summary":"s"}`); err == nil {
		t.Fatal("want missing findings error")
	}
}
