# 🎩 wonder

[![release](https://img.shields.io/github/v/release/vianel/wonder)](https://github.com/vianel/wonder/releases/latest)
[![ci](https://github.com/vianel/wonder/actions/workflows/ci.yml/badge.svg)](https://github.com/vianel/wonder/actions/workflows/ci.yml)
[![license](https://img.shields.io/github/license/vianel/wonder)](LICENSE)

```
                         .-""-.
        ~~~ steam ~~~   / 🎩   \        W O N D E R W O R K S
           )   )   )   |  ._.  |
          (   (   (     \_____/      "Issues go in one end,
       ___|___|___|___   |   |        merged PRs come out the other."
      |  _   _   _   |__|___|__
      | |_| |_| |_| |  ~~~~~~~~~ ~~~ 🍫 the chocolate river of PRs ~~~
      |   _______   |  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
      |  |  ___  |  |
______|__|_|_W_|_|__|____________________________________________________
```

**wonder** is a sweet little factory that turns a GitHub issue into a reviewed,
merged pull request. Your coding agents (Claude Code and Codex) are the workers
on the factory floor. You hold the only **golden ticket**: nothing ships until
you say so.

Every golden-ticket decision you make is written down. Later, when you want to
change a recipe (a prompt), wonder taste-tests the new recipe against every
batch you've already judged before it goes on the line.
---

## 🍫 A tour of the factory

```
  GitHub issue #42
        │
        ▼
 ┌────────────────────┐   Reads the issue and writes the recipe:
 │ 🏭 Planning Room    │   goal, acceptance criteria, risk.
 │    (spec)          │   Claude · read-only
 └─────────┬──────────┘
           ▼
 ┌────────────────────┐   Cooks up the change in its own worktree.
 │ ⚙️  Invention Room   │   Codex · writes files, never touches git
 │    (build)         │
 └─────────┬──────────┘
           ▼
 ┌────────────────────┐   wonder commits, pushes and opens the PR,
 │ 🍬 Production Line  │   then watches CI. A red build goes back to the
 │    (ci + fix)      │   fixer, up to 3 times. Merge conflicts too.
 └─────────┬──────────┘
           ▼
 ┌────────────────────┐   Two tasters, in parallel:
 │ 🔍 Quality Control  │   Codex and Claude (opus) each say
 │    (review)        │   approve / changes, with findings.
 └─────────┬──────────┘
           ▼
 ┌────────────────────┐   Rules decide: any taster unhappy? Diff too big?
 │ 🎫 Golden Ticket    │   Tests deleted? High risk? → it waits for you.
 │    (router + gate) │   You press  s  ship   or   c  request changes.
 └─────────┬──────────┘           │
           ▼                      └──► back to the Production Line,
 ┌────────────────────┐                 then straight back to you
 │ ✨ Delivery         │
 │    (merge)         │   Squash-merge, close the issue, sweep up the worktree.
 └────────────────────┘
```

**The one rule of the factory floor:** workers only edit files and hand back
JSON. wonder itself does every commit, push, PR, comment and merge, so a
confused worker can't push, merge or delete anything.

---

## 🎟️ Getting in

You need:

- `git`
- the [GitHub CLI](https://cli.github.com), logged in (`gh auth login`)
- at least one worker: [Claude Code](https://docs.claude.com/en/docs/claude-code), [Codex](https://github.com/openai/codex) or [GitHub Copilot CLI](https://github.com/features/copilot/cli)
- optional: [herdr](https://herdr.dev), to watch each worker in its own pane

```sh
# macOS (Homebrew)
brew install vianel/tap/wonder

# macOS or Linux
curl -fsSL https://raw.githubusercontent.com/vianel/wonder/main/install.sh | sh

# from source (Go 1.23+)
go install github.com/vianel/wonder/cmd/wonder@latest
```

The script puts `wonder` in `~/.local/bin` (set `WONDER_INSTALL_DIR` to change
that) and checks the download against the release checksums. To upgrade, run
`brew upgrade wonder` or the same `curl` line again; `wonder --version` shows
what you have.

Missing something? `wonder doctor --fix` offers to install `gh`, `claude`, `codex`
and `herdr` for you, one at a time. It shows the exact command and does nothing
unless you type `y`:

```
claude is not installed (a worker: Claude Code).
  wonder can run:  curl -fsSL https://claude.ai/install.sh | bash
  Run it now? [y/N] y
  ✓ claude installed
  Log in now (claude auth login)? [y/N] y
```

Workers use whatever login `claude`, `codex` and `copilot` already have,
subscription or API key. With only one of them installed, it staffs every room.
Copilot is opt-in when Claude or Codex is there: set `worker = "copilot"` on any
station in `wonder.toml`. (AWS Copilot also installs a `copilot`; `wonder doctor`
tells the two apart.)

---

## 🍭 How to use it

### 1. Open the factory in your repo

```
$ cd my-repo
$ wonder init
✓ wrote wonder.toml (workers: claude, codex)
✓ default prompts in .wonder/prompts/ (commit them; they are versioned)
✓ updated .gitignore

Next: `wonder doctor`, then `wonder run <issue>`.
```

### 2. Check the machinery

```
$ wonder doctor
✓ git        /Users/you/my-repo
✓ gh         logged in
✓ claude     found
✓ codex      found
✓ herdr      workers will run in herdr panes
✓ ci         1 GitHub Actions workflow(s)
✓ config     wonder.toml is valid

🍫 The factory is ready.
```

Anything missing shows up as `✗` or `·`, with how to install it.

### 3. Send an issue down the line

```
$ wonder run 7          # also takes #7 or the issue URL
🎩 workers run in herdr panes (max 3)
#7    🏭 Planning Room      writing spec for "Add an IsPalindrome function"
#7    🏭 Planning Room      ✓ spec ready (risk low, 8 criteria)
#7    ⚙️  Invention Room   building
#7    ⚙️  Invention Room   ✓ Added rune-aware, case-insensitive IsPalindrome and table-driven tests.
#7    🍬 Production Line    ✓ pushed https://github.com/you/my-repo/pull/8
#7    🍬 Production Line    waiting for CI on PR #8
#7    🍬 Production Line    ✓ CI passed
#7    🔍 Quality Control    reviewing with codex + claude
#7    🔍 Quality Control    codex: approve (0 findings)
#7    🔍 Quality Control    claude: approve (0 findings)
#7    🎫 Golden Ticket      waiting for you: all rules pass, but router.allow_auto is off
```

If the issue leaves real choices open, the Planning Room asks you about them
**before anything is built**. Each question comes with a suggested answer; press
enter to take it. After that, nothing needs you until the golden ticket:

```
🏭 Planning Room has 4 question(s) before building. Answer now, then you can walk away.
Press enter to take the suggested answer.

  #13 Add a Pad function
  1. Which side should the padding go on: right (left-aligned text), left
     (right-aligned text), or should the caller choose?
     suggested: Right-pad only, with a single Pad(s, width) signature.
     › Let the caller choose: add PadLeft and PadRight
  2. What should the padding character be: a space, or configurable?
     suggested: A space, not configurable.
     ›

✓ Got it. Nothing else needs you until the golden ticket. Go get a chocolate. 🍫
```

Your answers go into the spec, so the builder, the fixer and the reviewers all
see them, and the golden ticket shows them again. In a hurry? `wonder run --yes`
takes every suggested answer without asking, so you can start a batch and leave
right away.

Got a whole batch? `wonder run 12 13 14` writes every spec first and asks all
their questions in one go. Then it runs them in parallel, up to
`max_panes` workers at once, each in its own worktree. Or run `wonder pick` and
choose from a list:

```
🎩 Pick issues for the factory (space to select, enter to run, q to quit)

› [x] #12    Add a Reverse function
  [ ] #13    Add a WordCount function
  [x] #14    Fix crash on empty config
```

### 4. Hold the golden ticket

When a PR reaches the gate, your terminal bell rings and the decision card opens:

```
╭──────────────────────────────────────────────────────────────────────╮
│ 🎫 Golden Ticket                                                     │
│ #7 Add an IsPalindrome function                                      │
│ PR #8 · 2 files · +36 −1 · CI ✓ passed                               │
╰──────────────────────────────────────────────────────────────────────╯

  ✓ Both reviewers approved, no findings.
  Waiting for you because auto-merge is off (router.allow_auto in wonder.toml).
  Spent so far: $0.42 + 207k unpriced tok (API prices) in 2 min of agent time.

  What it should do (8)
    • text.go defines func IsPalindrome(s string) bool in package sandbox
    • Case is ignored: IsPalindrome("Aba") and IsPalindrome("RaceCar") return true
    • Comparison is by rune, not byte: IsPalindrome("åbÅ") returns true
    … and 5 more (press o to see the PR)

  Reviewers
    claude  ✓ approve  Compares runes from both ends with unicode.ToLower,
                       so case is ignored and multi-byte characters work.
    codex   ✓ approve  Implementation and table tests meet all criteria.

  Merge this PR?

   s  Ship it — merge PR #8 and close issue #7
   c  Request changes — the fix agent makes them, CI reruns,
      then it comes back here
   d  View diff    o  Open in browser
   q  Decide later (wonder gate 7)
```

Press `c` and tell the fixer what you want:

```
  Request changes on PR #8
  Tell the fix agent what to change. It gets this text as its instructions.

  > also strip spaces and punctuation, and add a test for "A man, a plan"

   enter send    esc back
```

The PR goes back down the Production Line (fix → push → CI) and returns to you.
Reviewers don't taste it again: only your **first** decision on each PR is used
to grade them.

### 5. Walk away whenever you like

Everything is saved after every station. Close the lid, hit Ctrl+C, or hit a
usage limit, then pick up where you left off:

```
$ wonder status
# example output
ISSUE  STATION            STATUS             PR   UPDATED              NOTE
#7     ✨ Delivery         done               #8   2026-10-30 10:12:41
#12    🎫 Golden Ticket    waiting_gate       #15  2026-10-30 10:20:03
#13    ⚙️  Invention Room   paused_rate_limit  —    2026-10-30 10:21:44  resets Oct 30 15:00
#14    🍬 Production Line  blocked            #16  2026-10-30 10:22:10  blocked: which config…
#15    🏭 Planning Room    waiting_answers    —    2026-10-30 10:23:02

$ wonder gate 12        # answer the one waiting for you
$ wonder resume 13 14   # carry on after the limit resets / you unblocked it
$ wonder resume 15      # answer the questions you skipped
```

A **blocked** worker asked a question. wonder never answers it for you: it
shows you the question and waits.

---

## 🧪 The tasting room: test prompts before they ship

Every first golden-ticket decision lands in `.wonder/verdicts.jsonl`, with what
each reviewer said and a label:

| label | meaning |
|---|---|
| `agree` | the reviewer gave the same answer you did |
| `miss` | the reviewer approved, you asked for changes (or the PR got reverted within 7 days) |
| `false_alarm` | the reviewer asked for changes, you shipped it |

See how your tasters are doing:

```
$ wonder stats        # example output
24 verdicts,  71% shipped at the first gate

reviewer  n   agree  miss  false alarm
claude    24   83%    8%    8%
codex     24   75%    4%   21%

router rules (ignoring allow_auto): 19 correct, 5 unnecessary reviews, 0 unsafe auto-merges
babysit: 0.4 CI fixes per run on average, max 2
```

Got a new review recipe? Taste-test it against every past batch. wonder checks
out each PR at the exact commit the reviewers saw, runs the current and the new
prompt 3 times each, and scores them. A judge model reads your gate notes to
decide whether a reviewer missed what you caught:

```
$ wonder replay --station review --prompt stricter-review.md     # example output
issue  gate     current FA/miss  candidate FA/miss
#7     ship     0/0              0/0
#12    changes  0/6              0/2
#13    ship     3/0              1/0
…
total           9/14             4/6     (over 144 runs each)
✓ candidate 3fa9c1d2 passes: fewer misses, no more false alarms.

$ wonder promote --station review --prompt stricter-review.md
✓ review prompt is now stricter-review.md (3fa9c1d2)
```

`promote` refuses unless the replay passed. Router rules can be replayed too,
for free (no agents needed):

```
$ wonder replay --station router --allow-auto --max-diff-lines 600
```

Pull merges and reverts from GitHub into the log now and then:

```
$ wonder harvest
#12 PR #15 was reverted: approving reviewers now count as misses
harvested 6 verdicts
```

Under ~15 verdicts the numbers are noisy, and wonder says so.

---

## 🪙 What did that cost?

Every agent call is metered: tokens, dollars and time. `wonder status` has a
COST column, the golden ticket shows what's been spent so far, the merge line
shows the total, and `wonder stats` averages it per station:

```
#15   ✨ Delivery           ✓ merged PR #16 · spent $0.42 + 207k unpriced tok in 2 min of agent time
```

Claude Code reports its own cost (at API prices, also on a subscription).
Codex and Copilot report tokens only (Copilot bills premium requests); give
them prices in `wonder.toml` to see dollars:

```toml
[pricing.codex]   # USD per million tokens
input = 1.25
cached_input = 0.125
output = 10.0
```

---

## 🧰 All the levers

| command | does |
|---|---|
| `wonder doctor` | checks git, gh, claude/codex/copilot, herdr, CI and config (`--fix` offers to install what's missing) |
| `wonder init` | writes `wonder.toml` and prompts, updates `.gitignore` (`--commit-verdicts` keeps the log in git) |
| `wonder run <issue>...` | runs issues through the factory (`--yes` takes the suggested answers to the spec's questions) |
| `wonder pick` | choose open issues from a list |
| `wonder gate [issue]` | answer runs waiting for you |
| `wonder resume <issue>...` | continue after a stop, a usage limit or a block |
| `wonder status` | every run and where it is |
| `wonder stats` | reviewer, router and babysit numbers |
| `wonder replay` | test a candidate review prompt or router rules on history |
| `wonder promote` | switch to a candidate prompt that passed replay |
| `wonder harvest` | record merges and reverts from GitHub |

## 📜 The recipe book

- [Configuration (`wonder.toml`)](docs/config.md)
- [Worker contract and JSON schemas](docs/contract.md)
- [Run state and the verdict log](docs/verdicts.md)
- [Prompt templates](docs/prompts.md)
- [Watching workers in herdr](docs/herdr.md)
- [Contributing, including adding a new worker](CONTRIBUTING.md)

## License

MIT
