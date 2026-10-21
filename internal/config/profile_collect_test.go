package config

import "testing"

func TestProfileOffKeepsNothingInChartMode(t *testing.T) {
	settings := &Settings{CollectAllEndpoints: false}
	if ProfileEnabled(settings) {
		t.Fatal("missing profile switch should be off")
	}
	if KeepProfileEndpoint(settings, "classic_zero") {
		t.Fatal("chart-data mode stored a ladder while the profile was off")
	}
}

func TestProfileOnKeepsOnlyCheckedLayers(t *testing.T) {
	settings := &Settings{
		CollectAllEndpoints: false,
		ProfileSettings: map[string]interface{}{
			"enabled": true,
			"layers": map[string]interface{}{
				"classic": true,
				"gamma":   true,
			},
		},
	}
	got := ProfileEndpoints(settings)
	want := map[string]bool{
		"classic_full": true, "classic_zero": true, "classic_one": true,
		"gamma_zero": true, "gamma_one": true,
	}
	if len(got) != len(want) {
		t.Fatalf("endpoints = %v", got)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("unexpected endpoint %s in %v", name, got)
		}
		if !KeepProfileEndpoint(settings, name) {
			t.Fatalf("checked layer %s was dropped", name)
		}
	}
	if KeepProfileEndpoint(settings, "state_zero") || KeepProfileEndpoint(settings, "vanna_one") {
		t.Fatal("unchecked layers were kept")
	}
}

func TestCollectAllKeepsEveryLadder(t *testing.T) {
	settings := &Settings{CollectAllEndpoints: true}
	if !KeepProfileEndpoint(settings, "charm_one") {
		t.Fatal("collect-all dropped a ladder")
	}
}

func TestMissingLayersMeanClassicOnly(t *testing.T) {
	settings := &Settings{ProfileSettings: map[string]interface{}{"enabled": true}}
	got := ProfileEndpoints(settings)
	if len(got) != 3 || got[0] != "classic_full" || got[1] != "classic_zero" || got[2] != "classic_one" {
		t.Fatalf("default layers = %v", got)
	}
}
