package main

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"kube-vm/control"
)

func TestTemplateConfigRoundTrip(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "kube-vm.sock")
	payload := map[string]any{"mem": 2048, "cpu": 2, "kernel": "bzImage", "tap": "tap0"}

	stored, err := saveTemplate(socketPath, "ubuntu-k8s", payload)
	if err != nil {
		t.Fatalf("saveTemplate returned error: %v", err)
	}
	if stored["name"] != "ubuntu-k8s" {
		t.Fatalf("stored name = %q, want %q", stored["name"], "ubuntu-k8s")
	}

	templates, err := listTemplates(socketPath)
	if err != nil {
		t.Fatalf("listTemplates returned error: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("len(templates) = %d, want 1", len(templates))
	}
	if templates[0]["name"] != "ubuntu-k8s" {
		t.Fatalf("template[0].name = %q, want %q", templates[0]["name"], "ubuntu-k8s")
	}

	if err := deleteTemplate(socketPath, "ubuntu-k8s"); err != nil {
		t.Fatalf("deleteTemplate returned error: %v", err)
	}
	if templates, err = listTemplates(socketPath); err != nil {
		t.Fatalf("listTemplates after delete returned error: %v", err)
	} else if len(templates) != 0 {
		t.Fatalf("len(templates) after delete = %d, want 0", len(templates))
	}
}

func TestResolveSSHTargetUsesIPAddressDirectly(t *testing.T) {
	got, err := resolveSSHTarget("/tmp/unused.sock", "10.0.2.15", "")
	if err != nil {
		t.Fatalf("resolveSSHTarget returned error: %v", err)
	}
	if got != "10.0.2.15" {
		t.Fatalf("resolveSSHTarget = %q, want %q", got, "10.0.2.15")
	}
}

func TestResolveSSHTargetResolvesUIDToGuestIP(t *testing.T) {
	oldDial := controlDial
	controlDial = func(socketPath, action string, payload map[string]any) (control.Response, error) {
		if socketPath != "/tmp/kube-vm.sock" {
			t.Fatalf("socketPath = %q, want %q", socketPath, "/tmp/kube-vm.sock")
		}
		if action != "examine" {
			t.Fatalf("action = %q, want %q", action, "examine")
		}
		if gotUID, _ := payload["uid"].(string); gotUID != "vm-123" {
			t.Fatalf("payload uid = %q, want %q", gotUID, "vm-123")
		}
		return control.Response{OK: true, VM: &control.VMDetails{NATGuestIP: "10.0.2.15"}}, nil
	}
	defer func() { controlDial = oldDial }()

	got, err := resolveSSHTarget("/tmp/kube-vm.sock", "vm-123", "root")
	if err != nil {
		t.Fatalf("resolveSSHTarget returned error: %v", err)
	}
	if got != "10.0.2.15" {
		t.Fatalf("resolveSSHTarget = %q, want %q", got, "10.0.2.15")
	}
}

func TestResolveSSHTargetRequiresGuestIPForUID(t *testing.T) {
	oldDial := controlDial
	controlDial = func(socketPath, action string, payload map[string]any) (control.Response, error) {
		return control.Response{OK: true, VM: &control.VMDetails{}}, nil
	}
	defer func() { controlDial = oldDial }()

	_, err := resolveSSHTarget("/tmp/kube-vm.sock", "vm-123", "")
	if err == nil || err.Error() != "vm vm-123 does not have a guest IP address" {
		t.Fatalf("resolveSSHTarget error = %v, want missing guest IP error", err)
	}
}

