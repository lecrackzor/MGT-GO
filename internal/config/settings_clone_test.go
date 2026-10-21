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
	redacted := RedactedSettings(&Settings{APITKey: "secret-value", EnableDebug: true})
	if !redacted.HasAPIKey {
		t.Fatal("HasAPIKey should be true")
	}
	if redacted.APITKey != "" {
		t.Fatal("redacted copy still holds the key")
	}
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "APITKey") || strings.Contains(text, "api_key") || strings.Contains(text, "secret-value") {
		t.Fatalf("key leaked into JSON: %s", text)
	}
	if !strings.Contains(text, `"HasAPIKey":true`) {
		t.Fatalf("HasAPIKey missing: %s", text)
	}

	empty := RedactedSettings(&Settings{})
	if empty.HasAPIKey {
		t.Fatal("empty key reported as present")
	}
}
