package lamport

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/stretchr/testify/require"
)

func TestPersistedClock(t *testing.T) {
	root := memfs.New()

	c, err := NewPersistedClock(root, "test-clock")
	require.NoError(t, err)

	testClock(t, c)
}

func TestPersistedClockSerializesIndependentInstances(t *testing.T) {
	root := osfs.New(t.TempDir())
	first, err := NewPersistedClock(root, "clocks/test")
	require.NoError(t, err)
	second, err := NewPersistedClock(root, "clocks/test")
	require.NoError(t, err)

	const incrementsPerClock = 50
	results := make(chan Time, incrementsPerClock*2)
	errors := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, clock := range []*PersistedClock{first, second} {
		clock := clock
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < incrementsPerClock; i++ {
				value, err := clock.Increment()
				if err != nil {
					errors <- err
					return
				}
				results <- value
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	values := make([]int, 0, incrementsPerClock*2)
	for value := range results {
		values = append(values, int(value))
	}
	sort.Ints(values)
	require.Len(t, values, incrementsPerClock*2)
	for i, value := range values {
		require.Equal(t, i+2, value)
	}

	loaded, err := LoadPersistedClock(root, "clocks/test")
	require.NoError(t, err)
	require.Equal(t, Time(incrementsPerClock*2+1), loaded.Time())
}

func TestPersistedClockRecoversCorruptFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "clocks"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clocks", "test"), []byte("12 trailing-data"), 0644))

	clock, err := NewPersistedClock(osfs.New(dir), "clocks/test")
	require.NoError(t, err)
	require.Equal(t, Time(1), clock.Time())
	value, err := clock.Increment()
	require.NoError(t, err)
	require.Equal(t, Time(2), value)
}
