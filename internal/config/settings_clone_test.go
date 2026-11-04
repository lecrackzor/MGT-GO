package config

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestCloneSettingsDoesNotShareProfileMaps(t *testing.T) {
	live := &Settings{
		ProfileSettings: map[string]interface{}{
			"enabled": true,
			"layers": map[string]interface{}{
				"classic": true,
				"state":   false,
			},
		},
	}
	snapshot, err := CloneSettings(live)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			next, err := CloneSettings(live)
			if err != nil {
				t.Error(err)
				return
			}
			if next.ProfileSettings == nil {
				next.ProfileSettings = map[string]interface{}{}
			}
			next.ProfileSettings["layers"] = map[string]interface{}{
				"classic": i%2 == 0,
				"gamma":   true,
			}
			_ = ProfileLayers(next)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = ProfileLayers(snapshot)
		}
	}()
	wg.Wait()

	got := ProfileLayers(snapshot)
	if !got["classic"] || got["gamma"] {
		t.Fatalf("writer snapshot changed: %#v", got)
	}
}

func TestRedactedSettingsOmitsAPIKey(t *testing.T) {
	redacted := RedactedSettings(&Settings{APITKey: "secret-value", NinjaAPIKey: "ninja-secret", EnableDebug: true})
	if !redacted.HasAPIKey {
		t.Fatal("HasAPIKey should be true")
	}
	if !redacted.HasNinjaAPIKey {
		t.Fatal("HasNinjaAPIKey should be true")
	}
	if redacted.APITKey != "" || redacted.NinjaAPIKey != "" {
		t.Fatal("redacted copy still holds a key")
	}
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, `"APITKey"`) || strings.Contains(text, "api_key") || strings.Contains(text, "secret-value") || strings.Contains(text, "ninja-secret") || strings.Contains(text, `"NinjaAPIKey"`) {
		t.Fatalf("key leaked into JSON: %s", text)
	}
	if !strings.Contains(text, `"HasAPIKey":true`) || !strings.Contains(text, `"HasNinjaAPIKey":true`) {
		t.Fatalf("key flags missing: %s", text)
	}

	empty := RedactedSettings(&Settings{})
	if empty.HasAPIKey {
		t.Fatal("empty key reported as present")
	}
}
