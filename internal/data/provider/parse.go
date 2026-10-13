// Package provider implements the pinned Volcano/HAMi read-only adapter.
package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

const NodeRegister = "volcano.sh/node-vgpu-register"
const NodeHandshake = "volcano.sh/node-vgpu-handshake"
const AssignedIDs = "volcano.sh/vgpu-ids-new"
const AssignedTime = "volcano.sh/vgpu-time"

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
func ParseAssignedTime(s string) (time.Time, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	// Volcano writes time.Now().Unix(), including both reviewed scheduler
	// releases. Reject nanosecond-shaped values instead of guessing the unit.
	if err != nil || n <= 0 || n > 253402300799 || strconv.FormatInt(n, 10) != s {
		return time.Time{}, biz.Invalid("PROVIDER_TIME_INVALID")
	}
	return time.Unix(n, 0).UTC(), nil
}
func PhysicalID(cluster, node, uuid string) string {
	// Deterministic UUIDv8 bound to cluster, Node UID and actual NVIDIA UUID.
	h := sha256.Sum256([]byte("acc-physical-v1\x00" + cluster + "\x00" + node + "\x00" + uuid))
	h[6] = (h[6] & 15) | 128
	h[8] = (h[8] & 63) | 128
	s := hex.EncodeToString(h[:16])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

type RuntimeAllocation struct {
	UUID        string
	MemoryMiB   int64
	CorePercent uint32
}

// ParseContainerdInfo consumes ContainerStatus(verbose=true).Info["info"]
// from the supported containerd CRI implementation, specifically the OCI
// runtimeSpec.process.env passed to the container. It is not GPU utilization.
func ParseContainerdInfo(raw string) (RuntimeAllocation, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return RuntimeAllocation{}, biz.Invalid("RUNTIME_INFO_UNAVAILABLE")
	}
	var info struct {
		RuntimeSpec struct {
			Process struct {
				Env []string `json:"env"`
			} `json:"process"`
		} `json:"runtimeSpec"`
	}
	if json.Unmarshal([]byte(raw), &info) != nil {
		return RuntimeAllocation{}, biz.Invalid("RUNTIME_INFO_INVALID")
	}
	env := map[string]string{}
	for _, entry := range info.RuntimeSpec.Process.Env {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if k == "NVIDIA_VISIBLE_DEVICES" || strings.HasPrefix(k, "CUDA_DEVICE_MEMORY_LIMIT_") || k == "CUDA_DEVICE_SM_LIMIT" {
			if _, exists := env[k]; exists {
				return RuntimeAllocation{}, biz.Invalid("RUNTIME_DUPLICATE_FIELD")
			}
			env[k] = v
		}
	}
	id := env["NVIDIA_VISIBLE_DEVICES"]
	if !physicalUUID.MatchString(id) {
		return RuntimeAllocation{}, biz.Invalid("PROVIDER_DEVICE_IDENTITY")
	}
	for k := range env {
		if strings.HasPrefix(k, "CUDA_DEVICE_MEMORY_LIMIT_") && k != "CUDA_DEVICE_MEMORY_LIMIT_0" {
			return RuntimeAllocation{}, biz.Invalid("SHAPE_UNSUPPORTED")
		}
	}
	text := env["CUDA_DEVICE_MEMORY_LIMIT_0"]
	if !strings.HasSuffix(text, "m") {
		return RuntimeAllocation{}, biz.Invalid("MEMORY_UNIT_UNVERIFIED")
	}
	m, e1 := strconv.ParseInt(strings.TrimSuffix(text, "m"), 10, 64)
	cores, e2 := strconv.ParseUint(env["CUDA_DEVICE_SM_LIMIT"], 10, 32)
	if e1 != nil || e2 != nil || m <= 0 || m > math.MaxInt32 || cores > 100 {
		return RuntimeAllocation{}, biz.Invalid("RUNTIME_LIMIT_INVALID")
	}
	return RuntimeAllocation{id, m, uint32(cores)}, nil
}
