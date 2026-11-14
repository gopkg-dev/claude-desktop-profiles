package desktopusage

import (
	"net/netip"
	"strings"
	"testing"
)

func TestSupportedRegionSnapshot(t *testing.T) {
	if len(strings.Fields(supportedCountryCodes)) != 185 || len(strings.Fields(supportedTerritoryCodes)) != 47 {
		t.Fatal("supported-region snapshot does not match the reviewed policy")
	}
	policy := ClaudeSupportedEgressPolicy()
	if len(policy.AllowedIPs) != 0 || len(policy.AllowedCountries) != 231 {
		t.Fatal("default policy must permit reviewed countries without an IP restriction")
	}
	compiled, err := compileEgressPolicy(policy)
	if err != nil || len(compiled.countries) != len(policy.AllowedCountries) {
		t.Fatal("default policy has malformed or duplicate country codes")
	}
	address := netip.MustParseAddr("198.51.100.1")
	for _, country := range []string{"US", "GB", "JP", "SG", "TW", "CD", "CF", "PR", "GU", "RE", "AX"} {
		if !compiled.allows(address, country) {
			t.Errorf("supported country or territory %s was denied", country)
		}
	}
	for _, country := range []string{"CN", "HK", "MO", "RU", "IR", "KP", "AF", "UA", "CK", "NU", "AQ", "XX", ""} {
		if compiled.allows(address, country) {
			t.Errorf("unverified or unsupported region %s was permitted", country)
		}
	}
	policy.AllowedCountries[0] = "CN"
	fresh, err := compileEgressPolicy(ClaudeSupportedEgressPolicy())
	if err != nil || fresh.allows(address, "CN") {
		t.Fatal("caller mutation changed the shared country policy")
	}
}
