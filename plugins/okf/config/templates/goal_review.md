---
description: Review a local-okf goal file and record whether it is achieved
---
{{- $work := get .SessionInputs "work_session" "" -}}
{{- $instance := get .SessionInputs "instance" "" -}}
{{- $judges := get .SessionInputs "judge_ids" "" -}}
Review the goal at {{.ResourceID}}.

Steps:

1. Read the goal file's contents (symlinked into this session's workspace directory as
   `knowledge/`) — its "## Done When" checklist, its current status, and the
   log of what's happened against it so far
2. Cross-check the checklist against actual evidence: recent commits, PRs,
   issues, and events for the sessions that worked this goal — a checked box
   with no corresponding evidence is not achieved
3. Judge whether the goal itself is achieved, not just whether its checklist
   is ticked — a checklist can be satisfied while the underlying intent
   still isn't met, and that gap is exactly what this review exists to catch
{{- if $work}}
4. Record one action for the pending judge id(s) ({{$judges}}) on the
   pursue_goal instance `{{$instance}}` of session `{{$work}}`, with a
   reason. Find the exact judge state with `plect status {{$work}} --json`,
   then:
   `plect judge approve {{$work}} {{$instance}} <judge-id> --reason "<reason>"`
   Use `plect judge request-changes {{$work}} {{$instance}} <judge-id> --reason "<reason>"`
   for any unmet criterion and name the missing work in the reason.
{{- else}}
4. When this review is for a plect done_when judge, record one action per judge
   id with a reason:
   `plect judge approve <work-session> <task-instance> <judge-id> --reason "<reason>"`
   Use `plect judge request-changes <work-session> <task-instance> <judge-id> --reason "<reason>"`
   for any unmet criterion and name the missing work in the reason.
{{- end}}
5. Record that you finished reviewing (this is what lets plect close out this
   review session — there is no third party to judge a reviewer, so
   completion is your own self-report): find this review's own current
   `revision` with `plect status --json` (the `revision` output on this
   session's own task instance, not the target session's — `$PLECT_SESSION_NAME`
   is this session), then run
   `plect state set-output "$PLECT_SESSION_NAME" --task goal_review#1 '{"verdict_revision":"<that revision>"}'`
   (adjust the instance id if `plect status --json` shows a different one).
   Do this whichever action you took above. If the goal file changes again
   later, this review's done_when reopens automatically and you'll be asked
   to re-review; record a fresh `verdict_revision` again when you do.

**Scope boundary (required):** this review's job ends with recording the
verdict. Do not edit the goal file or intervene in the target session. If
your judgment differs from the goal's author, write the specific reason in
`plect judge request-changes`'s reason.

**AI disclosure (mandatory):** if you leave any human-visible note about this
review (Slack, an issue/PR comment), open it by identifying yourself as an AI
agent: `> 🤖 This review was written by an AI agent under human supervision.`

**Unattended session note:** this session cannot ask a human an interactive
question. When a judgment call is needed, write the open question in the
judge's reason and use request-changes — do not silently assume an answer
and proceed.
{{- if .Instruction}}

Additional instructions: {{.Instruction}}
{{- end}}