func TestSSHCommandBuildsInvocation(t *testing.T) {
	oldDial := controlDial
	oldRunner := sshCommandRunner
	controlDial = func(socketPath, action string, payload map[string]any) (control.Response, error) {
		return control.Response{OK: true, VM: &control.VMDetails{NATGuestIP: "10.0.2.15"}}, nil
	}
	var gotInvocation sshInvocation
	sshCommandRunner = func(invocation sshInvocation) error {
		gotInvocation = invocation
		return nil
	}
	defer func() {
		controlDial = oldDial
		sshCommandRunner = oldRunner
	}()

	cmd := newRootCmd()
	cmd.SetArgs([]string{"ssh", "-l", "root", "-p", "2222", "--insecure-ignore-host-key", "vm-123", "--", "echo", "ok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	want := sshInvocation{Host: "10.0.2.15", User: "root", Port: 2222, Command: []string{"echo", "ok"}, IgnoreHostKey: true}
	if !reflect.DeepEqual(gotInvocation, want) {
		t.Fatalf("ssh invocation = %#v, want %#v", gotInvocation, want)
	}
}

func TestShellQuoteArgs(t *testing.T) {
	got := shellQuoteArgs([]string{"echo", "hello world", "it's", "safe"})
	want := "echo 'hello world' 'it\"'\"'s' safe"
	if got != want {
		t.Fatalf("shellQuoteArgs = %q, want %q", got, want)
	}
}

func TestResolveSSHPasswordUsesEnvironment(t *testing.T) {
	oldEnv, hadEnv := os.LookupEnv("KUBEVM_SSH_PASSWORD")
	oldPrompter := sshPasswordPrompter
	if err := os.Setenv("KUBEVM_SSH_PASSWORD", "secret"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	sshPasswordPrompter = func(invocation sshInvocation) (string, error) {
		t.Fatal("password prompter should not be called when KUBEVM_SSH_PASSWORD is set")
		return "", nil
	}
	defer func() {
		sshPasswordPrompter = oldPrompter
		if hadEnv {
			_ = os.Setenv("KUBEVM_SSH_PASSWORD", oldEnv)
		} else {
			_ = os.Unsetenv("KUBEVM_SSH_PASSWORD")
		}
	}()

	password, err := resolveSSHPassword(sshInvocation{User: "root", Host: "10.0.2.15"})
	if err != nil {
		t.Fatalf("resolveSSHPassword returned error: %v", err)
	}
	if password != "secret" {
		t.Fatalf("resolveSSHPassword = %q, want %q", password, "secret")
	}
}

func TestResolveSSHPasswordUsesPrompter(t *testing.T) {
	oldEnv, hadEnv := os.LookupEnv("KUBEVM_SSH_PASSWORD")
	oldPrompter := sshPasswordPrompter
	if err := os.Unsetenv("KUBEVM_SSH_PASSWORD"); err != nil {
		t.Fatalf("Unsetenv returned error: %v", err)
	}
	sshPasswordPrompter = func(invocation sshInvocation) (string, error) {
		if invocation.User != "root" || invocation.Host != "10.0.2.15" {
			t.Fatalf("invocation = %#v, want root@10.0.2.15", invocation)
		}
		return "prompted", nil
	}
	defer func() {
		sshPasswordPrompter = oldPrompter
		if hadEnv {
			_ = os.Setenv("KUBEVM_SSH_PASSWORD", oldEnv)
		} else {
			_ = os.Unsetenv("KUBEVM_SSH_PASSWORD")
		}
	}()

	password, err := resolveSSHPassword(sshInvocation{User: "root", Host: "10.0.2.15"})
	if err != nil {
		t.Fatalf("resolveSSHPassword returned error: %v", err)
	}
	if password != "prompted" {
		t.Fatalf("resolveSSHPassword = %q, want %q", password, "prompted")
	}
}

func TestApproveUnknownSSHHostUsesEnvironment(t *testing.T) {
	oldEnv, hadEnv := os.LookupEnv("KUBEVM_SSH_ACCEPT_NEW_HOSTS")
	oldPrompter := sshHostKeyPrompter
	if err := os.Setenv("KUBEVM_SSH_ACCEPT_NEW_HOSTS", "true"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	sshHostKeyPrompter = func(hostname string, key ssh.PublicKey) (bool, error) {
		t.Fatal("host key prompter should not be called when KUBEVM_SSH_ACCEPT_NEW_HOSTS is set")
		return false, nil
	}
	defer func() {
		sshHostKeyPrompter = oldPrompter
		if hadEnv {
			_ = os.Setenv("KUBEVM_SSH_ACCEPT_NEW_HOSTS", oldEnv)
		} else {
			_ = os.Unsetenv("KUBEVM_SSH_ACCEPT_NEW_HOSTS")
		}
	}()

	signer := mustNewTestSigner(t)
	accepted, err := approveUnknownSSHHost("10.0.2.15:22", signer.PublicKey())
	if err != nil {
		t.Fatalf("approveUnknownSSHHost returned error: %v", err)
	}
	if !accepted {
		t.Fatal("approveUnknownSSHHost = false, want true")
	}
}

func TestAppendKnownHostCreatesReusableEntry(t *testing.T) {
	knownHostsPath := t.TempDir() + "/known_hosts"
	signer := mustNewTestSigner(t)
	if err := appendKnownHost(knownHostsPath, "10.0.2.15:22", signer.PublicKey()); err != nil {
		t.Fatalf("appendKnownHost returned error: %v", err)
	}

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		t.Fatalf("knownhosts.New returned error: %v", err)
	}
	if err := callback("10.0.2.15:22", &net.TCPAddr{IP: net.ParseIP("10.0.2.15"), Port: 22}, signer.PublicKey()); err != nil {
		t.Fatalf("known hosts callback returned error: %v", err)
	}
}

func TestShouldIgnoreSSHHostKeyUsesEnvironment(t *testing.T) {
	oldEnv, hadEnv := os.LookupEnv("KUBEVM_SSH_IGNORE_HOST_KEY")
	if err := os.Setenv("KUBEVM_SSH_IGNORE_HOST_KEY", "true"); err != nil {
		t.Fatalf("Setenv returned error: %v", err)
	}
	defer func() {
		if hadEnv {
			_ = os.Setenv("KUBEVM_SSH_IGNORE_HOST_KEY", oldEnv)
		} else {
			_ = os.Unsetenv("KUBEVM_SSH_IGNORE_HOST_KEY")
		}
	}()

	if !shouldIgnoreSSHHostKey() {
		t.Fatal("shouldIgnoreSSHHostKey = false, want true")
	}
}

func mustNewTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey returned error: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("ssh.NewSignerFromKey returned error: %v", err)
	}
	return signer
}
