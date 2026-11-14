package desktopusage

import "strings"

// This snapshot was verified on 2026-11-14 against the policy effective 2026-11-12.
// Source: https://www.anthropic.com/legal/supported-regions.
// The 185 explicit entries are ISO 3166-1 alpha-2 codes, not Cloudflare colo codes.
const supportedCountryCodes = `
AD AE AG AL AM AO AR AT AU AZ BA BB BD BE BF BG BH BI BJ BN BO BR BS BT BW BZ
CA CD CF CG CH CI CL CM CO CR CV CY CZ DE DJ DK DM DO DZ EC EE EG ER ES ET
FI FJ FM FR GA GB GD GE GH GM GN GQ GR GT GW GY HN HR HT HU ID IE IL IN IQ
IS IT JM JO JP KE KG KH KI KM KN KR KW KZ LA LB LC LI LK LR LS LT LU LV LY
MA MC MD ME MG MH MK ML MN MR MT MU MV MW MX MY MZ NA NE NG NI NL NO NP NR
NZ OM PA PE PG PH PK PL PS PT PW PY QA RO RS RW SA SB SC SD SE SG SI SK SL
SM SN SO SR SS ST SV SZ TD TG TH TJ TL TM TN TO TR TT TV TW TZ UA UG US UY
UZ VA VC VN VU WS ZA ZM ZW
`

// The policy also supports territories and dependencies of listed countries.
// CK and NU are freely associated states, so they are not inferred as territories.
const supportedTerritoryCodes = `
AI AS AW AX BL BM BQ BV CC CW CX FK FO GF GG GI GL GP GS GU HM IM IO JE KY
MF MP MQ MS NC NF PF PM PN PR RE SH SJ SX TC TF TK UM VG VI WF YT
`

// ClaudeSupportedEgressPolicy returns a fresh country-only policy for manual quota queries.
// UA has excluded subregions which trace.loc cannot distinguish, so it is blocked conservatively.
func ClaudeSupportedEgressPolicy() EgressPolicy {
	countries := strings.Fields(supportedCountryCodes + supportedTerritoryCodes)
	allowed := make([]string, 0, len(countries)-1)
	for _, country := range countries {
		if country == "UA" {
			continue
		}
		allowed = append(allowed, country)
	}
	return EgressPolicy{AllowedCountries: allowed}
}
