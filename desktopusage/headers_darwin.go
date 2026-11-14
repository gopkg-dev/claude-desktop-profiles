package desktopusage

import (
	"errors"
	"math"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func clientHeaders(version string) (http.Header, error) {
	if !versionPattern.MatchString(version) {
		return nil, ErrHeaders
	}
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil || memory == 0 {
		return nil, ErrHeaders
	}
	osVersion, err := unix.Sysctl("kern.osproductversion")
	if err != nil || !versionPattern.MatchString(osVersion) {
		return nil, ErrHeaders
	}
	class := "gt16"
	switch {
	case memory < 6e9:
		class = "le4"
	case memory < 12e9:
		class = "8"
	case memory < 20e9:
		class = "16"
	}
	headers := make(http.Header)
	headers.Set("User-Agent", "ClaudeProfiles/"+version)
	headers.Set("anthropic-client-platform", "desktop_app")
	headers.Set("anthropic-client-app", "local.claudeprofiles.app")
	headers.Set("anthropic-client-version", version)
	headers.Set("anthropic-client-device-class", class)
	headers.Set("anthropic-client-total-memory-gb", strconv.FormatInt(int64(math.Round(float64(memory)/(1<<30))), 10))
	headers.Set("anthropic-client-os-platform", "darwin")
	headers.Set("anthropic-client-os-version", osVersion)
	return headers, nil
}

func checkManagedProxy() error {
	current, err := user.Current()
	if err != nil || current.Username == "" || strings.ContainsAny(current.Username, `/\`) {
		return ErrProxy
	}
	for _, path := range []string{
		"/Library/Managed Preferences/com.anthropic.claudefordesktop.plist",
		filepath.Join("/Library/Managed Preferences", current.Username, "com.anthropic.claudefordesktop.plist"),
	} {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		// Desktop's managed group policy can suppress the entire local proxy group.
		// Until that full schema is supported, fail closed rather than select another route.
		return ErrProxy
	}
	return nil
}
