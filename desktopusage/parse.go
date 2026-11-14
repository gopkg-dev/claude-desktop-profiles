package desktopusage

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type usageWindow struct {
	Utilization json.RawMessage `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

func parseUsage(body []byte, now time.Time) (*UsageRecord, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, ErrResponse
	}
	result := &UsageRecord{UpdatedAt: now}
	var err error
	result.FiveHour, result.FiveHourReset, err = parseWindow(root["five_hour"])
	if err != nil {
		return nil, err
	}
	result.SevenDay, result.SevenDayReset, err = parseWindow(root["seven_day"])
	if err != nil {
		return nil, err
	}
	for _, named := range []struct{ key, name string }{{"seven_day_sonnet", "Sonnet"}, {"seven_day_opus", "Opus"}} {
		percent, reset, windowErr := parseWindow(root[named.key])
		if windowErr != nil {
			return nil, windowErr
		}
		if percent != nil || reset != nil {
			result.Scoped = append(result.Scoped, ScopedLimit{Name: named.name, Percent: percent, ResetAt: reset})
		}
	}
	if raw := root["limits"]; len(raw) > 0 && !isNull(raw) {
		var limits []struct {
			Kind     string          `json:"kind"`
			Percent  json.RawMessage `json:"percent"`
			ResetsAt json.RawMessage `json:"resets_at"`
			Scope    struct {
				Model *struct {
					DisplayName string `json:"display_name"`
				} `json:"model"`
				Surface json.RawMessage `json:"surface"`
			} `json:"scope"`
		}
		if json.Unmarshal(raw, &limits) != nil || len(limits) > 128 {
			return nil, ErrResponse
		}
		seen := map[string]bool{}
		for _, limit := range limits {
			percent, percentErr := parsePercent(limit.Percent)
			if percentErr != nil {
				return nil, ErrResponse
			}
			reset := parseReset(limit.ResetsAt)
			if limit.Scope.Model == nil && isNull(limit.Scope.Surface) {
				switch limit.Kind {
				case "session", "weekly_all":
					if seen[limit.Kind] {
						return nil, ErrResponse
					}
					seen[limit.Kind] = true
					if limit.Kind == "session" {
						result.FiveHour, result.FiveHourReset = percent, reset
					} else {
						result.SevenDay, result.SevenDayReset = percent, reset
					}
				}
			}
			if limit.Kind != "weekly_scoped" || limit.Scope.Model == nil || !isNull(limit.Scope.Surface) {
				continue
			}
			name := cleanName(limit.Scope.Model.DisplayName)
			if name == "" {
				continue
			}
			scoped := ScopedLimit{Name: name, Percent: percent, ResetAt: reset}
			replaced := false
			for i := range result.Scoped {
				if strings.EqualFold(result.Scoped[i].Name, name) {
					result.Scoped[i], replaced = scoped, true
					break
				}
			}
			if !replaced {
				result.Scoped = append(result.Scoped, scoped)
			}
		}
	}
	_, fivePresent := root["five_hour"]
	_, sevenPresent := root["seven_day"]
	_, limitsPresent := root["limits"]
	if !fivePresent && !sevenPresent && !limitsPresent {
		return nil, ErrResponse
	}
	readResetGrants(root["cedar_ember"], result, now)
	return result, nil
}

func parseWindow(raw json.RawMessage) (*float64, *time.Time, error) {
	if isNull(raw) {
		return nil, nil, nil
	}
	var window usageWindow
	if json.Unmarshal(raw, &window) != nil {
		return nil, nil, ErrResponse
	}
	percent, err := parsePercent(window.Utilization)
	return percent, parseReset(window.ResetsAt), err
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func parsePercent(raw json.RawMessage) (*float64, error) {
	if isNull(raw) {
		return nil, nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 10000 {
		return nil, ErrResponse
	}
	return &value, nil
}

func parseReset(raw json.RawMessage) *time.Time {
	if isNull(raw) {
		return nil
	}
	var stamp time.Time
	var text string
	if json.Unmarshal(raw, &text) == nil {
		stamp, _ = time.Parse(time.RFC3339Nano, text)
	} else {
		var seconds float64
		if json.Unmarshal(raw, &seconds) != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
			seconds < 1577836800 || seconds > 4102444800 {
			return nil
		}
		whole, fraction := math.Modf(seconds)
		stamp = time.Unix(int64(whole), int64(fraction*1e9))
	}
	if stamp.Year() < 2020 || stamp.Year() > 2100 {
		return nil
	}
	return &stamp
}

func cleanName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 80 {
		return ""
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return ""
		}
	}
	return value
}
