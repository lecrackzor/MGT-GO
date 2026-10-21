package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailReadsWhatWasWritten(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewLogger(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	logger.Printf("hello from the log")
	path, text := logger.Tail(1000)
	if path == "" || !strings.HasPrefix(path, dir) {
		t.Fatalf("path = %q", path)
	}
	if !strings.Contains(text, "hello from the log") {
		t.Fatalf("tail = %q", text)
	}
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatal(err)
	}
}
