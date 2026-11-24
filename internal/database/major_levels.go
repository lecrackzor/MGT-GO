package database

import (
	"database/sql"
	"fmt"
	"time"
)

// MajorLevel is one sample of the historical majors series the NinjaTrader
// indicator draws: the major call gamma and major put gamma levels.
//
// These are not part of a chain snapshot. The public chart endpoints carry
// major_pos_vol / major_neg_vol (the largest positive and negative gamma
// snapshots, sized by volume) and the major_pos_oi / major_neg_oi strikes,
// which are a different calculation. The call and put levels only come from
// the Ninja history feed, so unless they are written here they exist only in
// the memory cache and vanish with the process. The feed also prunes old
// sessions, so a session that is not stored while it is still served is gone.
type MajorLevel struct {
	Timestamp float64
	Call      float64
	Put       float64
}

// FillMajorLevels writes the call and put levels onto timestamps that already
// exist in the day. It never inserts a row and never replaces a value that is
// already set, so it is safe to run on every poll: a second pass over the same
// timestamps only fills what is still empty.
//
// A missing timestamp is left alone rather than created. The chart only draws
// these levels where it has a bar, and inserting a row would add a bar with
// every other column null.
func (dw *DataWriter) FillMajorLevels(ticker string, date time.Time, levels []MajorLevel) (int, error) {
	if len(levels) == 0 {
		return 0, nil
	}
	path := dw.getDBPath(ticker, date)
	db, err := dw.pool.GetConnection(path, false)
	if err != nil {
		dw.pool.Discard(path)
		return 0, fmt.Errorf("failed to open database: %w", err)
	}
	// An archive-recovered day is written without these columns, and the
	// schema cache may still hold a claim that this file has them. Forget the
	// cached column set so the check below looks at the file.
	dw.forgetSchema(path)
	if err := dw.ensureSchema(db, path, []string{"major_call_gamma", "major_put_gamma"}); err != nil {
		return 0, fmt.Errorf("failed to ensure schema: %w", err)
	}
	return dw.updateMajorLevels(db, levels)
}

// updateMajorLevels fills the two columns in one transaction, in chunks so a
// full session never holds the file open for a long single write.
func (dw *DataWriter) updateMajorLevels(db *sql.DB, levels []MajorLevel) (int, error) {
	const chunkSize = 2000
	filled := 0
	for start := 0; start < len(levels); start += chunkSize {
		end := start + chunkSize
		if end > len(levels) {
			end = len(levels)
		}
		n, err := dw.updateMajorLevelChunk(db, levels[start:end])
		filled += n
		if err != nil {
			return filled, err
		}
	}
	return filled, nil
}

func (dw *DataWriter) updateMajorLevelChunk(db *sql.DB, levels []MajorLevel) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// A stored value wins. Only a null or zero column is filled, which keeps
	// the pass idempotent and never overwrites a level that was already known.
	stmt, err := tx.Prepare(`
		UPDATE ticker_data
		   SET major_call_gamma = CASE
		         WHEN major_call_gamma IS NULL OR major_call_gamma = 0 THEN ?
		         ELSE major_call_gamma
		       END,
		       major_put_gamma = CASE
		         WHEN major_put_gamma IS NULL OR major_put_gamma = 0 THEN ?
		         ELSE major_put_gamma
		       END
		 WHERE timestamp = ?
		   AND ((major_call_gamma IS NULL OR major_call_gamma = 0)
		     OR (major_put_gamma IS NULL OR major_put_gamma = 0))`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare major level update: %w", err)
	}
	defer stmt.Close()

	filled := 0
	for _, level := range levels {
		result, err := stmt.Exec(level.Call, level.Put, level.Timestamp)
		if err != nil {
			return filled, fmt.Errorf("failed to store major levels at %v: %w", level.Timestamp, err)
		}
		if count, err := result.RowsAffected(); err == nil && count > 0 {
			filled += int(count)
		}
	}
	if err := tx.Commit(); err != nil {
		return filled, fmt.Errorf("failed to commit major levels: %w", err)
	}
	return filled, nil
}
