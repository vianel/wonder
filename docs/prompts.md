# Prompt templates

Prompts are Go [`text/template`](https://pkg.go.dev/text/template) files.
`wonder init` copies the defaults to `.wonder/prompts/`. Edit them there and
commit them. Every run records the hash of each prompt it used.

Fields you can use:

| field | stations |
|---|---|
| `{{.Repo}}` | spec (`owner/name`) |
| `{{.Issue.Number}}`, `{{.Issue.Title}}`, `{{.Issue.Body}}` | all (Body is empty during replay) |
| `{{.Spec}}` | build, fix, review (the spec as JSON) |
| `{{.Diff}}` | review (`git diff base..head`, truncated at 200 KB) |
| `{{.Reason}}`, `{{.Input}}` | fix (why it's there; CI logs or the gate's notes) |
| `{{.Review}}`, `{{.GateNotes}}` | the miss judge |

Referencing a field that doesn't exist is an error.

To try a new review prompt, copy `review.md`, edit the copy, then run
`wonder replay --station review --prompt my-review.md`. If the replay passes,
`wonder promote` switches `wonder.toml` to it.
