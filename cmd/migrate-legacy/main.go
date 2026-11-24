// Command migrate-legacy moves the legacy profiles_blob column out of archived
// chart databases into the sibling .profiles.db file, then drops the column.
// It runs as a dry run by default; pass --apply to write. Each migrated chart
// file keeps a .pre-migrate backup of the original until you delete it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"market-terminal/internal/database"
)

func main() {
	apply := flag.Bool("apply", false, "migrate (backups are kept as .pre-migrate)")
	flag.Parse()

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}

	var files []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", root, err)
			continue
		}
		if !info.IsDir() {
			if isChartDB(root) {
				files = append(files, root)
			}
			continue
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if isChartDB(path) {
				files = append(files, path)
			}
			return nil
		})
	}

	migrated, skipped, problems := 0, 0, 0
	for i, path := range files {
		rep, err := database.InspectLegacyChart(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: ERROR %v\n", i+1, len(files), path, err)
			problems++
			continue
		}
		if rep.Skipped != "" {
			continue // already clean or unmigratable; not counted as a problem
		}
		if !*apply {
			fmt.Printf("[%d/%d] %s  (would migrate %d blob rows)\n", i+1, len(files), path, rep.BlobRows)
			migrated++
			continue
		}

		rep, err = database.MigrateLegacyChart(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: ERROR %v\n", i+1, len(files), path, err)
			problems++
			continue
		}
		fmt.Printf("[%d/%d] %s  %d blob rows -> %d ladder rows across %d endpoints (backup: %s)\n",
			i+1, len(files), path, rep.BlobRows, rep.LadderRows, rep.Endpoints, filepath.Base(rep.BackupPath))
		migrated++
	}

	for _, path := range files {
		if rep, err := database.InspectLegacyChart(path); err == nil && rep.Skipped != "" {
			skipped++
		}
	}

	if !*apply {
		fmt.Printf("\nDRY RUN: %d chart DBs would be migrated. Rerun with --apply to write.\n", migrated)
	} else {
		fmt.Printf("\nDONE: %d migrated, %d skipped (flat or already clean), %d problems.\n", migrated, skipped, problems)
	}
}

func isChartDB(path string) bool {
	return strings.HasSuffix(path, ".db") &&
		!strings.HasSuffix(path, ".profiles.db") &&
		!strings.HasSuffix(path, ".pre-migrate")
}
