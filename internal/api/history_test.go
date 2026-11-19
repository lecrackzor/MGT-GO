package api

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"market-terminal/internal/config"
)

// A Classic account must never spend a request on a Greek or orderflow
// category, because that is a 403 and a wasted quota slot.
func TestHistoryCategoriesFollowTiers(t *testing.T) {
	settings := &config.Settings{APISubscriptionTiers: []string{"classic"}}
	categories := HistoryCategoriesForEndpoints(GetEndpointsForTiers(settings.APISubscriptionTiers))
	if len(categories) == 0 {
		t.Fatal("classic account resolved no categories")
	}
	for _, category := range categories {
		if category.Package != "classic" {
			t.Fatalf("classic account requested %s/%s", category.Package, category.Category)
		}
	}

	state := HistoryCategoriesForEndpoints(GetEndpointsForTiers([]string{"state"}))
	seen := map[string]bool{}
	for _, category := range state {
		seen[category.Package] = true
	}
	if !seen["state"] {
		t.Fatalf("state account resolved %v", state)
	}
	if seen["orderflow"] {
		t.Fatal("state account requested orderflow")
	}

	found := false
	for _, category := range HistoryCategoriesForEndpoints(GetEndpointsForTiers([]string{"orderflow"})) {
		if category.Endpoint == "orderflow" {
			found = true
		}
	}
	if !found {
		t.Fatal("orderflow account resolved no orderflow category")
	}
}

func TestArchiveEndpointMapping(t *testing.T) {
	cases := map[string]string{
		"classic/gex_zero":    "classic_zero",
		"classic/gex_full":    "classic_full",
		"state/delta_zero":    "delta_zero",
		"state/gamma_one":     "gamma_one",
		"orderflow/orderflow": "orderflow",
	}
	for path, want := range cases {
		parts := strings.SplitN(path, "/", 2)
		if got := EndpointForArchive(parts[0], parts[1]); got != want {
			t.Fatalf("%s -> %q, want %q", path, got, want)
		}
	}
	if EndpointForArchive("classic", "volume_zero") != "" {
		t.Fatal("volume_zero must not map to an endpoint")
	}
}

func TestArchiveDateFromDisposition(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="SPX_2026-11-17.zip"`: "2026-11-17",
		`attachment; filename=SPX_20261117.zip`:     "2026-11-17",
		`attachment; filename="spx-archive.zip"`:    "",
		``:                                          "",
	}
	for header, want := range cases {
		if got := archiveDateFromDisposition(header); got != want {
			t.Fatalf("%q -> %q, want %q", header, got, want)
		}
	}
}

func TestParseHistoryStreamForms(t *testing.T) {
	ndjson := `{"timestamp":1700000000,"spot":6000,"strikes":[[6000,1,2,[9,8,7,6,5]]]}` + "\n" +
		`{"timestamp":1700000001,"spot":6001}` + "\n"
	array := `[{"timestamp":1700000000,"spot":6000,"strikes":[[6000,1,2,[9,8,7,6,5]]]},{"timestamp":1700000001,"spot":6001}]`
	for name, body := range map[string]string{"ndjson": ndjson, "array": array} {
		var got []HistorySample
		err := parseHistoryStream(strings.NewReader(body), func(sample HistorySample) error {
			got = append(got, sample)
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s: %d samples", name, len(got))
		}
		if got[0].Timestamp != 1700000000 || got[1].Timestamp != 1700000001 {
			t.Fatalf("%s timestamps: %v", name, got)
		}
		if _, ok := got[0].Payload["timestamp"]; ok {
			t.Fatalf("%s kept the timestamp field", name)
		}
		if _, ok := got[0].Payload["strikes"]; !ok {
			t.Fatalf("%s dropped the ladder", name)
		}
	}
}

// A rate limited history call must return straight away so the pass can stop.
func TestFetchIntradayHistoryRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient("test-key", func(string, string) {})
	client.SetBaseURL(server.URL)
	_, err := client.FetchIntradayHistory(context.Background(), "SPX", "classic", "gex_zero", 1, 2)
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("error = %v, want RateLimitError", err)
	}
	if limited.RetryAfter != "120" {
		t.Fatalf("retry after = %q", limited.RetryAfter)
	}
}

// The archive reader decodes a gzipped entry and skips entries with no
// snapshot endpoint, without decoding them.
func TestReadEODArchiveSkipsUnknownEntries(t *testing.T) {
	archive := buildTestArchive(t, map[string]string{
		"SPX/classic/gex_zero/SPX_2026-11-17.json.gz":    `{"timestamp":1700000000,"spot":6000}` + "\n",
		"SPX/orderflow/orderflow/SPX_2026-11-17.json.gz": `{"timestamp":1700000000,"zgr":1}` + "\n",
		"SPX/notes/readme.txt":                           "not a snapshot",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="SPX_2026-11-17.zip"`)
		w.Write(archive)
	}))
	defer server.Close()

	client := NewClient("test-key", func(string, string) {})
	client.SetBaseURL(server.URL)
	var endpoints []string
	date, err := client.ReadEODArchive(context.Background(), "SPX", func(endpoint string, sample HistorySample) error {
		endpoints = append(endpoints, endpoint)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-11-17" {
		t.Fatalf("archive date = %q", date)
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %v", endpoints)
	}
	for _, endpoint := range endpoints {
		if endpoint == "" {
			t.Fatal("unknown archive entry was decoded")
		}
	}
}

// A gateway blip must be retried, not surfaced as a permanent failure.
func TestFetchEndpointRetriesServerErrors(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"timestamp":1700000000,"spot":6000,"strikes":[[6000,1,2]]}`))
	}))
	defer server.Close()

	client := NewClient("test-key", func(string, string) {})
	client.SetBaseURL(server.URL)
	data, err := client.FetchEndpoint(context.Background(), "classic_zero", "SPX")
	if err != nil {
		t.Fatal(err)
	}
	if data["spot"] != 6000.0 {
		t.Fatalf("spot = %#v", data["spot"])
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

// A truncated body decodes as invalid JSON; one retry recovers it.
func TestFetchEndpointRetriesInvalidJSON(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"timestamp":1700000000,"spot":`))
			return
		}
		w.Write([]byte(`{"timestamp":1700000000,"spot":6000}`))
	}))
	defer server.Close()

	client := NewClient("test-key", func(string, string) {})
	client.SetBaseURL(server.URL)
	if _, err := client.FetchEndpoint(context.Background(), "classic_zero", "SPX"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// A 403 is the account's tier, not a blip, and must not be retried.
func TestFetchEndpointDoesNotRetryForbidden(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	client := NewClient("test-key", func(string, string) {})
	client.SetBaseURL(server.URL)
	_, err := client.FetchEndpoint(context.Background(), "gamma_zero", "SPX")
	var subscription *SubscriptionError
	if !errors.As(err, &subscription) {
		t.Fatalf("error = %v, want SubscriptionError", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// buildTestArchive builds a zip whose entries are gzipped JSON, the shape the
// end-of-day route serves.
func buildTestArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".gz") {
			gz := gzip.NewWriter(w)
			if _, err := gz.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
