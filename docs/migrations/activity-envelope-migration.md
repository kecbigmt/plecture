# Activity envelope migration

This migration covers the activity probe envelope's move from the `status`
enum to the fingerprint-centric shape. See
[`../adr/2026-08-18-activity-envelope-fingerprint-centric.md`](../adr/2026-08-18-activity-envelope-fingerprint-centric.md).

The change is intentionally breaking. Plecture is pre-1.0, so probes migrate
once instead of relying on a compatibility read. A probe still printing a
`status` envelope carries no `fingerprint`, so it is rejected as invalid probe
output: it contributes nothing and `plect status` warns about it every cycle.
Nothing is silently reinterpreted.

Only probes are affected. No persisted state changes shape, so there is no
data-directory backup step; the one file worth clearing is each agent
plugin's activity record, covered below.

## Probe changes

In every task definition declaring `[health].activity` — global
(`~/.config/plect/tasks/`) and repo overlay (`.plect/tasks/`) — rewrite what
the probe prints:

```json
// before
{ "status": "active", "fingerprint": "abc", "observed_at": "..." }
{ "status": "idle",   "fingerprint": "abc", "observed_at": "..." }
{ "status": "none" }

// after
{ "fingerprint": "abc", "observed_at": "..." }
{ "fingerprint": "abc", "silence_expected": true, "observed_at": "..." }
(nothing — exit 0 with empty stdout)
```

Translate as:

| Old envelope | New envelope |
|---|---|
| `"status": "active"` | drop the field; keep `fingerprint` |
| `"status": "idle"` | replace with `"silence_expected": true` |
| `"status": "none"` | print nothing and exit 0 |

`fingerprint` is now required in any envelope. A probe that reaches a point
where it has no fingerprint to report exits 0 without printing, rather than
printing an envelope that says so.

`silence_expected` is the pardon and the only field that changes what core
concludes, so set it only when the probe really knows the surface's stillness
is intended — the turn ended, the queue drained. Omitting it is always safe.

## Exit-code changes

The exit code now reports the health of the probe itself, independently of
what it printed:

- **Exit 0** — the probe is well. Its stdout decides whether it contributed.
- **Non-zero** — the probe is broken. It contributes nothing, and the health
  report carries a `probe_error` entry with the command, the exit code, and a
  stderr digest, which `plect status` prints as a warning.

A probe that used to exit non-zero to mean "nothing to report" must exit 0
with empty stdout instead, or it will warn on every health cycle.

## Clearing stale agent activity records

The shipped `claude` and `codex` probes read a record their own hook writes,
and records written before this change store the old `status` field. The probe
reads a missing `silence_expected` as `false` — the safe direction, no
pardon — so nothing breaks, but the first turn boundary after the upgrade
rewrites the record in the new shape anyway. To drop the stale records up
front:

```bash
rm -rf "${XDG_STATE_HOME:-$HOME/.local/state}/plect/claude-activity" \
       "${XDG_STATE_HOME:-$HOME/.local/state}/plect/codex-activity"
```

A cleared record means "no boundary crossed yet", which the probe reports as
exit 0 with empty stdout — not a stall.

## Verifying

Run a health cycle and confirm no probe warnings:

```bash
plect status <session>
```

A line of the form `warning: <instance>: activity probe: ...` names a probe
still on the old shape (or otherwise broken); anything else means every
declared probe either contributed or reported no basis cleanly.
