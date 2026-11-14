// Package desktopauth reads an existing Desktop credential without refreshing or exporting it.
package desktopauth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

var (
	ErrUnavailable = errors.New("此账号尚无可读取的 Desktop 凭据，请先在 Claude Desktop 登录")
	ErrIdentity    = errors.New("账号或组织与本地凭据不一致，已停止查询")
	ErrChanged     = errors.New("账号配置在读取期间发生变化，请稍后重新刷新")
	ErrFormat      = errors.New("无法解析此版本的 Desktop 凭据，请先在 Claude Desktop 打开账号")
	ErrExpired     = errors.New("此账号的 Desktop 凭据已过期，请切换到该账号并在 Claude Desktop 更新登录")
	ErrKeychain    = errors.New("无法读取 Claude 钥匙串凭据，请检查系统访问授权后重试")
	ErrUnsupported = errors.New("此系统暂不支持读取 Claude Desktop 凭据")
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const (
	desktopClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	apiHost         = "https://api.anthropic.com"
)

// Credential is ephemeral and deliberately omits its token from JSON and formatting.
type Credential struct {
	AccessToken    string `json:"-"`
	ExpiresAt      time.Time
	OrganizationID string
}

func (Credential) String() string   { return "<Desktop credential: redacted>" }
func (Credential) GoString() string { return "<Desktop credential: redacted>" }

// UserMessage returns a fixed message without exposing file, native, or credential details.
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	for _, known := range []error{
		ErrUnavailable, ErrIdentity, ErrChanged, ErrFormat, ErrExpired, ErrKeychain, ErrUnsupported,
	} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "无法读取 Desktop 凭据，请稍后重试"
}

// Load decrypts an existing, unexpired credential scoped to this account and organization.
// It never authorizes, refreshes, or changes Desktop's saved credentials.
func Load(directory, accountID, organizationID string, now time.Time) (Credential, error) {
	if !uuidPattern.MatchString(organizationID) {
		return Credential{}, ErrIdentity
	}
	if !platformSupported {
		return Credential{}, ErrUnsupported
	}
	return loadWithSecret(directory, accountID, organizationID, now, readKeychainSecret)
}

// LoadForAccount discovers the single organization from this account's credential keys.
func LoadForAccount(directory, accountID string, now time.Time) (Credential, error) {
	if !platformSupported {
		return Credential{}, ErrUnsupported
	}
	return loadWithSecret(directory, accountID, "", now, readKeychainSecret)
}

func loadWithSecret(
	directory, accountID, organizationID string,
	now time.Time,
	secretReader func() ([]byte, error),
) (Credential, error) {
	if !uuidPattern.MatchString(accountID) || organizationID != "" && !uuidPattern.MatchString(organizationID) || now.IsZero() {
		return Credential{}, ErrIdentity
	}
	before, err := readConfig(directory)
	if err != nil {
		return Credential{}, err
	}
	if before.AccountID != accountID {
		return Credential{}, ErrIdentity
	}
	if before.Encrypted == "" {
		return Credential{}, ErrUnavailable
	}
	encrypted, err := base64.StdEncoding.Strict().DecodeString(before.Encrypted)
	if err != nil || len(encrypted) <= 3 || !bytes.Equal(encrypted[:3], []byte("v10")) {
		return Credential{}, ErrFormat
	}
	secret, err := secretReader()
	if err != nil {
		return Credential{}, err
	}
	defer clear(secret)
	plain, err := decrypt(encrypted, secret)
	if err != nil {
		return Credential{}, err
	}
	defer clear(plain)
	credential, err := selectCredential(plain, accountID, organizationID, now)
	if err != nil {
		return Credential{}, err
	}
	after, err := readConfig(directory)
	if err != nil || before.AccountID != after.AccountID || before.Encrypted != after.Encrypted ||
		!sameDirectory(before.Directory, after.Directory) {
		return Credential{}, ErrChanged
	}
	return credential, nil
}

func decrypt(encrypted, secret []byte) ([]byte, error) {
	if len(encrypted) <= 3 || !bytes.Equal(encrypted[:3], []byte("v10")) ||
		(len(encrypted)-3)%aes.BlockSize != 0 || len(secret) == 0 {
		return nil, ErrFormat
	}
	key, err := pbkdf2.Key(sha1.New, string(secret), []byte("saltysalt"), 1003, aes.BlockSize)
	if err != nil {
		return nil, ErrFormat
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrFormat
	}
	plain := make([]byte, len(encrypted)-3)
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(plain, encrypted[3:])
	padding := int(plain[len(plain)-1])
	valid := subtle.ConstantTimeLessOrEq(1, padding) & subtle.ConstantTimeLessOrEq(padding, aes.BlockSize)
	for index := 0; index < aes.BlockSize; index++ {
		inPadding := subtle.ConstantTimeLessOrEq(index+1, padding)
		equal := subtle.ConstantTimeByteEq(plain[len(plain)-1-index], byte(padding))
		valid &= (1 - inPadding) | equal
	}
	if valid != 1 {
		clear(plain)
		return nil, ErrFormat
	}
	result := plain[:len(plain)-padding]
	clear(plain[len(result):])
	return result, nil
}

func selectCredential(data []byte, accountID, organizationID string, now time.Time) (Credential, error) {
	var entries map[string]json.RawMessage
	if json.Unmarshal(data, &entries) != nil || entries == nil {
		return Credential{}, ErrFormat
	}
	prefix := "acct:" + accountID + "|" + desktopClientID + ":"
	var selected Credential
	var expired, invalid bool
	for key, raw := range entries {
		remainder, matched := strings.CutPrefix(key, prefix)
		organization, scope, scoped := strings.Cut(remainder, ":"+apiHost+":")
		if !matched || !scoped || !uuidPattern.MatchString(organization) ||
			organizationID != "" && organizationID != organization || !matchesBaseScopes(scope) {
			continue
		}
		var entry struct {
			Token     string  `json:"token"`
			ExpiresAt float64 `json:"expiresAt"`
		}
		if json.Unmarshal(raw, &entry) != nil || !validToken(entry.Token) ||
			math.IsNaN(entry.ExpiresAt) || math.IsInf(entry.ExpiresAt, 0) ||
			entry.ExpiresAt < 1577836800000 || entry.ExpiresAt > 4102444800000 {
			invalid = true
			continue
		}
		expires := time.UnixMilli(int64(entry.ExpiresAt))
		if !expires.After(now) {
			expired = true
			continue
		}
		if selected.AccessToken != "" && selected.OrganizationID != organization {
			return Credential{}, ErrIdentity
		}
		if selected.AccessToken == "" || expires.After(selected.ExpiresAt) {
			selected = Credential{AccessToken: entry.Token, ExpiresAt: expires, OrganizationID: organization}
		}
	}
	if selected.AccessToken != "" {
		return selected, nil
	}
	if expired {
		return Credential{}, ErrExpired
	}
	if invalid {
		return Credential{}, ErrFormat
	}
	return Credential{}, ErrUnavailable
}

func matchesBaseScopes(scope string) bool {
	parts := strings.Split(scope, " ")
	if len(parts) != 3 {
		return false
	}
	wanted := map[string]bool{"user:inference": false, "user:file_upload": false, "user:profile": false}
	for _, part := range parts {
		seen, exists := wanted[part]
		if !exists || seen {
			return false
		}
		wanted[part] = true
	}
	return true
}

func validToken(token string) bool {
	if len(token) == 0 || len(token) > 8192 {
		return false
	}
	for _, character := range token {
		if character < 33 || character > 126 {
			return false
		}
	}
	return true
}
