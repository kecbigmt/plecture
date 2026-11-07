# Slack thread node subscription migration

`official.slack.slack_thread` creates a root message and returns `thread_ts`,
`channel_id`, and `permalink`. It no longer subscribes the session or removes
that subscription in its cleanup.

## Backup

Before upgrading, back up the runtime data and trusted configuration:

```bash
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DATA_HOME="${PLECT_DATA_HOME:-$HOME/.local/share/plect}"
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
cp -r "$DATA_HOME" "$DATA_HOME.migration-backup.$STAMP"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Also preserve version-controlled workflow files in their repository history.
Follow the breaking-release session migration before starting the new runtime.

## Update workflows that receive replies

Add `subscription` to each `slack_thread` node whose session must receive
plain replies and mentions from the created thread:

```toml
[[review.nodes]]
id = "thread"
uses = "official.slack.slack_thread"
subscription = { from = "self.outputs.permalink" }
```

Leave the field out when the thread only reports progress and another session
owns the conversation. Its outputs and channel bindings remain available.

An existing session keeps the effect and workflow declarations captured in
its saved revision. Its old cleanup removes the old subscription. A workflow
rebase that changes a session-scoped `slack_thread` node requires recreation;
a run-scoped node with only a subscription change requires a restart. Review
the `plect workflow rebase` classification before moving a session to the new
workflow revision.
