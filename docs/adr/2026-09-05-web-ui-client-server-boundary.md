# Separate the Plecture Web UI from execution through HTTP

## Context

The session-first reference UI combines a hierarchical session tree, an event
timeline, task inspection, an execution graph, and terminal captures.
The existing Web UI renders Go templates with htmx and calls the service layer
through SessionService. The event bus behind `plect serve` provides event
HTTP/SSE endpoints, not a complete session-management API.

The first release needs browser access through localhost or a VPN. A later
desktop application should start a local server or act as a remote frontend.
The core language, durable state, event contracts, and lifecycle semantics
must remain unchanged.

## Decision

Use React and TypeScript with Vite, Tailwind CSS, and shadcn/ui. Use TanStack
Query for server-data retrieval and caching, and React Flow with ELK.js for
graph interaction and layout. Embed the static UI build in `plect-web`.
Keep Go's service layer and `net/http`; add JSON adapters and the read
projections required for inspection. Relay existing SSE server-side.

Keep session UI behavior on the HTTP boundary. Isolate browser connection
handling from components and isolate future OS/process functions from session
operations. The initial browser deployment uses a single origin.

Tauri 2 is the leading desktop candidate, not a committed framework choice.
Validate local startup and remote authentication/streaming before selecting
the desktop shell. Do not add desktop abstractions with no present consumer.

The [Web UI design](../design/web-ui.md) specifies the interaction and data
boundaries. The [reference prototype](../design/web-ui/prototype/README.md)
preserves the visual reference independently from production implementation.

## Consequences

- Browser and desktop clients can share session UI and service semantics.
- Running the Web UI requires no separate Node.js server; frontend builds do.
- JSON DTOs, authentication/CSRF handling, and SSE recovery require work.
  Existing Go services do not mean all required HTTP routes already exist.
- A versioned API and explicit compatibility checks handle independent client
  and server releases; compatibility shims are not assumed.
- Desktop packaging requires readiness checks, process ownership, runtime
  prerequisites, credential handling, signing, and update decisions.
- The prototype's mock schema and hand-positioned SVG graph do not constrain
  the production API or graph implementation.

## Alternatives considered

**Go templates and htmx throughout.** This preserves the existing rendering
stack, but the coordinated selection, drafts, graph interaction, and detail
pane motivate a client-side workspace. Existing business logic remains in Go.

**Next.js or a separate Node.js application server.** No SSR or Node-specific
server requirement justifies another runtime for this local control surface.
Vite produces static assets suitable for Go embedding.

**Wails.** Go-centered desktop development is attractive. Its native calls
must not become a second session API that differs from remote HTTP access.
Wails v3 offers server mode and is beta at the time of this decision;
re-evaluate its release status during desktop selection.

**Electron.** Chromium and Node.js offer a JavaScript-centered desktop
environment, with those runtimes included in distribution. This remains an
option if its integration benefits outweigh that packaging cost.

**Tauri 2.** External-binary sidecars fit the independent Go-server boundary.
The tradeoff is a Rust desktop component and platform-specific packaging.
This fit makes it the leading candidate without requiring immediate adoption.

## Sources

Official documentation consulted on 2026-09-05:

- [Vite production builds](https://vite.dev/guide/build)
- [TanStack Query](https://tanstack.com/query/latest/docs/framework/react/overview)
- [React Flow layout](https://reactflow.dev/learn/layouting/layouting)
- [Tauri external binaries](https://v2.tauri.app/develop/sidecar/)
- [Wails server mode](https://v3.wails.io/guides/server-build/) and
  [release status](https://v3.wails.io/faq/)
- [Electron](https://www.electronjs.org/docs/latest/)
