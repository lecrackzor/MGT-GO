package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingDataHintOnlyReportsDataInAnotherRoot(t *testing.T) {
	cases := []struct {
		name string
		// root and cwd list the day folders to create beside each prefix.
		root     []string
		cwd      []string
		wantHint bool
	}{
		{
			name:     "resolved root has the data",
			root:     []string{"Tickers 11.19.2026"},
			cwd:      []string{"Tickers 11.19.2026"},
			wantHint: false,
		},
		{
			name:     "data only in the working directory is reported",
			cwd:      []string{"Tickers 11.19.2026", "Tickers 11.18.2026"},
			wantHint: true,
		},
		{
			name:     "first run stays quiet",
			wantHint: false,
		},
		{
			name:     "a stray folder that is not a day folder stays quiet",
			cwd:      []string{"logs", "Tickers"},
			wantHint: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rootParent := t.TempDir()
			root := filepath.Join(rootParent, "Tickers")
			cwd := t.TempDir()
			for _, name := range tc.root {
				if err := os.MkdirAll(filepath.Join(rootParent, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range tc.cwd {
				if err := os.MkdirAll(filepath.Join(cwd, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			got := missingDataHint(root, cwd)
			if !tc.wantHint {
				if got != "" {
					t.Fatalf("hint = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, cwd) {
				t.Fatalf("hint %q does not name the folder that holds the data %q", got, cwd)
			}
			if !strings.Contains(got, "data_directory") {
				t.Fatalf("hint %q does not say how to fix it", got)
			}
		})
	}
}

func TestMissingDataHintIgnoresAbsoluteDirectories(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "Tickers")
	if got := MissingDataHint(abs); got != "" {
		t.Fatalf("hint = %q, want none for an explicit path", got)
	}
}

func TestIsDayFolderName(t *testing.T) {
	day := []string{"Tickers 11.19.2026", "ES 01.02.2026", "x 12.31.2025"}
	notDay := []string{"Tickers", "Tickers 2026", "logs", "Tickers 11.19.2026 tmp", "11.19.2026"}
	for _, name := range day {
		if !isDayFolderName(name) {
			t.Fatalf("%q should be a day folder", name)
		}
	}
	for _, name := range notDay {
		if isDayFolderName(name) {
			t.Fatalf("%q should not be a day folder", name)
		}
	}
}
