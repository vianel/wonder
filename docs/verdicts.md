# Run state and verdict log (v1)

Everything lives in `.wonder/` at the repo root, which is gitignored by default.
The exception is `.wonder/prompts/`, which you should commit.

| path | what |
|---|---|
| `.wonder/runs/<issue>.json` | run state: station, status, PR, worktree, head sha, spec, reviews, router decision, gate rounds. Written atomically after every transition. |
| `.wonder/worktrees/<issue>/` | one git worktree per issue, removed after merge |
| `.wonder/verdicts.jsonl` | one line per **first** gate verdict |
| `.wonder/replays/review-<hash>.json` | replay results, read by `promote` |
| `.wonder/promotions.jsonl` | one line per promotion |
| `.wonder/tmp/` | scratch output of failed worker runs, for debugging |

## Run statuses

`running` → `waiting_gate` → `done`, plus `paused_rate_limit`, `blocked` and
`failed`. `wonder resume <issue>` continues from any of them.

## Verdict line

```json
{"v": 1, "ts": "2026-10-29T17:40:13Z", "issue": 42, "pr": 57, "title": "…",
 "base_sha": "…", "head_sha": "…",
 "gate_first": "ship|changes", "gate_notes": "…",
 "reviews": {"codex": {"verdict": "approve", "findings": [], "summary": "…"}, "claude": {…}},
 "labels": {"codex": "agree|miss|false_alarm", "claude": "…"},
 "router": {"route": "gate", "reason": "…", "rule_overrode": false, "rules_route": "auto"},
 "diff_stats": {"files": 2, "added": 40, "deleted": 3, "tests_deleted": false, "tests_weakened": false},
 "babysit_fixes": 1, "spec": {…},
 "models": {"spec": "sonnet", "build": "codex:default", "review.claude": "opus", "review.codex": "codex:default"},
 "prompts": {"spec": "<sha8>", "build": "<sha8>", "review": "<sha8>", "fix": "<sha8>"},
 "harvest": {"ts": "…", "merged": true, "merged_at": "…", "merge_sha": "…", "reverted": false}}
```

- **Labels.** `agree`: the reviewer gave the same answer as the gate. `miss`: the
  reviewer approved, but the gate asked for changes, or the PR was reverted
  within 7 days (added by `wonder harvest`). `false_alarm`: the reviewer asked
  for changes, but the gate shipped.
- Only the first gate verdict counts. Later rounds (changes → fix → CI → gate)
  don't go back through review, so they say nothing about the reviewers.
- `rules_route` is where the router rules alone would send the PR, ignoring
  `allow_auto`. `stats` and `replay --station router` score it against the gate.
- `prompts` holds the first 8 hex digits of the SHA-256 of each prompt template.
  A model left unset is recorded as `<worker>:default`.
- With `wonder init --commit-verdicts`, the log is kept in git. It contains gate
  notes and review text, so only do this where that's fine.
