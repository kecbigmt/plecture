# Resident daemon command migration

This migration covers the command rename decided in
`docs/adr/2026-08-17-resident-daemon-command-vocabulary.md`: the resident
daemon starts with `plect serve`.

The change is intentionally breaking. Plecture is pre-1.0, so operators migrate
host service configuration once instead of relying on a compatibility shim.

## Backup

Before editing host service definitions, back up the unit or launch
configuration that starts the resident plect process.

For a systemd user unit:

```bash
UNIT="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/plect.service"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp "$UNIT" "$UNIT.migration-backup.$STAMP"
```

For a system service:

```bash
sudo cp /etc/systemd/system/plect.service \
  "/etc/systemd/system/plect.service.migration-backup.$(date -u +%Y%m%dT%H%M%SZ)"
```

## Update The Service Command

Edit the service definition that starts the resident daemon.

Change:

```ini
ExecStart=/path/to/plect bus serve
```

to:

```ini
ExecStart=/path/to/plect serve
```

Keep any existing flags and environment entries. For example:

```ini
ExecStart=/path/to/plect serve --socket %t/plect/bus.sock
Environment=PLECT_BUS_SOCKET=%t/plect/bus.sock
Environment=PLECT_BUS_TOKEN=...
```

`PLECT_BUS_SOCKET`, `PLECT_BUS_TOKEN`, and the default `bus.sock` path still
name the event bus API exposed by the resident process.

## Reload And Restart

For a systemd user unit:

```bash
systemctl --user daemon-reload
systemctl --user restart plect.service
systemctl --user status plect.service
```

For a system service:

```bash
sudo systemctl daemon-reload
sudo systemctl restart plect.service
sudo systemctl status plect.service
```

## Verification

Confirm the new command is the supported surface:

```bash
plect serve --help
```

Confirm the event bus socket is present at the configured path:

```bash
test -S "${PLECT_BUS_SOCKET:-${XDG_RUNTIME_DIR:-/tmp}/plect/bus.sock}"
```

## Rollback

Restore the service definition from the backup, reload systemd, and use a
plect binary built before this change:

```bash
cp "$UNIT.migration-backup.$STAMP" "$UNIT"
systemctl --user daemon-reload
systemctl --user restart plect.service
```
