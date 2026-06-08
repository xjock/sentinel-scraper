package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResumableDownload_Fresh(t *testing.T) {
	tmpDir := t.TempDir()
	data := []byte("HELLO_WORLD_DATA")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			t.Error("fresh download should not send Range")
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}))
	defer srv.Close()

	dest := filepath.Join(tmpDir, "test.bin")
	finalSize, total, skipped, err := resumableDownload(context.Background(), http.DefaultClient, srv.URL, NoOpAuth{}, dest, "test", 0)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if skipped {
		t.Error("fresh download should not be skipped")
	}
	if total != int64(len(data)) {
		t.Errorf("expected total=%d, got %d", len(data), total)
	}
	if finalSize != int64(len(data)) {
		t.Errorf("expected finalSize=%d, got %d", len(data), finalSize)
	}

	got, _ := os.ReadFile(dest)
	if string(got) != string(data) {
		t.Errorf("content mismatch: %s vs %s", got, data)
	}
}

func TestResumableDownload_Resume(t *testing.T) {
	tmpDir := t.TempDir()
	data := []byte("ABCDEFGHIJKLMNOP")
	partial := data[:8]

	if err := os.WriteFile(filepath.Join(tmpDir, "test.bin"), partial, 0644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHdr := r.Header.Get("Range")
		if rangeHdr != "bytes=8-" {
			t.Errorf("expected Range=bytes=8-, got %s", rangeHdr)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 8-%d/%d", len(data)-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[8:])
	}))
	defer srv.Close()

	dest := filepath.Join(tmpDir, "test.bin")
	finalSize, total, skipped, err := resumableDownload(context.Background(), http.DefaultClient, srv.URL, NoOpAuth{}, dest, "test", int64(len(data)))
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if skipped {
		t.Error("resume should not be skipped")
	}
	if total != int64(len(data)) {
		t.Errorf("expected total=%d, got %d", len(data), total)
	}
	if finalSize != int64(len(data)) {
		t.Errorf("expected finalSize=%d, got %d", len(data), finalSize)
	}

	got, _ := os.ReadFile(dest)
	if string(got) != string(data) {
		t.Errorf("content mismatch after resume: %s vs %s", got, data)
	}
}

func TestResumableDownload_Skipped(t *testing.T) {
	tmpDir := t.TempDir()
	data := []byte("COMPLETE")
	dest := filepath.Join(tmpDir, "test.bin")
	if err := os.WriteFile(dest, data, 0644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer srv.Close()

	finalSize, _, skipped, err := resumableDownload(context.Background(), http.DefaultClient, srv.URL, NoOpAuth{}, dest, "test", int64(len(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skipped {
		t.Error("expected skipped=true for 416")
	}
	if finalSize != int64(len(data)) {
		t.Errorf("expected finalSize=%d, got %d", len(data), finalSize)
	}
}

func TestResumableDownload_HTTPError(t *testing.T) {
	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()

	dest := filepath.Join(tmpDir, "test.bin")
	_, _, _, err := resumableDownload(context.Background(), http.DefaultClient, srv.URL, NoOpAuth{}, dest, "test", 0)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got: %v", err)
	}
}

func TestResumableDownload_ContextCancelled(t *testing.T) {
	tmpDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("late"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	dest := filepath.Join(tmpDir, "test.bin")
	_, _, _, err := resumableDownload(ctx, http.DefaultClient, srv.URL, NoOpAuth{}, dest, "test", 0)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestProgressReader(t *testing.T) {
	data := make([]byte, 1024*1024) // 1 MB
	for i := range data {
		data[i] = byte(i % 256)
	}

	pr := &progressReader{
		r:     strings.NewReader(string(data)),
		total: int64(len(data)),
		label: "test",
	}

	totalRead := int64(0)
	buf := make([]byte, 4096)
	for {
		n, err := pr.Read(buf)
		totalRead += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
	}

	if totalRead != int64(len(data)) {
		t.Errorf("expected %d bytes read, got %d", len(data), totalRead)
	}
	if pr.current != int64(len(data)) {
		t.Errorf("expected progress current=%d, got %d", len(data), pr.current)
	}
}

func TestProgressReader_UnknownTotal(t *testing.T) {
	data := make([]byte, 25*1024*1024) // 25 MB
	pr := &progressReader{
		r:     strings.NewReader(string(data)),
		total: 0,
		label: "test",
	}

	buf := make([]byte, 4096)
	for {
		_, err := pr.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
	}

	if pr.current != int64(len(data)) {
		t.Errorf("expected current=%d, got %d", len(data), pr.current)
	}
}
