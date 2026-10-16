package virtq_test

import (
	"reflect"
	"testing"

	"kube-vm/virtio/virtq"
)

func TestQueueRestoreLeavesGuestMemoryUntouched(t *testing.T) {
	ring := []virtq.Desc{
		{Addr: 1234, ID: 42, Len: 512, Flags: virtq.DescFAvail},
		{Addr: 5678, ID: 43, Len: 256, Flags: virtq.DescFUsed},
	}
	want := append([]virtq.Desc(nil), ring...)
	driver := virtq.EventSuppress{Desc: 1, Flags: 2}
	device := virtq.EventSuppress{Desc: 0x8001, Flags: 1}
	q := virtq.New(ring, &driver, &device, virtq.Config{})
	if err := q.RestoreState(virtq.QueueState{AvailIndex: 1, UsedIndex: 1}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ring, want) || driver != (virtq.EventSuppress{Desc: 1, Flags: 2}) || device != (virtq.EventSuppress{Desc: 0x8001, Flags: 1}) {
		t.Fatal("restoring host cursors modified restored guest memory")
	}
}