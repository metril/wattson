//go:build !linux

package blue

import "errors"

// NewLocal is only available on Linux (BlueZ).
func NewLocal() (Transport, error) {
	return nil, errors.ErrUnsupported
}
