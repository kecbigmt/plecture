# Explicit socket for a custom data home

`plect serve` requires `--socket` when `--data-home` or `PLECT_DATA_HOME`
selects a non-default data directory. Migrate each resident command that uses
a custom data home as follows:

1. Stop the resident daemon and clients that depend on its bus.
2. Back up the custom data directory, including `storage.db` and the durable
   event log, before changing the launch command.
3. Choose a short socket path in a directory accessible to the intended
   clients. Add `--socket <path>` to the `plect serve` command.
4. Configure each bus client with the same path through its existing socket
   setting or `PLECT_BUS_SOCKET`. `--data-home` does not select a socket for
   clients.
5. Restart the daemon and confirm that its startup message names the chosen
   socket path, then reconnect the clients.
