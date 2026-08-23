Review the goal at {{ resource.id }}.

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
4. When this review is for a plect done_when judge, record one action per judge
   id with a reason:
   `plect judge approve <work-session> <task-instance> <judge-id> --reason "<reason>"`
   Use `plect judge request-changes <work-session> <task-instance> <judge-id> --reason "<reason>"`
   for any unmet criterion and name the missing work in the reason.
5. Record that you finished reviewing (this is what lets plect close out this
   review session — there is no third party to judge a reviewer, so
   completion is your own self-report): find this review's own current
   `revision` with `plect status "$PLECT_SESSION_NAME" --json` (the `revision`
   under `observed.state` on this session's own task instance, not the target
   session's — `$PLECT_SESSION_NAME` is this session), then run
   `plect state set "$PLECT_SESSION_NAME" --instance goal_review#1 '{"verdict_revision":"<that revision>"}'`
   (adjust the instance id if `plect status "$PLECT_SESSION_NAME" --json` shows a different one).
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

Additional instructions from the dispatcher (may be empty): {{ inputs.instruction }}
