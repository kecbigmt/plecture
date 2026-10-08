# Node execution sequence repair

A build that persisted a failed setup attempt at `sequence = 0` could leave a
node with two executions in `storage.db`: an older cleaned one at a high
sequence, and the failed attempt at 0 below it. A session load reads the
highest sequence as the node's current execution, so it saw the cleaned one,
while saving a fresh setup found the still-unreleased failed row. `plect up`
then failed on every retry with:

```
a concurrent writer already created execution ... for this node since this attempt's setup began
```

New builds give a failed attempt a real sequence, so the state cannot arise
again. A database that already holds it needs the one-time repair below. It
raises each hidden execution's sequence above every sequence its session
records and changes nothing else: no execution, output or history row is
deleted.

## Check whether a database is affected

Stop every plect process that uses the data directory, then list the affected
executions without writing anything:

```bash
plect storage repair-node-executions --dry-run
```

`nothing to repair` means the database is unaffected. Pass the global
`--data-home <dir>` to name a data directory other than the default.

## Repair

```bash
plect storage repair-node-executions
```

The command copies `storage.db` and its `-wal` and `-shm` siblings to a dated
`storage.db.backup-<timestamp>` next to it before opening the database, and
prints that path. A database with nothing to repair gets no backup. Run the
command again afterward; it reports `nothing to repair`.

## Roll back

Stop every plect process, then move the backup files back over `storage.db`
and its `-wal` and `-shm` siblings.
