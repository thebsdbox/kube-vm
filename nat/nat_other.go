//go:build !linux

package nat

import (
	"fmt"
	"io"
)

type Config struct {
	TapName    string
	SubnetCIDR string
	GatewayIP  string
	GuestIP    string
}

func OpenBackend(uid string) (io.ReadWriteCloser, Config, error) {
	return nil, Config{}, fmt.Errorf("nat networking is only supported on linux")
}
