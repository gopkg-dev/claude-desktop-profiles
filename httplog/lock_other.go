//go:build !unix

package httplog

import "os"

func lockDirectory(*os.Root) (func(), error) {
	return nil, ErrUnsupported
}
