// Package desktopusage performs explicitly requested, read-only Desktop quota queries.
package desktopusage

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"claudeprofiles/desktopauth"
)

const endpoint = "https://api.anthropic.com/api/oauth/usage"
const maxResponseBytes = 1 << 20

var (
	ErrNetwork     = errors.New("额度查询网络连接失败，请检查 Claude Desktop 的代理设置")
	ErrProxy       = errors.New("无法确认 Claude Desktop 的有效代理，已停止查询")
	ErrResponse    = errors.New("额度接口返回的数据无法识别，已保留原额度")
	ErrRedirect    = errors.New("额度接口发生跳转，已停止查询")
	ErrUnsupported = errors.New("当前平台不支持读取 Claude Desktop 的网络配置")
	ErrCanceled    = errors.New("查询已取消或超时")
	ErrHeaders     = errors.New("无法读取本机客户端信息，已停止查询")
	ErrHTTPLog     = errors.New("无法保存 HTTP RAW 日志，请检查日志目录的权限和空间")
	ErrTLSProfile  = errors.New("TLS 配置无法识别，已停止查询")
	versionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+_-]{0,63}$`)
)

// Target identifies the account and organization already verified by the profile manager.
type Target struct {
	Directory      string
	AccountID      string
	OrganizationID string
}

// Client never starts timers or refreshes credentials. QueryAccount is the only quota entry point.
type Client struct {
	HTTPLogDirectory string
	LiveDir          string
	AppVersion       string
	EgressPolicy     EgressPolicy
	TLSFingerprint   TLSFingerprint
	loadCredential   func(string, string, string, time.Time) (desktopauth.Credential, error)
	resolveProxy     func(context.Context, string) (*url.URL, error)
	clientHeaders    func(string) (http.Header, error)
	roundTripper     http.RoundTripper
	now              func() time.Time
	dialContext      func(context.Context, string, string) (net.Conn, error)
	tlsConfig        *tls.Config
}

type statusError struct {
	status  int
	retryAt time.Time
}

func (e *statusError) Error() string {
	switch e.status {
	case http.StatusUnauthorized:
		return "登录凭证已失效，请在 Claude Desktop 中重新登录"
	case http.StatusForbidden:
		return "服务器拒绝本次额度查询（HTTP 403）"
	case http.StatusTooManyRequests:
		return "额度查询过于频繁，请稍后重试（HTTP 429）"
	default:
		return "额度查询失败（HTTP " + strconv.Itoa(e.status) + "）"
	}
}

// UserMessage returns only known safe error messages, never transport errors or response bodies.
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var status *statusError
	if errors.As(err, &status) {
		return status.Error()
	}
	for _, known := range []error{ErrNetwork, ErrProxy, ErrResponse, ErrRedirect, ErrUnsupported,
		ErrCanceled, ErrHeaders, ErrHTTPLog, ErrTLSProfile, ErrEgressPolicyMissing, ErrEgressPolicyInvalid, ErrEgressCheck,
		ErrEgressNotAllowed, ErrEgressConnectionChanged, ErrExitTrace} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return desktopauth.UserMessage(err)
}

// RetryAt exposes a bounded server-directed cooldown without response header contents.
func RetryAt(err error) time.Time {
	var status *statusError
	if errors.As(err, &status) {
		return status.retryAt
	}
	return time.Time{}
}

func (c *Client) query(
	ctx context.Context, target Target, address string,
	parse func([]byte, time.Time) (*UsageRecord, error),
) (record *UsageRecord, queryErr error) {
	policy, err := compileEgressPolicy(c.EgressPolicy)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	headers := clientHeaders
	if c.clientHeaders != nil {
		headers = c.clientHeaders
	}
	requestHeaders, err := headers(c.AppVersion)
	if err != nil {
		return nil, ErrHeaders
	}
	if address == resetUsageEndpoint {
		requestHeaders.Set("User-Agent", resetUserAgent())
	}
	resolve := resolveDesktopProxy
	if c.resolveProxy != nil {
		resolve = c.resolveProxy
	}
	proxy, err := resolve(ctx, c.LiveDir)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrCanceled
		}
		return nil, ErrProxy
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	gate := &connectionGate{dialer: c.dialContext}
	capture, err := newRawCapture(c.HTTPLogDirectory, proxy, c.TLSFingerprint)
	if err != nil {
		return nil, err
	}
	var wrapTLS func(net.Conn) net.Conn
	if capture != nil {
		wrapTLS = capture.wrap
		defer func() {
			if err := capture.finish(queryErr); err != nil && queryErr == nil {
				record, queryErr = nil, err
			}
		}()
	}
	transport, err := c.newTransport(proxy, gate, wrapTLS)
	if err != nil {
		return nil, err
	}
	defer transport.CloseIdleConnections()
	var roundTripper http.RoundTripper = transport
	if c.roundTripper != nil {
		roundTripper = c.roundTripper
	}
	if capture != nil {
		roundTripper = &capturingTransport{base: roundTripper, capture: capture}
	}
	client := &http.Client{
		Transport:     roundTripper,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if err := verifyEgress(ctx, client, gate, policy, requestHeaders.Get("User-Agent")); err != nil {
		if ctx.Err() != nil {
			return nil, ErrCanceled
		}
		return nil, err
	}
	if capture != nil {
		capture.egressAllowed = true
	}
	if ctx.Err() != nil {
		return nil, ErrCanceled
	}
	load := desktopauth.Load
	if target.OrganizationID == "" {
		load = func(directory, account, _ string, at time.Time) (desktopauth.Credential, error) {
			return desktopauth.LoadForAccount(directory, account, at)
		}
	}
	if c.loadCredential != nil {
		load = c.loadCredential
	}
	credential, err := load(target.Directory, target.AccountID, target.OrganizationID, now())
	if err != nil {
		return nil, err
	}
	if !validToken(credential.AccessToken) {
		return nil, ErrHeaders
	}
	usageCtx, usageCancel := gate.usageContext(ctx)
	defer usageCancel(nil)
	request, err := http.NewRequestWithContext(usageCtx, http.MethodGet, address, nil)
	if err != nil {
		return nil, ErrNetwork
	}
	request.Header = requestHeaders
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("anthropic-version", "2023-06-01")
	request.Header.Set("Accept", "application/json")
	defer func() {
		request.Header.Del("Authorization")
		credential.AccessToken = ""
	}()
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(context.Cause(usageCtx), ErrEgressConnectionChanged) ||
			errors.Is(err, ErrEgressConnectionChanged) {
			return nil, ErrEgressConnectionChanged
		}
		if ctx.Err() != nil {
			return nil, ErrCanceled
		}
		return nil, ErrNetwork
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode <= 399 {
		return nil, ErrRedirect
	}
	if response.StatusCode != http.StatusOK {
		failure := &statusError{status: response.StatusCode}
		if response.StatusCode == http.StatusTooManyRequests {
			failure.retryAt = parseRetryAfter(response.Header.Get("Retry-After"), now())
		}
		return nil, failure
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, ErrResponse
	}
	defer clear(body)
	if address == profileEndpoint && credential.OrganizationID != "" {
		var identity struct {
			Organization struct {
				UUID string `json:"uuid"`
			} `json:"organization"`
		}
		if json.Unmarshal(body, &identity) != nil || identity.Organization.UUID != credential.OrganizationID {
			return nil, desktopauth.ErrIdentity
		}
	}
	return parse(body, now())
}

func validToken(token string) bool {
	if len(token) == 0 || len(token) > 8192 {
		return false
	}
	for _, ch := range token {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}

func parseRetryAfter(value string, now time.Time) time.Time {
	deadline := now.Add(time.Minute)
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		if seconds > 24*60*60 {
			seconds = 24 * 60 * 60
		}
		deadline = now.Add(time.Duration(seconds) * time.Second)
	} else if stamp, err := http.ParseTime(value); err == nil {
		deadline = stamp
	}
	if deadline.Before(now.Add(time.Minute)) {
		return now.Add(time.Minute)
	}
	if deadline.After(now.Add(24 * time.Hour)) {
		return now.Add(24 * time.Hour)
	}
	return deadline
}
