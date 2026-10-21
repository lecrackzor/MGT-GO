package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// A full ladder is written on the first sample, when the strike list changes,
// and after this many deltas. Reads walk back only as far as that full ladder.
const profileKeyframeEvery = 40

var profileLadderKeys = []string{"strikes", "mini_contracts"}

var profileStoreReady sync.Map

func ensureProfileTable(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS endpoint_profile (
			timestamp REAL,
			endpoint TEXT,
			payload BLOB,
			PRIMARY KEY (timestamp, endpoint)
		) WITHOUT ROWID
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_endpoint_profile_ep_ts ON endpoint_profile(endpoint, timestamp)`); err != nil {
		return err
	}
	return nil
}

// prepareProfileStore creates the profile table and its endpoint index once.
// The loader's pooled connection is read-only, so this uses its own handle.
func prepareProfileStore(path string) error {
	if _, ok := profileStoreReady.Load(path); ok {
		return nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout=10000"); err != nil {
		return err
	}
	if err := ensureProfileTable(db); err != nil {
		return err
	}
	profileStoreReady.Store(path, struct{}{})
	return nil
}

func markProfileStoreReady(path string) {
	profileStoreReady.Store(path, struct{}{})
}

// encodeProfile stores a full ladder, or a delta of the strikes and fields that
// changed. skip means the sample is identical to the previous one.
// The returned chain is how many deltas have been written since the last full ladder.
func encodeProfile(prev []byte, prevChain int, next []byte) (stored []byte, chain int, skip bool, err error) {
	next = bytes.TrimSpace(next)
	prev = bytes.TrimSpace(prev)
	if len(next) == 0 || next[0] != '{' || !hasStrikeLadder(next) {
		return next, 0, false, nil
	}
	if len(prev) == 0 || prev[0] != '{' || prevChain+1 >= profileKeyframeEvery {
		return next, 0, false, nil
	}
	if bytes.Equal(compact(prev), compact(next)) {
		return nil, prevChain, true, nil
	}
	var prevObj, nextObj map[string]json.RawMessage
	if json.Unmarshal(prev, &prevObj) != nil {
		return next, 0, false, nil
	}
	if err := json.Unmarshal(next, &nextObj); err != nil {
		return nil, 0, false, err
	}
	delta := map[string]interface{}{"_d": 1, "n": prevChain + 1}
	changed := false
	for _, key := range profileLadderKeys {
		nextRaw, inNext := nextObj[key]
		if !inNext {
			continue
		}
		prevRaw, inPrev := prevObj[key]
		prevRows, prevOK := decodeRows(prevRaw)
		nextRows, nextOK := decodeRows(nextRaw)
		if !inPrev || !prevOK || !nextOK || !sameStrikeSeq(prevRows, nextRows) {
			return next, 0, false, nil
		}
		var rows []json.RawMessage
		for i := range nextRows {
			if !bytes.Equal(compact(prevRows[i]), compact(nextRows[i])) {
				rows = append(rows, json.RawMessage(compact(nextRows[i])))
			}
		}
		if len(rows) > 0 {
			delta[key] = rows
			changed = true
		}
	}
	set := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for key, value := range nextObj {
		if isLadderKey(key) {
			continue
		}
		seen[key] = true
		prevValue, ok := prevObj[key]
		if !ok || !bytes.Equal(compact(prevValue), compact(value)) {
			set[key] = json.RawMessage(compact(value))
		}
	}
	if len(set) > 0 {
		delta["set"] = set
		changed = true
	}
	var drop []string
	for key := range prevObj {
		if isLadderKey(key) || seen[key] {
			continue
		}
		drop = append(drop, key)
	}
	if len(drop) > 0 {
		sort.Strings(drop)
		delta["drop"] = drop
		changed = true
	}
	if !changed {
		return nil, prevChain, true, nil
	}
	stored, err = json.Marshal(delta)
	if err != nil {
		return nil, 0, false, err
	}
	return stored, prevChain + 1, false, nil
}

// profileQuery is the Query method on *sql.DB and *sql.Tx.
type profileQuery interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// profileAt rebuilds one endpoint as of at (nil means the newest sample).
func profileAt(db profileQuery, endpoint string, at *float64) ([]byte, float64, int, error) {
	query := `SELECT timestamp, payload FROM endpoint_profile WHERE endpoint = ?`
	args := []interface{}{endpoint}
	if at != nil {
		query += ` AND timestamp <= ?`
		args = append(args, *at)
	}
	query += ` ORDER BY timestamp DESC LIMIT ?`
	args = append(args, profileKeyframeEvery+2)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	type sample struct {
		ts  float64
		raw []byte
	}
	var items []sample
	for rows.Next() {
		var ts float64
		var payload []byte
		if err := rows.Scan(&ts, &payload); err != nil {
			return nil, 0, 0, err
		}
		raw, err := gunzipBytes(payload)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("profile %s at %v: %w", endpoint, ts, err)
		}
		items = append(items, sample{ts: ts, raw: raw})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	if len(items) == 0 {
		return nil, 0, 0, nil
	}
	base := -1
	for i, item := range items {
		if !isProfileDelta(item.raw) {
			base = i
			break
		}
	}
	if base < 0 {
		return nil, 0, 0, fmt.Errorf("profile %s is missing its full ladder", endpoint)
	}
	body := items[base].raw
	for i := base - 1; i >= 0; i-- {
		var applyErr error
		body, applyErr = applyProfileDelta(body, items[i].raw)
		if applyErr != nil {
			return nil, 0, 0, fmt.Errorf("profile %s at %v: %w", endpoint, items[i].ts, applyErr)
		}
	}
	chain := 0
	if isProfileDelta(items[0].raw) {
		chain = profileDeltaChain(items[0].raw)
	}
	return body, items[0].ts, chain, nil
}

func applyProfileDelta(base, delta []byte) ([]byte, error) {
	var doc struct {
		Strikes []json.RawMessage          `json:"strikes"`
		Mini    []json.RawMessage          `json:"mini_contracts"`
		Set     map[string]json.RawMessage `json:"set"`
		Drop    []string                   `json:"drop"`
	}
	if err := json.Unmarshal(delta, &doc); err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(base, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	if len(doc.Strikes) > 0 {
		merged, err := mergeLadder(obj["strikes"], doc.Strikes)
		if err != nil {
			return nil, err
		}
		obj["strikes"] = merged
	}
	if len(doc.Mini) > 0 {
		merged, err := mergeLadder(obj["mini_contracts"], doc.Mini)
		if err != nil {
			return nil, err
		}
		obj["mini_contracts"] = merged
	}
	for key, value := range doc.Set {
		obj[key] = value
	}
	for _, key := range doc.Drop {
		delete(obj, key)
	}
	return marshalObject(obj), nil
}

func mergeLadder(base json.RawMessage, changes []json.RawMessage) (json.RawMessage, error) {
	rows, ok := decodeRows(base)
	if !ok {
		return nil, fmt.Errorf("ladder base is not a strike list")
	}
	index := make(map[string]int, len(rows))
	for i, row := range rows {
		token, ok := strikeToken(row)
		if !ok {
			return nil, fmt.Errorf("strike row is missing its price")
		}
		index[token] = i
	}
	for _, row := range changes {
		token, ok := strikeToken(row)
		if !ok {
			return nil, fmt.Errorf("changed strike row is missing its price")
		}
		at, found := index[token]
		if !found {
			return nil, fmt.Errorf("changed strike %s is not in the saved ladder", token)
		}
		rows[at] = row
	}
	return joinRows(rows), nil
}

func listProfileEndpoints(db *sql.DB, at *float64) ([]string, error) {
	query := `SELECT DISTINCT endpoint FROM endpoint_profile`
	var args []interface{}
	if at != nil {
		query += ` WHERE timestamp <= ?`
		args = append(args, *at)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func hasStrikeLadder(raw []byte) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}
	for _, key := range profileLadderKeys {
		rows, ok := decodeRows(obj[key])
		if ok && len(rows) > 0 {
			return true
		}
	}
	return false
}

func isLadderKey(key string) bool {
	for _, name := range profileLadderKeys {
		if key == name {
			return true
		}
	}
	return false
}

func isProfileDelta(raw []byte) bool {
	var probe struct {
		D int `json:"_d"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.D == 1
}

func profileDeltaChain(raw []byte) int {
	var probe struct {
		N int `json:"n"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.N < 0 {
		return 0
	}
	return probe.N
}

func decodeRows(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil, false
	}
	for _, row := range rows {
		if _, ok := strikeToken(row); !ok {
			return nil, false
		}
	}
	return rows, true
}

func sameStrikeSeq(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, okA := strikeToken(a[i])
		right, okB := strikeToken(b[i])
		if !okA || !okB || left != right {
			return false
		}
	}
	return true
}

func strikeToken(row json.RawMessage) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(row))
	token, err := dec.Token()
	if err != nil || token != json.Delim('[') {
		return "", false
	}
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		return "", false
	}
	return string(compact(first)), true
}

func joinRows(rows []json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, row := range rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(compact(row))
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

func marshalObject(fields map[string]json.RawMessage) []byte {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(compact(fields[key]))
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func compact(raw []byte) []byte {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return bytes.TrimSpace(raw)
	}
	return buf.Bytes()
}
