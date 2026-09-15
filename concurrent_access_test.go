package vfs

import (
	"io"
	"os"
	"sync"
	"testing"
)

// Opening a file while another goroutine writes and closes one for the same
// path must be race-free. NewRFile/NewWFile read Data and Mode through
// fileData, and (*file).Close writes both, so both sides have to take the
// file's lock. Run with -race; before the fix this reported a write/read pair
// between fileData and Close.
func TestConcurrentOpenAndCloseIsRaceFree(t *testing.T) {
	fs := Memory()
	const path = "/dists/stable/InRelease"

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			w, err := fs.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err != nil {
				return
			}
			_, _ = w.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n"))
			_ = w.Close()
		}()
		go func() {
			defer wg.Done()
			r, err := fs.Open(path)
			if err != nil {
				return
			}
			_, _ = io.ReadAll(r)
			_ = r.Close()
		}()
	}
	wg.Wait()
}

// The same for a compressed file, which takes fileData's decompress path and
// makes Close rewrite Mode as well as Data.
func TestConcurrentOpenAndCloseCompressedIsRaceFree(t *testing.T) {
	fs := Memory()
	const path = "/payload"

	w, err := fs.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if c, ok := w.(interface{ SetCompressed(bool) }); ok {
		c.SetCompressed(true)
	}
	// Compressible payload, so Close keeps ModeCompress.
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = 'A'
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			r, err := fs.Open(path)
			if err != nil {
				return
			}
			_, _ = io.ReadAll(r)
			_ = r.Close()
		}()
		go func() {
			defer wg.Done()
			if fi, err := fs.Stat(path); err == nil {
				_ = fi.Mode()
				_ = fi.Size()
			}
		}()
	}
	wg.Wait()
}

// Closing the same handle from several goroutines must not race on the
// handle's closed flag, and must stay idempotent.
func TestConcurrentCloseOfSameHandleIsRaceFree(t *testing.T) {
	fs := Memory()

	w, err := fs.OpenFile("/f", os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := ReadFile(fs, "/f")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("content = %q, want %q", got, "data")
	}
}
