# 🎩 wonder

```
$ wonder run 42

🏭 Planning Room     ✓ spec ready (risk low, 3 criteria)
⚙️  Invention Room    ✓ added --json flag to export
🍬 Production Line   ✓ CI passed
🔍 Quality Control   codex: approve · claude: approve
🎟️  Golden Ticket     waiting for you
✨ Delivery          merged PR #57
```

**wonder** turns a GitHub issue into a reviewed, merged pull request. It runs a
fixed pipeline of coding agents (Claude Code and Codex), and it records every
human verdict so you can test prompt changes against real history before they go
live.

> Status: early. Inspired by Arize's
> [software factory that tests its own prompts](https://arize.com/blog/how-to-build-a-software-factory-that-tests-its-own-prompts/).

## Two layers

1. **Workers** are the agent CLIs you already use: `claude -p` and `codex exec`.
   They run on whatever login those CLIs have (subscription or API key). Each one
   edits files in a worktree and replies with JSON. Nothing more.
2. **The factory** is this CLI. It chains the stations, owns every git and GitHub
   action (commit, push, PR, comment, merge), stores verdicts, and replays them
   to evaluate prompts.

Agents never touch git or GitHub, so a confused agent can't push, merge, or
delete anything.

## Stations

| station | room | default worker | does |
|---|---|---|---|
| spec | 🏭 Planning Room | Claude (sonnet), read-only | issue → goal, acceptance criteria, risk |
| build | ⚙️ Invention Room | Codex, write | implements the spec |
| ci + fix | 🍬 Production Line | Codex, write | fixes failing CI, up to 3 times |
| review | 🔍 Quality Control | Codex **and** Claude (opus), read-only, in parallel | approve / changes + findings |
| router | 🎟️ Golden Ticket | rules | auto-merge or ask a human |
| gate | 🎟️ Golden Ticket | you | ship, or changes + notes (loops back through fix + CI) |

With only one of the two CLIs installed, every station uses that one.

## Quick start

```sh
go install github.com/vianel/wonder/cmd/wonder@latest
cd your-repo
wonder init      # writes wonder.toml and .wonder/prompts/
wonder doctor    # checks git, gh, claude/codex, herdr, CI
wonder run 42    # or: wonder pick
```

You need `git`, the [GitHub CLI](https://cli.github.com) (logged in), and at
least one of [Claude Code](https://docs.claude.com/en/docs/claude-code) or
[Codex](https://github.com/openai/codex). [herdr](https://herdr.dev) is
optional. Inside herdr, each worker runs in its own named pane (`f42-build`),
so you can watch it.

## Commands

| command | does |
|---|---|
| `wonder doctor` | checks what's installed and what's missing |
| `wonder init` | writes `wonder.toml`, copies prompts, updates `.gitignore` (`--commit-verdicts` to keep the verdict log in git) |
| `wonder run <issue>...` | runs issues, up to `max_panes` workers at once |
| `wonder pick` | choose open issues from a list |
| `wonder resume <issue>` | continue after a kill, a usage limit, or a blocked worker |
| `wonder gate [issue]` | answer runs waiting on you (`s` ship · `c` changes · `o` open PR · `d` diff) |
| `wonder status` | every run and its station |
| `wonder stats` | miss / false-alarm rate per reviewer, router accuracy, CI fix counts |
| `wonder harvest` | pulls merges and reverts from GitHub; a revert within 7 days turns approvals into misses |
| `wonder replay --station review --prompt cand.md` | runs the current and candidate review prompts on every past verdict |
| `wonder replay --station router --max-diff-lines 600` | the same for the router rules (no agents needed) |
| `wonder promote --station review --prompt cand.md` | switches to the candidate only if its replay had fewer misses and no more false alarms |

## Testing prompts on your own history

The first gate verdict on every run goes into `.wonder/verdicts.jsonl`, along
with what each reviewer said, a label per reviewer (`agree`, `miss`,
`false_alarm`), and the hash of every prompt used. To evaluate a candidate
prompt, `replay` checks out each PR at the commit the reviewers saw. It then
runs both prompts N times and scores them: false alarms by code, misses with an
LLM judge that reads your gate notes. Under ~15 verdicts the numbers are noisy,
and wonder says so.

## Docs

- [Configuration](docs/config.md)
- [Worker contract and schemas](docs/contract.md)
- [Run state and verdict log](docs/verdicts.md)
- [Prompt templates](docs/prompts.md)
- [herdr runner](docs/herdr.md)
- [Contributing, including adding a worker](CONTRIBUTING.md)

## License

MIT
