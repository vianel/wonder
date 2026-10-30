package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Copilot runs the GitHub Copilot CLI headless: the prompt on stdin, -s for
// only the final reply on stdout. Deny rules win over --allow-all-tools.
type Copilot struct{}

func (Copilot) Name() string { return "copilot" }

func (Copilot) Command(req Request, tmp string) []string {
	argv := []string{"copilot", "-s", "-C", req.Dir, "--no-auto-update", "--no-ask-user",
		// The built-in GitHub MCP server could open PRs; the pipeline does that.
		"--disable-builtin-mcps", "--allow-all-tools",
		"--deny-tool", "shell(git:*)", "--deny-tool", "shell(gh:*)",
		"--usage-output-file", filepath.Join(tmp, "usage.json")}
	if !req.Write {
		argv = append(argv, "--deny-tool", "write", "--deny-tool", "shell")
	}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	return append(argv, req.Args...)
}

// Parse reads tokens from the usage file. Copilot bills premium requests, not
// dollars per token; a cost comes from [pricing.copilot] in wonder.toml, if set.
func (Copilot) Parse(stdout []byte, tmp string) (string, Usage, error) {
	var u Usage
	if b, err := os.ReadFile(filepath.Join(tmp, "usage.json")); err == nil {
		type count struct {
			N int64 `json:"tokenCount"`
		}
		var f struct {
			Tokens struct {
				Input      count `json:"input"`
				CacheRead  count `json:"cache_read"`
				CacheWrite count `json:"cache_write"`
				Output     count `json:"output"`
			} `json:"tokenDetails"`
		}
		if json.Unmarshal(b, &f) == nil {
			u = Usage{Input: f.Tokens.Input.N + f.Tokens.CacheWrite.N, CachedInput: f.Tokens.CacheRead.N, Output: f.Tokens.Output.N}
		}
	}
	return strings.TrimSpace(string(stdout)), u, nil
}
