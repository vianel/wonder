# Worker contract (v1)

Every station's reply must be a JSON object that matches a schema in
[`schemas/`](../schemas). The agent may wrap it in prose or a ```json fence:
wonder takes the whole reply if it is JSON, then the last fenced block, then
the last balanced `{…}`.

If the reply doesn't validate, wonder retries once with the validation error
appended to the prompt. A second failure fails the station.

| station | schema |
|---|---|
| spec | [`spec.v1.json`](../schemas/spec.v1.json): `goal`, `acceptance_criteria[]`, `assumptions[]`, `risk` (low/medium/high), `files_likely_touched[]`, optional `questions[{question, suggested}]` (max 5) |
| build, fix | [`worker.v1.json`](../schemas/worker.v1.json): see below |
| review | [`review.v1.json`](../schemas/review.v1.json): `verdict` (approve/changes), `findings[{severity, area, issue}]`, `summary` |
| replay judge | [`judge.v1.json`](../schemas/judge.v1.json): `missed` (bool), `reason` |

## Questions up front

The spec may return `questions`, each with a `suggested` answer. wonder writes
every spec in the batch first, asks all the questions at once, and records each
answer in the spec (`answer`, plus `answered_by`: `you`, or `suggested` when
nobody was asked because of `--yes` or no terminal). Later stations get the
spec with the answers in it. Questions left open park the run as
`waiting_answers`; `wonder resume` asks them again.

Write stations (build and fix) share this contract:

```json
{"status": "done|blocked|failed", "summary": "str", "files_changed": ["str"], "notes": "str"}
```

- `done`: wonder commits the worktree and pushes.
- `blocked`: the run stops with status `blocked` and shows `notes` to the human.
  wonder never answers an agent's question for you. Run `wonder resume <issue>`
  after you sort it out (for example, by editing the issue).
- `failed`: the run fails, and `wonder resume` retries the station.

Breaking changes to a schema add a new `vN` file and bump the version.

## Usage limits

When a CLI exits with a usage-limit error, the run becomes `paused_rate_limit`,
with the reset time if the CLI gave one. No new stations start in that process.
`wonder resume` continues from there. wonder never retries in a loop.
