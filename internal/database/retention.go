package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"market-terminal/internal/utils"
)

// dayFolderLayout is the "01.02.2006" suffix of a day folder name.
const dayFolderLayout = "01.02.2006"

// PruneOldDays removes whole day folders older than keepDays and returns the
// folders it removed. keepDays <= 0 keeps everything. The current market date
// is never removed, and a folder is only removed when its name parses as a day
// folder for this data root, so an unrelated directory is left alone.
func PruneOldDays(dataDir string, keepDays int, now time.Time) ([]string, error) {
	if keepDays <= 0 {
		return nil, nil
	}
	root := DataRoot(dataDir)
	parent := filepath.Dir(root)
	prefix := filepath.Base(root) + " "

	entries, err := os.ReadDir(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	location := utils.GetMarketTimezone()
	nowET := now.In(location)
	today := utils.GetMarketDateForDate(nowET)
	cutoff := today.AddDate(0, 0, -keepDays)

	var removed []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		stamp := strings.TrimPrefix(entry.Name(), prefix)
		day, parseErr := time.ParseInLocation(dayFolderLayout, stamp, location)
		if parseErr != nil {
			continue
		}
		if !day.Before(cutoff) {
			continue
		}
		// Belt and braces: never remove the session being written right now.
		if day.Format(dayFolderLayout) == today.Format(dayFolderLayout) {
			continue
		}
		full := filepath.Join(parent, entry.Name())
		if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(parent)+string(os.PathSeparator)) {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return removed, fmt.Errorf("failed to prune %s: %w", full, err)
		}
		removed = append(removed, entry.Name())
	}
	return removed, nil
}
