//go:build !darwin

package desktopauth

const platformSupported = false

func readKeychainSecret() ([]byte, error) {
	return nil, ErrUnsupported
}
