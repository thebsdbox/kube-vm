package main

import (
	"fmt"
	"sort"
	"strings"

	"kube-vm/control"
)

// resolveVMUID gives exact identifiers priority over unique prefix matches.
func resolveVMUID(socket, prefix string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("uid is required")
	}
	resp, err := controlDial(socket, "list", nil)
	if err != nil {
		return "", fmt.Errorf("list VMs: %w", err)
	}
	if !resp.OK {
		if resp.Error != "" {
			return "", fmt.Errorf("list VMs: %s", resp.Error)
		}
		return "", fmt.Errorf("control request to list VMs failed")
	}
	var matches []string
	for _, vm := range resp.VMs {
		if vm.UID == prefix {
			return vm.UID, nil
		}
		if strings.HasPrefix(vm.UID, prefix) {
			matches = append(matches, vm.UID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no VM matches UID or prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("ambiguous VM UID prefix %q: matches %s; use more characters", prefix, strings.Join(matches, ", "))
	}
}

// dialVM resolves targets only for operations on existing VMs. Creation and
// template commands continue using controlDial directly. Empty targets retain
// their existing semantics (notably daemon-wide status).
func dialVM(socket, action string, payload map[string]any) (control.Response, error) {
	prefix, _ := payload["uid"].(string)
	if prefix == "" {
		return controlDial(socket, action, payload)
	}
	resolved, err := resolveVMUID(socket, prefix)
	if err != nil {
		return control.Response{}, err
	}
	request := make(map[string]any, len(payload))
	for key, value := range payload {
		request[key] = value
	}
	request["uid"] = resolved
	resp, err := controlDial(socket, action, request)
	if err == nil && resp.OK && resp.UID == "" {
		resp.UID = resolved
	}
	return resp, err
}