package service

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/kecbigmt/plecture/app/internal/state"
)

var errLifecycleBusy = errors.New("session lifecycle is in progress")

func withLifecycleLock(store *state.Store, name string, fn func() error) error {
	return lifecycleLock(store, name, false, fn)
}

func tryLifecycleLock(store *state.Store, name string, fn func() error) error {
	return lifecycleLock(store, name, true, fn)
}

func lifecycleLock(store *state.Store, name string, try bool, fn func() error) error {
	path := filepath.Join(store.Dir(), "lifecycle-locks", url.PathEscape(name)+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	mode := syscall.LOCK_EX
	if try {
		mode |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), mode); err != nil {
		if try && (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) {
			return errLifecycleBusy
		}
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}
