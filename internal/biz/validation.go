package biz

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Error is transport independent. Details are static and must never contain
// credentials, raw provider responses, or another tenant's identity.
type Error struct {
	Kind   string
	Reason string
}

func (e *Error) Error() string         { return e.Reason }
func Invalid(reason string) error      { return &Error{"invalid", reason} }
func Precondition(reason string) error { return &Error{"precondition", reason} }
func Conflict(reason string) error     { return &Error{"conflict", reason} }
func Denied() error                    { return &Error{"denied", "PERMISSION_DENIED"} }
func NotFound() error                  { return &Error{"not_found", "NOT_FOUND"} }
func Unavailable() error               { return &Error{"unavailable", "DEPENDENCY_UNAVAILABLE"} }

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var namePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var labelPattern = regexp.MustCompile(`^[a-zA-Z0-9]([-_.a-zA-Z0-9]*[a-zA-Z0-9])?$`)
var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}$`)

func UUID(s string) bool   { return uuidPattern.MatchString(s) }
func Digest(s string) bool { return digestPattern.MatchString(s) }
func Name(s string) bool   { return len(s) > 0 && len(s) <= 63 && namePattern.MatchString(s) }
func Label(s string) bool  { return len(s) > 0 && len(s) <= 63 && labelPattern.MatchString(s) }
func Ref(s string) bool {
	return refPattern.MatchString(s) && !strings.Contains(s, "://") && !strings.Contains(s, "..")
}
func Text(s string, max int) bool {
	return utf8.ValidString(s) && len(s) > 0 && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system random source unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func Add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, Invalid("INTEGER_OVERFLOW")
	}
	return a + b, nil
}
func Multiply(a, b int64) (int64, error) {
	if a < 0 || b < 0 || (b != 0 && a > math.MaxInt64/b) {
		return 0, Invalid("INTEGER_OVERFLOW")
	}
	return a * b, nil
}
func ValidTime(s string) bool {
	t, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && t.Year() >= 1970 && t.Format(time.RFC3339Nano) == s && strings.HasSuffix(s, "Z")
}
func ValidateRef(r *UsageRef) error {
	if r == nil || !UUID(r.TenantID) || !UUID(r.ResourceID) || !UUID(r.CreateOperationID) {
		return Invalid("INVALID_USAGE_REF")
	}
	if r.OwnerService != "ani-inference" && r.OwnerService != "ani-resource-service" {
		return Invalid("OWNER_UNSUPPORTED")
	}
	return nil
}
func ValidatePod(p PodRef) error {
	if !UUID(p.ClusterID) || !Name(p.Namespace) || !Text(p.Name, 253) || !Ref(p.UID) || !Name(p.ContainerName) {
		return Invalid("INVALID_POD_REF")
	}
	return nil
}
func ValidateActor(a *Actor) error {
	if a == nil || !Ref(a.Type) || !Ref(a.ID) {
		return Invalid("INVALID_ACTOR")
	}
	return nil
}
func ValidateBaseline(b *ProviderBaseline, mode SupplyMode) error {
	if b == nil || b.MemoryBlockMiB == 0 || b.MemoryBlockMiB > math.MaxInt32 {
		return Precondition("MEMORY_UNIT_UNVERIFIED")
	}
	if mode != Whole && mode != Shared {
		return Invalid("SHAPE_UNSUPPORTED")
	}
	// These are reviewed source contracts, not hardware verification. Scheduler
	// and device-plugin releases are independent; unknown combinations stay closed.
	if !UUID(b.BaselineID) || b.AdapterID != "volcano-hami-factor-v1" || (b.VolcanoVersion != "v1.12.0" && b.VolcanoVersion != "v1.15.2") || b.DevicePluginVersion != "v1.12.0" || b.RecipeVersion != "volcano-hami-v1" || b.MemoryEncoding != "VOLCANO_BLOCKS_TO_MIB" {
		return Precondition("BASELINE_UNSUPPORTED")
	}
	if !imageDigest(b.VolcanoImageDigest) || !imageDigest(b.DevicePluginImageDigest) || !Ref(b.HamiCoreRevision) || !Ref(b.FactorEvidenceRef) || !Digest(b.NodeConfigDigest) || !Digest(b.SchedulerConfigDigest) {
		return Precondition("BASELINE_INCOMPLETE")
	}
	if b.MemoryScalePercent != 100 || b.CoreScalePercent != 100 || b.SplitCount == 0 || b.SplitCount > math.MaxInt32 || (mode == Whole && b.SplitCount != 1) || (mode == Shared && b.SplitCount < 2) {
		return Precondition("CONFIG_DRIFT")
	}
	d, err := BaselineDigest(b)
	if err != nil {
		return err
	}
	if b.BaselineDigest != d {
		return Invalid("BASELINE_DIGEST_MISMATCH")
	}
	return nil
}
func imageDigest(s string) bool {
	p := strings.LastIndex(s, "@sha256:")
	if p >= 0 {
		return Ref(s[:p]) && Digest(s[p+8:])
	}
	return strings.HasPrefix(s, "sha256:") && Digest(strings.TrimPrefix(s, "sha256:"))
}
func ValidateSpec(s *ProfileSpec, b *ProviderBaseline) error {
	if s == nil || !UUID(s.GroupID) || !Label(s.ModelKey) || s.MaxDevicesPerReplica < 1 || s.MaxDevicesPerReplica > 2 {
		return Invalid("SHAPE_UNSUPPORTED")
	}
	if err := ValidateBaseline(b, s.Mode); err != nil {
		return err
	}
	switch s.Mode {
	case Whole:
		if s.SharedMemoryMiB != 0 || s.CoreLimitPercent != 100 || s.IsolationClass != "WHOLE_DEVICE_EXCLUSIVE" {
			return Invalid("SHAPE_UNSUPPORTED")
		}
	case Shared:
		if s.MaxDevicesPerReplica != 1 || s.SharedMemoryMiB <= 0 || s.CoreLimitPercent < 1 || s.CoreLimitPercent > 99 || s.IsolationClass != "SOFTWARE_COOPERATIVE" {
			return Invalid("SHAPE_UNSUPPORTED")
		}
		if _, err := EncodeMemory(s.SharedMemoryMiB, b.MemoryBlockMiB); err != nil {
			return err
		}
	default:
		return Invalid("SHAPE_UNSUPPORTED")
	}
	return nil
}
func EncodeMemory(m int64, f uint32) (int64, error) {
	if f == 0 || f > math.MaxInt32 {
		return 0, Precondition("MEMORY_UNIT_UNVERIFIED")
	}
	if m <= 0 {
		return 0, Invalid("INVALID_MEMORY")
	}
	if m%int64(f) != 0 {
		return 0, Invalid("MEMORY_GRANULARITY_MISMATCH")
	}
	q := m / int64(f)
	if q > math.MaxInt32 || m > math.MaxInt32 {
		return 0, Invalid("PROVIDER_INTEGER_OVERFLOW")
	}
	return q, nil
}

// NormalizeMemory accepts an original source value, never a previously
// normalized observation. The factor belongs to that observation's baseline.
func NormalizeMemory(n int64, unit string, f uint32) (int64, error) {
	if n < 0 {
		return 0, Invalid("INVALID_MEMORY")
	}
	switch unit {
	case "BLOCKS":
		if f == 0 || f > math.MaxInt32 || n > math.MaxInt32 {
			return 0, Precondition("MEMORY_UNIT_UNVERIFIED")
		}
		return Multiply(n, int64(f))
	case "MIB":
		return n, nil
	case "BYTES":
		if n%(1<<20) != 0 {
			return 0, Invalid("MEMORY_GRANULARITY_MISMATCH")
		}
		return n / (1 << 20), nil
	default:
		return 0, Precondition("MEMORY_UNIT_UNVERIFIED")
	}
}
func QuantizedBlocks(bytes []uint64, f uint32) (int64, error) {
	if f == 0 || f > math.MaxInt32 {
		return 0, Precondition("MEMORY_UNIT_UNVERIFIED")
	}
	var sum int64
	for _, n := range bytes {
		if n > math.MaxInt64 {
			return 0, Invalid("INTEGER_OVERFLOW")
		}
		var err error
		sum, err = Add(sum, int64(n/(1<<20))/int64(f))
		if err != nil {
			return 0, err
		}
	}
	return sum, nil
}
func ValidateRequest(r *GpuRequest, maxReplicas uint32) error {
	if r == nil || !UUID(r.ClusterID) || !UUID(r.PoolID) || !UUID(r.ProfileID) || r.ProfileVersion != 1 {
		return Invalid("INVALID_GPU_REQUEST")
	}
	if maxReplicas == 0 || maxReplicas > 256 || r.Replicas == 0 || r.Replicas > maxReplicas || r.DevicesPerReplica < 1 || r.DevicesPerReplica > 2 || !Name(r.ContainerName) {
		return Invalid("SHAPE_UNSUPPORTED")
	}
	return nil
}
func reasonf(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Reason
	}
	return fmt.Sprint("DEPENDENCY_UNAVAILABLE")
}
