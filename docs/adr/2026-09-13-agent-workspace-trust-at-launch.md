# Recording workspace trust before an unattended agent launch

## Context

An agent CLI launched into a directory it has never run in asks the operator
whether that directory is trusted, before it will do anything else. The prompt
is modal, it is not covered by the CLI's permission mode, and the CLI exposes
no flag to answer it: the only thing it consults is a per-directory record in
a state file under the operator's home directory.

A dispatched session has no operator at the pane. Its launch types a command
line into a terminal and then sends Enter repeatedly until the CLI registers
itself, because the CLI's own startup can stall on a banner that nothing but
Enter clears. Those keystrokes reach the trust prompt too, and the prompt's
default choice is refusal — so the first Enter answers "no" and the CLI exits.
The launch then spends its whole detection timeout sending Enter into a bare
shell prompt before failing, and every subsequent `plect up` against that
directory fails the same way. The observed failure was a session that never
started while its pane accumulated hundreds of empty prompts.

The state file holding the record is one the CLI itself rewrites in full from
every running session, with no cross-process lock. A deployment running many
sessions at once therefore cannot treat "edit that file" as a free operation:
each whole-file write can discard a concurrent one.

## Decision

The agent launch effect records the session's workspace directory as trusted
before it types the launch line, reading the directory from the `workspace.dir`
root rather than from an input, so no workflow wiring is required and a session
with no workspace provider is skipped rather than failed.

The write is a convergence, not an assignment. The effect reads the current
record first and returns without writing when the directory is already trusted,
so the window in which a concurrent whole-file writer could lose an update
exists only at a directory's first launch. When it does write, it renders the
merged document to a scratch file and renames it into place, matching how the
CLI writes the same file, so no reader observes a partial one.

A trust record that cannot be written fails the launch immediately, before the
launch line is typed. The alternative is to type a command line whose only
possible outcome is the detection timeout.

## Consequences

A directory produced by a workspace provider is trusted by the agent CLI from
the moment a session launches into it, including for a human who later attaches
to that pane or opens the CLI in that directory themselves.

The effect now writes to a state file it does not own, which is a coupling to
that CLI's storage layout: a change to where the CLI keeps the record breaks
the launch again, visibly, as a hang rather than an error. The scenario record
for the effect pins the observable halves of this — the converged document, and
the absence of a scratch file when nothing needed changing.

## Alternatives considered

**Leave it to the operator.** Every new workspace directory would need a manual
first launch before automation could use it. This makes the failure a standing
operational step, and it fails in the least legible way available: a hang, at
dispatch time, in a pane nobody is watching.

**Make it an opt-out parameter.** There is no configuration under which the
prompt can be answered: the surface that would answer it is the same keystroke
stream that refuses it. A parameter to disable the record would only offer a
way to reproduce the hang, so it is a flag with no consumer.

**Answer the prompt instead of pre-empting it.** The launch would have to
recognize this specific prompt in the pane's rendered output and send the
keystrokes that move the selection. That couples the launch to the CLI's
screen layout, which is less stable than its state file, and it would have to
distinguish this prompt from every other thing the CLI can display at startup.

**Let the workspace provider do it.** The provider would then have to know
which agent CLI the workflow launches and where that CLI keeps its state. The
record belongs to the agent, so the agent's plugin owns it.
