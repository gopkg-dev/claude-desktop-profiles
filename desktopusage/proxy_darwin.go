package desktopusage

import (
	"context"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
)

const utf8Encoding = 0x08000100

type cfProxyAPI struct {
	settings       func() uintptr
	proxies        func(uintptr, uintptr) uintptr
	pacURL         func(uintptr, uintptr, uintptr, *streamContext) uintptr
	pacScript      func(uintptr, uintptr, uintptr, *streamContext) uintptr
	retain         func(uintptr) uintptr
	release        func(uintptr)
	stringCreate   func(uintptr, string, uint32) uintptr
	stringGet      func(uintptr, []byte, int64, uint32) bool
	urlCreate      func(uintptr, uintptr, uintptr) uintptr
	urlString      func(uintptr) uintptr
	arrayCount     func(uintptr) int64
	arrayAt        func(uintptr, int64) uintptr
	dictionaryGet  func(uintptr, uintptr) uintptr
	numberGet      func(uintptr, int64, *int64) bool
	equal          func(uintptr, uintptr) bool
	getType        func(uintptr) uintptr
	arrayType      func() uintptr
	dictionaryType func() uintptr
	stringType     func() uintptr
	numberType     func() uintptr
	urlType        func() uintptr
	runLoopGet     func() uintptr
	runLoopAdd     func(uintptr, uintptr, uintptr)
	runLoopRun     func(uintptr, float64, bool) int32
	invalidate     func(uintptr)
	constants      map[string]uintptr
	callback       uintptr
}

// streamContext mirrors the C context struct; unused fields keep its memory layout.
type streamContext struct {
	version     int64 //lint:ignore U1000 C layout
	info        uintptr
	retain      uintptr //lint:ignore U1000 C layout
	release     uintptr //lint:ignore U1000 C layout
	description uintptr //lint:ignore U1000 C layout
}

type pacResult struct {
	mu      sync.Mutex
	done    bool
	proxies uintptr
}

var (
	cfOnce      sync.Once
	cfAPI       cfProxyAPI
	cfError     error
	pacSequence atomic.Uint64
	pacRequests sync.Map
)

func loadCFProxy() (*cfProxyAPI, error) {
	cfOnce.Do(func() {
		defer func() {
			if recover() != nil {
				cfError = ErrProxy
			}
		}()
		foundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			cfError = ErrProxy
			return
		}
		network, err := purego.Dlopen("/System/Library/Frameworks/CFNetwork.framework/CFNetwork", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			cfError = ErrProxy
			return
		}
		for _, item := range []struct {
			pointer any
			name    string
		}{
			{&cfAPI.settings, "CFNetworkCopySystemProxySettings"},
			{&cfAPI.proxies, "CFNetworkCopyProxiesForURL"},
			{&cfAPI.pacURL, "CFNetworkExecuteProxyAutoConfigurationURL"},
			{&cfAPI.pacScript, "CFNetworkExecuteProxyAutoConfigurationScript"},
		} {
			purego.RegisterLibFunc(item.pointer, network, item.name)
		}
		for _, item := range []struct {
			pointer any
			name    string
		}{
			{&cfAPI.retain, "CFRetain"}, {&cfAPI.release, "CFRelease"},
			{&cfAPI.stringCreate, "CFStringCreateWithCString"}, {&cfAPI.stringGet, "CFStringGetCString"},
			{&cfAPI.urlCreate, "CFURLCreateWithString"}, {&cfAPI.arrayCount, "CFArrayGetCount"},
			{&cfAPI.urlString, "CFURLGetString"},
			{&cfAPI.arrayAt, "CFArrayGetValueAtIndex"}, {&cfAPI.dictionaryGet, "CFDictionaryGetValue"},
			{&cfAPI.numberGet, "CFNumberGetValue"}, {&cfAPI.equal, "CFEqual"},
			{&cfAPI.getType, "CFGetTypeID"}, {&cfAPI.arrayType, "CFArrayGetTypeID"},
			{&cfAPI.dictionaryType, "CFDictionaryGetTypeID"}, {&cfAPI.stringType, "CFStringGetTypeID"},
			{&cfAPI.numberType, "CFNumberGetTypeID"}, {&cfAPI.urlType, "CFURLGetTypeID"},
			{&cfAPI.runLoopGet, "CFRunLoopGetCurrent"}, {&cfAPI.runLoopAdd, "CFRunLoopAddSource"},
			{&cfAPI.runLoopRun, "CFRunLoopRunInMode"}, {&cfAPI.invalidate, "CFRunLoopSourceInvalidate"},
		} {
			purego.RegisterLibFunc(item.pointer, foundation, item.name)
		}
		cfAPI.constants = map[string]uintptr{}
		libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			cfError = ErrProxy
			return
		}
		var memoryCopy func(*uintptr, uintptr, uintptr) uintptr
		purego.RegisterLibFunc(&memoryCopy, libc, "memcpy")
		for _, name := range []string{
			"kCFProxyTypeKey", "kCFProxyHostNameKey", "kCFProxyPortNumberKey", "kCFProxyUsernameKey", "kCFProxyPasswordKey",
			"kCFProxyAutoConfigurationURLKey", "kCFProxyAutoConfigurationJavaScriptKey", "kCFProxyTypeNone",
			"kCFProxyTypeHTTP", "kCFProxyTypeHTTPS", "kCFProxyTypeSOCKS", "kCFProxyTypeAutoConfigurationURL",
			"kCFProxyTypeAutoConfigurationJavaScript",
		} {
			address, symbolErr := purego.Dlsym(network, name)
			if symbolErr != nil || address == 0 {
				cfError = ErrProxy
				return
			}
			var reference uintptr
			memoryCopy(&reference, address, 8)
			cfAPI.constants[name] = reference
		}
		cfAPI.constants["mode"] = cfAPI.stringCreate(0, "ClaudeProfilesProxyResolution", utf8Encoding)
		if cfAPI.constants["mode"] == 0 {
			cfError = ErrProxy
			return
		}
		cfAPI.callback = purego.NewCallback(func(token, proxies, failure uintptr) {
			value, ok := pacRequests.Load(token)
			if !ok {
				return
			}
			result := value.(*pacResult)
			result.mu.Lock()
			defer result.mu.Unlock()
			if result.done {
				return
			}
			result.done = true
			if failure == 0 && proxies != 0 && cfAPI.getType(proxies) == cfAPI.arrayType() {
				result.proxies = cfAPI.retain(proxies)
			}
		})
	})
	if cfError != nil {
		return nil, cfError
	}
	return &cfAPI, nil
}

