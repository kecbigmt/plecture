---
supersedes:
  - 2026-08-16-plugin-service-lifecycle
  - 2026-08-17-plugin-boundary-contracts
---

# Operator-declared plugin services

## Context

The service lifecycle decision made enabling a plugin sufficient to start its
resident processes. A plugin manifest can declare only one instance of each
service. The supervisor also passes its environment to every child, so a Slack
adapter can receive GitHub credentials and two Slack adapters can receive each
other's credentials. Enabling a plugin can start a resident process; a later
explicit `plect plugin update` then replaces the code of that already-running
process without a separate service declaration.

The Slack adapter supports multiple instances at the executable level. Its
local HTTP endpoint has no authentication. Deployments also use both a single
mention dispatcher and populations that create one session per mentioned
thread or labeled pull request. The resident service boundary must support
both delivery policies and protect its local APIs.

### Local API threat model

The local API must reject requests from web content in a browser running on
the same host. Agents can direct a headless Chromium at arbitrary pages, and a
page can try DNS rebinding or a simple cross-origin POST to a loopback API.
Other OS users on the host must also be unable to call the API.

Code running as the same OS user as plect is outside this boundary. An agent
session with a shell or plugin code under that user can read service env files
and `/proc/<pid>/environ`, and can use any socket the user owns. Containing
such code requires a separate OS user or a sandbox, which is a separate
decision.

## Supersession scope

This decision replaces these decisions of
`2026-08-16-plugin-service-lifecycle`:

- `## Decision`: `plugin.toml` service declarations and start on plugin enablement.
- `## Decision`: plugin-provided service environment and `required_env` gating.
- `## Consequences`: the service identity and CLI surface derived from plugin service declarations.

It replaces these decisions of `2026-08-17-plugin-boundary-contracts`:

- `## Decision`: retained plugin-owned `[[services]]` declarations.
- `## Consequences`: manifest parsing and validation for plugin-owned `[[services]]`.

## Decision

Plugins mark resident-capable executables with `service = true` in
`[[executables]]`. The marker is metadata only. A `plugin.toml` containing
`[[services]]` fails to load, with an error pointing to the service migration
procedure. The plugin manifest format advances to schema version 3 to make
the break explicit; the catalog manifest format remains version 2. Enabling
or updating a plugin does not create a new service. `plect plugin update` is
the operator action that authorizes replacing the code of an already declared
service; the supervisor restarts that instance on a content change.

The operator declares instances in user-owned `<config-home>/services/*.toml`.
Each `[[services]]` table has a unique operator-chosen `name`, a fully
qualified `executable` (`<catalog>/<plugin>/<executable>`), optional `args`,
and an `env_file`. The executable must exist in an enabled plugin and
carry `service = true`, or configuration loading fails. A file may contain
multiple service tables. Names are unique across the entire user config,
including separate files; they use lowercase ASCII letters, digits, and
hyphens, start with a letter, and are at most 64 characters. A declaration
names an instance, so several names may use the same executable.

`env_file` is an absolute path to an operator-owned file of `KEY=VALUE` lines.
Blank lines and lines beginning with `#` are ignored. Duplicate keys, invalid
names, unreadable files, and malformed lines fail configuration loading. The
file must be readable only by its owner. Relative paths and environment
interpolation are rejected. A service receives only these entries and the
runtime values plect supplies for its own APIs and instance paths. It does not
inherit the supervisor environment. Operators put any required `PATH`, `HOME`,
credential, or provider-specific setting in that service's file. The executable
validates its own required inputs; there is no `required_env` gate. A
conflicting `PLECT_*` runtime key in an env file is a load error.

An optional `[services.config]` table holds plugin-specific, non-secret
settings. The service-marked executable declares a `config_schema` path in its
manifest. plect validates the table against that schema, writes the validated
config to a private file in the service state directory, and passes its path
as `--config` to the service. Core does not interpret the table. Credentials
belong only in `env_file`. Plugin authors must keep secret settings out of the
schema; plect cannot infer whether an arbitrary schema field is a secret.

