package desktopusage

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const resetUsageEndpoint = endpoint + "?cedar_ember=1"

var cliVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

var resetUserAgent = sync.OnceValue(func() string {
	version := installedCodeVersion()
	if version == "" {
		// Compatibility version of the referenced read-only entitlement protocol.
		version = "2.1.321"
	}
	return "claude-cli/" + version + " (external, cli)"
})

func installedCodeVersion() string {
	if path, err := exec.LookPath("claude"); err == nil {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			for current, depth := resolved, 0; depth < 6; current, depth = filepath.Dir(current), depth+1 {
				if version := filepath.Base(current); cliVersionPattern.MatchString(version) {
					return version
				}
				data, err := os.ReadFile(filepath.Join(current, "package.json"))
				var manifest struct{ Name, Version string }
				if err == nil && len(data) <= 64<<10 && json.Unmarshal(data, &manifest) == nil &&
					manifest.Name == "@anthropic-ai/claude-code" && cliVersionPattern.MatchString(manifest.Version) {
					return manifest.Version
				}
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(home, ".local", "share", "claude", "versions"))
	if err != nil {
		return ""
	}
	latest := ""
	for _, entry := range entries {
		if !entry.IsDir() && cliVersionPattern.MatchString(entry.Name()) && laterCodeVersion(entry.Name(), latest) {
			latest = entry.Name()
		}
	}
	return latest
}

func laterCodeVersion(candidate, current string) bool {
	if current == "" {
		return true
	}
	left, right := strings.Split(candidate, "."), strings.Split(current, ".")
	for index := range left {
		candidatePart, _ := strconv.Atoi(left[index])
		currentPart, _ := strconv.Atoi(right[index])
		if candidatePart != currentPart {
			return candidatePart > currentPart
		}
	}
	return false
}

func readResetGrants(raw json.RawMessage, record *UsageRecord, at time.Time) {
	if isNull(raw) {
		return
	}
	var status struct {
		Reason string          `json:"ineligible_reason"`
		Grants json.RawMessage `json:"grants"`
	}
	if json.Unmarshal(raw, &status) != nil || status.Reason == "surface" || isNull(status.Grants) {
		return
	}
	var rows []struct {
		ID        string          `json:"id"`
		Label     string          `json:"label"`
		Remaining *int            `json:"resets_left"`
		EndsAt    json.RawMessage `json:"ends_at"`
		Clears    []string        `json:"clears"`
		Paused    bool            `json:"paused"`
	}
	if json.Unmarshal(status.Grants, &rows) != nil || len(rows) > 64 {
		return
	}
	grants := make([]ResetGrant, 0, len(rows))
	for _, row := range rows {
		if row.Remaining == nil || *row.Remaining < 0 || *row.Remaining > 1000 {
			return
		}
		grant := ResetGrant{Remaining: *row.Remaining, Paused: row.Paused}
		grant.Kind, grant.Model = grantKind(row.Clears, row.ID+" "+row.Label, record.Scoped)
		if ends := parseReset(row.EndsAt); ends != nil {
			grant.ExpiresAt = *ends
		}
		grants = append(grants, grant)
	}
	record.ResetGrants = grants
	record.ResetGrantsKnown, record.ResetGrantsAt = true, at
}

// grantKind tells a full reset, which clears the weekly limit, from one that gives back a model's share of
// it, and names that model from the account's model limits.
func grantKind(clears []string, text string, scoped []ScopedLimit) (string, string) {
	if len(clears) == 0 {
		return "", ""
	}
	for _, limit := range clears {
		if limit == "seven_day" {
			return ResetFull, ""
		}
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, limit := range scoped {
		name := strings.ToLower(limit.Name)
		for _, word := range words {
			if word == name {
				return ResetModel, limit.Name
			}
		}
	}
	return ResetModel, ""
}
