package desktopusage

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// exitHTTPClient follows Desktop's proxy without credentials or environment proxy fallback.
func exitHTTPClient(ctx context.Context, liveDir string) (*http.Client, *url.URL, error) {
	proxy, err := resolveDesktopProxy(ctx, liveDir)
	if err != nil {
		return nil, nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxy)
	return &http.Client{Transport: transport, Timeout: 12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, proxy, nil
}
