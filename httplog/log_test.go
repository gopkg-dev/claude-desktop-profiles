package httplog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("could not prepare test directory")
	}
	directory := filepath.Join(base, "private-http")
	logger, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	return logger, directory
}

func sampleExchange(t *testing.T) (*http.Request, *http.Response) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		t.Fatal("could not build test request")
	}
	response := &http.Response{StatusCode: 200, Status: "200 OK", Proto: "HTTP/1.1", Header: make(http.Header)}
	return request, response
}

func exchanges(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal("could not list test exchanges")
	}
	var names []string
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		if !exchangeName.MatchString(entry.Name()) || !entry.IsDir() {
			t.Fatal("published exchange was not atomic or had an unsafe filename")
		}
		names = append(names, filepath.Join(directory, entry.Name()))
	}
	return names
}

func fileContents(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal("could not read test log")
	}
	return data
}

func readDocument(t *testing.T, directory string) document {
	t.Helper()
	var meta document
	if err := json.Unmarshal(fileContents(t, filepath.Join(directory, "metadata.json")), &meta); err != nil {
		t.Fatal("invalid metadata JSON")
	}
	return meta
}

func assertPermissions(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Fatal("log permission was not private")
	}
}

func TestRawLogsPreserveBytesAndCredentialsOnlyInPrivateFiles(t *testing.T) {
	logger, directory := testLogger(t)
	request, response := sampleExchange(t)
	rawRequest := []byte("GET /api/oauth/usage HTTP/1.1\r\nHost: api.anthropic.com\r\n" +
		"aUtHoRiZaTiOn: Bearer fixture-access-token\r\nProxy-Authorization: Basic fixture-proxy-token\r\n" +
		"Cookie: fixture-session=one\r\nCookie: fixture-second=two\r\nX-Fixture: ordered-last\r\n\r\n")
	rawResponse := []byte("HTTP/1.1 200 OK\r\nSet-Cookie: fixture-response=three\r\n" +
		"Content-Type: application/json\r\n\r\n{\"five_hour\":{\"utilization\":42}}")
	if err := logger.RecordRaw(Metadata{
		Purpose: "usage", ProxyType: "http", Outcome: "success", TLSFingerprint: "standard",
		StartedAt: time.Now(), Duration: 23 * time.Millisecond,
	}, request, response, rawRequest, rawResponse); err != nil {
		t.Fatal(err)
	}
	stored := exchanges(t, directory)
	if len(stored) != 1 {
		t.Fatal("wrong exchange count")
	}
	if !bytes.Equal(fileContents(t, filepath.Join(stored[0], "request.raw")), rawRequest) ||
		!bytes.Equal(fileContents(t, filepath.Join(stored[0], "response.raw")), rawResponse) {
		t.Fatal("captured bytes or header order were altered")
	}
	assertPermissions(t, directory, 0o700)
	assertPermissions(t, stored[0], 0o700)
	for _, filename := range []string{"request.raw", "response.raw", "metadata.json"} {
		assertPermissions(t, filepath.Join(stored[0], filename), 0o600)
	}
	assertPermissions(t, filepath.Join(directory, ".lock"), 0o600)
	meta := readDocument(t, stored[0])
	if meta.Format != "captured-http-raw" || meta.Purpose != "usage" || meta.StatusCode != 200 ||
		meta.ProxyType != "http" || meta.DurationMS != 23 || meta.Truncated || meta.TLSFingerprint != "standard" {
		t.Fatal("operational metadata does not match the exchange")
	}
	if bytes.Contains(fileContents(t, filepath.Join(stored[0], "metadata.json")), []byte("fixture-access-token")) {
		t.Fatal("credentials leaked into metadata")
	}
}

type unreadBody struct {
	reads  int
	closed bool
}

func (body *unreadBody) Read([]byte) (int, error) {
	body.reads++
	return 0, io.EOF
}

func (body *unreadBody) Close() error {
	body.closed = true
	return nil
}

func TestRecordNeverConsumesBodiesAndKeepsHeaderValues(t *testing.T) {
	logger, directory := testLogger(t)
	request, response := sampleExchange(t)
	request.Header["authorization"] = []string{"Bearer fixture-one", "Bearer fixture-two"}
	request.Header["Cookie"] = []string{"fixture-cookie"}
	response.Header["Set-Cookie"] = []string{"fixture-response-one", "fixture-response-two"}
	body := &unreadBody{}
	response.Body = body
	if err := logger.Record(Metadata{}, request, response, []byte("fixture-payload")); err != nil {
		t.Fatal(err)
	}
	if body.reads != 0 || body.closed {
		t.Fatal("the logger consumed or closed the response body")
	}
	stored := exchanges(t, directory)
	requestData := fileContents(t, filepath.Join(stored[0], "request.raw"))
	responseData := fileContents(t, filepath.Join(stored[0], "response.raw"))
	for _, value := range []string{"fixture-one", "fixture-two", "fixture-cookie"} {
		if !bytes.Contains(requestData, []byte(value)) {
			t.Fatal("requested raw credential value was changed")
		}
	}
	for _, value := range []string{"fixture-response-one", "fixture-response-two", "fixture-payload"} {
		if !bytes.Contains(responseData, []byte(value)) {
			t.Fatal("response data was altered")
		}
	}
	if readDocument(t, stored[0]).Format != "reconstructed-http-text" {
		t.Fatal("reconstructed headers were mislabeled as wire capture")
	}
}

