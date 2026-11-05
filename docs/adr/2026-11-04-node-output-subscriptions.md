# Workflow node output subscriptions

## Context

A workflow can create an external resource whose identifier is known only
from a node's setup outputs. The Slack thread effect created such a resource
and subscribed its session inside the effect script. Core did not record that
binding, could not retry a failed subscription, and could not decide whether
cleanup should unsubscribe while another part of the session still needed the
same resource. A worker that used the effect only to report progress received
thread mentions alongside the orchestrator's mention subscription.

Node input wiring cannot read the node's own outputs, because inputs resolve
before setup. Resource actions already own the subscription and unsubscribe
operations for a session/resource pair.

## Decision

A workflow node may declare a `subscription` value containing exactly one
`self.outputs.<key>` projection. After the node is produced, core records the
resolved resource identifier on its execution and uses the matching resource
`subscribe` action. A failed hook is queued for retry. After cleanup, core
uses the resource `unsubscribe` action if neither the entry resource, a task,
nor another live node needs the identifier. A liveness rebuild releases the
old binding before setup binds the new execution.

The declaration passes an empty resource action input object, as automatic
entry and task bindings do. A known resource requiring inputs fails revision
loading; an identifier resolved at runtime reports a subscription failure and
queues a retry. The declaration has no separate input evaluation surface.

A subscription-only revision change receives the existing node-change class:
`recreate` for a session-scoped node and `restart` for a run-scoped node.
`slack_thread` creates the thread and reports its existing outputs; workflows
that need replies declare the subscription on that node.

## Consequences

Workflows choose whether a thread's replies reach the session. Core owns the
subscription lifetime and retries failed hooks. Existing workflows that
relied on `slack_thread` subscribing implicitly must add the node declaration
when they move to the new release. Saved workflow revisions retain the old
cleanup until the session is recreated or rebased.

## Alternatives considered

Keeping the subscription inside `slack_thread` with an opt-out input would
preserve an effect-level binding that core could neither record nor retry.
Allowing resource action inputs in the node declaration would introduce a
second input binding model before one is needed by a concrete node consumer.
Automatic in-place rebasing would require a separate transition for removing
and adding a binding against saved outputs; node recreation already handles
that transition for this release.
