package httplog

import (
	"errors"
	"os"
	"strings"
	"time"
)

// Summary describes the stored exchanges without reading their contents.
type Summary struct {
	Exchanges int
	Bytes     int64
	Latest    time.Time
	// MaxExchanges and MaxBytes are the rotation limits.
	MaxExchanges int
	MaxBytes     int64
}

// Summarize counts the completed exchanges in directory. A directory that does not exist yet is empty.
func Summarize(directory string) (Summary, error) {
	summary := Summary{MaxExchanges: defaultMaxExchanges, MaxBytes: defaultMaxBytes}
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return summary, nil
	}
	root, err := openDirectory(directory, false)
	if err != nil {
		return summary, err
	}
	defer root.Close()
	entries, err := readExchanges(root)
	if err != nil {
		return summary, err
	}
	for _, entry := range entries {
		summary.Exchanges++
		summary.Bytes += entry.size
		if at, err := time.Parse("20060102T150405.000000000Z", strings.SplitN(entry.name, "-", 2)[0]); err == nil &&
			at.After(summary.Latest) {
			summary.Latest = at
		}
	}
	return summary, nil
}

// Clear removes every completed exchange in directory while holding the log lock.
func Clear(directory string) error {
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	root, err := openDirectory(directory, false)
	if err != nil {
		return err
	}
	defer root.Close()
	unlock, err := lockDirectory(root)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := readExchanges(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.name); err != nil {
			return ErrWrite
		}
	}
	return syncDirectory(root)
}

func readExchanges(root *os.Root) ([]storedExchange, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, ErrDirectory
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return nil, ErrDirectory
	}
	var exchanges []storedExchange
	for _, entry := range entries {
		name := entry.Name()
		if !exchangeName.MatchString(name) {
			continue
		}
		child, err := checkedChild(root, name)
		if err != nil {
			return nil, err
		}
		size, err := exchangeSize(child)
		child.Close()
		if err != nil {
			return nil, err
		}
		exchanges = append(exchanges, storedExchange{name: name, size: size})
	}
	return exchanges, nil
}
