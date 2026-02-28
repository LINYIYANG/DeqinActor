package persistence

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWALAppendReplay(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "a.wal"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	_ = w.Append([]byte("x"))
	_ = w.Append([]byte("yy"))
	recs, err := w.Replay()
	if err != nil || len(recs) != 2 || string(recs[0]) != "x" || string(recs[1]) != "yy" {
		t.Fatalf("replay: %v %#v", err, recs)
	}
	_ = w.Close()
	if _, err := w.Replay(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected closed err, got: %v", err)
	}
}

func TestWALAppendEdgeCases(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatalf("expected open error")
	}
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "b.wal"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	if err := w.Append(nil); err != nil {
		t.Fatalf("append nil: %v", err)
	}
	_ = w.Close()
	if err := w.Append([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected closed append err, got: %v", err)
	}
}

func TestWALReplayTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = w.Close()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("openfile: %v", err)
	}
	_, _ = f.Write([]byte{1, 2, 3})
	_ = f.Close()

	w, err = Open(path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer w.Close()
	recs, err := w.Replay()
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected empty replay: %v %#v", err, recs)
	}
}

func TestWALErrorBranches(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "d.wal"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = w.Append([]byte{})
	if w.f == nil {
		t.Fatalf("missing file")
	}
	_ = w.f.Close()
	if err := w.Append([]byte("x")); err == nil {
		t.Fatalf("expected write error")
	}
	if _, err := w.Replay(); err == nil {
		t.Fatalf("expected seek error")
	}
	_ = w.Close()
	if err := w.Close(); err != nil {
		t.Fatalf("expected nil close")
	}
}

func TestWALReplayZeroRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "e.wal")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.Write([]byte{0, 0, 0, 0})
	_ = f.Close()
	w, err := Open(path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer w.Close()
	recs, err := w.Replay()
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected empty: %v %#v", err, recs)
	}
}

func TestWALReplayPayloadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.wal")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.Write([]byte{5, 0, 0, 0})
	_, _ = f.Write([]byte{1, 2, 3})
	_ = f.Close()
	w, err := Open(path)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	defer w.Close()
	if _, err := w.Replay(); err == nil {
		t.Fatalf("expected error")
	}
}
