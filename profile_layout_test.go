package main

import "testing"

func TestMergeProfileLayout(t *testing.T) {
	profile := map[string]interface{}{
		"enabled":            true,
		"profile_side":       "right",
		"side_width_percent": 28.0,
	}
	mergeProfileLayout(profile, map[string]interface{}{
		"profile_side":       "left",
		"side_width_percent": 80.0,
		"bar_width_percent":  4.0,
		"profile_x_percent":  50.0,
		"enabled":            false,
	})
	if profile["profile_side"] != "left" {
		t.Fatalf("side = %v", profile["profile_side"])
	}
	if profile["side_width_percent"] != 50.0 {
		t.Fatalf("side width = %v", profile["side_width_percent"])
	}
	if profile["bar_width_percent"] != 8.0 {
		t.Fatalf("bar width = %v", profile["bar_width_percent"])
	}
	if profile["profile_x_percent"] != 50.0 {
		t.Fatalf("center = %v", profile["profile_x_percent"])
	}
	if profile["enabled"] != true {
		t.Fatalf("enabled should stay untouched, got %v", profile["enabled"])
	}
	mergeProfileLayout(profile, map[string]interface{}{"profile_side": "middle"})
	if profile["profile_side"] != "left" {
		t.Fatalf("bad side was stored: %v", profile["profile_side"])
	}
}
