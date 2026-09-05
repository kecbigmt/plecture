# Population query means selection

## Context

[Standing session dispatch from resource observation](2026-09-05-standing-session-dispatch.md)
Decision 4 derives a population's membership lifecycle from the means its
resource observer's query declares — the evaluator runs every means the
observer offers, with no field to choose among them — and deliberately
forbids opting out of poll: "If the observer query declares poll, every
population using it accepts poll's membership authority and must set
`poll_every`; an entry cannot opt out while retaining subscribe."

That absence of choice has a concrete consumer on the subscribe side. The
shipped `official.github.pull_request` observer declares both means; its
subscribe action is a webhook receiver that is fail-closed by design and
exits non-zero at startup without a configured secret and a reachable
endpoint ([issue #390](https://github.com/kecbigmt/plecture/issues/390)). A
deployment that wants poll-only operation runs that subscribe action anyway
under the resident population supervisor, which becomes a permanent
bounded-backoff restart loop plus a stream of
`plect.workflow_population.failure` events, even though poll alone fully
serves the population. The only workaround today is standing up webhook
infrastructure the deployment does not need, or forking the observer into a
private catalog to drop the `subscribe` table — which also forces every task
written for that observer to be re-pointed, since a population's
`session.task` must resolve to the exact same observer as
`resource_observer`.

The mirror problem exists for poll: a deployment whose poll is too
expensive (rate limits, slow enumeration) or whose conditions the query
cannot express is likewise forced to fork, and Decision 4 forbids solving it
by the same mechanism it grants for subscribe.

## Decision

Add one required, non-empty field to a population entry, `uses`: an array of
the query means (`"poll"`, `"subscribe"`) this entry runs. Each keyword must
be one the resolved observer's query declares; duplicates, an empty array, an
omitted field, or an undeclared keyword are load errors naming the entry.
`uses` reuses the workflow vocabulary's existing selection word (a node's
`uses`, a channel's `uses`), here selecting query means instead of an effect
or channel definition.

Requiring the array rather than defaulting it serves two purposes:
explicitness — a population reads completely without cross-referencing the
observer's definition to know which means actually run — and no silent
activation — a means a plugin observer adds to its query later cannot start
running in an existing deployment that never declared an opinion about it,
matching the same explicit-opt-in philosophy Decision 4 already applies to
`auto_down` and `auto_destroy` defaulting to false.

Semantics derive from the effective selection, not from what the observer
additionally declares: `poll` selected makes it the membership and absence
authority for this entry and requires `poll_every`; `poll` not selected
follows the subscribe-only rules and requires `expire_after`, even against an
observer whose query also declares poll. **This withdraws Decision 4's
prohibition on opting out of poll and downgrades it to a recommendation**: an
enumerable resource should normally keep poll selected, because deselecting
it trades away absence detection and missed-event repair (a subscribe
delivery gap is never independently corrected) for `expire_after`-based
quiescence semantics. Nothing else in Decision 4 is revisited — subscribe was
already acceleration layered on poll's authority, never authority itself, and
remains so.

Runtime: the evaluator starts only the means named in `uses`. The engine's
own poll-tombstone-suppresses-subscribe check and its poll-vs-expire-after
branch in the sweep both key off the entry's current `uses`, not off what the
observer declares, so they already answer correctly for every selection
without a separate mechanism.

**Reload semantics when `uses` changes.** An entry keeps its owned sessions
and their provenance across a reload that changes its selection — removal or
replacement of policy is never evidence about a resource, per Decision 4's
existing rule for population removal — but membership authority is
re-derived from the new selection alone. Because that authority check is
evaluated live against the current definition rather than cached at some
earlier point, a resource poll-tombstoned under a since-deselected `poll` is
not locked out waiting for a poll snapshot that will never arrive again: the
next accepted subscribe appearance admits it under the new selection's
authority directly. Symmetrically, re-selecting `poll` after a subscribe-only
period runs an immediate poll snapshot on evaluator restart, which
authoritatively reconciles membership right away rather than waiting out the
ordinary cadence.

`plect workflow show` reports the entry's `uses` selection, so the choice is
visible without cross-referencing the observer definition.

## Consequences

A poll-and-subscribe observer becomes usable in poll-only deployments without
forking it, and — newly, since this decision reverses Decision 4's poll
prohibition — in subscribe-only deployments too, restoring the population
feature's premise that a resource observer is the sole recognition, query,
and observation authority regardless of which means a given deployment can
or wants to operate. A population's fingerprint already includes the whole
population entry, so changing `uses` restarts the entry's evaluator like any
other population edit, which is what makes the reload semantics above the
evaluator's ordinary restart path rather than a special case.

Because `uses` is required, every population declaration written against the
prior (means-implied-by-observer) surface gains a `uses` line; no known
deployment declares populations yet, so this is a pre-adoption correction of
the v2 config-language dialect rather than a breaking change requiring a
migration procedure — no `schema_version` bump accompanies it.

The worked examples inside
[the standing-session-dispatch ADR](2026-09-05-standing-session-dispatch.md#poll-and-subscribe-observer-sketch)
predate this field and are missing `uses`; that ADR is accepted, and this
project's convention permits only a supersession-frontmatter edit to an
accepted ADR's file, not a body correction. They are illustrative sketches,
not an executable specification — `docs/language/workflows.md` carries that
role, quoting a fixture verbatim, and is updated by this decision. A reader
comparing the two should treat the language chapter as current and the ADR's
sketches as historical illustration of the shape existing before this
decision.

## Alternatives considered

**Let a subscribe executable idle instead of failing when unconfigured.**
Decision 4 states "termination or non-zero exit is a source failure and
restarts under the resident supervisor's bounded backoff." An executable
that silently idles instead of exiting non-zero when it cannot actually
subscribe blurs that contract and hides real misconfiguration from a
deployment that does want subscribe running. A declared selection keeps
fail-closed behavior intact for the deployments that need it and makes the
poll-only or subscribe-only choice an explicit, auditable one instead of an
executable's implicit self-diagnosis.

**A boolean `subscribe = false` opt-out, forbidding poll opt-out as before.**
The initial shape proposed for this issue. Rejected in favor of the general
`uses` selection: a boolean only solves the subscribe side and leaves the
mirror poll problem unaddressed, adds a second piece of vocabulary
(`subscribe = false` alongside the means an observer declares) where one
selection field already says the whole thing, and does not extend cleanly if
the language ever grows a third query means.

**Keep poll opt-out forbidden while adding subscribe opt-out.** Rejected:
once an explicit selection field exists, forbidding one specific value
combination (`uses` present without `poll`) is an arbitrary carve-out with no
load-bearing justification distinct from an ordinary configuration
trade-off, which the recommendation in the Decision section states instead
of enforcing.
