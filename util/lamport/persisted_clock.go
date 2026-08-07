package lamport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/util"
)

var (
	ErrClockNotExist = errors.New("clock doesn't exist")
	ErrClockCorrupt  = errors.New("clock is corrupt")
)

type PersistedClock struct {
	*MemClock
	root     billy.Filesystem
	filePath string
}

// NewPersistedClock create a new persisted Lamport clock
func NewPersistedClock(root billy.Filesystem, filePath string) (*PersistedClock, error) {
	clock := &PersistedClock{
		MemClock: NewMemClock(),
		root:     root,
		filePath: filePath,
	}

	err := clock.withLock(func() error {
		err := clock.readUnlocked()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrClockNotExist) && !errors.Is(err, ErrClockCorrupt) {
			return err
		}
		return clock.writeUnlocked()
	})
	if err != nil {
		return nil, err
	}

	return clock, nil
}

// LoadPersistedClock load a persisted Lamport clock from a file
func LoadPersistedClock(root billy.Filesystem, filePath string) (*PersistedClock, error) {
	clock := &PersistedClock{
		root:     root,
		filePath: filePath,
	}

	err := clock.withLock(clock.readUnlocked)
	if err != nil {
		return nil, err
	}

	return clock, nil
}

// Increment is used to return the value of the lamport clock and increment it afterwards
func (pc *PersistedClock) Increment() (Time, error) {
	var result Time
	err := pc.withLock(func() error {
		if err := pc.refreshUnlocked(); err != nil {
			return err
		}
		var err error
		result, err = pc.MemClock.Increment()
		if err != nil {
			return err
		}
		return pc.writeUnlocked()
	})
	return result, err
}

// Witness is called to update our local clock if necessary after
// witnessing a clock value received from another process
func (pc *PersistedClock) Witness(time Time) error {
	return pc.withLock(func() error {
		if err := pc.refreshUnlocked(); err != nil {
			return err
		}
		if err := pc.MemClock.Witness(time); err != nil {
			return err
		}
		return pc.writeUnlocked()
	})
}

func (pc *PersistedClock) readUnlocked() error {
	f, err := pc.root.Open(pc.filePath)
	if os.IsNotExist(err) {
		return ErrClockNotExist
	}
	if err != nil {
		return err
	}

	content, err := io.ReadAll(f)
	if err != nil {
		_ = f.Close()
		return err
	}

	err = f.Close()
	if err != nil {
		return err
	}

	value, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrClockCorrupt, err)
	}

	pc.MemClock = NewMemClockWithTime(value)

	return nil
}

func (pc *PersistedClock) Write() error {
	return pc.withLock(pc.writeUnlocked)
}

func (pc *PersistedClock) writeUnlocked() error {
	data := []byte(fmt.Sprintf("%d", pc.counter))
	return util.WriteFile(pc.root, pc.filePath, data, 0644)
}

func (pc *PersistedClock) refreshUnlocked() error {
	current := pc.MemClock.Time()
	err := pc.readUnlocked()
	if errors.Is(err, ErrClockNotExist) || errors.Is(err, ErrClockCorrupt) {
		pc.MemClock = NewMemClockWithTime(uint64(current))
		return nil
	}
	if err != nil {
		return err
	}
	return pc.MemClock.Witness(current)
}

func (pc *PersistedClock) withLock(fn func() error) (err error) {
	lockPath := filepath.Join("locks", pc.filePath)
	if err := pc.root.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}
	f, err := pc.root.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	if err := f.Lock(); err != nil {
		return err
	}
	return fn()
}
