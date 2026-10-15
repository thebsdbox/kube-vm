package main

import (
	"reflect"
	"testing"
)

func TestStandaloneArgsIgnoresLeadingStartVerb(t *testing.T) {
	got := standaloneArgs([]string{"start", "--mem", "2048", "--cpu", "4"})
	want := []string{"--mem", "2048", "--cpu", "4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("standaloneArgs() = %#v, want %#v", got, want)
	}
}

func TestStandaloneArgsLeavesFlagsAlone(t *testing.T) {
	got := standaloneArgs([]string{"--mem", "2048", "--cpu", "4"})
	want := []string{"--mem", "2048", "--cpu", "4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("standaloneArgs() = %#v, want %#v", got, want)
	}
}
