package desktopusage

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/imroc/req/v3"
	utls "github.com/refraction-networking/utls"
)

// TLSFingerprint selects the ClientHello template without changing HTTP client identity.
type TLSFingerprint string

const (
	// TLSFingerprintChrome uses the pinned uTLS Chrome template, restricted to HTTP/1.1.
	TLSFingerprintChrome TLSFingerprint = "chrome"
	// TLSFingerprintStandard uses the Go TLS handshake, also restricted to HTTP/1.1.
	TLSFingerprintStandard TLSFingerprint = "standard"
)

func (c *Client) newTransport(
	proxy *url.URL,
	gate *connectionGate,
	wrapTLS func(net.Conn) net.Conn,
) (*req.Transport, error) {
	fingerprint := c.TLSFingerprint
	if fingerprint == "" {
		fingerprint = TLSFingerprintChrome
	}
	if fingerprint != TLSFingerprintChrome && fingerprint != TLSFingerprintStandard {
		return nil, ErrTLSProfile
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.tlsConfig != nil {
		config = c.tlsConfig.Clone()
	}
	config.NextProtos = []string{"http/1.1"}
	transport := req.NewTransport()
	transport.Proxy = http.ProxyURL(proxy)
	transport.DialContext = gate.dial
	transport.MaxConnsPerHost = 1
	transport.MaxIdleConns = 1
	transport.MaxIdleConnsPerHost = 1
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.IdleConnTimeout = 30 * time.Second
	transport.TLSClientConfig = config
	// The handshake owns its deadline, avoiding a second timer racing req's connection assignment.
	transport.TLSHandshakeTimeout = 0
	transport.DisableCompression = true
	transport.DisableAutoDecode()
	transport.SetTLSHandshake(tlsHandshake(config, fingerprint, wrapTLS))
	return transport, nil
}

func tlsHandshake(
	config *tls.Config,
	fingerprint TLSFingerprint,
	wrapTLS func(net.Conn) net.Conn,
) func(context.Context, string, net.Conn) (net.Conn, *tls.ConnectionState, error) {
	return func(ctx context.Context, address string, plain net.Conn) (net.Conn, *tls.ConnectionState, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		localConfig := config.Clone()
		if localConfig.ServerName == "" {
			localConfig.ServerName = address
			if host, _, err := net.SplitHostPort(address); err == nil {
				localConfig.ServerName = host
			}
		}
		if fingerprint == TLSFingerprintStandard {
			connection := tls.Client(plain, localConfig)
			if err := connection.HandshakeContext(ctx); err != nil {
				return nil, nil, err
			}
			state := connection.ConnectionState()
			if wrapTLS != nil {
				return wrapTLS(connection), &state, nil
			}
			return connection, &state, nil
		}
		spec, err := chromeHTTP1Spec()
		if err != nil {
			return nil, nil, err
		}
		connection := utls.UClient(plain, &utls.Config{
			ServerName: localConfig.ServerName, RootCAs: localConfig.RootCAs,
			MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
		}, utls.HelloCustom)
		if err := connection.ApplyPreset(&spec); err != nil {
			return nil, nil, err
		}
		if err := connection.HandshakeContext(ctx); err != nil {
			return nil, nil, err
		}
		state := connection.ConnectionState()
		var result net.Conn = connection
		if wrapTLS != nil {
			result = wrapTLS(connection)
		}
		return result, &tls.ConnectionState{
			Version: state.Version, HandshakeComplete: state.HandshakeComplete,
			DidResume: state.DidResume, CipherSuite: state.CipherSuite,
			NegotiatedProtocol: state.NegotiatedProtocol,
			ServerName:         state.ServerName, PeerCertificates: state.PeerCertificates,
			VerifiedChains: state.VerifiedChains, OCSPResponse: state.OCSPResponse,
			SignedCertificateTimestamps: state.SignedCertificateTimestamps,
		}, nil
	}
}

func chromeHTTP1Spec() (utls.ClientHelloSpec, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
	if err != nil {
		return spec, ErrTLSProfile
	}
	filtered := spec.Extensions[:0]
	for _, extension := range spec.Extensions {
		switch value := extension.(type) {
		case *utls.ALPNExtension:
			value.AlpnProtocols = []string{"http/1.1"}
		case *utls.ApplicationSettingsExtension, *utls.ApplicationSettingsExtensionNew:
			// Chrome's ALPS advertises HTTP/2 settings, which this guarded transport does not use.
			continue
		}
		filtered = append(filtered, extension)
	}
	spec.Extensions = filtered
	return spec, nil
}
