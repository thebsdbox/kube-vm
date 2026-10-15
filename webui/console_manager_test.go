package webui

import (
	"testing"
)

func TestConsoleSessionPollUsesOffsetsWithoutDraining(t *testing.T) {
	s := &consoleSession{}
	s.append([]byte("abc"))

	chunk, closed, nextOffset := s.poll(0)
	if closed {
		t.Fatal("closed = true, want false")
	}
	if string(chunk) != "abc" {
		t.Fatalf("first poll = %q, want %q", string(chunk), "abc")
	}
	if nextOffset != 3 {
		t.Fatalf("nextOffset = %d, want 3", nextOffset)
	}

	chunk, closed, nextOffset = s.poll(1)
	if closed {
		t.Fatal("closed = true, want false")
	}
	if string(chunk) != "bc" {
		t.Fatalf("second poll = %q, want %q", string(chunk), "bc")
	}
	if nextOffset != 3 {
		t.Fatalf("nextOffset = %d, want 3", nextOffset)
	}

	s.append([]byte("de"))
	chunk, closed, nextOffset = s.poll(3)
	if closed {
		t.Fatal("closed = true, want false")
	}
	if string(chunk) != "de" {
		t.Fatalf("third poll = %q, want %q", string(chunk), "de")
	}
	if nextOffset != 5 {
		t.Fatalf("nextOffset = %d, want 5", nextOffset)
	}
}
