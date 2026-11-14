package desktopusage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"

	"claudeprofiles/desktopauth"
)

func TestTLSProfileRejectsUnknownAndChromeOnlyAdvertisesHTTP1(t *testing.T) {
	client := &Client{TLSFingerprint: "unknown"}
	if _, err := client.newTransport(nil, &connectionGate{}, nil); !errors.Is(err, ErrTLSProfile) {
		t.Fatal("unknown TLS profile accepted")
	}
	spec, err := chromeHTTP1Spec()
	if err != nil {
		t.Fatal(err)
	}
	var alpn, grease bool
	for _, extension := range spec.Extensions {
		switch value := extension.(type) {
		case *utls.ALPNExtension:
			alpn = slices.Equal(value.AlpnProtocols, []string{"http/1.1"})
		case *utls.UtlsGREASEExtension:
			grease = true
		case *utls.ApplicationSettingsExtension, *utls.ApplicationSettingsExtensionNew:
			t.Fatal("HTTP/2 application settings survived the HTTP/1 template")
		}
	}
	if !alpn || !grease {
		t.Fatal("Chrome template lost its GREASE extensions or HTTP/1 ALPN")
	}
}

func TestCancelDuringTLSHandshakeClosesConnectionWithoutCredentials(t *testing.T) {
	for _, fingerprint := range []TLSFingerprint{TLSFingerprintChrome, TLSFingerprintStandard} {
		t.Run(string(fingerprint), func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			client := fixtureClient(nil)
			client.TLSFingerprint = fingerprint
			client.roundTripper = nil
			client.dialContext = func(context.Context, string, string) (net.Conn, error) { return local, nil }
			var credentialReads atomic.Int32
			client.loadCredential = func(string, string, string, time.Time) (desktopauth.Credential, error) {
				credentialReads.Add(1)
				return desktopauth.Credential{}, errors.New("unexpected credential read")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := client.queryUsage(ctx, Target{})
				finished <- err
			}()
			_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
			buffer := make([]byte, 16384)
			if _, err := peer.Read(buffer); err != nil {
				t.Fatal("TLS ClientHello did not start")
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, ErrCanceled) || credentialReads.Load() != 0 {
					t.Fatalf("cancelled handshake advanced query: reads=%d error=%v", credentialReads.Load(), err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled handshake did not terminate")
			}
			if _, err := peer.Read(buffer); !errors.Is(err, io.EOF) {
				t.Fatal("cancelled handshake left the connection open")
			}
		})
	}
}

func TestQuotaUsesSingleCONNECTTunnelWithChromeOrStandardTLS(t *testing.T) {
	for _, fingerprint := range []TLSFingerprint{TLSFingerprintChrome, TLSFingerprintStandard} {
		for _, dropConnection := range []bool{false, true} {
			name := string(fingerprint) + "/keepalive"
			if dropConnection {
				name = string(fingerprint) + "/dropped"
			}
			t.Run(name, func(t *testing.T) {
				var requests, authenticated, connects, dials atomic.Int32
				var connectionMu sync.Mutex
				var targetConnection net.Conn
				target := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					if request.Host != "api.anthropic.com" || request.Method != http.MethodGet ||
						request.ProtoMajor != 1 || request.TLS.NegotiatedProtocol != "http/1.1" ||
						request.Header.Get("User-Agent") != "ClaudeProfiles/0.6.0" ||
						request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Cookie") != "" {
						t.Error("unexpected target HTTP identity, protocol, or proxy credentials")
					}
					if request.URL.Path == "/cdn-cgi/trace" {
						if request.Header.Get("Authorization") != "" || request.Header.Get("Anthropic-Beta") != "" {
							t.Error("trace carried account authentication")
						}
						_, _ = writer.Write([]byte("ip=192.0.2.10\nloc=US\n"))
						return
					}
					if request.URL.Path != "/api/oauth/usage" || request.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Error("unexpected quota path or authorization")
					}
					authenticated.Add(1)
					_, _ = writer.Write([]byte(`{"five_hour":{"utilization":42}}`))
				}))
				target.EnableHTTP2 = true
				target.TLS = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
				target.Config.ConnState = func(connection net.Conn, state http.ConnState) {
					if state == http.StateNew {
						connectionMu.Lock()
						targetConnection = connection
						connectionMu.Unlock()
					}
				}
				target.StartTLS()
				defer target.Close()

				proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					connects.Add(1)
					if request.Method != http.MethodConnect || request.Host != "api.anthropic.com:443" ||
						request.Header.Get("Authorization") != "" {
						t.Error("unexpected CONNECT target or leaked OAuth header")
					}
					upstream, err := net.Dial("tcp", target.Listener.Addr().String())
					if err != nil {
						t.Error(err)
						return
					}
					defer upstream.Close()
					connection, buffered, err := writer.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer connection.Close()
					_, _ = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
					finished := make(chan struct{})
					go func() {
						defer close(finished)
						_, _ = io.Copy(connection, upstream)
						_ = connection.Close()
					}()
					_, _ = io.Copy(upstream, buffered)
					_ = upstream.Close()
					<-finished
				}))
				defer proxy.Close()
				proxyURL, err := url.Parse(proxy.URL)
				if err != nil {
					t.Fatal(err)
				}
				client := fixtureClient(nil)
				client.TLSFingerprint = fingerprint
				client.roundTripper = nil
				client.resolveProxy = func(context.Context, string) (*url.URL, error) { return proxyURL, nil }
				client.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					dials.Add(1)
					if address != proxyURL.Host {
						return nil, errors.New("unexpected direct connection")
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}
				roots := x509.NewCertPool()
				roots.AddCert(target.Certificate())
				client.tlsConfig = &tls.Config{
					RootCAs: roots, ServerName: target.Certificate().DNSNames[0], MinVersion: tls.VersionTLS12,
				}
				if dropConnection {
					client.loadCredential = func(string, string, string, time.Time) (desktopauth.Credential, error) {
						connectionMu.Lock()
						defer connectionMu.Unlock()
						if targetConnection == nil {
							return desktopauth.Credential{}, errors.New("missing verified connection")
						}
						_ = targetConnection.Close()
						return desktopauth.Credential{AccessToken: "fixture-token"}, nil
					}
				}
				_, queryErr := client.queryUsage(context.Background(), Target{})
				if dropConnection {
					if queryErr == nil || requests.Load() != 1 || authenticated.Load() != 0 {
						t.Fatalf("closed tunnel accepted quota: requests=%d auth=%d error=%v",
							requests.Load(), authenticated.Load(), queryErr)
					}
				} else if queryErr != nil || requests.Load() != 2 || authenticated.Load() != 1 {
					t.Fatalf("single tunnel query failed: requests=%d auth=%d error=%v",
						requests.Load(), authenticated.Load(), queryErr)
				}
				if connects.Load() != 1 || dials.Load() != 1 {
					t.Fatalf("gate allowed redial: CONNECT=%d TCP=%d", connects.Load(), dials.Load())
				}
			})
		}
	}
}
