package desktopusage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const geoOrigin = "https://ipwho.is/"

// ErrExitTrace indicates that the credential-free exit observation did not complete.
var ErrExitTrace = errors.New("Claude 出口查询失败，请稍后重试")

// ExitInfo is the last credential-free trace observation and its cached GEO enrichment.
type ExitInfo struct {
	IP           string    `json:"ip"`
	TraceCountry string    `json:"traceCountry"`
	CountryCode  string    `json:"countryCode"`
	Country      string    `json:"country"`
	Region       string    `json:"region"`
	City         string    `json:"city"`
	ISP          string    `json:"isp"`
	Proxy        string    `json:"proxy"`
	ASN          int64     `json:"asn"`
	CheckedAt    time.Time `json:"checkedAt"`
	GeoUpdatedAt time.Time `json:"geoUpdatedAt"`
	GeoRetryAt   time.Time `json:"geoRetryAt"`
	NextAllowed  time.Time `json:"nextAllowed"`
	LastError    string    `json:"lastError,omitempty"`
	GeoError     string    `json:"geoError,omitempty"`
	Failures     int       `json:"failures"`
}

// ExitClient follows the selected Desktop proxy without sending account credentials.
type ExitClient struct {
	LiveDir       string
	clientFactory func(context.Context, string) (*http.Client, *url.URL, error)
	now           func() time.Time
}

func readExitResponse(ctx context.Context, client *http.Client, address string) ([]byte, *http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response, errors.New("检测服务暂不可用")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return nil, response, errors.New("检测服务响应无效")
	}
	return data, response, nil
}

// Query checks trace and reuses GEO for the same IP for up to 24 hours.
func (c *ExitClient) Query(ctx context.Context, previous ExitInfo) (ExitInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	factory := c.clientFactory
	if factory == nil {
		factory = exitHTTPClient
	}
	client, proxy, err := factory(ctx, c.LiveDir)
	if err != nil {
		return ExitInfo{}, ErrProxy
	}
	defer client.CloseIdleConnections()
	data, response, err := readExitResponse(ctx, client, traceEndpoint)
	if err != nil {
		result := ExitInfo{}
		if response != nil && response.StatusCode == http.StatusTooManyRequests {
			result.NextAllowed = parseRetryAfter(response.Header.Get("Retry-After"), now())
		}
		return result, ErrExitTrace
	}
	ip, country, err := parseTrace(data)
	if err != nil {
		return ExitInfo{}, ErrExitTrace
	}
	result := ExitInfo{IP: ip.String(), TraceCountry: country, CountryCode: country,
		CheckedAt: now(), Proxy: "系统代理 / VPN 路由"}
	if proxy != nil {
		result.Proxy = proxy.Redacted()
	}
	if previous.IP == result.IP && !previous.GeoUpdatedAt.IsZero() && !previous.GeoUpdatedAt.After(now()) {
		result.CountryCode = previous.CountryCode
		result.Country, result.Region, result.City = previous.Country, previous.Region, previous.City
		result.ISP, result.ASN, result.GeoUpdatedAt = previous.ISP, previous.ASN, previous.GeoUpdatedAt
		if now().Sub(previous.GeoUpdatedAt) < 24*time.Hour {
			return result, nil
		}
	}
	if previous.GeoRetryAt.After(now()) {
		result.GeoRetryAt = previous.GeoRetryAt
		result.GeoError = "GEO 查询处于冷却时间内"
		return result, nil
	}
	data, response, err = readExitResponse(ctx, client, geoOrigin+url.PathEscape(result.IP))
	if err != nil {
		result.GeoError = "GEO 暂未取得，出口 IP 仍有效"
		result.GeoRetryAt = now().Add(15 * time.Minute)
		if response != nil && response.StatusCode == http.StatusTooManyRequests {
			if retry := parseRetryAfter(response.Header.Get("Retry-After"), now()); retry.After(result.GeoRetryAt) {
				result.GeoRetryAt = retry
			}
		}
		return result, nil
	}
	var geo struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		City        string `json:"city"`
		Connection  struct {
			ASN int64  `json:"asn"`
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	decodeErr := json.Unmarshal(data, &geo)
	geoIP, parseErr := netip.ParseAddr(geo.IP)
	countryCode := strings.ToUpper(geo.CountryCode)
	if decodeErr != nil || !geo.Success || parseErr != nil || geoIP.Unmap() != ip.Unmap() ||
		!validCountry(countryCode) {
		result.GeoError = "GEO 数据未通过 IP 核对"
		result.GeoRetryAt = now().Add(15 * time.Minute)
		return result, nil
	}
	result.CountryCode = countryCode
	result.Country, result.Region, result.City = cleanName(geo.Country), cleanName(geo.Region), cleanName(geo.City)
	result.ISP, result.ASN, result.GeoUpdatedAt = cleanName(geo.Connection.ISP), geo.Connection.ASN, now()
	return result, nil
}
