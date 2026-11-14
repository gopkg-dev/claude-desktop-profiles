//go:build unix

package httplog

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHeldProcessLockFailsPromptly(t *testing.T) {
	logger, directory := testLogger(t)
	file, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal("could not prepare lock fixture")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		t.Fatal("could not hold fixture lock")
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}
	defer release()
	request, response := sampleExchange(t)
	done := make(chan error, 1)
	go func() { done <- logger.RecordRaw(Metadata{}, request, response, nil, nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrWrite) {
			t.Fatal("held lock did not produce the fixed logging failure")
		}
	case <-time.After(time.Second):
		release()
		<-done
		t.Fatal("HTTP logging waited indefinitely for another process lock")
	}
	if len(exchanges(t, directory)) != 0 {
		t.Fatal("a blocked logger published a partial exchange")
	}
	release()
	if err := logger.RecordRaw(Metadata{}, request, response, nil, nil); err != nil {
		t.Fatal("logging did not recover after the lock was released")
	}
}
