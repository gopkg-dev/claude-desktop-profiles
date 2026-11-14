// Package httplog keeps explicitly requested HTTP diagnostics in private, bounded local files.
package httplog

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxRequestBytes      = 128 << 10
	MaxResponseHeadBytes = 64 << 10
	MaxResponseBodyBytes = 1 << 20
	defaultMaxBytes      = 20 << 20
	defaultMaxExchanges  = 100
)

var (
	ErrDirectory   = errors.New("无法访问私有 HTTP 日志目录")
	ErrUnsafePath  = errors.New("HTTP 日志路径不安全，已停止记录")
	ErrUnsupported = errors.New("此请求不在 HTTP 日志允许范围内")
	ErrWrite       = errors.New("HTTP 日志保存失败")
	ErrTooLarge    = errors.New("HTTP 日志超过保存上限")
	exchangeName   = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{16}$`)
	directoryLocks sync.Map
)

// Metadata accepts operational information only; account IDs, tokens, and proxy URLs do not belong here.
type Metadata struct {
	StartedAt      time.Time
	Duration       time.Duration
	Purpose        string
	ProxyType      string
	Outcome        string
	TLSFingerprint string
	RawTruncated   bool
}

// Logger writes raw credentials only into private files when explicitly called by its owner.
// It never writes request contents or underlying errors to stdout, stderr, or application logs.
type Logger struct {
	directory    string
	mu           *sync.Mutex
	maxBytes     int64
	maxExchanges int
}

type document struct {
	Version           int       `json:"version"`
	StartedAt         time.Time `json:"started_at"`
	RecordedAt        time.Time `json:"recorded_at"`
	DurationMS        int64     `json:"duration_ms"`
	Purpose           string    `json:"purpose"`
	Method            string    `json:"method"`
	URL               string    `json:"url"`
	ProxyType         string    `json:"proxy_type"`
	Outcome           string    `json:"outcome"`
	TLSFingerprint    string    `json:"tls_fingerprint"`
	RequestProtocol   string    `json:"request_protocol,omitempty"`
	ResponseProtocol  string    `json:"response_protocol,omitempty"`
	StatusCode        int       `json:"status_code,omitempty"`
	Format            string    `json:"format"`
	RequestBytes      int       `json:"request_bytes"`
	ResponseBytes     int       `json:"response_bytes"`
	Truncated         bool      `json:"truncated"`
	RequestTruncated  bool      `json:"request_truncated"`
	ResponseTruncated bool      `json:"response_truncated"`
}

// New prepares a private directory; it neither reads credentials nor starts background work.
func New(directory string) (*Logger, error) {
	root, err := openDirectory(directory, true)
	if err != nil {
		return nil, err
	}
	root.Close()
	mutex, _ := directoryLocks.LoadOrStore(directory, &sync.Mutex{})
	return &Logger{
		directory: directory, mu: mutex.(*sync.Mutex),
		maxBytes: defaultMaxBytes, maxExchanges: defaultMaxExchanges,
	}, nil
}

// Record builds an HTTP text representation without reading either request or response Body.
// Use RecordRaw for captured bytes: reconstructing structs cannot recover wire header order or HTTP/2 frames.
func (l *Logger) Record(meta Metadata, request *http.Request, response *http.Response, body []byte) error {
	if _, err := allowedRequest(request); err != nil {
		return err
	}
	requestRaw, err := requestText(request)
	if err != nil {
		return err
	}
	defer clear(requestRaw)
	responseRaw, err := responseText(response, body)
	if err != nil {
		return err
	}
	defer clear(responseRaw)
	return l.record(meta, request, response, requestRaw, responseRaw, "reconstructed-http-text")
}

// RecordRaw preserves captured HTTP bytes, including credential headers, only in private local files.
// Captures may be empty when transport setup failed; neither Body stream is consumed or closed.
func (l *Logger) RecordRaw(
	meta Metadata, request *http.Request, response *http.Response, rawRequest, rawResponse []byte,
) error {
	return l.record(meta, request, response, rawRequest, rawResponse, "captured-http-raw")
}

func (l *Logger) record(
	meta Metadata, request *http.Request, response *http.Response, rawRequest, rawResponse []byte, format string,
) error {
	if l == nil || l.mu == nil || l.maxBytes < 1 || l.maxExchanges < 1 {
		return ErrDirectory
	}
	purpose, err := allowedRequest(request)
	if err != nil {
		return err
	}
	if meta.Purpose != "" && meta.Purpose != purpose {
		return ErrUnsupported
	}
	requestData, requestTruncated := bounded(rawRequest, MaxRequestBytes)
	responseData, responseTruncated := boundedResponse(rawResponse)
	requestData, responseData = bytes.Clone(requestData), bytes.Clone(responseData)
	defer clear(requestData)
	defer clear(responseData)
	now := time.Now().UTC()
	started := meta.StartedAt
	if started.IsZero() || started.Before(now.Add(-24*time.Hour)) || started.After(now.Add(time.Minute)) {
		started = now
	}
	duration := meta.Duration
	if duration < 0 || duration > 24*time.Hour {
		duration = 0
	}
	entry := document{
		Version: 1, StartedAt: started.UTC(), RecordedAt: now, DurationMS: duration.Milliseconds(),
		Purpose: purpose, Method: http.MethodGet, URL: request.URL.String(),
		ProxyType: enum(meta.ProxyType, "direct", "system", "pac", "http", "https", "socks", "socks5", "socks5h"),
		Outcome: enum(meta.Outcome, "success", "http_error", "network_error", "canceled", "timeout", "error",
			"egress_allowed", "egress_denied", "egress_failed"),
		TLSFingerprint:  enum(meta.TLSFingerprint, "chrome", "standard"),
		RequestProtocol: protocol(request.Proto), Format: format,
		RequestBytes: len(requestData), ResponseBytes: len(responseData),
		Truncated:        meta.RawTruncated || requestTruncated || responseTruncated,
		RequestTruncated: requestTruncated, ResponseTruncated: responseTruncated,
	}
	if response != nil {
		entry.ResponseProtocol = protocol(response.Proto)
		if response.StatusCode >= 100 && response.StatusCode <= 599 {
			entry.StatusCode = response.StatusCode
		}
	}
	metadata, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return ErrWrite
	}
	metadata = append(metadata, '\n')
	files := map[string][]byte{"request.raw": requestData, "response.raw": responseData, "metadata.json": metadata}
	total := int64(len(requestData)) + int64(len(responseData)) + int64(len(metadata))
	if total > l.maxBytes {
		return ErrTooLarge
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	root, err := openDirectory(l.directory, false)
	if err != nil {
		return err
	}
	defer root.Close()
	unlock, err := lockDirectory(root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := l.rotate(root, total); err != nil {
		return err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrWrite
	}
	name := now.Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(nonce[:])
	pending := ".pending-" + name
	if err := root.Mkdir(pending, 0o700); err != nil {
		return ErrWrite
	}
	defer root.RemoveAll(pending)
	staging, err := checkedChild(root, pending)
	if err != nil {
		return err
	}
	for _, filename := range []string{"request.raw", "response.raw", "metadata.json"} {
		if err := writePrivateFile(staging, filename, files[filename]); err != nil {
			staging.Close()
			return err
		}
	}
	if err := syncDirectory(staging); err != nil {
		staging.Close()
		return err
	}
	staging.Close()
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return ErrWrite
	}
	if err := root.Rename(pending, name); err != nil {
		return ErrWrite
	}
	return syncDirectory(root)
}

func allowedRequest(request *http.Request) (string, error) {
	if request == nil || request.URL == nil || request.Method != http.MethodGet ||
		request.URL.Scheme != "https" || request.URL.Host != "api.anthropic.com" ||
		request.URL.User != nil || request.URL.ForceQuery ||
		request.URL.Fragment != "" || request.URL.RawFragment != "" || request.URL.Opaque != "" ||
		request.URL.RawPath != "" || (request.Host != "" && request.Host != "api.anthropic.com") ||
		request.ContentLength > 0 || (request.Body != nil && request.Body != http.NoBody) ||
		len(request.TransferEncoding) > 0 || len(request.Trailer) > 0 {
		return "", ErrUnsupported
	}
	if request.URL.RawQuery != "" &&
		(request.URL.Path != "/api/oauth/usage" || request.URL.RawQuery != "cedar_ember=1") {
		return "", ErrUnsupported
	}
	switch request.URL.Path {
	case "/api/oauth/profile":
		return "profile", nil
	case "/api/oauth/usage":
		return "usage", nil
	case "/cdn-cgi/trace":
		return "trace", nil
	default:
		return "", ErrUnsupported
	}
}

func bounded(data []byte, limit int) ([]byte, bool) {
	if len(data) <= limit {
		return data, false
	}
	return data[:limit], true
}

func boundedResponse(data []byte) ([]byte, bool) {
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		return bounded(data, MaxResponseHeadBytes)
	}
	headerLength := headerEnd + 4
	if headerLength > MaxResponseHeadBytes {
		return data[:MaxResponseHeadBytes], true
	}
	return bounded(data, headerLength+MaxResponseBodyBytes)
}

func enum(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return candidate
		}
	}
	return "unknown"
}

func protocol(value string) string {
	if value == "HTTP/1.0" || value == "HTTP/1.1" || value == "HTTP/2.0" || value == "HTTP/3.0" {
		return value
	}
	return ""
}

func requestText(request *http.Request) ([]byte, error) {
	var buffer bytes.Buffer
	proto := protocol(request.Proto)
	if proto == "" {
		proto = "HTTP/1.1"
	}
	fmt.Fprintf(&buffer, "GET %s %s\r\nHost: api.anthropic.com\r\n", request.URL.RequestURI(), proto)
	if err := request.Header.WriteSubset(&buffer, map[string]bool{"Host": true}); err != nil {
		return nil, ErrWrite
	}
	buffer.WriteString("\r\n")
	return buffer.Bytes(), nil
}

func responseText(response *http.Response, body []byte) ([]byte, error) {
	if response == nil {
		if len(body) != 0 {
			return nil, ErrUnsupported
		}
		return nil, nil
	}
	if response.StatusCode < 100 || response.StatusCode > 599 {
		return nil, ErrUnsupported
	}
	proto := protocol(response.Proto)
	if proto == "" {
		proto = "HTTP/1.1"
	}
	status := strconv.Itoa(response.StatusCode) + " " + http.StatusText(response.StatusCode)
	if strings.HasPrefix(response.Status, strconv.Itoa(response.StatusCode)+" ") &&
		!strings.ContainsAny(response.Status, "\r\n") && len(response.Status) <= 1024 {
		status = response.Status
	}
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "%s %s\r\n", proto, status)
	if err := response.Header.Write(&buffer); err != nil {
		return nil, ErrWrite
	}
	buffer.WriteString("\r\n")
	buffer.Write(body)
	return buffer.Bytes(), nil
}

type storedExchange struct {
	name string
	size int64
}

func (l *Logger) rotate(root *os.Root, incoming int64) error {
	directory, err := root.Open(".")
	if err != nil {
		return ErrDirectory
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return ErrDirectory
	}
	var exchanges []storedExchange
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		pending := strings.HasPrefix(name, ".pending-") && exchangeName.MatchString(strings.TrimPrefix(name, ".pending-"))
		if !exchangeName.MatchString(name) && !pending {
			continue
		}
		child, err := checkedChild(root, name)
		if err != nil {
			return err
		}
		size, err := exchangeSize(child)
		child.Close()
		if err != nil {
			return err
		}
		if pending {
			if err := root.RemoveAll(name); err != nil {
				return ErrWrite
			}
			continue
		}
		exchanges = append(exchanges, storedExchange{name: name, size: size})
		total += size
	}
	sort.Slice(exchanges, func(i, j int) bool { return exchanges[i].name < exchanges[j].name })
	for len(exchanges) >= l.maxExchanges || total+incoming > l.maxBytes {
		if len(exchanges) == 0 {
			return ErrTooLarge
		}
		oldest := exchanges[0]
		if err := root.RemoveAll(oldest.name); err != nil {
			return ErrWrite
		}
		total -= oldest.size
		exchanges = exchanges[1:]
	}
	return nil
}

func exchangeSize(root *os.Root) (int64, error) {
	directory, err := root.Open(".")
	if err != nil {
		return 0, ErrDirectory
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return 0, ErrDirectory
	}
	var total int64
	for _, entry := range entries {
		if entry.Name() != "request.raw" && entry.Name() != "response.raw" && entry.Name() != "metadata.json" {
			return 0, ErrUnsafePath
		}
		info, err := root.Lstat(entry.Name())
		if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > defaultMaxBytes {
			return 0, ErrUnsafePath
		}
		total += info.Size()
	}
	return total, nil
}