func TestOnlyExactAllowedGETRequestsCanBeRecorded(t *testing.T) {
	for index, target := range []string{
		"https://example.invalid/api/oauth/usage", "http://api.anthropic.com/api/oauth/usage",
		"https://api.anthropic.com:443/api/oauth/usage", "https://fixture-secret@api.anthropic.com/api/oauth/usage",
		"https://api.anthropic.com/api/oauth/usage?token=fixture-secret", "https://api.anthropic.com/api/oauth/usage?",
		"https://api.anthropic.com/api/oauth/usage#fixture-secret", "https://api.anthropic.com/v1/oauth/token",
		"https://api.anthropic.com/api/oauth/%75sage",
		"https://api.anthropic.com/api/oauth/profile/", "https://api.anthropic.com/api/oauth/%70rofile",
		"https://api.anthropic.com/api/oauth/profile?cedar_ember=1",
		"https://api.anthropic.com/api/oauth/usage?cedar_ember=0",
		"https://api.anthropic.com/api/oauth/usage?cedar_ember=1&cedar_ember=1",
		"https://api.anthropic.com/api/oauth/usage?cedar_ember=1&token=fixture-secret",
		"https://api.anthropic.com/api/oauth/usage?%63edar_ember=1",
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			logger, directory := testLogger(t)
			request, err := http.NewRequest(http.MethodGet, target, nil)
			if err != nil {
				t.Fatal("invalid fixture")
			}
			err = logger.RecordRaw(Metadata{}, request, nil, nil, nil)
			if !errors.Is(err, ErrUnsupported) || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("out-of-scope URL accepted or echoed")
			}
			if len(exchanges(t, directory)) != 0 {
				t.Fatal("out-of-scope request was written")
			}
		})
	}
	logger, _ := testLogger(t)
	request, response := sampleExchange(t)
	request.Method = http.MethodPost
	if !errors.Is(logger.Record(Metadata{}, request, response, nil), ErrUnsupported) {
		t.Fatal("write request accepted")
	}
	request.Method, request.Host = http.MethodGet, "elsewhere.invalid"
	if !errors.Is(logger.Record(Metadata{}, request, response, nil), ErrUnsupported) {
		t.Fatal("host override accepted")
	}
	request.Host, request.Body = "api.anthropic.com", &unreadBody{}
	if !errors.Is(logger.Record(Metadata{}, request, response, nil), ErrUnsupported) {
		t.Fatal("request body accepted")
	}
}

func TestProfileRequestCanBeLoggedWithMatchingPurpose(t *testing.T) {
	logger, directory := testLogger(t)
	request, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/profile", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordRaw(Metadata{Purpose: "profile"}, request, nil, nil, nil); err != nil {
		t.Fatalf("account profile is not supported by the HTTP logger: %v", err)
	}
	stored := exchanges(t, directory)
	if len(stored) != 1 || readDocument(t, stored[0]).Purpose != "profile" {
		t.Fatal("profile request was not recorded with its own purpose")
	}
	if err := logger.RecordRaw(Metadata{Purpose: "usage"}, request, nil, nil, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("profile request accepted with incorrect purpose")
	}
}

