package desktopusage

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const traceEndpoint = "https://api.anthropic.com/cdn-cgi/trace"
const maxTraceBytes = 16 << 10

var (
	ErrEgressPolicyMissing     = errors.New("请先设置允许的出口 IP 或国家，再刷新额度")
	ErrEgressPolicyInvalid     = errors.New("出口规则格式不正确，请检查 IP、CIDR 和国家代码")
	ErrEgressCheck             = errors.New("出口检测失败，未读取凭证或查询额度")
	ErrEgressNotAllowed        = errors.New("当前出口不符合允许规则，未读取凭证或查询额度")
	ErrEgressConnectionChanged = errors.New("出口检测后连接发生变化，已停止额度查询，请手动重试")
)

// EgressPolicy permits only explicitly selected IP ranges and/or country codes.
// When both lists are populated, both conditions must match.
type EgressPolicy struct {
	AllowedIPs       []string `json:"allowedIPs,omitempty"`
	AllowedCountries []string `json:"allowedCountries,omitempty"`
}

type compiledEgressPolicy struct {
	prefixes  []netip.Prefix
	countries map[string]bool
}

func compileEgressPolicy(policy EgressPolicy) (compiledEgressPolicy, error) {
	result := compiledEgressPolicy{countries: map[string]bool{}}
	if len(policy.AllowedIPs) == 0 && len(policy.AllowedCountries) == 0 {
		return result, ErrEgressPolicyMissing
	}
	if len(policy.AllowedIPs) > 256 || len(policy.AllowedCountries) > 256 {
		return result, ErrEgressPolicyInvalid
	}
	for _, value := range policy.AllowedIPs {
		value = strings.TrimSpace(value)
		if address, err := netip.ParseAddr(value); err == nil && address.Zone() == "" {
			address = address.Unmap()
			result.prefixes = append(result.prefixes, netip.PrefixFrom(address, address.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Zone() != "" {
			return result, ErrEgressPolicyInvalid
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return result, ErrEgressPolicyInvalid
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		result.prefixes = append(result.prefixes, prefix.Masked())
	}
	for _, value := range policy.AllowedCountries {
		country := strings.ToUpper(strings.TrimSpace(value))
		if !validCountry(country) {
			return result, ErrEgressPolicyInvalid
		}
		result.countries[country] = true
	}
	return result, nil
}

func validCountry(value string) bool {
	return len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z'
}

func (policy compiledEgressPolicy) allows(address netip.Addr, country string) bool {
	ipOK := len(policy.prefixes) == 0
	for _, prefix := range policy.prefixes {
		if prefix.Contains(address.Unmap()) {
			ipOK = true
			break
		}
	}
	countryOK := len(policy.countries) == 0 || policy.countries[country]
	return ipOK && countryOK
}

func parseTrace(data []byte) (netip.Addr, string, error) {
	if len(data) == 0 || len(data) > maxTraceBytes {
		return netip.Addr{}, "", ErrEgressCheck
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" || len(key) > 64 || len(value) > 4096 {
			return netip.Addr{}, "", ErrEgressCheck
		}
		if _, duplicate := fields[key]; duplicate {
			return netip.Addr{}, "", ErrEgressCheck
		}
		for _, ch := range key {
			if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '_' {
				return netip.Addr{}, "", ErrEgressCheck
			}
		}
		for _, ch := range value {
			if ch < 32 || ch > 126 {
				return netip.Addr{}, "", ErrEgressCheck
			}
		}
		fields[key] = value
	}
	address, err := netip.ParseAddr(fields["ip"])
	country := fields["loc"]
	if err != nil || address.Zone() != "" || !validCountry(country) {
		return netip.Addr{}, "", ErrEgressCheck
	}
	return address.Unmap(), country, nil
}

// connectionGate owns the transport for one trace+quota sequence and forbids new dials after verification.
type connectionGate struct {
	mu               sync.Mutex
	verified         bool
	connection       net.Conn
	traceConnections int
	dialer           func(context.Context, string, string) (net.Conn, error)
}

func (gate *connectionGate) dial(ctx context.Context, network, address string) (net.Conn, error) {
	gate.mu.Lock()
	sealed := gate.verified
	gate.mu.Unlock()
	if sealed {
		return nil, ErrEgressConnectionChanged
	}
	if gate.dialer != nil {
		return gate.dialer(ctx, network, address)
	}
	dialer := net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return dialer.DialContext(ctx, network, address)
}

func (gate *connectionGate) traceContext(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		gate.mu.Lock()
		gate.connection = info.Conn
		gate.traceConnections++
		gate.mu.Unlock()
	}})
}

func (gate *connectionGate) seal() error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.connection == nil || gate.traceConnections != 1 {
		return ErrEgressCheck
	}
	gate.verified = true
	return nil
}

func (gate *connectionGate) usageContext(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		gate.mu.Lock()
		match := gate.verified && info.Conn != nil && info.Conn == gate.connection && info.Reused
		gate.mu.Unlock()
		if match {
			return
		}
		// Closing synchronously prevents any credential bytes reaching a different connection.
		if info.Conn != nil {
			_ = info.Conn.Close()
		}
		cancel(ErrEgressConnectionChanged)
	}}), cancel
}

func verifyEgress(
	ctx context.Context,
	client *http.Client,
	gate *connectionGate,
	policy compiledEgressPolicy,
	userAgent string,
) error {
	request, err := http.NewRequestWithContext(gate.traceContext(ctx), http.MethodGet, traceEndpoint, nil)
	if err != nil {
		return ErrEgressCheck
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return ErrEgressCheck
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrEgressCheck
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxTraceBytes+1))
	if err != nil || len(data) > maxTraceBytes {
		return ErrEgressCheck
	}
	defer clear(data)
	address, country, err := parseTrace(data)
	if err != nil {
		return err
	}
	if !policy.allows(address, country) {
		return ErrEgressNotAllowed
	}
	if response.Close {
		return ErrEgressConnectionChanged
	}
	return gate.seal()
}
