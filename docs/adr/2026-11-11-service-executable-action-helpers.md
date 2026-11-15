---
supersedes: 2026-11-09-operator-declared-plugin-services
---

# Service executables as action helpers

## Context

A service-capable executable can also provide a short-lived action command.
GitHub resource observation uses the watcher executable for authenticated API
requests even when no resident watcher is configured. Requiring an operator
service declaration for that action would make standalone observation fail.
The service marker only says the executable can be supervised; it does not
declare that a resident process exists.

## Supersession scope

This decision replaces one decision of
`2026-11-09-operator-declared-plugin-services`:

- `## Decision`: the requirement that an action invoking a service-marked executable without an operator declaration fails.

## Decision

![An action invoking a service-marked executable runs as a helper with its action environment and the plugin cache when no operator declaration exists, replacing the rule that it fails; a valid declaration adds its env-file values and config, and an invalid one for this executable fails the action. Splitting each helper into its own manifest executable is rejected.](assets/2026-11-11-service-executable-action-helpers-decision.svg)

An action may invoke a service-marked executable without an operator service
declaration. It receives the plugin's shared cache directory and its ordinary
action environment. It receives no service env-file values. An action that
explicitly refers to `service.config` still fails when there is no declaration.

When a declaration exists, the action receives that declaration's private
env-file values and can refer to its validated config as specified by the
operator-services decision. An invalid declaration for the executable fails
the action; errors in other service declarations do not block it.

## Consequences

Standalone resource observation can use the watcher's helper commands without
starting a resident service. A helper that needs service credentials still
requires a valid operator declaration. The service marker does not grant
access to another service's credentials.

## Alternatives considered

- Require a declaration for every action invocation. This prevents standalone
  resource observation when the resident watcher is not configured.
- Split each helper command into a separate manifest executable. This creates
  duplicate executable identities for one binary without a distinct lifecycle
  or credential boundary.