func TestTraceFailureKeepsSafeMetadataAndEmptyRawFiles(t *testing.T) {
	logger, directory := testLogger(t)
	request, _ := http.NewRequest(http.MethodGet, "https://api.anthropic.com/cdn-cgi/trace", nil)
	if err := logger.RecordRaw(Metadata{
		Purpose: "trace", Outcome: "egress_failed", ProxyType: "https://fixture-secret@private.invalid",
		TLSFingerprint: "chrome", RawTruncated: true,
	}, request, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	stored := exchanges(t, directory)
	meta := readDocument(t, stored[0])
	if meta.Outcome != "egress_failed" || meta.ProxyType != "unknown" || !meta.Truncated || meta.StatusCode != 0 {
		t.Fatal("failed exchange metadata incorrect")
	}
	if bytes.Contains(fileContents(t, filepath.Join(stored[0], "metadata.json")), []byte("fixture-secret")) {
		t.Fatal("free-form metadata reached the log")
	}
	if len(fileContents(t, filepath.Join(stored[0], "request.raw"))) != 0 ||
		len(fileContents(t, filepath.Join(stored[0], "response.raw"))) != 0 {
		t.Fatal("failed transport invented HTTP data")
	}
}

func TestRawLimitsKeepPrefixesAndMarkTruncation(t *testing.T) {
	logger, directory := testLogger(t)
	request, response := sampleExchange(t)
	requestData := bytes.Repeat([]byte{'a'}, MaxRequestBytes+128)
	header := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")
	responseData := append(bytes.Clone(header), bytes.Repeat([]byte{'b'}, MaxResponseBodyBytes+128)...)
	if err := logger.RecordRaw(Metadata{}, request, response, requestData, responseData); err != nil {
		t.Fatal(err)
	}
	stored := exchanges(t, directory)
	if !bytes.Equal(fileContents(t, filepath.Join(stored[0], "request.raw")), requestData[:MaxRequestBytes]) ||
		!bytes.Equal(fileContents(t, filepath.Join(stored[0], "response.raw")),
			responseData[:len(header)+MaxResponseBodyBytes]) {
		t.Fatal("truncated log did not preserve the original prefix")
	}
	meta := readDocument(t, stored[0])
	if !meta.Truncated || !meta.RequestTruncated || !meta.ResponseTruncated {
		t.Fatal("truncated exchange was presented as complete")
	}
	tooLongHead := bytes.Repeat([]byte{'h'}, MaxResponseHeadBytes+1)
	tooLongHead = append(tooLongHead, []byte("\r\n\r\nbody")...)
	if err := logger.RecordRaw(Metadata{}, request, response, nil, tooLongHead); err != nil {
		t.Fatal(err)
	}
	stored = exchanges(t, directory)
	if len(fileContents(t, filepath.Join(stored[1], "response.raw"))) != MaxResponseHeadBytes {
		t.Fatal("oversized header was not bounded")
	}
}

func TestLogPathsRejectSymlinksAtEveryLevel(t *testing.T) {
	logger, directory := testLogger(t)
	outside := filepath.Join(filepath.Dir(directory), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal("could not create fixture")
	}
	link := filepath.Join(filepath.Dir(directory), "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal("could not create fixture symlink")
	}
	if _, err := New(filepath.Join(link, "nested")); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("symlink ancestor accepted")
	}
	if _, err := New(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("symlink log root accepted")
	}
	request, response := sampleExchange(t)
	if err := os.Symlink(filepath.Join(outside, "lock"), filepath.Join(directory, ".lock")); err != nil {
		t.Fatal("could not create lock fixture")
	}
	if err := logger.RecordRaw(Metadata{}, request, response, nil, nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("symlink lock accepted")
	}
	if err := os.Remove(filepath.Join(directory, ".lock")); err != nil {
		t.Fatal("could not remove fixture")
	}
	if err := logger.RecordRaw(Metadata{}, request, response, nil, nil); err != nil {
		t.Fatal(err)
	}
	stored := exchanges(t, directory)
	if err := os.Remove(filepath.Join(stored[0], "request.raw")); err != nil {
		t.Fatal("could not change fixture")
	}
	outsideFile := filepath.Join(outside, "keep")
	if err := os.WriteFile(outsideFile, []byte("keep unchanged"), 0o600); err != nil {
		t.Fatal("could not create outside fixture")
	}
	if err := os.Symlink(outsideFile, filepath.Join(stored[0], "request.raw")); err != nil {
		t.Fatal("could not create file fixture")
	}
	if err := logger.RecordRaw(Metadata{}, request, response, nil, nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatal("symlink exchange file accepted")
	}
	if !bytes.Equal(fileContents(t, outsideFile), []byte("keep unchanged")) {
		t.Fatal("logger changed a file outside its directory")
	}
}

func TestRotationBoundsCountBytesAndConcurrentWriters(t *testing.T) {
	logger, directory := testLogger(t)
	logger.maxExchanges, logger.maxBytes = 3, 12<<10
	second, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	second.maxExchanges, second.maxBytes = logger.maxExchanges, logger.maxBytes
	request, response := sampleExchange(t)
	payload := append([]byte("HTTP/1.1 200 OK\r\n\r\n"), bytes.Repeat([]byte{'x'}, 3<<10)...)
	var workers sync.WaitGroup
	errorsFound := make(chan error, 30)
	for index := range 30 {
		workers.Go(func() {
			writer := logger
			if index%2 == 1 {
				writer = second
			}
			errorsFound <- writer.RecordRaw(Metadata{}, request, response, nil, payload)
		})
	}
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	stored := exchanges(t, directory)
	if len(stored) != 3 {
		t.Fatal("retention count was not bounded")
	}
	var total int64
	for _, exchange := range stored {
		for _, filename := range []string{"request.raw", "response.raw", "metadata.json"} {
			info, err := os.Stat(filepath.Join(exchange, filename))
			if err != nil {
				t.Fatal("partial exchange became visible")
			}
			total += info.Size()
		}
	}
	if total > logger.maxBytes {
		t.Fatal("retention byte limit exceeded")
	}
	logger.maxBytes = 2 << 10
	if err := logger.RecordRaw(Metadata{}, request, response, nil, payload); !errors.Is(err, ErrTooLarge) {
		t.Fatal("single oversized exchange accepted")
	}
}
