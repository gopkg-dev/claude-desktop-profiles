package desktopusage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type proxyConfig struct {
	kind    string
	address string
}

func parsePACRoute(value string) (*url.URL, error) {
	parts := strings.Fields(value)
	if len(parts) == 1 && strings.EqualFold(parts[0], "DIRECT") {
		return nil, nil
	}
	if len(parts) != 2 {
		return nil, ErrProxy
	}
	scheme := ""
	switch strings.ToUpper(parts[0]) {
	case "PROXY", "HTTP":
		scheme = "http"
	case "HTTPS":
		scheme = "https"
	case "SOCKS5":
		scheme = "socks5"
	default:
		// Chromium's SOCKS/SOCKS4 routes must not silently become SOCKS5 or DIRECT.
		return nil, ErrProxy
	}
	host, port, err := net.SplitHostPort(parts[1])
	if err != nil || host == "" || strings.ContainsAny(host, "@/?#\\") {
		return nil, ErrProxy
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, ErrProxy
	}
	return &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(number))}, nil
}

var proxyConfigID = regexp.MustCompile(`^[a-f0-9-]{36}$`)

// resolveDesktopProxy follows Desktop's selected local configuration, then the macOS system settings.
func resolveDesktopProxy(ctx context.Context, liveDir string) (*url.URL, error) {
	config, err := readDesktopProxy(liveDir)
	if err != nil {
		return nil, err
	}
	if config.kind == "fixed" {
		return url.Parse(config.address)
	}
	return nativeProxy(ctx, config)
}

func readDesktopProxy(liveDir string) (proxyConfig, error) {
	if !filepath.IsAbs(liveDir) || filepath.Clean(liveDir) != liveDir {
		return proxyConfig{}, ErrProxy
	}
	if err := checkManagedProxy(); err != nil {
		return proxyConfig{}, err
	}
	library := filepath.Join(filepath.Dir(liveDir), "Claude-3p", "configLibrary")
	if override := os.Getenv("CLAUDE_USER_DATA_DIR"); override != "" {
		// A differently launched Desktop cannot be reliably inferred from our environment.
		return proxyConfig{}, ErrProxy
	}
	meta, err := readConfigFile(filepath.Join(library, "_meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return proxyConfig{kind: "system"}, nil
	}
	if err != nil {
		return proxyConfig{}, ErrProxy
	}
	var state struct {
		AppliedID     string          `json:"appliedId"`
		HybridPointer json.RawMessage `json:"hybridPointer"`
	}
	if json.Unmarshal(meta, &state) != nil || !isNull(state.HybridPointer) {
		return proxyConfig{}, ErrProxy
	}
	if state.AppliedID == "" {
		return proxyConfig{kind: "system"}, nil
	}
	if !proxyConfigID.MatchString(state.AppliedID) {
		return proxyConfig{}, ErrProxy
	}
	data, err := readConfigFile(filepath.Join(library, state.AppliedID+".json"))
	if err != nil {
		return proxyConfig{}, ErrProxy
	}
	return parseProxyConfig(data)
}

func parseProxyConfig(data []byte) (proxyConfig, error) {
	var config map[string]json.RawMessage
	if json.Unmarshal(data, &config) != nil || config == nil {
		return proxyConfig{}, ErrProxy
	}
	for _, field := range []struct{ key, kind string }{
		{"egressProxyPacUrl", "pac"}, {"egressProxyUrl", "fixed"},
	} {
		raw := config[field.key]
		if isNull(raw) {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return proxyConfig{}, ErrProxy
		}
		if value == "" {
			continue
		}
		address, err := url.Parse(value)
		if err != nil || address.Hostname() == "" || address.User != nil || address.Fragment != "" ||
			(address.Scheme != "http" && address.Scheme != "https" && address.Scheme != "socks5") {
			return proxyConfig{}, ErrProxy
		}
		if field.kind == "pac" && address.Scheme == "socks5" {
			return proxyConfig{}, ErrProxy
		}
		if field.kind == "fixed" {
			if address.RawQuery != "" || (address.Path != "" && address.Path != "/") {
				return proxyConfig{}, ErrProxy
			}
			address.Path = ""
			if address.Port() == "" {
				port := "80"
				if address.Scheme == "https" {
					port = "443"
				}
				address.Host = net.JoinHostPort(address.Hostname(), port)
			}
		}
		return proxyConfig{kind: field.kind, address: address.String()}, nil
	}
	return proxyConfig{kind: "system"}, nil
}

func readConfigFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrProxy
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, ErrProxy
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/") {
		info, statErr := root.Lstat(part)
		if statErr != nil {
			root.Close()
			return nil, statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, ErrProxy
		}
		next, nextErr := root.OpenRoot(part)
		root.Close()
		if nextErr != nil {
			return nil, ErrProxy
		}
		opened, openErr := next.Stat(".")
		if openErr != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, ErrProxy
		}
		root = next
	}
	defer root.Close()
	name := filepath.Base(path)
	file, err := root.OpenFile(name, os.O_RDONLY|noFollowFlag, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > 1<<20 {
		return nil, ErrProxy
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || int64(len(data)) != before.Size() {
		return nil, ErrProxy
	}
	after, err := root.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrProxy
	}
	return data, nil
}
