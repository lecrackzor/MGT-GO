package api

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The recovery routes can stream a whole session or a whole day archive, so
// they use a client without the snapshot timeout. The context still bounds
// them.
func (c *Client) longClient() *http.Client {
	return &http.Client{Transport: c.httpClient.Transport}
}

// HistorySample is one recovered snapshot. Payload keeps the decoded scalar
// fields and the raw ladders, ready for SplitBackfillSample.
type HistorySample struct {
	Timestamp float64
	Payload   map[string]interface{}
}

// FetchIntradayHistory streams today's one-second snapshots for one category.
// POST /hist/{ticker}/{package}/{category}, up to eight hours per request.
func (c *Client) FetchIntradayHistory(ctx context.Context, ticker, pkg, category string, start, end int64) ([]HistorySample, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	apiKey := strings.TrimSpace(c.getAPIKey())
	if apiKey == "" {
		return nil, &SubscriptionError{Endpoint: pkg + "/" + category, Message: "API key is not configured"}
	}
	if end <= start {
		return nil, fmt.Errorf("history range is empty")
	}
	url := fmt.Sprintf("%s/hist/%s/%s/%s", c.getBaseURL(), ticker, pkg, category)
	body, err := json.Marshal(map[string]int64{"start": start, "end": end})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")

	c.requestCount.Add(1)
	resp, err := c.longClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("history request failed: %w", err)
	}
	defer resp.Body.Close()
	if err := historyStatusError(resp, pkg+"/"+category); err != nil {
		return nil, err
	}

	var samples []HistorySample
	err = parseHistoryStream(resp.Body, func(sample HistorySample) error {
		samples = append(samples, sample)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("history %s/%s: %w", pkg, category, err)
	}
	return samples, nil
}

// ReadEODArchive downloads the latest archive for one ticker and hands every
// sample to visit as it is decoded, so a large day never has to sit in memory
// as decoded maps. The archive date comes from Content-Disposition.
// GET /hist/eod/{ticker}
func (c *Client) ReadEODArchive(ctx context.Context, ticker string, visit func(endpoint string, sample HistorySample) error) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	apiKey := strings.TrimSpace(c.getAPIKey())
	if apiKey == "" {
		return "", &SubscriptionError{Endpoint: "hist/eod", Message: "API key is not configured"}
	}
	url := fmt.Sprintf("%s/hist/eod/%s", c.getBaseURL(), ticker)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/zip")

	c.requestCount.Add(1)
	resp, err := c.longClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("archive request failed: %w", err)
	}
	defer resp.Body.Close()
	if err := historyStatusError(resp, "hist/eod"); err != nil {
		return "", err
	}
	archiveDate := archiveDateFromDisposition(resp.Header.Get("Content-Disposition"))

	const maxArchiveBytes = 512 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return archiveDate, fmt.Errorf("archive read failed: %w", err)
	}
	if len(raw) > maxArchiveBytes {
		return archiveDate, fmt.Errorf("archive is larger than %d MB", maxArchiveBytes>>20)
	}

	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		// Some deployments answer the route with a single JSON stream.
		err = parseHistoryStream(bytes.NewReader(raw), func(sample HistorySample) error {
			return visit("", sample)
		})
		if err != nil {
			return archiveDate, fmt.Errorf("archive is not a readable zip: %w", err)
		}
		return archiveDate, nil
	}

	for _, file := range reader.File {
		select {
		case <-ctx.Done():
			return archiveDate, ctx.Err()
		default:
		}
		if file.FileInfo().IsDir() {
			continue
		}
		pkg, category, ok := archivePathParts(file.Name)
		if !ok {
			continue
		}
		endpoint := EndpointForArchive(pkg, category)
		if endpoint == "" {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			return archiveDate, fmt.Errorf("archive entry %s: %w", file.Name, err)
		}
		var data io.Reader = entry
		var gz *gzip.Reader
		if strings.HasSuffix(strings.ToLower(file.Name), ".gz") {
			gz, err = gzip.NewReader(entry)
			if err != nil {
				entry.Close()
				return archiveDate, fmt.Errorf("archive entry %s: %w", file.Name, err)
			}
			data = gz
		}
		err = parseHistoryStream(data, func(sample HistorySample) error {
			return visit(endpoint, sample)
		})
		if gz != nil {
			gz.Close()
		}
		entry.Close()
		if err != nil {
			return archiveDate, fmt.Errorf("archive entry %s: %w", file.Name, err)
		}
	}
	return archiveDate, nil
}

