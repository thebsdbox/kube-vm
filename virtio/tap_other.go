//go:build !linux

package virtio

import (
	"errors"
	"os"
)

func OpenTAP(name string) (*os.File, string, error) {
	return nil, "", errors.New("tap devices are only supported on linux")
}
