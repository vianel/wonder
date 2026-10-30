You are judging a code reviewer. A human looked at the same change and asked for
changes. Decide whether the reviewer missed a serious issue that the human found.

Human notes:
{{.GateNotes}}

Reviewer output:
{{.Review}}

The reviewer did NOT miss it if any finding covers the same problem, even in
different words. Do not use any tools.

Reply with ONLY a JSON object, no prose:
{"missed": true|false, "reason": "one sentence"}
