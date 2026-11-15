# Move the GitHub watcher baseline into plugin data

In v0.4.0, the GitHub watcher stores each session's observed values in its
`subscriptions.json`. By default this file is at
`${XDG_DATA_HOME:-$HOME/.local/share}/github-watcher/subscriptions.json`; a
watcher started with `--data-dir` uses that directory instead. The new service
reads `<data-home>/plugins/<alias>/github/watch-state.json` through
`PLECT_DATA_DIR`. It receives subscription membership from core. Copying the
old ledger to the new directory does not move the baseline.

Stop the old watcher and plect, then back up the old watcher directory, config
home, and plect data home. Update the binary, cache-home override if set, and
the lock for every enabled plugin. Run the
[core subscription importer](core-subscriptions.md) with the old
`subscriptions.json` in its manifest. While services remain stopped, convert
the observed values with this command. Replace both paths; `<alias>` is the
enabled catalog alias, such as `official`. The destination must not already
exist.

```sh
python3 - /path/to/github-watcher/subscriptions.json \
  /path/to/plect-data/plugins/official/github/watch-state.json <<'PY'
import json
import pathlib
import sys

source, target = map(pathlib.Path, sys.argv[1:])
ledger = json.loads(source.read_text())
if ledger.get("version") != 2 or not isinstance(ledger.get("subscriptions"), dict):
    raise SystemExit("expected a v0.4.0 watcher subscriptions.json")
if target.exists():
    raise SystemExit(f"destination already exists: {target}")

states = {}
conflicts = set()
for row in ledger["subscriptions"].values():
    resource = row["resource"]
    last = row.get("last") or {}
    if resource in conflicts or not last:
        continue
    if resource in states and states[resource]["last"] != last:
        del states[resource]
        conflicts.add(resource)
    else:
        states[resource] = {"last": last}

target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
with target.open("x") as output:
    json.dump(states, output, indent=2, sort_keys=True)
    output.write("\n")
target.chmod(0o600)
print(f"moved baselines for {len(states)} resources")
for resource in sorted(conflicts):
    print(f"baseline omitted (sessions disagreed): {resource}", file=sys.stderr)
PY
```

Review every omitted resource against the backup. The old ledger has separate
baselines per session; the new watcher has one baseline per resource. For a
resource whose old baselines disagree, the command leaves its new baseline
empty so the operator does not silently select one session's value. If a
resource had no old observed values, it also starts with an empty baseline.
The old ETag and App token caches are disposable and need no move. The
conversion resets consecutive fetch-failure counts.

Start the declared watcher service after the remaining configuration changes.
Check that the service stays running, the core subscription list contains the
imported resources, and `watch-state.json` contains entries for resources
whose baselines were copied. Keep the old ledger backup for rollback.

If the conversion is skipped, subscriptions imported into core still reach the
watcher. It starts with empty baselines, so its first successful poll records
the values it sees without emitting change events. Changes between the old
watcher's last poll and that first new poll are not reported by the watcher.
Later changes are reported normally. If the import is skipped as well, the
old watcher subscriptions do not become core memberships.