No directory or data store is shared between a service and action processes.
The Slack adapter owns its subscriber and unbound-mention registration state
in its private service directory. The GitHub watcher owns its subscription
registry, rate budget, and token cache there. Hooks, channels, tasks, and
population queries use the selected service's Unix socket to access that
state. The watcher exposes subscription and token/rate-budget operations
needed by its hooks and query helpers through that socket; hooks do not write
`subscriptions.json`.

GitHub resource `observe` remains callable without a session or a declared
watcher service. Its `github-issue-pr` action does not resolve a service and
does not use `github-watcher gh-api` or the watcher's private cache. It makes
GitHub requests with the action process's separately configured credentials,
using a process-local rate budget and App token cache when App auth is used.
The action does not read or write `$XDG_DATA_HOME/github-watcher`. The issue
and pull-request observer declarations stop passing `--watcher-bin`. Query
helpers are different: a resident population explicitly names its watcher
service and uses its socket for token and rate-budget operations.

`restart` defaults to `on-failure`, with `never` available. `health` defaults
to `{ type = "process" }`; that is the only health kind in this decision.
`args` defaults to an empty list. The operator can override the two policies
per service. The resident process supervises declared instances and records
their state, pid, restart count, and last error. It restarts a declared instance
when its plugin content changes. Plugin removal makes a referencing service
invalid at the next config load; it does not silently switch executable.

For one Slack App, the user file contains one block:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-primary.env"

[services.config]
channel_id = "C0123"
mention_session = "operations/_orchestrator"
allowed_user_ids = ["U0123"]
```

For two Apps, the file contains two blocks that reference the same executable
but use separate credentials and state:

```toml
[[services]]
name = "slack-primary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-primary.env"

[services.config]
channel_id = "C0123"
mention_session = "operations/_orchestrator"
allowed_user_ids = ["U0123"]

[[services]]
name = "slack-secondary"
executable = "official/slack/slack-adapter"
env_file = "/srv/plect/config/service-env/slack-secondary.env"

[services.config]
channel_id = "C0456"
allowed_user_ids = ["U0456"]
```

`mention_session` is the one optional default-recipient field for each Slack
instance. A mention is routed in this order:

1. A subscribed thread session receives it. This includes sessions created by
   a per-thread population and handles their follow-up mentions.
2. Each durably registered unbound-mention population whose `channel_ids`
   includes the mention's channel receives it through the feed.
3. If no registration matches and `mention_session` is set, the adapter
   publishes the mention to that session with `plect event publish`.
4. Otherwise the adapter drops it and logs its channel, thread, and reason.

The adapter persists each population consumer's identity, channel filter,
pending mentions, and delivery watermark in its private state. Registration,
including a changed channel filter, is durable before the consumer reads the
feed. A disconnected consumer remains registered; the adapter holds matching
mentions and replays them on reconnect until acknowledged. A live connection
is never the routing authority. The population evaluator invokes the query's
unregister action with the old service name and stable consumer identity when
removing a population; closing its connection does not unregister it. An
unregister failure retains the registration for retry and reports the failure.
Overlapping registrations each receive the mention. A registration can
coexist with `mention_session` on one instance. The default receives only
mentions for which no registration matches. A population's later user filter
or failed admission does not send the mention back to the default. The
bot-scoped `mentions` resource and instance registry are removed.

Each service gets a private state directory derived from `name`, under
`<data-home>/services/<name>/`, and a Unix socket path `api.sock` in
that directory. plect creates the directory with mode `0700`; the adapter
creates the socket with owner-only mode `0600` before accepting connections.
plect passes `PLECT_SERVICE_STATE_DIR` and `PLECT_SERVICE_SOCKET` to the child.
It also passes `PLECT_BUS_SOCKET` and, when needed, `PLECT_BUS_TOKEN` for the local
event bus. A service's own `env_file` can set its adapter-specific config and
secrets but cannot override these `PLECT_*` runtime values. If the resolved
socket path exceeds the platform's Unix socket limit, loading fails with an
actionable path error. The service name is the stable state identity; renaming
it starts a distinct instance with distinct state. The 64-character name
limit is a syntax bound, not a promise that every name fits every data-home
path; the socket-path check applies to the resolved path.

Local APIs used by hooks, including the Slack adapter API, listen only on that
Unix socket. The browser's network APIs cannot dial a Unix socket, so DNS
rebinding and cross-origin POSTs have no TCP listener to reach. The directory
and socket modes exclude other OS users. No loopback proxy is supplied, since
one would reopen the browser path.

Every caller of the local API uses a Unix-socket client. The shipped Slack
`slack`, `stream`, and `status` channels remain `exec` channels invoking
`curl`: their configured `base_url` input becomes a service-name input,
and their argv adds `--unix-socket` with the path resolved by plect while the
request URL becomes a fixed `http://localhost/<endpoint>` transport
placeholder. The `thread` resource's query input and the `slack_thread` task
input likewise name a service. Thread subscribe/unsubscribe hooks resolve the
service named by the workflow input that selected it. A population's literal
`query.service` selects its instance; the population-created session receives
that name as a workflow input, so its resource hooks and task use the same
instance. A manual session must supply the workflow's service input. The
GitHub watcher subscription hooks use the same rule. plect resolves a name
against the loaded `[[services]]` table and projects its socket path into the
action; an unknown literal name fails config validation, while a dynamic
unknown name or stopped service fails the request for retry. A path supplied
by an action or user input is not an
authority. These are generic core name-resolution mechanics and plugin-side
client changes, with no new channel primitive. All clients run as the
service-owning OS user; a caller under another user needs a separately
designed mediation boundary, not wider socket permissions. A provider's
public ingress, such as a webhook receiver, keeps its own external
authentication and is not this local hook API.

