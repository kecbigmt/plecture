# channel/slack

Delivers a session event to its Slack thread by posting to a companion
[slack-adapter](../../slack-adapter) instance's HTTP API.

## Contents

- `channels/slack.toml` — the `slack` channel. Inputs: `base_url` (the
  slack-adapter instance's `http://host:port`, e.g.
  `http://127.0.0.1:7890` for its default `listen_addr`), `channel_id`, and
  `thread_ts` (typically wired from whatever task created the thread via
  slack-adapter's `POST /threads`). Posts the event body to `POST
  <base_url>/messages`, falling back to the event summary when the body is
  empty.

## Install

Requires a running [slack-adapter](../../slack-adapter) instance — this
plugin ships only the delivery channel that talks to it, not the adapter
itself (build and run instructions are in slack-adapter's own README).

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/channel/slack
```

## Not included

- The slack-adapter service and its own configuration (bot/app tokens,
  channel id, listen address) — a separate companion process, not this
  plugin's concern.
- Which sessions get a Slack thread bound at all, and any workflow node that
  creates one via slack-adapter's `POST /threads` — operator/team choices,
  composed in your own workflow overlay.
