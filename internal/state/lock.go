package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Lock struct {
	path string
	file *os.File
}

func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("another llmloot process holds the lock, or a stale lock remains at %s; if no llmloot process is running, remove that file", path)
	}
	if err != nil {
		return nil, fmt.Errorf("create process lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		file.Close()
		os.Remove(path)
		return nil, fmt.Errorf("write process lock: %w", err)
	}
	return &Lock{path: path, file: file}, nil
}

func (lock *Lock) Release() error {
	closeErr := lock.file.Close()
	removeErr := os.Remove(lock.path)
	if closeErr != nil {
		return fmt.Errorf("close process lock: %w", closeErr)
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("remove process lock: %w", removeErr)
	}
	return nil
}
