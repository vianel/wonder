# wonder.toml (format v1)

`wonder init` writes this file at the repo root, using only the agent CLIs it
finds on your PATH. Unknown keys are an error, so typos don't pass silently.

```toml
[factory]
max_panes = 3            # max workers running at once, across all issues
base_branch = "main"

[router]
allow_auto = false       # true: PRs that pass every rule merge without the gate
max_diff_lines = 400

[stations.spec]
worker = "claude"        # claude | codex
model = "sonnet"         # optional; omitted = the CLI's own default
prompt = ".wonder/prompts/spec.md"

[stations.build]
worker = "codex"
prompt = ".wonder/prompts/build.md"
# args = ["-c", "model_reasoning_effort=high"]   # extra CLI args, any station

[stations.fix]
worker = "codex"
prompt = ".wonder/prompts/fix.md"
max_attempts = 3         # CI fix attempts before the run fails

[stations.review]
prompt = ".wonder/prompts/review.md"

[[stations.review.workers]]
worker = "codex"

[[stations.review.workers]]
worker = "claude"
model = "opus"

[eval]
runs = 3                 # replay runs per prompt per example

[eval.judge]             # the LLM judge that scores misses during replay
worker = "claude"
model = "sonnet"
prompt = ".wonder/prompts/judges/miss.md"
```

Prompt paths are relative to the repo root. If a prompt file is missing, wonder
falls back to the built-in default with the same name.

## Router rules

A PR goes to the human gate if any of these hold:

- any reviewer says `changes`
- the diff is longer than `max_diff_lines` (added + deleted)
- a test file was deleted, or lost more lines than it gained
- the spec's risk is `high`

Otherwise it auto-merges, but only when `allow_auto = true`. The default is to
always ask.
