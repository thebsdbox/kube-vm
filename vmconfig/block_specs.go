package vmconfig

import (
	"fmt"
	"net/url"
	"strings"
)

// EncodeCOWBlockSpec builds the canonical block spec for a copy-on-write disk.
func EncodeCOWBlockSpec(basePath, overlayPath string) (string, error) {
	basePath = strings.TrimSpace(basePath)
	overlayPath = strings.TrimSpace(overlayPath)
	if basePath == "" {
		return "", fmt.Errorf("kube-vm: COW base path is required")
	}
	if overlayPath == "" {
		return "", fmt.Errorf("kube-vm: COW overlay path is required")
	}
	q := url.Values{}
	q.Set("overlay", overlayPath)
	return "cow:" + basePath + "?" + q.Encode(), nil
}

// MergeBlockSpecs appends canonical COW block specs generated from BASE=OVERLAY
// inputs onto an existing set of raw block specs.
func MergeBlockSpecs(blocks, cowBlocks []string) ([]string, error) {
	out := make([]string, 0, len(blocks)+len(cowBlocks))
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block != "" {
			out = append(out, block)
		}
	}
	for _, raw := range cowBlocks {
		base, overlay, ok := strings.Cut(strings.TrimSpace(raw), "=")
		if !ok || strings.TrimSpace(base) == "" || strings.TrimSpace(overlay) == "" {
			return nil, fmt.Errorf("invalid COW block %q; want BASE=OVERLAY", raw)
		}
		spec, err := EncodeCOWBlockSpec(base, overlay)
		if err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}