func historyStatusError(resp *http.Response, what string) error {
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return &SubscriptionError{Endpoint: what, Message: "Unauthorized. Check the API key."}
	case http.StatusForbidden:
		return &SubscriptionError{Endpoint: what, Message: "This endpoint requires a subscription tier you don't have."}
	case http.StatusTooManyRequests:
		return &RateLimitError{
			Endpoint:   what,
			Message:    "Rate limit exceeded",
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return &RequestError{
		Endpoint:   what,
		StatusCode: resp.StatusCode,
		Message:    fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
	}
}

// parseHistoryStream reads either an NDJSON stream or a JSON array of snapshot
// objects and calls visit for each one.
func parseHistoryStream(r io.Reader, visit func(HistorySample) error) error {
	buffered := bufio.NewReaderSize(r, 64*1024)
	first, err := firstNonSpace(buffered)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	if first == '[' {
		dec := json.NewDecoder(buffered)
		if _, err := dec.Token(); err != nil {
			return err
		}
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return err
			}
			sample, ok, err := decodeHistorySample(raw)
			if err != nil {
				return err
			}
			if ok {
				if err := visit(sample); err != nil {
					return err
				}
			}
		}
		_, err := dec.Token()
		return err
	}

	scanner := bufio.NewScanner(buffered)
	scanner.Buffer(make([]byte, 0, 256*1024), 32<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		sample, ok, err := decodeHistorySample(line)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := visit(sample); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func firstNonSpace(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			if err := r.UnreadByte(); err != nil {
				return 0, err
			}
			return b, nil
		}
	}
}

// decodeHistorySample pulls the timestamp out of one object and leaves the
// rest exactly as the snapshot routes return it.
func decodeHistorySample(raw []byte) (HistorySample, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return HistorySample{}, false, nil
	}
	payload, err := decodeResponse(raw)
	if err != nil {
		return HistorySample{}, false, err
	}
	ts, found := historyTimestamp(payload)
	if !found {
		return HistorySample{}, false, nil
	}
	for _, key := range []string{"timestamp", "t", "time", "ts"} {
		delete(payload, key)
	}
	return HistorySample{Timestamp: ts, Payload: payload}, true, nil
}

func historyTimestamp(payload map[string]interface{}) (float64, bool) {
	for _, key := range []string{"timestamp", "t", "time", "ts"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		ts, ok := toSeconds(value)
		if ok && ts > 0 {
			return ts, true
		}
	}
	return 0, false
}

func toSeconds(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return normalizeEpoch(v), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return normalizeEpoch(f), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return normalizeEpoch(f), true
	default:
		return 0, false
	}
}

// normalizeEpoch accepts seconds, milliseconds and microseconds.
func normalizeEpoch(v float64) float64 {
	for v > 1e11 {
		v /= 1000
	}
	return v
}

// archivePathParts reads {TICKER}/{package}/{category}/{file} entry names.
func archivePathParts(name string) (string, string, bool) {
	parts := strings.Split(strings.ReplaceAll(name, "\\", "/"), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	pkg := strings.ToLower(strings.TrimSpace(parts[len(parts)-3]))
	category := strings.ToLower(strings.TrimSpace(parts[len(parts)-2]))
	if pkg == "" || category == "" {
		return "", "", false
	}
	return pkg, category, true
}

// archiveDatePattern finds the session date inside an archive file name, in
// either 2026-11-17 or 20261117 form.
var archiveDatePattern = regexp.MustCompile(`(20\d{2})-?(\d{2})-?(\d{2})`)

func archiveDateFromDisposition(value string) string {
	if value == "" {
		return ""
	}
	match := archiveDatePattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	parsed, err := time.Parse("2006-01-02", match[1]+"-"+match[2]+"-"+match[3])
	if err != nil || parsed.Year() < 2000 {
		return ""
	}
	return parsed.Format("2006-01-02")
}
