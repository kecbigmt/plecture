# Resident daemon command vocabulary

## Context

The resident plect process owns more than event delivery. It hosts the durable
event log API, channel dispatch followers, tick reactors, healthcheck sweeps,
plugin service supervision, and periodic config re-resolution with deadman
detection. The old command spelling named only the event delivery part, so the
operator-facing CLI surface understated the process role.

Plecture still needs one host-owned resident process. Splitting the daemon from
the CLI is not justified by the current lifecycle, privilege, or size profile.

## Decision

The resident process command is `plect serve`.

`serve` is a verb that fits the existing single-binary CLI grammar and names
the process as the local runtime server without committing the top-level command
to one internal follower. The event delivery follower can still use bus
vocabulary where it specifically names the event bus API, socket variables, or
event contract.

The plugin service supervisor is named by plugin service responsibility, not by
the event bus. Plugin-owned `[[services]]` daemons are supervised by the
resident process started with `plect serve`.

## Consequences

Operators update host service definitions, including systemd `ExecStart`
commands, from `plect bus serve` to `plect serve`. The socket path and
`PLECT_BUS_SOCKET` / `PLECT_BUS_TOKEN` names remain unchanged because they name
the event bus API specifically, not the resident process as a whole.

The top-level CLI help lists `serve` as the resident daemon command and does
not advertise `bus` as a user-facing command group.

Pre-1.0 compatibility policy applies. There is no `plect bus serve` shim.

## Alternatives considered

### `plect daemon`

`daemon` names the resident nature of the process, but it reads as a noun in a
command set whose lifecycle actions are verbs such as `up`, `down`, `destroy`,
and `serve`. It also over-emphasizes Unix process shape rather than the role
operators ask the command to perform. `serve` keeps the command action-oriented
and still describes a long-running server process.

### Separate daemon binary: `plectd`

A separate binary was rejected. Single responsibility at the binary layer is
not absolute: Herdr and Caddy run daemon and CLI roles in one binary. The
Docker and kubectl split correlates with system-service privilege and lifecycle
separation that Plecture does not need yet.

Binary size is not material. The measured current single binary is about 14 MB,
and a split would be about 24 MB total; both are operationally insignificant.

The single binary prevents CLI and daemon version skew. A split binary would
introduce skew as a new failure mode. Revisit a split during team-adoption
infrastructure work if the daemon and CLI acquire genuinely different update
cadences.

### Keep `bus` as a hidden compatibility command

A hidden compatibility command would reduce one migration step, but Plecture is
pre-1.0 and does not carry backward-compatibility shims. Operators migrate the
host unit once, and command help stays aligned with the supported surface.
