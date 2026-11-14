//go:build darwin

package desktopauth

import "testing"

// TestKeychainFrameworkBindings builds a query but never asks the user's keychain for an item.
func TestKeychainFrameworkBindings(t *testing.T) {
	t.Parallel()
	native := nativeKeychain()
	if native.err != nil {
		t.Fatal("could not load system security framework bindings")
	}
	api := native.api
	service := api.createString(0, "Fixture service - never queried", 0x08000100)
	if service == 0 {
		t.Fatal("could not create fixture string")
	}
	defer api.release(service)
	query := api.newQuery(service)
	if query == 0 || api.getType(query) != api.dictionaryType() {
		t.Fatal("could not create fixture dictionary")
	}
	defer api.release(query)
	if api.getDictionary(query, api.constants["kSecAttrService"]) != service ||
		api.getDictionary(query, api.constants["kSecClass"]) != api.constants["kSecClassGenericPassword"] {
		t.Fatal("query bindings returned inconsistent values")
	}
}
