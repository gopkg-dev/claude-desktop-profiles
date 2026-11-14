//go:build !darwin

package desktopusage

import (
	"context"
	"net/http"
	"net/url"
)

func nativeProxy(context.Context, proxyConfig) (*url.URL, error) { return nil, ErrUnsupported }
func clientHeaders(string) (http.Header, error)                  { return nil, ErrUnsupported }
func checkManagedProxy() error                                   { return ErrUnsupported }
