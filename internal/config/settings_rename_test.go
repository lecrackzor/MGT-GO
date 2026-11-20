package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The intraday request cap was renamed from the misleading
// intraday_fill_max_requests_per_day, which was a per-category sweep cap all
// along. A config written before the rename must keep its value, and a zero cap
// would read as "unlimited", so the fallback matters.
func TestLoadSettingsRenamesIntradayRequestCap(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want int
	}{
		{
			name: "new key wins",
			yaml: "intraday_fill_max_requests_per_category: 3\nintraday_fill_max_requests_per_day: 9\n",
			want: 3,
		},
		{
			name: "old key carries over",
			yaml: "intraday_fill_max_requests_per_day: 5\n",
			want: 5,
		},
		{
			name: "absent key falls back to the default",
			yaml: "enable_debug: true\n",
			want: 8,
		},
		{
			name: "explicit zero is replaced so the cap is not unlimited",
			yaml: "intraday_fill_max_requests_per_category: 0\n",
			want: 8,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := NewSettingsManager(path).LoadSettings()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.IntradayFillMaxRequestsPerCategory != tc.want {
				t.Fatalf("cap = %d, want %d", got.IntradayFillMaxRequestsPerCategory, tc.want)
			}
		})
	}
}