func nativeProxy(ctx context.Context, config proxyConfig) (*url.URL, error) {
	api, err := loadCFProxy()
	if err != nil {
		return nil, err
	}
	text := api.stringCreate(0, endpoint, utf8Encoding)
	if text == 0 {
		return nil, ErrProxy
	}
	defer api.release(text)
	target := api.urlCreate(0, text, 0)
	if target == 0 {
		return nil, ErrProxy
	}
	defer api.release(target)
	if config.kind == "pac" {
		return api.evaluatePACURL(ctx, target, config.address)
	}
	settings := api.settings()
	if settings == 0 {
		return nil, ErrProxy
	}
	defer api.release(settings)
	if api.getType(settings) != api.dictionaryType() {
		return nil, ErrProxy
	}
	proxies := api.proxies(target, settings)
	if proxies == 0 {
		return nil, ErrProxy
	}
	defer api.release(proxies)
	return api.selectProxy(ctx, target, proxies, true)
}

func (api *cfProxyAPI) stringValue(value uintptr) (string, bool) {
	if value == 0 || api.getType(value) != api.stringType() {
		return "", false
	}
	buffer := make([]byte, 4096)
	if !api.stringGet(value, buffer, int64(len(buffer)), utf8Encoding) {
		return "", false
	}
	for index, ch := range buffer {
		if ch == 0 {
			return string(buffer[:index]), true
		}
	}
	return "", false
}

func (api *cfProxyAPI) selectProxy(ctx context.Context, target, proxies uintptr, allowPAC bool) (*url.URL, error) {
	if ctx.Err() != nil || proxies == 0 || api.getType(proxies) != api.arrayType() || api.arrayCount(proxies) < 1 {
		return nil, ErrProxy
	}
	// Use the first configured route. A failed proxy never causes a silent direct fallback.
	proxy := api.arrayAt(proxies, 0)
	if proxy == 0 || api.getType(proxy) != api.dictionaryType() {
		return nil, ErrProxy
	}
	kind := api.dictionaryGet(proxy, api.constants["kCFProxyTypeKey"])
	if kind == 0 {
		return nil, ErrProxy
	}
	if api.equal(kind, api.constants["kCFProxyTypeNone"]) {
		return nil, nil
	}
	for _, pac := range []struct {
		kind, key string
		script    bool
	}{
		{"kCFProxyTypeAutoConfigurationURL", "kCFProxyAutoConfigurationURLKey", false},
		{"kCFProxyTypeAutoConfigurationJavaScript", "kCFProxyAutoConfigurationJavaScriptKey", true},
	} {
		if !api.equal(kind, api.constants[pac.kind]) {
			continue
		}
		if !allowPAC {
			return nil, ErrProxy
		}
		value := api.dictionaryGet(proxy, api.constants[pac.key])
		if value == 0 {
			return nil, ErrProxy
		}
		if pac.script && api.getType(value) != api.stringType() || !pac.script && api.getType(value) != api.urlType() {
			return nil, ErrProxy
		}
		if pac.script {
			text, ok := api.stringValue(value)
			if !ok {
				return nil, ErrProxy
			}
			return api.evaluatePACScript(ctx, target, text)
		}
		address, ok := api.stringValue(api.urlString(value))
		if !ok {
			return nil, ErrProxy
		}
		return api.evaluatePACURL(ctx, target, address)
	}
	scheme := ""
	if api.equal(kind, api.constants["kCFProxyTypeHTTP"]) || api.equal(kind, api.constants["kCFProxyTypeHTTPS"]) {
		// CFProxyTypeHTTPS denotes an HTTP CONNECT proxy for HTTPS targets, not TLS to the proxy.
		scheme = "http"
	} else if api.equal(kind, api.constants["kCFProxyTypeSOCKS"]) {
		scheme = "socks5"
	}
	if scheme == "" {
		return nil, ErrProxy
	}
	host, ok := api.stringValue(api.dictionaryGet(proxy, api.constants["kCFProxyHostNameKey"]))
	var port int64
	portRef := api.dictionaryGet(proxy, api.constants["kCFProxyPortNumberKey"])
	if !ok || host == "" || portRef == 0 || api.getType(portRef) != api.numberType() ||
		!api.numberGet(portRef, 4, &port) || port < 1 || port > 65535 {
		return nil, ErrProxy
	}
	result := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.FormatInt(port, 10))}
	if strings.HasPrefix(host, "cp-pac-") && strings.HasSuffix(host, ".invalid") && port == 1 {
		encoded := strings.TrimSuffix(strings.TrimPrefix(host, "cp-pac-"), ".invalid")
		decoded, decodeErr := hex.DecodeString(strings.ReplaceAll(encoded, ".", ""))
		if decodeErr != nil {
			return nil, ErrProxy
		}
		return parsePACRoute(string(decoded))
	}
	username, hasUser := api.stringValue(api.dictionaryGet(proxy, api.constants["kCFProxyUsernameKey"]))
	password, hasPassword := api.stringValue(api.dictionaryGet(proxy, api.constants["kCFProxyPasswordKey"]))
	if hasUser && username != "" {
		if hasPassword {
			result.User = url.UserPassword(username, password)
		} else {
			result.User = url.User(username)
		}
	}
	return result, nil
}