A per-thread population binds its query to a Slack service and durably
registers its channel filter:

```toml
[[thread_work.populations]]
name = "mentions"
uses = ["subscribe"]
expire_after = "8h"

[thread_work.populations.query]
service = "slack-secondary"
channel_ids = ["C0456"]

[thread_work.populations.session.inputs]
service = { from = "query.service" }
```

The `thread.query.subscribe` action registers with that service through its
resolved socket and reads its durable `/unbound-mentions` feed. Its stable
consumer identity comes from the workflow population, not a connection. It
yields one thread resource per mention and acknowledges the feed item only
after the population durably records its appearance. The created session's
thread subscription receives the initiating mention
through the adapter's pending-mention catch-up. This is a subscribe-only
population because mentions have no absence snapshot.

A per-PR population can use the same declared-service model:

```toml
[[services]]
name = "github-reviews"
executable = "official/github/github-watcher"
args = ["serve"]
env_file = "/srv/plect/config/service-env/github-reviews.env"
```

The pull-review workflow declares:

```toml
[[pull_review.populations]]
name = "labeled_pulls"
uses = ["poll", "subscribe"]
poll_every = "5m"

[pull_review.populations.query]
service = "github-reviews"
repositories = ["example/project"]
labels = ["agent-review"]
state = "open"
draft = false

[pull_review.populations.session.inputs]
service = { from = "query.service" }
```

The GitHub query polls matching pull requests and uses the externally
authenticated webhook receiver for early appearances. Its polling helper
resolves `github-reviews` for the watcher's token and rate budget API; GitHub
remains the membership authority. Each created review session's resource
subscription resolves the same service and calls its socket API. The watcher
retains its subscription state privately.

### Open question: default-recipient target

Should the single `mention_session` field become a target described by
workflow, resource, and task, so the adapter can start or reuse the session
that workflow resolves for the resource and set up a task with the mention?
The recommendation for this ADR is to keep `mention_session` as one field
until that behavior is specified separately. A population with one constant
session key covers only a fixed resource and repeated appearances. A target
that chooses a resource and sets up a task for each mention has additional
session admission, task identity, and retry semantics; calling it a
population would hide those decisions. If the owner chooses the richer
target, replace this one field and specify those semantics in a subsequent
decision, without adding a parallel default-recipient field.

