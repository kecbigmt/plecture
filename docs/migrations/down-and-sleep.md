# Down and sleep migration

Before installing a build with the sleep migration, stop the resident `plect`
process and follow
[storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md#before-upgrading).
Keep the backup until the new build has started and you have inspected the
sessions below.

On the first start, the database migration adds nullable
`sessions.sleep_since`. An existing `down` session stays fully stopped by
default. A `down` session whose latest `lifecycle.down` event is followed by
`plect.workflow_population.down` with `reason=capacity` receives the timestamp
of that capacity event as its sleep marker. Other `down` sessions receive a
`plect.session.stop` event so population admission can recognize their full
stop after upgrading. That migration event does not suppress forwarding of
inbound events that were already unread. The migration does not start any
runtime nodes. A future inbound event requests a wake for a sleeping member.

After upgrading, run `plect ls` and inspect the `SLEEP` column. A full stop
shows `-`; an event-waiting sleep shows `event`; a queued sleep shows its queue
position. For a session intentionally stopped under the old behavior that
should now wake on inbound events, run `plect down --sleep <session>`. To keep
a migrated capacity member fully stopped, run `plect down <session>`.

To roll back, follow
[storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md#rollback).
The rollback removes every session and event created since the backup.
