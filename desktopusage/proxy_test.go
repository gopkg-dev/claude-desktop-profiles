package desktopusage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProxyConfigPriorityAndValidation(t *testing.T) {
	for _, fixture := range []struct {
		body, kind, address string
		fails               bool
	}{
		{`{}`, "system", "", false},
		{`{"egressProxyUrl":"http://localhost:8080"}`, "fixed", "http://localhost:8080", false},
		{`{"egressProxyUrl":"socks5://localhost:1080"}`, "fixed", "socks5://localhost:1080", false},
		{`{"egressProxyPacUrl":"https://proxy.example/pac","egressProxyUrl":"http://localhost:1"}`,
			"pac", "https://proxy.example/pac", false},
		{`{"egressProxyUrl":"http://name:secret@localhost:8080"}`, "", "", true},
		{`{"egressProxyPacUrl":"file:///private/proxy.pac"}`, "", "", true},
		{`{"egressProxyPacUrl":42}`, "", "", true},
		{`{"egressProxyUrl":"http://localhost:8080/other"}`, "", "", true},
	} {
		config, err := parseProxyConfig([]byte(fixture.body))
		if fixture.fails {
			if !errors.Is(err, ErrProxy) {
				t.Fatal("invalid proxy accepted")
			}
			continue
		}
		if err != nil || config.kind != fixture.kind || config.address != fixture.address {
			t.Fatalf("wrong proxy selection: %v", err)
		}
	}
}

func TestProxyConfigReaderRejectsSymlinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "_meta.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(path); err != nil {
		t.Fatal("normal private config could not be read")
	}
	link := filepath.Join(base, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(link); err == nil {
		t.Fatal("symlink config accepted")
	}
}
