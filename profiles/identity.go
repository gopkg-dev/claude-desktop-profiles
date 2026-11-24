package profiles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// accountIdentity retains only the public account ID, never a credential value.
func accountIdentity(dir string) (string, error) {
	if err := checkedDir(dir, false); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.json")
	if err := checkedFile(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return "", errors.New("无法识别 Claude Desktop 的账号配置，请先打开 Desktop 完成登录")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return "", ErrInvalidState
		}
		if key == "lastKnownAccountUuid" {
			var id string
			if err := dec.Decode(&id); err != nil {
				return "", ErrInvalidState
			}
			return id, nil
		}
		if err := discardValue(dec); err != nil {
			return "", ErrInvalidState
		}
	}
	return "", nil
}

// discardValue walks values without saving unknown configuration fields.
func discardValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite || (delim != '{' && delim != '[') {
		return nil
	}
	for dec.More() {
		if delim == '{' {
			if _, err := dec.Token(); err != nil {
				return err
			}
		}
		if err := discardValue(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func (m *Manager) reconcile(s *manifest, id string, dir string) (string, error) {
	i := profileIndex(*s, id)
	if i < 0 {
		return "", ErrNotFound
	}
	actual, err := accountIdentity(dir)
	if err != nil {
		return "", err
	}
	if actual == "" {
		return "", nil
	}
	if recorded := s.Profiles[i].AccountID; recorded != "" && recorded != actual {
		return "", ErrIdentityMismatch
	}
	for j, p := range s.Profiles {
		if j != i && p.AccountID == actual {
			return "", ErrDuplicateAccount
		}
	}
	if s.Profiles[i].AccountID == "" {
		s.Profiles[i].AccountID = actual
		if err := m.save(*s); err != nil {
			return "", err
		}
	}
	return actual, nil
}

// credentialStamp fingerprints Desktop's encrypted token cache so a new login can be noticed
// without decrypting it. It returns "" when no token cache is saved.
func credentialStamp(dir string) (string, error) {
	path := filepath.Join(dir, "config.json")
	if err := checkedFile(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 4<<20))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return "", ErrInvalidState
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return "", ErrInvalidState
		}
		if key == "oauth:tokenCacheV2" {
			var encrypted string
			if err := dec.Decode(&encrypted); err != nil {
				return "", ErrInvalidState
			}
			if encrypted == "" {
				return "", nil
			}
			sum := sha256.Sum256([]byte(encrypted))
			return hex.EncodeToString(sum[:8]), nil
		}
		if err := discardValue(dec); err != nil {
			return "", ErrInvalidState
		}
	}
	return "", nil
}
