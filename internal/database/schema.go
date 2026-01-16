package database

import (
	"database/sql"
	"fmt"
	"strings"
)

// SchemaManager manages database schema creation and migration
type SchemaManager struct {
	db *sql.DB
}

// NewSchemaManager creates a new schema manager
func NewSchemaManager(db *sql.DB) *SchemaManager {
	return &SchemaManager{db: db}
}

// EnsureTable ensures the ticker_data table exists with proper schema
func (sm *SchemaManager) EnsureTable(scalarFields []string) error {
	// Create base table if it doesn't exist
	_, err := sm.db.Exec(`
		CREATE TABLE IF NOT EXISTS ticker_data (
			timestamp REAL PRIMARY KEY,
			profiles_blob BLOB
		) WITHOUT ROWID
	`)
	if err != nil {
		return fmt.Errorf("failed to create base table: %w", err)
	}

	// Get existing columns
	existingColumns, err := sm.getExistingColumns()
	if err != nil {
		return fmt.Errorf("failed to get existing columns: %w", err)
	}

	// Add missing columns
	for _, field := range scalarFields {
		sanitized := sanitizeFieldName(field)
		if sanitized == "timestamp" || sanitized == "profiles_blob" {
			continue // Skip base columns
		}

		if !existingColumns[sanitized] {
			// Determine column type (default to REAL for numbers)
			colType := "REAL"
			// Could check field name patterns here for TEXT fields if needed

			_, err := sm.db.Exec(fmt.Sprintf(
				"ALTER TABLE ticker_data ADD COLUMN %s %s",
				sanitized, colType,
			))
			if err != nil {
				// Column might already exist (race condition) - ignore
				if !strings.Contains(err.Error(), "duplicate column") {
					return fmt.Errorf("failed to add column %s: %w", sanitized, err)
				}
			}
		}
	}

	// Create indexes if they don't exist
	_, err = sm.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_timestamp_desc
		ON ticker_data(timestamp DESC)
	`)
	if err != nil {
		return fmt.Errorf("failed to create descending index: %w", err)
	}

	_, err = sm.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_timestamp_asc
		ON ticker_data(timestamp ASC)
	`)
	if err != nil {
		return fmt.Errorf("failed to create ascending index: %w", err)
	}

	return nil
}

// getExistingColumns returns a map of existing column names
func (sm *SchemaManager) getExistingColumns() (map[string]bool, error) {
	rows, err := sm.db.Query(`
		SELECT name FROM pragma_table_info('ticker_data')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}

	return columns, rows.Err()
}

// sanitizeFieldName sanitizes a field name for use as a SQL column name
// Matches Python version's sanitize_field_name() behavior for compatibility
func sanitizeFieldName(field string) string {
	// Replace special characters with underscores (matches Python: replace('-', '_').replace('.', '_').replace(' ', '_'))
	result := strings.Builder{}
	for _, r := range field {
		if r == '-' || r == '.' || r == ' ' {
			result.WriteRune('_')
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	
	sanitized := result.String()
	
	// Remove leading/trailing underscores (matches Python: strip('_'))
	sanitized = strings.Trim(sanitized, "_")
	
	// Ensure it starts with a letter or underscore (matches Python behavior)
	if len(sanitized) > 0 {
		first := sanitized[0]
		if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
			sanitized = "_" + sanitized
		}
	}
	
	return sanitized
}
