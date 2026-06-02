package cmd

import "os"

// withStdin temporarily replaces os.Stdin with a pipe fed by data, so the
// kubectl resource builder (which reads os.Stdin directly for the "-" filename)
// consumes the rendered manifest. The write runs in a goroutine because the OS
// pipe buffer (~64KB) is smaller than a typical manifest, so writing must
// overlap the reader to avoid a deadlock.
//
// It returns a wait function the caller MUST invoke after the kubectl Run
// completes: wait blocks until the writer goroutine has finished (so there is no
// concurrent access to the pipe), restores os.Stdin and the original read end,
// and returns any write error. Sequencing the close+restore through wait (rather
// than swapping os.Stdin from a goroutine and a deferred Restore, as the old
// go-fakeio usage did) removes the data race and double-Close.
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
		e := <-werr // writer goroutine done: safe to restore and close the read end
		os.Stdin = orig
		_ = r.Close()
		return e
	}, nil
}
