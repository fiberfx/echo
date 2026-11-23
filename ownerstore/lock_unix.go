// SPDX-License-Identifier: MIT

//go:build !windows

package ownerstore

import (
	"errors"
	"os"
	"syscall"
)

// fileLock is an exclusive, non-blocking advisory lock on a file, held until release.
type fileLock struct{ f *os.File }

// lockFile creates the file if needed and locks it, or returns ErrHeld when another holder has it.
func lockFile(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// release drops the lock. The file stays.
func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}

// syncDir flushes the directory entry of a file created or renamed in dir.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
