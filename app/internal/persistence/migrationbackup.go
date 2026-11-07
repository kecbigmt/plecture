package persistence

import (
	"context"
	"errors"
	"io/fs"
)

const BackupRetainEnvVar = "PLECT_STORAGE_BACKUP_RETAIN"

type Backup struct {
	Name    string
	Version int64
	Size    int64
}

func ListBackups(ctx context.Context, dbPath string) ([]Backup, error) { return nil, nil }

func restore(ctx context.Context, dbPath, name string, migrations fs.FS) (string, error) {
	return "", errors.New("not implemented")
}
