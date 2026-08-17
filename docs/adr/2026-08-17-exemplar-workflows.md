# Exemplar workflows are copied into user-owned config

## Context

The OKF goal-review workflow composition references effect, task document, and
channel ids outside the OKF plugin. Some of those ids belong to other provider
plugins, and some are local user configuration. Carrying that composition as
runnable plugin config makes the plugin unresolvable on a fresh install and
blurs the boundary between a capability package and a team's workflow policy.

The plugin boundary decision already says core must not grow provider-specific
knowledge or plugin dependency metadata. The missing distinction is where useful
cross-plugin starter workflows live when they are not runnable plugin content.

Workflow composition chooses models, runtime chains, guards, conversation
delivery, and review policy. Those choices are owned by the operator or team
running the workflow.

## Decision

Plecture treats running workflows as user-owned config. Catalogs do not mount
cross-plugin workflows as runnable workflow definitions.

Catalogs may ship exemplar workflows: inert copy-templates with metadata that
states which enabled plugins in the registered catalog provide each referenced
effect, task document, channel, workspace provider, or resource observer.
`plect workflow init <name> --from <catalog-alias>/<exemplar-id>` copies one
exemplar into user-owned workflow config after verifying all references and
explicit placeholders.

The OKF goal review composition is catalog-level exemplar workflow content, not
plugin-mounted workflow content. Local-only references such as the host-owned
goal-review task document, team Slack-thread effect, environment-producing
effect, and initial-instruction effect are scaffold-time placeholders, not
hidden runtime dependencies.

Capability plugins must not ship runnable workflows that reference ids outside
the same plugin. Cross-plugin compositions belong in exemplar workflow packages
or in user-owned workflow config.

The detailed package shape, scaffold command, validation rules, OKF relocation,
and non-goals are specified in
[`docs/design/exemplar-workflows.md`](../design/exemplar-workflows.md).

## Consequences

Catalogs can still teach useful compositions without making those compositions
runtime dependencies. A user can inspect an exemplar, copy it, edit it, commit
it to their own config, and decide when it changes.

The scaffold path needs catalog-exemplar discovery, exemplar metadata parsing,
copy-time reference verification, placeholder replacement, and destination
collision checks. It does not need plugin dependency resolution, version
solving, auto-enable behavior, or auto-update subscriptions.

Plugin-mounted cross-plugin workflows need a migration to exemplar packages
when the scaffold implementation lands. Because Plecture is pre-1.0, the
implementation carries a one-time migration procedure rather than a
compatibility shim.

Teams that want centrally managed standard workflows publish user-owned config
overlays through their own repository or configuration-management system. Those
updates are explicit team policy updates, not catalog updates flowing into
running workflows.

## Alternatives considered

### Keep workflows mounted from capability plugins

Mounted workflows are convenient for demos, but they make a capability plugin
depend on ids it does not own. A fresh install can enable the OKF plugin and
receive a workflow that cannot resolve without a separate, implicit set of
runtime and local policy definitions.

### Add plugin dependencies

Dependency metadata could make a workflow-owning plugin name the plugins it
needs, but that hides the consumed contract behind package identity. It also
pulls Plecture toward dependency closure, version ranges, and auto-enable
behavior before the catalog model needs them.

### Ship auto-updating composition packs

Auto-updating packs keep teams near catalog defaults, but they let upstream
policy changes flow into running workflow behavior. Review gates, model choice,
guard placement, and handoff rules are operational policy and must change only
through the user's or team's own config process.

### Treat copying as a fallback

Copying is not a failure mode here. It is the ownership boundary. After the
copy, the workflow is normal user config because the operator is responsible for
the policy it encodes.

### Document manual copying only

Manual copying plus first-use workflow validation would avoid new scaffold
commands and metadata, but it reports missing ids only after the user has
already copied an opaque file into their config. It also cannot distinguish a
catalog-owned reference from a team-local placeholder, so the OKF starter would
still rely on prose to explain which failures are expected setup work and which
ones indicate a broken exemplar.

### Support diff-against-exemplar immediately

A comparison command may help users inspect drift later, but it is not required
to establish ownership or scaffold correctness. Adding it now would expand the
surface before the basic exemplar package and copy-time verification contract is
implemented.
