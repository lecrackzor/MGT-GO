// Command trimprofiles compacts archived profile files to the current storage
// format. It writes trimmed copies to an output directory and never touches the
// originals. Pass --apply to swap a verified copy over its source after backup.
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
	cadence := flag.Float64("cadence", 5.0, "sample cadence in seconds")
	outDir := flag.String("out", "trimmed-out", "directory that receives the trimmed copies")
	apply := flag.Bool("apply", false, "swap each verified copy over its source (source is backed up to .pre-trim)")
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
			if strings.HasSuffix(root, ".profiles.db") {
				files = append(files, root)
			}
			continue
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".profiles.db") {
				files = append(files, path)
			}
			return nil
		})
	}

	if len(files) == 0 {
		fmt.Println("no *.profiles.db files found")
		return
	}

	var totalSource, totalDest int64
	var totalDropped, totalDroppedChanged int
	problems := 0
	for i, src := range files {
		dst := filepath.Join(*outDir, src)
		report, err := database.TrimProfileFile(src, dst, *cadence)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: ERROR %v\n", i+1, len(files), src, err)
			problems++
			continue
		}
		totalSource += report.SourceBytes
		totalDest += report.DestBytes

		bad := report.IntegrityOK && !reportFailed(report)
		if reportFailed(report) {
			problems++
		}

		fmt.Printf("[%d/%d] %s\n", i+1, len(files), src)
		for _, ep := range report.Endpoints {
			var flags string
			if ep.Mismatch {
				flags += " MISMATCH"
			}
			if ep.Unreadable > 0 {
				flags += fmt.Sprintf(" UNREADABLE=%d", ep.Unreadable)
			}
			totalDropped += ep.DroppedRows
			totalDroppedChanged += ep.DroppedChanged
			if flags != "" || ep.Error != "" {
				fmt.Printf("   %-14s rows=%d kept=%d dropped=%d(diff=%d) unreadable=%d%s %s\n",
					ep.Name, ep.SourceRows, ep.KeptRows, ep.DroppedRows, ep.DroppedChanged, ep.Unreadable, flags, ep.Error)
			} else {
				fmt.Printf("   %-14s rows=%d kept=%d dropped=%d(diff=%d)\n",
					ep.Name, ep.SourceRows, ep.KeptRows, ep.DroppedRows, ep.DroppedChanged)
			}
		}
		fmt.Printf("   integrity=%v  %s -> %s (%.1fx smaller)\n",
			report.IntegrityOK, human(report.SourceBytes), human(report.DestBytes), ratio(report.SourceBytes, report.DestBytes))

		if *apply && bad {
			if err := applySwap(src, dst); err != nil {
				fmt.Fprintf(os.Stderr, "   swap failed: %v\n", err)
				problems++
			} else {
				fmt.Printf("   swapped %s over source\n", filepath.Base(src))
			}
		}
	}

	fmt.Printf("\nTOTAL: %d files, %s -> %s (%.1fx smaller), %d dropped (%d differed), %d problems\n",
		len(files), human(totalSource), human(totalDest), ratio(totalSource, totalDest),
		totalDropped, totalDroppedChanged, problems)
	if !*apply {
		fmt.Printf("dry run: trimmed copies are in %q; nothing was replaced.\n", *outDir)
		fmt.Println("review the report, then rerun with --apply to swap verified copies over the originals.")
	}
}

func reportFailed(r *database.TrimReport) bool {
	if !r.IntegrityOK {
		return true
	}
	for _, ep := range r.Endpoints {
		if ep.Mismatch || ep.Error != "" {
			return true
		}
	}
	return false
}

func applySwap(src, dst string) error {
	backup := src + ".pre-trim"
	if _, err := os.Stat(backup); err == nil {
		return fmt.Errorf("backup already exists: %s", backup)
	}
	if err := os.Rename(src, backup); err != nil {
		return err
	}
	if err := os.Rename(dst, src); err != nil {
		// try to put the source back
		_ = os.Rename(backup, src)
		return err
	}
	return nil
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func ratio(before, after int64) float64 {
	if after == 0 {
		return 0
	}
	return float64(before) / float64(after)
}
