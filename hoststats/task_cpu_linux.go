//go:build linux

package hoststats

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func ReadTaskCPUSec(tid int) (float64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/schedstat", tid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 1 {
		return 0, fmt.Errorf("schedstat parse failed for tid %d", tid)
	}
	ns, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return float64(ns) / float64(time.Second), nil
}
