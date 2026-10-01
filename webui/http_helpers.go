package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	copyFormValue(payload, r, "tap")
	copyFormValue(payload, r, "nat")
	copyFormValue(payload, r, "mem")
	copyFormValue(payload, r, "cpu")

	blocksRaw := strings.TrimSpace(r.FormValue("block"))
	if blocksRaw != "" {
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
		if len(blocks) > 0 {
			payload["block"] = blocks
		}
	}
	return uid, payload, nil
}

func copyFormValue(payload map[string]any, r *http.Request, key string) {
	if val := strings.TrimSpace(r.FormValue(key)); val != "" {
		payload[key] = val
	}
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
	if strings.TrimSpace(r.Header.Get("X-Auth-Token")) == token {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		if strings.TrimSpace(auth[7:]) == token {
			return true
		}
	}
	if strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true
	}
	return false
}

func randID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
