package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseStartRequestFormIncludesVhost(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("uid=testvm&tap=tap0&vhost=true&nat=false&snapshot=/tmp/test.snap"))
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
	if got := payload["snapshot"]; got != "/tmp/test.snap" {
		t.Fatalf("payload[snapshot] = %#v, want %q", got, "/tmp/test.snap")
	}
}

func TestParseStartRequestFormBuildsCOWBlockSpec(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("cow_base=/var/lib/base.img&cow_overlay=/var/lib/vm.overlay&block=/data/seed.img:ro"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, payload, err := parseStartRequest(req)
	if err != nil {
		t.Fatalf("parseStartRequest returned error: %v", err)
	}
	blocks, ok := payload["block"].([]string)
	if !ok {
		t.Fatalf("payload[block] type = %T, want []string", payload["block"])
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if blocks[0] != "/data/seed.img:ro" {
		t.Fatalf("blocks[0] = %q, want %q", blocks[0], "/data/seed.img:ro")
	}
	if want := "cow:/var/lib/base.img?overlay=%2Fvar%2Flib%2Fvm.overlay"; blocks[1] != want {
		t.Fatalf("blocks[1] = %q, want %q", blocks[1], want)
	}
}

func TestParseStartRequestFormPreservesSelectedBaseDisk(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("uid=testvm&base_disk=/var/lib/kube-vm/base-disks/ubuntu.img&block=/data/seed.img:ro"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, payload, err := parseStartRequest(req)
	if err != nil {
		t.Fatalf("parseStartRequest returned error: %v", err)
	}
	if got := payload["base_disk"]; got != "/var/lib/kube-vm/base-disks/ubuntu.img" {
		t.Fatalf("payload[base_disk] = %#v, want %q", got, "/var/lib/kube-vm/base-disks/ubuntu.img")
	}
}

func TestParseStartRequestFormIncludesSerialConsole(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("uid=testvm&serial_console=on"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, payload, err := parseStartRequest(req)
	if err != nil {
		t.Fatalf("parseStartRequest returned error: %v", err)
	}
	if got := payload["serial_console"]; got != "on" {
		t.Fatalf("payload[serial_console] = %#v, want %q", got, "on")
	}
}

func TestAuthorizedAcceptsBearerAndHeaderToken(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("uid=testvm"))
	req.Header.Set("Authorization", "Bearer secret-token")
	if !authorized(req, "secret-token") {
		t.Fatalf("authorized with bearer token returned false")
	}

	req2 := httptest.NewRequest("POST", "/api/v1/vms/start", strings.NewReader("uid=testvm"))
	req2.Header.Set("X-Auth-Token", "  secret-token  ")
	if !authorized(req2, "secret-token") {
		t.Fatalf("authorized with raw token header returned false")
	}
}

func TestConsoleReadOnlyPollRequestAllowsNoAuth(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/console/poll?id=session123&offset=0&console_mode=ro", nil)
	if !consoleReadOnlyPollRequest(req) {
		t.Fatal("consoleReadOnlyPollRequest returned false for read-only poll")
	}

	req2 := httptest.NewRequest("GET", "/api/v1/console/poll?id=session123&offset=0&console_mode=rw", nil)
	if consoleReadOnlyPollRequest(req2) {
		t.Fatal("consoleReadOnlyPollRequest returned true for read-write poll")
	}
}
