# Slack agent session status migration

The Slack adapter now uses `agents.sessions.setStatus`. Use this order when
deploying the updated runtime plugins and adapter.

1. Back up the Slack app manifest and the adapter's `config.toml`. Keep a
   copy of the previous plugin revision and `plect` binary so a deployment can
   be rolled back.
2. Upgrade the `plect` binary to a build that supports
   `state set-message --turn-id`. Verify it on every host that runs the Claude
   or Codex activity hooks:

   ```bash
   plect state set-message --help | grep -- --turn-id
   ```

   Upgrade `plect` before, or together with, the Claude and Codex plugin
   revisions or catalog pins. An older binary rejects the flag, and the hooks
   swallow that failure, leaving turn-scoped status unreported.
3. In Slack app settings, declare the app as an agent. Confirm that the
   declaration is active in the workspace. The existing bot token already
   has `assistant:write` and `chat:write`.
4. Change workflow bindings for `official.slack.status` to include only
   `plect.status_message`. Remove `plect.node.result` or other event types
   from that binding. The status event's text remains available to other
   channels, but Slack now displays its own “Working…” text rather than the
   event's explanation. Use an ordinary message channel if that explanation
   must appear in the thread.
5. Deploy the adapter and check one streamed answer, one answer delivered
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
binding before restoring an older `plect` binary. Retain the app's agent
declaration and scopes until the rollback behavior has been checked in Slack.
