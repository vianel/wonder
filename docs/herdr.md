# herdr runner

When wonder runs inside [herdr](https://herdr.dev) (`HERDR_ENV=1` and `herdr` on
PATH), each worker gets its own pane, so you can watch it. Anywhere else, workers
run as plain subprocesses, and wonder says so when it starts. Nothing else
changes.

Recorded from `herdr 0.9.5` (`herdr pane`, `herdr --skill`):

| step | command |
|---|---|
| open a pane | `herdr pane split --current --direction right --cwd <worktree> --no-focus` for the first pane, then `herdr pane split <newest wonder pane> --direction down …`, so columns stay wide enough |
| name it | `herdr pane rename <pane> f<issue>-<station>` (e.g. `f42-build`, `f42-review-codex`) |
| run | `herdr pane run <pane> "bash <tmp>/run.sh"` |
| wait | `herdr pane wait-output <pane> --match WONDER_DONE_<nonce> --source recent`, which blocks inside herdr (no sleep or poll loop in wonder) |
| clean up | `herdr pane close <pane>`, only for panes this process created |

`run.sh` runs the same headless command the exec runner would (`claude -p …` or
`codex exec …`), with the prompt on stdin. It writes stdout and the exit code
to files and tees stderr to the pane, so you can see progress. wonder reads the
files, not the screen.

## Decisions

- **Headless CLIs in panes, not interactive agents driven by `herdr agent prompt`.**
  One `Worker` implementation serves both runners, the JSON comes from a file
  instead of being scraped off the terminal, and headless CLIs never show an
  approval dialog. So a "blocked" worker always means the agent replied
  `status: blocked`. wonder shows that to you and never answers it for you.
- **`git worktree`, not `herdr worktree`.** `herdr worktree create` opens a herdr
  workspace per worktree. wonder must work without herdr, and one workspace per
  issue would clutter the sidebar. Worktrees live in `.wonder/worktrees/<issue>`.
- **`max_panes`** (default 3) caps how many workers run at once, across all
  issues and both runners.
