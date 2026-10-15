package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kube-vm/vmconfig"
)

func parseStartRequest(r *http.Request) (string, map[string]any, error) {
	payload := map[string]any{}
	uid := ""

	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return "", nil, fmt.Errorf("invalid JSON payload: %w", err)
		}
		if v, ok := raw["uid"].(string); ok {
			uid = strings.TrimSpace(v)
		}
		for k, v := range raw {
			if k == "uid" {
				continue
			}
			payload[k] = v
		}
		if err := appendCOWBlockPayload(payload, asJSONString(raw["cow_base"]), asJSONString(raw["cow_overlay"])); err != nil {
			return "", nil, err
		}
		return uid, payload, nil
	}

	if err := r.ParseForm(); err != nil {
		return "", nil, fmt.Errorf("invalid form payload: %w", err)
	}
	uid = strings.TrimSpace(r.FormValue("uid"))
	copyFormValue(payload, r, "kernel")
	copyFormValue(payload, r, "initrd")
	copyFormValue(payload, r, "firmware")
	copyFormValue(payload, r, "iso")
	copyFormValue(payload, r, "cmdline")
	copyFormValue(payload, r, "serial_console")
	copyFormValue(payload, r, "tap")
	copyFormValue(payload, r, "nat")
	copyFormValue(payload, r, "vhost")
	copyFormValue(payload, r, "snapshot")
	copyFormValue(payload, r, "mem")
	copyFormValue(payload, r, "cpu")
	copyFormValue(payload, r, "base_disk")

	blocks := splitBlockSpecs(strings.TrimSpace(r.FormValue("block")))
	if err := appendCOWBlockSpec(&blocks, strings.TrimSpace(r.FormValue("cow_base")), strings.TrimSpace(r.FormValue("cow_overlay"))); err != nil {
		return "", nil, err
	}
	if len(blocks) > 0 {
		payload["block"] = blocks
	}
	return uid, payload, nil
}

func copyFormValue(payload map[string]any, r *http.Request, key string) {
	if val := strings.TrimSpace(r.FormValue(key)); val != "" {
		payload[key] = val
	}
}

func appendCOWBlockPayload(payload map[string]any, cowBase, cowOverlay string) error {
	blocks := coerceStringSlice(payload["block"])
	if err := appendCOWBlockSpec(&blocks, cowBase, cowOverlay); err != nil {
		return err
	}
	if len(blocks) > 0 {
		payload["block"] = blocks
	}
	delete(payload, "cow_base")
	delete(payload, "cow_overlay")
	return nil
}

func normalizeBaseDiskPayload(payload map[string]any) {
	if payload == nil {
		return
	}
	if v, ok := payload["base_disk"]; ok {
		if s, ok := v.(string); ok {
			payload["base_disk"] = strings.TrimSpace(s)
		}
	}
}

func appendCOWBlockSpec(blocks *[]string, cowBase, cowOverlay string) error {
	if cowBase == "" && cowOverlay == "" {
		return nil
	}
	if cowBase == "" || cowOverlay == "" {
		return fmt.Errorf("cow base and cow overlay must both be set")
	}
	spec, err := vmconfig.EncodeCOWBlockSpec(cowBase, cowOverlay)
	if err != nil {
		return err
	}
	*blocks = append(*blocks, spec)
	return nil
}

func splitBlockSpecs(blocksRaw string) []string {
	if strings.TrimSpace(blocksRaw) == "" {
		return nil
	}
	parts := strings.FieldsFunc(blocksRaw, func(r rune) bool {
		return r == '\n' || r == ','
	})
	blocks := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			blocks = append(blocks, p)
		}
	}
	return blocks
}

func coerceStringSlice(v any) []string {
	appendSplit := func(out []string, raw string) []string {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}

	switch val := v.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, 0, len(val))
		for _, s := range val {
			out = appendSplit(out, s)
		}
		return out
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			out = appendSplit(out, strings.TrimSpace(asJSONString(item)))
		}
		return out
	case string:
		return splitBlockSpecs(val)
	default:
		s := strings.TrimSpace(asJSONString(v))
		if s == "" {
			return nil
		}
		return appendSplit(nil, s)
	}
}

func asJSONString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return true
	}

	for _, candidate := range []string{
		strings.TrimSpace(r.Header.Get("X-Auth-Token")),
		strings.TrimSpace(r.Header.Get("Authorization")),
		strings.TrimSpace(r.URL.Query().Get("token")),
	} {
		if candidate == "" {
			continue
		}
		if candidate == token {
			return true
		}
		if strings.HasPrefix(strings.ToLower(candidate), "bearer ") {
			if strings.TrimSpace(candidate[7:]) == token {
				return true
			}
		}
	}
	return false
}

func consoleReadOnlyPollRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Path != "/api/v1/console/poll" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("console_mode")), "ro")
}

func randID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
