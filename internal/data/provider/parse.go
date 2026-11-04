// Package provider implements the pinned Volcano/HAMi read-only adapter.
package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

const NodeRegister = "volcano.sh/node-vgpu-register"
const NodeHandshake = "volcano.sh/node-vgpu-handshake"
const AssignedIDs = "volcano.sh/vgpu-ids-new"

var physicalUUID = regexp.MustCompile(`^GPU-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type RegisteredDevice struct {
	UUID, Model  string
	SplitCount   uint32
	MemoryBlocks int64
	Healthy      bool
}
type Assignment struct {
	UUID         string
	MemoryBlocks int64
	CorePercent  uint32
}

func ParseNodeDevices(value string) ([]RegisteredDevice, error) {
	if len(value) == 0 || len(value) > 1<<20 || !strings.HasSuffix(value, ":") {
		return nil, biz.Invalid("PROVIDER_NODE_FORMAT")
	}
	var result []RegisteredDevice
	seen := map[string]bool{}
	for _, part := range strings.Split(strings.TrimSuffix(value, ":"), ":") {
		f := strings.Split(part, ",")
		if len(f) != 6 || !physicalUUID.MatchString(f[0]) || seen[f[0]] || !strings.HasPrefix(f[3], "NVIDIA-") || f[5] != "hami-core" {
			return nil, biz.Invalid("PROVIDER_DEVICE_IDENTITY")
		}
		count, e1 := strconv.ParseUint(f[1], 10, 31)
		memory, e2 := strconv.ParseInt(f[2], 10, 32)
		healthy, e3 := strconv.ParseBool(f[4])
		if e1 != nil || e2 != nil || e3 != nil || count == 0 || memory <= 0 {
			return nil, biz.Invalid("PROVIDER_NODE_QUANTITY")
		}
		seen[f[0]] = true
		result = append(result, RegisteredDevice{f[0], f[3], uint32(count), memory, healthy})
	}
	return result, nil
}

// Annotation container entries correspond to GPU-requesting containers in
// spec order, not every sidecar. Physical UUIDs are never derived from index.
func ParseAssignments(value string) ([][]Assignment, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 1<<20 {
		return nil, biz.Invalid("PROVIDER_ASSIGNMENT_FORMAT")
	}
	var result [][]Assignment
	for _, container := range strings.Split(value, ";") {
		if container == "" {
			result = append(result, nil)
			continue
		}
		if !strings.HasSuffix(container, ":") {
			return nil, biz.Invalid("PROVIDER_ASSIGNMENT_FORMAT")
		}
		var ds []Assignment
		seen := map[string]bool{}
		for _, part := range strings.Split(strings.TrimSuffix(container, ":"), ":") {
			f := strings.Split(part, ",")
			if len(f) != 4 || !physicalUUID.MatchString(f[0]) || seen[f[0]] || f[1] != "NVIDIA" {
				return nil, biz.Invalid("PROVIDER_DEVICE_IDENTITY")
			}
			m, e1 := strconv.ParseInt(f[2], 10, 32)
			cores, e2 := strconv.ParseUint(f[3], 10, 32)
			if e1 != nil || e2 != nil || m <= 0 || cores > 100 {
				return nil, biz.Invalid("PROVIDER_ASSIGNMENT_QUANTITY")
			}
			seen[f[0]] = true
			ds = append(ds, Assignment{f[0], m, uint32(cores)})
		}
		result = append(result, ds)
	}
	return result, nil
}
func ParseHandshake(s string) (time.Time, error) {
	if !strings.HasPrefix(s, "Reported ") {
		return time.Time{}, biz.Invalid("PROVIDER_TIME_MISSING")
	}
	s = strings.TrimPrefix(s, "Reported ")
	s, _, _ = strings.Cut(s, " m=")
	t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", s)
	if err != nil {
		return time.Time{}, biz.Invalid("PROVIDER_TIME_INVALID")
	}
	return t.UTC(), nil
}
func PhysicalID(cluster, node, uuid string) string {
	// Deterministic UUIDv8 bound to cluster, Node UID and actual NVIDIA UUID.
	h := sha256.Sum256([]byte("acc-physical-v1\x00" + cluster + "\x00" + node + "\x00" + uuid))
	h[6] = (h[6] & 15) | 128
	h[8] = (h[8] & 63) | 128
	s := hex.EncodeToString(h[:16])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
