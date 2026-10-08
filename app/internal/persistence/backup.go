package persistence

import (
	"fmt"
	"os"
	"time"

	"github.com/kecbigmt/plecture/contracts/atomicfile"
)

// BackupDatabaseFiles copies dbPath and its WAL-mode siblings, if any, to a
// dated backup next to it, called before dbPath is ever opened.
func BackupDatabaseFiles(dbPath string) (string, error) {
	// Nanosecond precision: a quick re-run must never overwrite the very
	// backup it is trying to recover from.
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := dbPath + ".backup-" + stamp
	if err := copyFileIfExists(dbPath, backupPath); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := copyFileIfExists(dbPath+suffix, backupPath+suffix); err != nil {
			return "", err
		}
	}
	return backupPath, nil
}

func copyFileIfExists(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", src, err)
	}
	return atomicfile.Write(dst, data)
}