`plect service list` displays declared services with name, executable, state,
pid, and restarts. `--all` also displays one `available` row for each
service-marked executable in an enabled plugin that no declaration uses. Its
name and pid are empty. When several declarations use one executable, it has
several declared rows and no `available` row. There is no `plugin show`
addition in this decision.

With the two Slack services running and an enabled but unused GitHub watcher,
`plect service list --all` shows:

```text
NAME             EXECUTABLE                           STATE      PID    RESTARTS
slack-primary    official/slack/slack-adapter         running    4102   0
slack-secondary  official/slack/slack-adapter         running    4103   1
                 official/github/github-watcher       available
```

`channel-server` remains a per-session MCP subprocess launched by Claude Code.
It is not marked as a resident service and has no operator service declaration.
This preserves its session-specific socket and lifecycle.

## Consequences

Plugin installation makes resident-capable programs available without running
them. Every running service has an explicit user-owned declaration and a
separate environment. The supervisor keeps one host-owned resident process,
`plect serve`, while service identity becomes the operator-chosen name.

The implementation removes manifest `[[services]]` and `required_env`, adds
user service config loading, updates the supervisor and CLI, and adds schema
and conformance fixtures for the user-service and manifest changes. The
`service.socket`, `query.<key>`, `population.id`, and query `unregister`
language additions need schema, conformance fixtures, and behavior tests for
unknown literal names, unknown dynamic names, stopped services, replay,
acknowledgement, and unregister failures. The Slack `thread` query replaces
`base_url` with required `service` in its input schema; the GitHub pull-request
query adds required `service` to its input schema. Both schemas reject unknown
keys. The evaluator retains the removed population's query inputs and service
name until explicit unregister succeeds. Its subscribe protocol sends an
acknowledgement to the query process's stdin only after an appearance with an
opaque `delivery_id` is recorded durably. The query process forwards that
acknowledgement to the service socket; writing an item to stdout alone is not
an acknowledgement. The Slack thread query item schema adds the required
`delivery_id` string. The GitHub observer loses shared rate-limit coordination
with the watcher and may mint an additional App token for each process
invocation. That cost preserves standalone observation when no watcher service
exists and avoids shared files.
The breaking change ships with a `docs/migrations/` procedure that begins with a
backup and gives one user-config block reproducing each resident shipped
service:
`slack-adapter` and `github-watcher`. The Slack block needs neither `HOME` nor
`XDG_STATE_HOME` when its config and state paths are explicit. The watcher
block supplies `HOME` for `gh` authentication and `XDG_CONFIG_HOME` if the
operator uses a nondefault `gh` config home. Its subscriptions, rate budget,
and token cache live in its private service state directory. The migration
backs up and moves existing subscriber and watcher state before starting the
new services; hook processes switch to the socket API. Its `channel-server` section
explains that Claude Code already starts it per session and needs no resident
declaration. There is no compatibility shim.

## Alternatives considered

### Per-service shared secrets for local HTTP

HTTP bearer tokens would preserve the adapter's TCP transport, but each hook
needs a reliable way to retrieve the right instance's token. Pointing hooks
at service env files couples them to operator credential storage. Unix socket
permissions already give same-user hooks the necessary local access.

### Shared rendezvous directory

A shared directory for services and action processes would reopen the
per-service boundary through shared files and require a new cross-plugin
namespace. Service-name resolution gives callers the right socket without
sharing state.

### Requiring a watcher for observation

Binding every GitHub `observe` action to a watcher service would make
standalone `plect resource status` depend on an optional resident process.
Choosing an ambient default watcher would reintroduce a second service-name
authority outside the resource contract. Process-local auth and rate state
keep observation independent at the stated extra-token and coordination cost.

### Deriving a TCP port from the service name

A name cannot reliably map to a free port. A deterministic private socket path
avoids collisions and requires no port allocation or shared listener.

### Resident channel-server

The channel-server connection and permission nonce state belongs to one Claude
Code session. A resident instance would require a different multiplexing
contract and would not reproduce the session-specific MCP lifecycle.