// evaluatePACURL downloads only the configured PAC without account credentials or redirects.
func (api *cfProxyAPI) evaluatePACURL(ctx context.Context, target uintptr, address string) (*url.URL, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrProxy
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, ErrProxy
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, ErrProxy
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrProxy
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, ErrProxy
	}
	return api.evaluatePACScript(ctx, target, string(data))
}

// CFNetwork supplies PAC helpers, but its route parser does not recognize Chromium's SOCKS5 token.
// Encode the first PAC route into an inert result hostname, then parse that route ourselves.
// The encoded hostname is never resolved or contacted; no failed proxy falls through to DIRECT.
func (api *cfProxyAPI) evaluatePACScript(ctx context.Context, target uintptr, source string) (*url.URL, error) {
	if len(source) == 0 || len(source) > 1<<20 || strings.ContainsRune(source, 0) {
		return nil, ErrProxy
	}
	wrapped := source + `
;(function() {
  var original = FindProxyForURL;
  FindProxyForURL = function(url, host) {
    var routes = String(original(url, host)).split(";");
    var route = "";
    for (var i = 0; i < routes.length; i++) {
      route = routes[i].replace(/^\s+|\s+$/g, "");
      if (route) break;
    }
    if (!route || route.length > 1024) throw new Error("unsupported PAC route");
    var encoded = "";
    for (var j = 0; j < route.length; j++) {
      var code = route.charCodeAt(j);
      if (code < 32 || code > 126) throw new Error("unsupported PAC route");
      encoded += (code < 16 ? "0" : "") + code.toString(16);
    }
    var parts = encoded.match(/.{1,50}/g);
    return "PROXY cp-pac-" + parts.join(".") + ".invalid:1";
  };
})();`
	script := api.stringCreate(0, wrapped, utf8Encoding)
	if script == 0 {
		return nil, ErrProxy
	}
	defer api.release(script)
	return api.executePAC(ctx, target, script, true)
}

func (api *cfProxyAPI) executePAC(ctx context.Context, target, configuration uintptr, script bool) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	token := uintptr(pacSequence.Add(1))
	result := &pacResult{}
	pacRequests.Store(token, result)
	defer pacRequests.Delete(token)
	clientContext := streamContext{info: token}
	execute := api.pacURL
	if script {
		execute = api.pacScript
	}
	source := execute(configuration, target, api.callback, &clientContext)
	if source == 0 {
		return nil, ErrProxy
	}
	defer api.release(source)
	defer api.invalidate(source)
	mode := api.constants["mode"]
	api.runLoopAdd(api.runLoopGet(), source, mode)
	for ctx.Err() == nil {
		result.mu.Lock()
		done, proxies := result.done, result.proxies
		result.mu.Unlock()
		if done {
			if proxies == 0 {
				return nil, ErrProxy
			}
			defer api.release(proxies)
			return api.selectProxy(ctx, target, proxies, false)
		}
		api.runLoopRun(mode, 0.05, true)
	}
	result.mu.Lock()
	result.done = true
	if result.proxies != 0 {
		api.release(result.proxies)
	}
	result.mu.Unlock()
	return nil, ErrProxy
}
