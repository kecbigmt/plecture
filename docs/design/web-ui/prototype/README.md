# Plecture Web UI reference prototype

This is a frozen, runnable visual reference dated 2026-09-05. It preserves the
session-first workspace, compact name/resource header, session hierarchy,
flat view tabs, and shared detail pane.

Read the [design](../../web-ui.md) for normative behavior and the
[architecture decision](../../../adr/2026-09-05-web-ui-client-server-boundary.md)
for production technology choices. Implement the production UI separately;
reuse individual components and styles where useful.

## Run

Requires Node.js 22.13 or later and pnpm 10.33.0 (pinned in package.json).

```sh
cd docs/design/web-ui/prototype
pnpm install --frozen-lockfile
pnpm dev
```

Open the localhost URL printed by Vite. To verify the reference:

```sh
pnpm test
pnpm build
pnpm preview
```

The development and preview servers bind to loopback. No Plecture installation,
credentials, backend service, deployment account, or environment file is needed.

## What is preserved

- Tree expansion and selection, session switching, search, and view selection.
- Conversation-centered events and links to related sessions and records.
- Tasks and node inspection through the shared detail pane.
- Graph zoom controls and a read-only terminal capture surface.
- Bounded mock Home activity and an Escalations history entry point.
- Session-local drafts and selection during the current page lifetime.

The reference code is included here; no external source repository or hosting
service is needed to run it.
The archive uses a plain Vite entry point, excludes deployment settings and
unused screens/components, and translates sample content into English.
Styles from the source snapshot are retained to preserve appearance.

## Mock boundaries

All records, timestamps, captures, resources, and relationships are sample data
in `app/workbench-data.ts`. That file is not a proposed API DTO or a complete
representation of Plecture state.

- Create adds an in-memory row; its Name input does not define the real Create
  contract. No workflow is run.
- Send appends an in-memory event. It does not deliver anything to an agent.
- Up and Down show a mock notice. They do not change a process or session.
- Refresh displays the sample capture again and updates a UI retrieval time.
- Graph layout is hand-positioned SVG, not React Flow/ELK.js. The snapshot does
  not implement nested-layer inspection or historical execution reconstruction.
- Home and Inbox filter the sample records. There is no unread/resolved state.
- Reloading resets all local changes. There is no persistence or API connection.
- HTTP(S) resource links navigate to their displayed URLs. Copy uses the browser
  clipboard API where available.

The prototype does not cover all design cases, including stalled health,
missing parents, explicit sibling grouping, real connection failures, and
configuration/record mismatches. Production acceptance follows the design,
not the set of cases illustrated by these fixtures.

## Verification

The included Node tests preserve existing mock behaviors: ancestor traversal,
escalation origin/receiver, record drill-down references, lifecycle versus
completion, and empty execution records for a new local draft.
They do not validate the real Go service or browser interaction.

The lockfile pins the reference dependencies. No production build, CI workflow,
Go package, Plecture configuration, or runtime contract depends on this folder.
