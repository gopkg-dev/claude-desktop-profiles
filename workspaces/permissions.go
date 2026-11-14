package workspaces

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// permissionModes are the session permission modes Desktop knows.
var permissionModes = map[string]bool{
	"default": true, "acceptEdits": true, "plan": true, "auto": true, "bypassPermissions": true,
}

// permissionFields are a record's permission mode and the in-app choices Desktop checks it against:
// without bypassChosenInApp or autoChosenInApp, Desktop drops bypass and auto back to asking.
func permissionFields(row record) map[string]any {
	if !permissionModes[row.PermissionMode] {
		return nil
	}
	fields := map[string]any{"permissionMode": row.PermissionMode}
	var choice any
	if len(row.AutoChosenInApp) > 0 && json.Unmarshal(row.AutoChosenInApp, &choice) == nil {
		if value, ok := choice.(bool); ok {
			fields["autoChosenInApp"] = value
		} else if choice == "adopted" {
			fields["autoChosenInApp"] = "adopted"
		}
	}
	if row.BypassChosenInApp != nil {
		fields["bypassChosenInApp"] = *row.BypassChosenInApp
	}
	return fields
}

// repairTarget fixes a target index that is already the newest copy: an import the user should not be asked
// to confirm again, or one that lost its permission mode in an earlier sync. Every other field stays as is.
func repairTarget(tx *transaction, item candidate, target Source, trusted bool, mode record, report *Report) error {
	confirm := trusted && item.record.ImportedFrom != "" && !item.record.ResumeConfirmed
	fields := map[string]any{}
	if item.record.PermissionMode == "" {
		fields = permissionFields(mode)
	}
	if !confirm && len(fields) == 0 {
		return nil
	}
	data, err := readBounded(item.indexPath, maxIndexBytes)
	if err != nil {
		return err
	}
	row := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&row) != nil {
		return nil
	}
	if confirm {
		row["resumeConfirmed"] = true
	}
	for key, value := range fields {
		row[key] = value
	}
	rel, err := filepath.Rel(target.Dir, item.indexPath)
	if err != nil {
		return ErrUnsafePath
	}
	if err := putJSON(tx, rel, row); err != nil {
		return err
	}
	report.CodeUpdated++
	return nil
}

const desktopConfig = "claude_desktop_config.json"

// Desktop keeps these preferences per account inside each profile's claude_desktop_config.json.
const (
	folderModesPrefix = "epitaxy-folder-permission-mode-v2."
	modeAcksPrefix    = "epitaxy-perm-mode-acks-v2."
	bypassOptIn       = "bypassPermissionsOptInByAccount"
)

type desktopPrefs struct {
	modified time.Time
	folders  map[string]string
	acks     []string
	optIn    bool
}

// readPrefs reads an account's permission preferences from a profile's Desktop config.
func readPrefs(source Source) (map[string]any, desktopPrefs, error) {
	path := filepath.Join(source.Dir, desktopConfig)
	data, err := readBounded(path, maxIndexBytes)
	if err != nil {
		return nil, desktopPrefs{}, err
	}
	root := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil {
		return nil, desktopPrefs{}, errInvalidConfig
	}
	prefs := desktopPrefs{folders: map[string]string{}}
	if info, err := os.Stat(path); err == nil {
		prefs.modified = info.ModTime()
	}
	preferences, _ := root["preferences"].(map[string]any)
	epitaxy, _ := preferences["epitaxyPrefs"].(map[string]any)
	account := source.Identity.AccountID
	if folders, ok := epitaxy[folderModesPrefix+account].(map[string]any); ok {
		for folder, mode := range folders {
			if text, ok := mode.(string); ok && permissionModes[text] && filepath.IsAbs(folder) {
				prefs.folders[folder] = text
			}
		}
	}
	if acks, ok := epitaxy[modeAcksPrefix+account].([]any); ok {
		for _, ack := range acks {
			if text, ok := ack.(string); ok {
				prefs.acks = append(prefs.acks, text)
			}
		}
	}
	if optIns, ok := preferences[bypassOptIn].(map[string]any); ok {
		prefs.optIn = optIns[account] == true
	}
	return root, prefs, nil
}

var errInvalidConfig = errors.New("invalid desktop config")

// mergePermissionPrefs gives the target account the folders' default permission modes the user chose under
// the other accounts, the mode notices they already acknowledged, and their opt-in to bypass mode. The
// target's own choices win; among the sources, the most recently saved config wins. Nothing else in the
// config is touched, and the organization's policy gate is left for Desktop to fetch again.
func mergePermissionPrefs(tx *transaction, sources []Source, target Source) error {
	root, mine, err := readPrefs(target)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, errInvalidConfig) {
		return nil
	}
	if err != nil {
		return err
	}
	others := []desktopPrefs{}
	for _, source := range sources {
		if source.Dir == target.Dir || !validIdentity(source.Identity) {
			continue
		}
		if _, prefs, err := readPrefs(source); err == nil {
			others = append(others, prefs)
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errInvalidConfig) {
			return err
		}
	}
	sort.SliceStable(others, func(i, j int) bool { return others[i].modified.After(others[j].modified) })
	account := target.Identity.AccountID
	preferences, _ := root["preferences"].(map[string]any)
	if preferences == nil {
		preferences = map[string]any{}
	}
	epitaxy, _ := preferences["epitaxyPrefs"].(map[string]any)
	if epitaxy == nil {
		epitaxy = map[string]any{}
	}
	changed := false
	folders, _ := epitaxy[folderModesPrefix+account].(map[string]any)
	if folders == nil {
		folders = map[string]any{}
	}
	seen := map[string]bool{}
	for _, ack := range mine.acks {
		seen[ack] = true
	}
	acks := append([]any{}, toAny(mine.acks)...)
	optIn := mine.optIn
	for _, prefs := range others {
		for folder, mode := range prefs.folders {
			if _, ok := folders[folder]; !ok {
				folders[folder] = mode
				changed = true
			}
		}
		for _, ack := range prefs.acks {
			if !seen[ack] {
				seen[ack] = true
				acks = append(acks, ack)
				changed = true
			}
		}
		optIn = optIn || prefs.optIn
	}
	if optIn && !mine.optIn {
		optIns, _ := preferences[bypassOptIn].(map[string]any)
		if optIns == nil {
			optIns = map[string]any{}
		}
		// An explicit "no" under the target account stays.
		if _, decided := optIns[account]; !decided {
			optIns[account] = true
			preferences[bypassOptIn] = optIns
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if len(folders) > 0 {
		epitaxy[folderModesPrefix+account] = folders
	}
	if len(acks) > 0 {
		epitaxy[modeAcksPrefix+account] = acks
	}
	preferences["epitaxyPrefs"] = epitaxy
	root["preferences"] = preferences
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(root); err != nil {
		return ErrIO
	}
	return tx.put(desktopConfig, buffer.Bytes())
}

func toAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
