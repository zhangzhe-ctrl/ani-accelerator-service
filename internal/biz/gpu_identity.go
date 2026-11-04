package biz

import "strings"

// NormalizeGPUUUID aligns metric selectors without changing inventory identity.
func NormalizeGPUUUID(v string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "gpu-")
}
