package config

// ProfileLayerIDs are the ladders a chart can turn on.
// Classic and state include 0 DTE, 1 DTE, and 90 DTE.
// Greeks have no 90 DTE category.
var ProfileLayerIDs = []string{"classic", "state", "gamma", "delta", "vanna", "charm"}

// ProfileEnabled reports whether the profile viewer and, in chart-data mode,
// profile collection are on. A missing key stays off.
func ProfileEnabled(settings *Settings) bool {
	if settings == nil || settings.ProfileSettings == nil {
		return false
	}
	on, ok := settingBool(settings.ProfileSettings["enabled"])
	return ok && on
}

// ProfileLayers returns which ladders chart-data mode should keep.
// A missing list means classic only.
func ProfileLayers(settings *Settings) map[string]bool {
	layers := map[string]bool{
		"classic": true,
		"state":   false,
		"gamma":   false,
		"delta":   false,
		"vanna":   false,
		"charm":   false,
	}
	if settings == nil || settings.ProfileSettings == nil {
		return layers
	}
	raw, ok := settings.ProfileSettings["layers"].(map[string]interface{})
	if !ok {
		return layers
	}
	for _, id := range ProfileLayerIDs {
		if value, exists := raw[id]; exists {
			if on, ok := settingBool(value); ok {
				layers[id] = on
			}
		}
	}
	return layers
}

// ProfileEndpoints lists the ladders for the enabled layers.
// The caller still has to drop names the subscription tier does not include.
func ProfileEndpoints(settings *Settings) []string {
	layers := ProfileLayers(settings)
	out := make([]string, 0, 8)
	if layers["classic"] {
		out = append(out, "classic_full", "classic_zero", "classic_one")
	}
	if layers["state"] {
		out = append(out, "state_full", "state_zero", "state_one")
	}
	for _, id := range []string{"gamma", "delta", "vanna", "charm"} {
		if layers[id] {
			out = append(out, id+"_zero", id+"_one")
		}
	}
	return out
}

// KeepProfileEndpoint reports whether a fetched ladder should be written.
// Collect-all keeps every ladder. Chart-data mode keeps a ladder only when
// the profile is on and that layer is checked.
func KeepProfileEndpoint(settings *Settings, endpoint string) bool {
	if settings == nil || settings.CollectAllEndpoints {
		return true
	}
	if !ProfileEnabled(settings) {
		return false
	}
	for _, name := range ProfileEndpoints(settings) {
		if name == endpoint {
			return true
		}
	}
	return false
}

func settingBool(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		if typed == "true" {
			return true, true
		}
		if typed == "false" {
			return false, true
		}
	}
	return false, false
}
