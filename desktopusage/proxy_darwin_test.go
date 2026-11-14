package desktopusage

import (
	"context"
	"testing"
	"time"
)

func TestNativePACScriptWithoutNetwork(t *testing.T) {
	api, err := loadCFProxy()
	if err != nil {
		t.Fatal("CFNetwork symbols unavailable")
	}
	text := api.stringCreate(0, endpoint, utf8Encoding)
	defer api.release(text)
	target := api.urlCreate(0, text, 0)
	defer api.release(target)
	for index, fixture := range []struct{ script, expected string }{
		{`function FindProxyForURL(url, host) { return "DIRECT"; }`, ""},
		{`function FindProxyForURL(url, host) { return "PROXY proxy.example:8080; DIRECT"; }`, "http://proxy.example:8080"},
		{`function FindProxyForURL(url, host) { return "SOCKS5 proxy.example:1080"; }`, "socks5://proxy.example:1080"},
		{`function FindProxyForURL(url, host) { return "HTTPS proxy.example:443; DIRECT"; }`, "https://proxy.example:443"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		proxy, resolveErr := api.evaluatePACScript(ctx, target, fixture.script)
		cancel()
		if resolveErr != nil {
			t.Fatalf("native PAC fixture %d failed: %v", index, resolveErr)
		}
		got := ""
		if proxy != nil {
			got = proxy.String()
		}
		if got != fixture.expected {
			t.Fatalf("unexpected native PAC route: %s", got)
		}
	}
	if _, err := api.evaluatePACScript(context.Background(), target,
		`function FindProxyForURL(url, host) { return "SOCKS4 proxy.example:1080; DIRECT"; }`); err == nil {
		t.Fatal("unsupported first proxy silently fell back to DIRECT")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	script := api.stringCreate(0, `function FindProxyForURL(url, host) { return "DIRECT"; }`, utf8Encoding)
	defer api.release(script)
	if _, err := api.executePAC(ctx, target, script, true); err == nil {
		t.Fatal("cancelled PAC evaluation accepted")
	}
}

func TestHeadersIdentifyThisApplication(t *testing.T) {
	headers, err := clientHeaders("0.6.0")
	if err != nil {
		t.Fatal("system fields unavailable")
	}
	if headers.Get("anthropic-client-app") != "local.claudeprofiles.app" ||
		headers.Get("User-Agent") != "ClaudeProfiles/0.6.0" || headers.Get("anthropic-client-version") != "0.6.0" {
		t.Fatal("incorrect application identity")
	}
	for _, field := range []string{"anthropic-client-platform", "anthropic-client-device-class",
		"anthropic-client-total-memory-gb", "anthropic-client-os-platform", "anthropic-client-os-version"} {
		if headers.Get(field) == "" {
			t.Fatal("missing system field")
		}
	}
}
