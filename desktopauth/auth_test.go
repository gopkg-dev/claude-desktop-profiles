package desktopauth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	fixtureAccount = "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
	fixtureOrg     = "cccccccc-1111-2222-3333-dddddddddddd"
	fixtureToken   = "fixture-token-not-a-real-credential"
	fixtureSecret  = "fixture-only-safe-storage-secret"
	fixtureScope   = "user:inference user:file_upload user:profile"
)

func TestDiscoverOrganizationFromCredentialWithoutBusinessCache(t *testing.T) {
	t.Parallel()
	data := credentialJSON(t, fixtureKey(fixtureScope), fixtureToken, fixtureNow.Add(time.Hour).UnixMilli())
	credential, err := selectCredential(data, fixtureAccount, "", fixtureNow)
	if err != nil || credential.OrganizationID != fixtureOrg || credential.AccessToken != fixtureToken {
		t.Fatal("could not discover scoped organization", err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(fixtureKey(fixtureScope), fixtureOrg, "eeeeeeee-1111-2222-3333-ffffffffffff", 1)
	entries[other] = entries[fixtureKey(fixtureScope)]
	data, _ = json.Marshal(entries)
	if _, err := selectCredential(data, fixtureAccount, "", fixtureNow); !errors.Is(err, ErrIdentity) {
		t.Fatal("ambiguous organization was silently selected", err)
	}
}

var fixtureNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func fixtureKey(scope string) string {
	return "acct:" + fixtureAccount + "|" + desktopClientID + ":" + fixtureOrg + ":" + apiHost + ":" + scope
}

func credentialJSON(t *testing.T, key, token string, expiry any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{key: map[string]any{"token": token, "expiresAt": expiry}})
	if err != nil {
		t.Fatal("could not encode fixture")
	}
	return data
}

func encryptFixture(t *testing.T, plain []byte) []byte {
	t.Helper()
	key, err := pbkdf2.Key(sha1.New, fixtureSecret, []byte("saltysalt"), 1003, aes.BlockSize)
	if err != nil {
		t.Fatal("could not derive fixture key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal("could not initialize fixture cipher")
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(bytes.Clone(plain), bytes.Repeat([]byte{byte(padding)}, padding)...)
	result := make([]byte, 3+len(padded))
	copy(result, "v10")
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(result[3:], padded)
	return result
}

func writeFixtureConfig(t *testing.T, directory, account string, encrypted []byte) {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"lastKnownAccountUuid": account,
		"oauth:tokenCacheV2":   base64.StdEncoding.EncodeToString(encrypted),
	})
	if err != nil || os.WriteFile(filepath.Join(directory, "config.json"), data, 0o600) != nil {
		t.Fatal("could not write fixture config")
	}
}

func privateDirectory(t *testing.T) string {
	t.Helper()
	// macOS's standard temporary-directory prefix may itself be a symlink.
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("could not resolve fixture directory")
	}
	return directory
}

func TestLoadExistingCredential(t *testing.T) {
	t.Parallel()
	directory := privateDirectory(t)
	expires := fixtureNow.Add(time.Hour)
	plain := credentialJSON(t, fixtureKey(fixtureScope), fixtureToken, expires.UnixMilli())
	writeFixtureConfig(t, directory, fixtureAccount, encryptFixture(t, plain))
	before, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := loadWithSecret(directory, fixtureAccount, fixtureOrg, fixtureNow, func() ([]byte, error) {
		calls++
		return []byte(fixtureSecret), nil
	})
	if err != nil || result.AccessToken != fixtureToken || !result.ExpiresAt.Equal(expires) || calls != 1 {
		t.Fatalf("unexpected result: credential=%v calls=%d err=%v", result, calls, err)
	}
	after, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("load changed fixture config")
	}
}

func TestTokenSelectionRequiresExactBinding(t *testing.T) {
	t.Parallel()
	validKey := fixtureKey(fixtureScope)
	for _, test := range []struct {
		name string
		key  string
	}{
		{"other account", strings.Replace(validKey, fixtureAccount, fixtureOrg, 1)},
		{"other organization", strings.Replace(validKey, fixtureOrg, fixtureAccount, 1)},
		{"other client", strings.Replace(validKey, desktopClientID, fixtureOrg, 1)},
		{"other host", strings.Replace(validKey, apiHost, "https://example.invalid", 1)},
		{"host suffix", strings.Replace(validKey, apiHost, apiHost+".example.invalid", 1)},
		{"extra scope", validKey + " user:extra"},
		{"duplicate scope", fixtureKey("user:profile user:profile user:inference")},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := credentialJSON(t, test.key, fixtureToken, fixtureNow.Add(time.Hour).UnixMilli())
			result, err := selectCredential(plain, fixtureAccount, fixtureOrg, fixtureNow)
			if !errors.Is(err, ErrUnavailable) || result.AccessToken != "" {
				t.Fatalf("credential with wrong binding was accepted: %v", err)
			}
		})
	}
	plain := credentialJSON(t, fixtureKey("user:profile user:file_upload user:inference"), fixtureToken,
		fixtureNow.Add(time.Hour).UnixMilli())
	if _, err := selectCredential(plain, fixtureAccount, fixtureOrg, fixtureNow); err != nil {
		t.Fatalf("equivalent scope ordering rejected: %v", err)
	}
}

