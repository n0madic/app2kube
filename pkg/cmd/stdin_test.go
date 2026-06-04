package cmd

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// withStdin must feed a payload larger than the OS pipe buffer (~64KB) without
// deadlocking (writer overlaps the reader), round-trip it byte-for-byte, report
// no error, and restore os.Stdin afterwards. Run under -race to catch the
// previous goroutine/Restore data race and double-Close.
func TestWithStdinFeedsLargePayloadAndRestores(t *testing.T) {
	orig := os.Stdin
	payload := bytes.Repeat([]byte("manifest-line\n"), 32*1024) // > 64KB

	wait, err := withStdin(payload)
	if err != nil {
		t.Fatalf("withStdin: %v", err)
	}

	got, rerr := io.ReadAll(os.Stdin)
	werr := wait()

	if rerr != nil {
		t.Fatalf("read faked stdin: %v", rerr)
	}
	if werr != nil {
		t.Fatalf("withStdin writer error: %v", werr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	if os.Stdin != orig {
		t.Errorf("os.Stdin was not restored after wait()")
	}
}

// Regression (#9): when kubectl errors before draining a manifest larger than
// the OS pipe buffer, the writer goroutine is stuck on w.Write. wait() must
// close the read end first so it unblocks instead of hanging forever.
func TestWithStdinDoesNotDeadlockWhenNotDrained(t *testing.T) {
	orig := os.Stdin
	payload := bytes.Repeat([]byte("x"), 256*1024) // > 64KB pipe buffer

	wait, err := withStdin(payload)
	if err != nil {
		t.Fatalf("withStdin: %v", err)
	}

	// Simulate kubectl returning without reading os.Stdin.
	done := make(chan error, 1)
	go func() { done <- wait() }()

	select {
	case <-done:
		// returned — no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("withStdin wait() deadlocked when stdin was not drained")
	}

	if os.Stdin != orig {
		t.Errorf("os.Stdin was not restored after wait()")
	}
}
