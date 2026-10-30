package worker

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Claude runs Claude Code headless: claude -p --output-format json.
type Claude struct{}

func (Claude) Name() string { return "claude" }

// Agents never touch git or GitHub; the pipeline does.
const claudeDenied = "Bash(git commit:*),Bash(git push:*),Bash(git checkout:*),Bash(git reset:*),Bash(git rebase:*),Bash(gh:*)"

func (Claude) Command(req Request, _ string) []string {
	argv := []string{"claude", "-p", "--output-format", "json"}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.Write {
		argv = append(argv, "--permission-mode", "acceptEdits",
			"--allowedTools", "Read,Grep,Glob,LS,Edit,MultiEdit,Write,Bash",
			"--disallowedTools", claudeDenied)
	} else {
		argv = append(argv, "--allowedTools", "Read,Grep,Glob,LS,Bash(git diff:*),Bash(git log:*),Bash(git show:*)",
			"--disallowedTools", "Edit,MultiEdit,Write,NotebookEdit,"+claudeDenied)
	}
	return append(argv, req.Args...)
}

func (Claude) Parse(stdout []byte, _ string) (string, Usage, error) {
	var out struct {
		Result  string  `json:"result"`
		IsError bool    `json:"is_error"`
		Subtype string  `json:"subtype"`
		Cost    float64 `json:"total_cost_usd"`
		Usage   struct {
			Input       int64 `json:"input_tokens"`
			CacheWrite  int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			OutputToken int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(stdout, &out); err != nil {
		return "", Usage{}, fmt.Errorf("claude: unexpected output: %w", err)
	}
	// Claude Code reports cost at API prices, also on a subscription.
	u := Usage{Input: out.Usage.Input + out.Usage.CacheWrite, CachedInput: out.Usage.CacheRead,
		Output: out.Usage.OutputToken, CostUSD: out.Cost, HasCost: true}
	if out.IsError {
		return "", u, errors.New("claude: " + out.Subtype + ": " + out.Result)
	}
	return out.Result, u, nil
}
