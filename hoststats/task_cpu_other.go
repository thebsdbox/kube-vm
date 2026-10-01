//go:build !linux

package hoststats

import "fmt"

func ReadTaskCPUSec(tid int) (float64, error) {
	return 0, fmt.Errorf("host task CPU metrics are only supported on linux")
}
