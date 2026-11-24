package main

import (
	"math"
	"testing"

	"market-terminal/internal/api"
	"market-terminal/internal/database"
)

func TestMajorLevelsFromSamplesSkipsBarsWithoutAFreshSample(t *testing.T) {
	base := float64(1_795_185_120) // arbitrary whole second
	timestamps := database.ChartSeries{base, base + 10, base + 400}
	samples := []api.HistMajor{
		{Timestamp: int64(base), Call: 100, Put: 90},
		// Nothing near base+400: more than the 300 s freshness window.
		{Timestamp: int64(base + 900), Call: 300, Put: 290},
	}

	got := majorLevelsFromSamples(timestamps, samples)
	if len(got) != 2 {
		t.Fatalf("levels = %+v, want 2 bars (the stale bar is skipped)", got)
	}
	if got[0].Timestamp != base || got[0].Call != 100 || got[0].Put != 90 {
		t.Fatalf("bar 0 = %+v", got[0])
	}
	// base+10 is held from the sample at base, which is inside the window.
	if got[1].Timestamp != base+10 || got[1].Call != 100 || got[1].Put != 90 {
		t.Fatalf("bar 1 = %+v", got[1])
	}
	for i := range got {
		if got[i].Timestamp == base+400 {
			t.Fatalf("bar %v was stored without a fresh sample: %+v", base+400, got[i])
		}
	}
}

func TestMajorLevelsFromSamplesKeepsOneSidedLevels(t *testing.T) {
	base := float64(1_795_185_120)
	timestamps := database.ChartSeries{base, base + 1, base + 2}
	samples := []api.HistMajor{
		{Timestamp: int64(base), Call: 100},   // no put level
		{Timestamp: int64(base + 1), Put: 90}, // no call level
		{Timestamp: int64(base + 2)},          // neither
	}

	got := majorLevelsFromSamples(timestamps, samples)
	if len(got) != 2 {
		t.Fatalf("levels = %+v, want 2 bars (the empty sample is skipped)", got)
	}
	if got[0].Call != 100 || got[0].Put != 0 {
		t.Fatalf("bar 0 = %+v, want call only", got[0])
	}
	if got[1].Call != 0 || got[1].Put != 90 {
		t.Fatalf("bar 1 = %+v, want put only", got[1])
	}
}

func TestMajorLevelsFromSamplesWithoutInputs(t *testing.T) {
	if got := majorLevelsFromSamples(nil, []api.HistMajor{{Timestamp: 1, Call: 1, Put: 1}}); len(got) != 0 {
		t.Fatalf("no bars = %+v", got)
	}
	if got := majorLevelsFromSamples(database.ChartSeries{1, 2}, nil); len(got) != 0 {
		t.Fatalf("no samples = %+v", got)
	}
}

func TestMergeMajorLevelsPrefersStoredValues(t *testing.T) {
	stored := database.ChartSeries{500, math.NaN(), 0}
	aligned := database.ChartSeries{100, 200, 300}
	got := mergeMajorLevels(stored, aligned)
	want := []float64{500, 200, 300}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged = %v, want %v", got, want)
		}
	}
}

func TestSeriesOfPadsAndTruncates(t *testing.T) {
	got := seriesOf(database.ChartSeries{1, 2, 3}, 5)
	if len(got) != 5 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0] != 1 || got[2] != 3 {
		t.Fatalf("got = %v", got)
	}
	if !math.IsNaN(got[3]) || !math.IsNaN(got[4]) {
		t.Fatalf("padding = %v", got[3:])
	}
	short := seriesOf(database.ChartSeries{1, 2, 3}, 2)
	if len(short) != 2 || short[1] != 2 {
		t.Fatalf("truncated = %v", short)
	}
	if allNaN := seriesOf(nil, 2); len(allNaN) != 2 || !math.IsNaN(allNaN[0]) {
		t.Fatalf("nil column = %v", allNaN)
	}
}

func TestIsLevelValue(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0} {
		if isLevelValue(bad) {
			t.Fatalf("%v reported as a level", bad)
		}
	}
	if !isLevelValue(0.01) || !isLevelValue(-5) {
		t.Fatal("a real level was rejected")
	}
}
