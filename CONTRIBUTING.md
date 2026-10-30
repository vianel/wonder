# Contributing

```sh
go test -race ./...   # all pipeline logic runs against fake CLIs in testdata/bin
go vet ./...
golangci-lint run
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org)
(`feat:`, `fix:`, `docs:`, …). Keep `.wonder/` data, tokens, and anything from
private repos out of fixtures.

## Layout

```
cmd/wonder/         cobra commands, doctor, init, pick
internal/config/    wonder.toml
internal/pipeline/  stations, state machine, router, gate decisions
internal/worker/    one file per agent CLI
internal/runner/    exec (subprocess) and herdr (pane) runners
internal/gh, gitx/  thin wrappers over the gh and git CLIs
internal/store/     run state and verdict log
internal/gate/      gate TUI
internal/eval/      replay, promote, stats, harvest
prompts/, schemas/  embedded defaults and the public JSON schemas
testdata/bin/       fake claude, codex, copilot, gh, herdr used by tests
```

## Adding a worker

A worker builds a command line and reads the agent's final message. It doesn't
know about git, GitHub, panes, or schemas. Here is a complete example for a
hypothetical `gemini` CLI:

```go
// internal/worker/gemini.go
package worker

type Gemini struct{}

func (Gemini) Name() string { return "gemini" }

// Command returns argv. The prompt arrives on stdin. tmp is a scratch dir for
// this one invocation.
func (Gemini) Command(req Request, tmp string) []string {
	argv := []string{"gemini", "--output-format", "text"}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if !req.Write {
		argv = append(argv, "--sandbox", "read-only")
	}
	return append(argv, req.Args...)
}

// Parse returns the agent's final message; wonder extracts and validates the
// JSON from it. Fill in Usage with whatever the CLI reports (tokens, and
// CostUSD + HasCost if it gives dollars); leave it zero if it reports nothing.
func (Gemini) Parse(stdout []byte, tmp string) (string, Usage, error) {
	return string(stdout), Usage{}, nil
}
```

Then:

1. Register it in `worker.Get` (`internal/worker/worker.go`) and in
   `knownWorkers` (`internal/config/config.go`).
2. Add a fake `testdata/bin/gemini` that sources `_agent.sh`, like `claude` and
   `codex` do.
3. If the CLI reports tokens but no dollars, users can price it with
   `[pricing.gemini]` in `wonder.toml`; add the name to `knownWorkers`.
4. If the CLI has its own usage-limit message, add a pattern to
   `DetectRateLimit` with a test.
5. Make sure read-only mode really can't write, and that write mode can't run
   `git commit`/`push` or `gh`.

## Releasing

Push a version tag; the `release` workflow does the rest with
[GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`):

```sh
git tag -a v0.2.0 -m v0.2.0 && git push origin v0.2.0
```

It builds macOS and Linux binaries (amd64, arm64), publishes the GitHub release
that `install.sh` downloads from, and updates the cask in
[vianel/homebrew-tap](https://github.com/vianel/homebrew-tap). That last step
needs the `HOMEBREW_TAP_TOKEN` secret: a fine-grained token with Contents
read/write on the tap repo.

If a release fails halfway, delete it (`gh release delete vX.Y.Z --yes`; the tag
stays) before re-running the job, or the uploads fail with "already exists".