func TestSelectionRejectsExpiredAndMalformedEntries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		token   string
		expires any
		want    error
	}{
		{"expired", fixtureToken, fixtureNow.Add(-time.Second).UnixMilli(), ErrExpired},
		{"exact expiry", fixtureToken, fixtureNow.UnixMilli(), ErrExpired},
		{"missing token", "", fixtureNow.Add(time.Hour).UnixMilli(), ErrFormat},
		{"header injection", "fixture\r\nInjected: yes", fixtureNow.Add(time.Hour).UnixMilli(), ErrFormat},
		{"boolean expiry", fixtureToken, true, ErrFormat},
		{"string expiry", fixtureToken, "NaN", ErrFormat},
		{"null expiry", fixtureToken, nil, ErrFormat},
		{"implausible expiry", fixtureToken, 9e18, ErrFormat},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := credentialJSON(t, fixtureKey(fixtureScope), test.token, test.expires)
			result, err := selectCredential(plain, fixtureAccount, fixtureOrg, fixtureNow)
			if !errors.Is(err, test.want) || result.AccessToken != "" {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecryptRejectsWrongFormatAndPadding(t *testing.T) {
	t.Parallel()
	for _, encrypted := range [][]byte{nil, []byte("v10"), []byte("v10short"), append([]byte("v11"), make([]byte, 16)...)} {
		if _, err := decrypt(encrypted, []byte(fixtureSecret)); !errors.Is(err, ErrFormat) {
			t.Fatal("malformed ciphertext was accepted")
		}
	}
	encrypted := encryptFixture(t, []byte(`{"fixture":true}`))
	if _, err := decrypt(encrypted, []byte("different-fixture-secret")); !errors.Is(err, ErrFormat) {
		t.Fatal("wrong key did not reject fixture padding")
	}
	encrypted[len(encrypted)-1] ^= 0xff
	if _, err := decrypt(encrypted, []byte(fixtureSecret)); !errors.Is(err, ErrFormat) {
		t.Fatal("corrupt fixture padding was accepted")
	}
}

func TestLoadRejectsIdentityBeforeKeychain(t *testing.T) {
	t.Parallel()
	directory := privateDirectory(t)
	plain := credentialJSON(t, fixtureKey(fixtureScope), fixtureToken, fixtureNow.Add(time.Hour).UnixMilli())
	writeFixtureConfig(t, directory, fixtureOrg, encryptFixture(t, plain))
	_, err := loadWithSecret(directory, fixtureAccount, fixtureOrg, fixtureNow, func() ([]byte, error) {
		t.Fatal("keychain accessed for mismatched profile")
		return nil, ErrKeychain
	})
	if !errors.Is(err, ErrIdentity) {
		t.Fatalf("got %v, want identity error", err)
	}
}

func TestLoadRejectsConcurrentAccountChange(t *testing.T) {
	t.Parallel()
	directory := privateDirectory(t)
	plain := credentialJSON(t, fixtureKey(fixtureScope), fixtureToken, fixtureNow.Add(time.Hour).UnixMilli())
	encrypted := encryptFixture(t, plain)
	writeFixtureConfig(t, directory, fixtureAccount, encrypted)
	result, err := loadWithSecret(directory, fixtureAccount, fixtureOrg, fixtureNow, func() ([]byte, error) {
		writeFixtureConfig(t, directory, fixtureOrg, encrypted)
		return []byte(fixtureSecret), nil
	})
	if !errors.Is(err, ErrChanged) || result.AccessToken != "" {
		t.Fatalf("concurrent account change accepted: %v", err)
	}
}

func TestLoadRejectsSymlinks(t *testing.T) {
	t.Parallel()
	root := privateDirectory(t)
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	plain := credentialJSON(t, fixtureKey(fixtureScope), fixtureToken, fixtureNow.Add(time.Hour).UnixMilli())
	writeFixtureConfig(t, actual, fixtureAccount, encryptFixture(t, plain))
	link := filepath.Join(root, "link")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(link); err == nil {
		t.Fatal("symlink parent accepted")
	}
	config := filepath.Join(actual, "config.json")
	if err := os.Rename(config, filepath.Join(actual, "other.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.json", config); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(actual); err == nil {
		t.Fatal("symlink credential file accepted")
	}
}

func TestCredentialCannotLeakThroughFormatting(t *testing.T) {
	t.Parallel()
	credential := Credential{AccessToken: fixtureToken, ExpiresAt: fixtureNow}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, credential), fixtureToken) {
			t.Fatal("credential leaked through formatting")
		}
	}
	encoded, err := json.Marshal(credential)
	if err != nil || bytes.Contains(encoded, []byte(fixtureToken)) {
		t.Fatal("credential leaked through JSON")
	}
}

func TestUserMessageNeverReturnsUnderlyingDetails(t *testing.T) {
	t.Parallel()
	if UserMessage(nil) != "" || UserMessage(fmt.Errorf("private detail: %w", ErrExpired)) != ErrExpired.Error() {
		t.Fatal("known credential error was not mapped")
	}
	if strings.Contains(UserMessage(errors.New(fixtureToken)), fixtureToken) {
		t.Fatal("unknown error details leaked")
	}
}
