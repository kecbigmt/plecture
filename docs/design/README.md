# Design documents

Documents in this directory describe the system's current state in present
tense: how it is specified to behave.

Do not put before/after transitions, migrations, superseded procedures, old
names, or rename verification here. ADRs record decisions and transitions;
implementation issues track execution evidence.

A design that introduces or changes configuration must show the resulting
config schema as a worked example plus validation rules for the new or changed
configuration section: required keys, all-or-nothing groups, and types. Prose
agreement alone does not fix a config shape.

Plecture configuration is a declarative wiring language. Its constructs
express structure, binding, and wiring: schemas, references, forwarding,
outputs tables, and nesting. Computation belongs in explicit hooks and core;
the existing template conditionals are the baseline. New control-flow
constructs, loops, user-defined functions, or other config-language constructs
require an ADR with precedent, observed need, a worked example, and validation
rules.

Prefer concrete examples over prose restatement. A design document conveys the
design core, not an exhaustive specification; long prose bases rot.
An example shows the mechanism or shape and elides incidental details whose
change would not change the design.
