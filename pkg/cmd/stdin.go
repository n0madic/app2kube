package cmd

import "os"

// withStdin temporarily replaces os.Stdin with a pipe fed by data, so the
// kubectl resource builder (which reads os.Stdin directly for the "-" filename)
// consumes the rendered manifest. The write runs in a goroutine because the OS
// pipe buffer (~64KB) is smaller than a typical manifest, so writing must
// overlap the reader to avoid a deadlock.
//
// It returns a wait function the caller MUST invoke after the kubectl Run
// completes: wait closes the read end (kubectl no longer reads stdin once Run
// returned) to unblock the writer, waits for the writer goroutine, restores
// os.Stdin, and returns any write error. Closing the read end FIRST is what
// prevents a deadlock when kubectl errors out before draining a manifest larger
// than the OS pipe buffer (~64KB): the writer is then stuck on w.Write and would
// never signal werr, so a wait that blocked on werr first would hang forever.
// Sequencing the close+restore through wait (rather than swapping os.Stdin from
// a goroutine and a deferred Restore, as the old go-fakeio usage did) removes the
// data race and double-Close.
func withStdin(data []byte) (wait func() error, err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	orig := os.Stdin
	os.Stdin = r

	werr := make(chan error, 1)
	go func() {
		_, writeErr := w.Write(data)
		if closeErr := w.Close(); writeErr == nil {
			writeErr = closeErr
		}
		werr <- writeErr
	}()

	return func() error {
		// kubectl's Run has returned, so it will not read os.Stdin again. Close the
		// read end first to unblock a writer goroutine still stuck on a full pipe
		// (kubectl errored before draining a >64KB manifest); otherwise <-werr
		// would block forever and hang the command.
		_ = r.Close()
		e := <-werr // writer goroutine done
		os.Stdin = orig
		return e
	}, nil
}
