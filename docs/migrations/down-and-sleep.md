# Down and sleep migration

Before installing a build with the sleep migration, stop the resident `plect`
process and back up its data directory, including `storage.db` and its SQLite
sidecars. For example, copy the entire configured data home to a separate
backup directory while the process is stopped. Keep that backup until the
new build has started and the sessions below have been inspected.

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

To roll back, stop the resident process, restore the backed-up data home, and
restart the earlier build. The rollback removes all sessions and events
created since that backup, so verify the backup before restoring it.
