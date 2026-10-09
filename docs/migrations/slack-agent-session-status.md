# Slack agent session status migration

The Slack adapter now uses `agents.sessions.setStatus`. Do these steps before
deploying the new adapter.

1. Back up the Slack app manifest and the adapter's `config.toml`. Keep a
   copy of the previous plugin revision so a deployment can be rolled back.
2. In Slack app settings, declare the app as an agent. Slack adds the
   `assistant:write` scope. Confirm that `chat:write` remains granted,
   then reinstall or reauthorize the app in its workspace.
3. Change workflow bindings for `official.slack.status` to include only
   `plect.status_message`. Remove `plect.node.result` or other event types
   from that binding. The status event's text remains available to other
   channels, but Slack now displays its own “Working…” text rather than the
   event's explanation. Use an ordinary message channel if that explanation
   must appear in the thread.
4. Deploy the adapter and check one streamed answer, one answer delivered
   after `chat.startStream` fails, and one turn with no answer. Confirm the
   loading display disappears after the turn ends in each case. The app owner
   performs these checks in a real Slack workspace.

The adapter does not subscribe to `agent_session_stopped`. Slack may include
`missing_agent_session_stopped_event_subscription` in a successful status
response, and the loading indicator has no interactive stop button.

This migration does not require `agent_view`. Switching an app to
`agent_view` is irreversible, and a distributed app may need Marketplace
review. Keep that decision separate from this API migration.

If rollback is needed, restore the saved plugin revision and workflow
binding. Retain the app's agent declaration and scopes until the rollback
behavior has been checked in Slack.
