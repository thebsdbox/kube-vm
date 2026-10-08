package hoststats

import (
	"runtime"
	"time"
)

// DaemonSnapshot captures process-level runtime metrics for the kube-vm daemon.
type DaemonSnapshot struct {
	State          string
	StartedAt      string
	UptimeSec      int64
	VMTotal        int
	GoRoutines     int
	HeapAllocBytes uint64
	HeapObjects    uint64
}

func CaptureDaemonSnapshot(state string, startedAt time.Time, vmTotal int) DaemonSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return DaemonSnapshot{
		State:          state,
		StartedAt:      startedAt.Format(time.RFC3339),
		UptimeSec:      int64(time.Since(startedAt).Seconds()),
		VMTotal:        vmTotal,
		GoRoutines:     runtime.NumGoroutine(),
		HeapAllocBytes: ms.HeapAlloc,
		HeapObjects:    ms.HeapObjects,
	}
}
