// SPDX-License-Identifier: MIT

//go:build windows

package ownerstore

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// fileLock is an exclusive, non-blocking lock on a file, held until release.
type fileLock struct{ f *os.File }

// lockFile creates the file if needed and locks it, or returns ErrHeld when another holder has it.
func lockFile(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
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
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, ol)
	err := l.f.Close()
	l.f = nil
	return err
}

// syncDir does nothing on Windows: a directory cannot be flushed there, and NTFS journals the rename.
func syncDir(string) error { return nil }
