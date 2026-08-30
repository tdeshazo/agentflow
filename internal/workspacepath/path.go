// Package workspacepath provides portable lexical validation for authored
// paths and glob patterns that must remain within a workflow workspace.
package workspacepath

import (
	"path"
	"strings"
)

// Clean trims and normalizes a workspace-relative path or glob pattern. The
// check treats both slash styles as separators so a workflow validated on one
// operating system cannot become an escaping path on another.
func Clean(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return "", false
	}

	value = strings.ReplaceAll(value, `\`, "/")
	if path.IsAbs(value) || hasWindowsDrivePrefix(value) {
		return "", false
	}

	cleaned := path.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}
