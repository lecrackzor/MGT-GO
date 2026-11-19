package main

// mergeProfileLayout copies the placement fields the chart menu is allowed to change.
func mergeProfileLayout(profile map[string]interface{}, body map[string]interface{}) {
	if profile == nil || body == nil {
		return
	}
	if side, ok := body["profile_side"].(string); ok && (side == "left" || side == "right") {
		profile["profile_side"] = side
	}
	if v, ok := layoutNumber(body["side_width_percent"]); ok {
		profile["side_width_percent"] = clampLayout(v, 12, 50)
	}
	if v, ok := layoutNumber(body["bar_width_percent"]); ok {
		profile["bar_width_percent"] = clampLayout(v, 8, 70)
	}
	if v, ok := layoutNumber(body["profile_x_percent"]); ok {
		profile["profile_x_percent"] = clampLayout(v, 0, 100)
	}
}

func layoutNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func clampLayout(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
