//go:build darwin

package desktopauth

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const platformSupported = true

type keychainAPI struct {
	createDictionary func(uintptr, int64, uintptr, uintptr) uintptr
	setDictionary    func(uintptr, uintptr, uintptr)
	getDictionary    func(uintptr, uintptr) uintptr
	createString     func(uintptr, string, uint32) uintptr
	getType          func(uintptr) uintptr
	arrayType        func() uintptr
	dictionaryType   func() uintptr
	stringType       func() uintptr
	dataType         func() uintptr
	arrayCount       func(uintptr) int64
	arrayValue       func(uintptr, int64) uintptr
	dataLength       func(uintptr) int64
	dataBytes        func(uintptr) unsafe.Pointer
	release          func(uintptr)
	match            func(uintptr, *uintptr) int32
	keyCallbacks     uintptr
	valueCallbacks   uintptr
	constants        map[string]uintptr
}

type keychainAPIResult struct {
	api *keychainAPI
	err error
}

var nativeKeychain = sync.OnceValue(loadKeychainAPI)

func loadKeychainAPI() (result keychainAPIResult) {
	result.err = ErrKeychain
	defer func() {
		if recover() != nil {
			result = keychainAPIResult{err: ErrKeychain}
		}
	}()
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW)
	if err != nil {
		return result
	}
	security, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW)
	if err != nil {
		return result
	}
	system, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
	if err != nil {
		return result
	}
	api := &keychainAPI{constants: make(map[string]uintptr)}
	purego.RegisterLibFunc(&api.createDictionary, cf, "CFDictionaryCreateMutable")
	purego.RegisterLibFunc(&api.setDictionary, cf, "CFDictionarySetValue")
	purego.RegisterLibFunc(&api.getDictionary, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&api.createString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&api.getType, cf, "CFGetTypeID")
	purego.RegisterLibFunc(&api.arrayType, cf, "CFArrayGetTypeID")
	purego.RegisterLibFunc(&api.dictionaryType, cf, "CFDictionaryGetTypeID")
	purego.RegisterLibFunc(&api.stringType, cf, "CFStringGetTypeID")
	purego.RegisterLibFunc(&api.dataType, cf, "CFDataGetTypeID")
	purego.RegisterLibFunc(&api.arrayCount, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&api.arrayValue, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&api.dataLength, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&api.dataBytes, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&api.release, cf, "CFRelease")
	purego.RegisterLibFunc(&api.match, security, "SecItemCopyMatching")
	api.keyCallbacks, err = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
	if err != nil {
		return result
	}
	api.valueCallbacks, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
	if err != nil {
		return result
	}
	var copyPointer func(*uintptr, uintptr, uintptr) uintptr
	purego.RegisterLibFunc(&copyPointer, system, "memcpy")
	for _, name := range []string{
		"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount", "kSecReturnAttributes",
		"kSecMatchLimit", "kSecMatchLimitAll", "kSecMatchLimitOne", "kSecReturnData", "kSecUseAuthenticationUI",
		"kSecUseAuthenticationUIAllow", "kCFBooleanTrue",
	} {
		library := security
		if name == "kCFBooleanTrue" {
			library = cf
		}
		address, err := purego.Dlsym(library, name)
		if err != nil {
			return result
		}
		var reference uintptr
		copyPointer(&reference, address, unsafe.Sizeof(reference))
		if reference == 0 {
			return result
		}
		api.constants[name] = reference
	}
	return keychainAPIResult{api: api}
}

func readKeychainSecret() ([]byte, error) {
	native := nativeKeychain()
	if native.err != nil {
		return nil, native.err
	}
	api := native.api
	service := api.createString(0, "Claude Safe Storage", 0x08000100)
	if service == 0 {
		return nil, ErrKeychain
	}
	defer api.release(service)
	attributesQuery := api.newQuery(service)
	if attributesQuery == 0 {
		return nil, ErrKeychain
	}
	defer api.release(attributesQuery)
	api.put(attributesQuery, "kSecReturnAttributes", "kCFBooleanTrue")
	api.put(attributesQuery, "kSecMatchLimit", "kSecMatchLimitAll")
	var attributes uintptr
	status := api.match(attributesQuery, &attributes)
	if attributes != 0 {
		defer api.release(attributes)
	}
	if status != 0 || attributes == 0 || api.getType(attributes) != api.arrayType() ||
		api.arrayCount(attributes) != 1 {
		return nil, ErrKeychain
	}
	item := api.arrayValue(attributes, 0)
	if item == 0 || api.getType(item) != api.dictionaryType() {
		return nil, ErrKeychain
	}
	account := api.getDictionary(item, api.constants["kSecAttrAccount"])
	if account == 0 || api.getType(account) != api.stringType() {
		return nil, ErrKeychain
	}
	query := api.newQuery(service)
	if query == 0 {
		return nil, ErrKeychain
	}
	defer api.release(query)
	api.setDictionary(query, api.constants["kSecAttrAccount"], account)
	api.put(query, "kSecReturnData", "kCFBooleanTrue")
	api.put(query, "kSecMatchLimit", "kSecMatchLimitOne")
	var secret uintptr
	status = api.match(query, &secret)
	if secret != 0 {
		defer api.release(secret)
	}
	if status != 0 || secret == 0 || api.getType(secret) != api.dataType() {
		return nil, ErrKeychain
	}
	length := api.dataLength(secret)
	if length <= 0 || length > 4096 {
		return nil, ErrKeychain
	}
	address := api.dataBytes(secret)
	if address == nil {
		return nil, ErrKeychain
	}
	result := append([]byte(nil), unsafe.Slice((*byte)(address), int(length))...)
	runtime.KeepAlive(secret)
	return result, nil
}

func (api *keychainAPI) newQuery(service uintptr) uintptr {
	query := api.createDictionary(0, 0, api.keyCallbacks, api.valueCallbacks)
	if query == 0 {
		return 0
	}
	api.put(query, "kSecClass", "kSecClassGenericPassword")
	api.setDictionary(query, api.constants["kSecAttrService"], service)
	api.put(query, "kSecUseAuthenticationUI", "kSecUseAuthenticationUIAllow")
	return query
}

func (api *keychainAPI) put(query uintptr, key, value string) {
	api.setDictionary(query, api.constants[key], api.constants[value])
}
