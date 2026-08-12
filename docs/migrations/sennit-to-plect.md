# plect directory migration

This one-time procedure moves existing Sennit or intermediate Plecture state
and config directories to plect's default locations. Stop every running
Plecture, plect, or Sennit process before starting so no writer can change the
files while they are copied.

```bash
set -euo pipefail

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_ROOT="${XDG_STATE_HOME:-$HOME/.local/state}/plect-migration-backups/$STAMP"
mkdir -p "$BACKUP_ROOT"

OLD_DATA_CANDIDATES=(
  "${XDG_DATA_HOME:-$HOME/.local/share}/sennit"
  "${XDG_DATA_HOME:-$HOME/.local/share}/plecture"
)
NEW_DATA="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
OLD_CONFIG_CANDIDATES=(
  "$HOME/.config/sennit"
  "$HOME/.config/plecture"
)
NEW_CONFIG="$HOME/.config/plect"

backup_and_move() {
  local target="$1"
  shift

  if [ -e "$target" ]; then
    cp -a "$target" "$BACKUP_ROOT/"
    return
  fi

  local source=""
  for path in "$@"; do
    if [ -e "$path" ]; then
      cp -a "$path" "$BACKUP_ROOT/"
      if [ -z "$source" ]; then
        source="$path"
      fi
    fi
  done

  if [ -n "$source" ]; then
    mkdir -p "$(dirname "$target")"
    mv "$source" "$target"
  fi
}

backup_and_move "$NEW_DATA" "${OLD_DATA_CANDIDATES[@]}"
backup_and_move "$NEW_CONFIG" "${OLD_CONFIG_CANDIDATES[@]}"

printf 'backup written to %s\n' "$BACKUP_ROOT"
```

After the move, run `plect ls` and `plect status <session>` against the
migrated data. If rollback is needed, stop Plecture again and restore the
copied directories from the printed backup path.
