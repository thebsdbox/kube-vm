package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseStartRequestFormIncludesVhost(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("uid=testvm&tap=tap0&vhost=true&nat=false"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	uid, payload, err := parseStartRequest(req)
	if err != nil {
		t.Fatalf("parseStartRequest returned error: %v", err)
	}
	if uid != "testvm" {
		t.Fatalf("uid = %q, want %q", uid, "testvm")
	}
	if got := payload["vhost"]; got != "true" {
		t.Fatalf("payload[vhost] = %#v, want %q", got, "true")
	}
	if got := payload["tap"]; got != "tap0" {
		t.Fatalf("payload[tap] = %#v, want %q", got, "tap0")
	}
}