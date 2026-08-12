# Plecture directory migration

This one-time procedure moves existing Sennit state and config directories to
Plecture's new default locations. Stop every running Plecture/Sennit process
before starting so no writer can change the files while they are copied.

```bash
set -euo pipefail

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_ROOT="${XDG_STATE_HOME:-$HOME/.local/state}/plecture-migration-backups/$STAMP"
mkdir -p "$BACKUP_ROOT"

OLD_DATA="${XDG_DATA_HOME:-$HOME/.local/share}/sennit"
NEW_DATA="${XDG_DATA_HOME:-$HOME/.local/share}/plecture"
OLD_CONFIG="$HOME/.config/sennit"
NEW_CONFIG="$HOME/.config/plecture"

for path in "$OLD_DATA" "$OLD_CONFIG"; do
  if [ -e "$path" ]; then
    cp -a "$path" "$BACKUP_ROOT/"
  fi
done

if [ -e "$OLD_DATA" ] && [ ! -e "$NEW_DATA" ]; then
  mkdir -p "$(dirname "$NEW_DATA")"
  mv "$OLD_DATA" "$NEW_DATA"
fi

if [ -e "$OLD_CONFIG" ] && [ ! -e "$NEW_CONFIG" ]; then
  mkdir -p "$(dirname "$NEW_CONFIG")"
  mv "$OLD_CONFIG" "$NEW_CONFIG"
fi

printf 'backup written to %s\n' "$BACKUP_ROOT"
```

After the move, run `plecture ls` and `plecture status <session>` against the
migrated data. If rollback is needed, stop Plecture again and restore the
copied directories from the printed backup path.
