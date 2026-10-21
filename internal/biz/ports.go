package biz

import (
	"context"
	"time"
)

// Reader is a consistent repository snapshot. Tenant relations always take an
// explicit tenant or full reference; platform facilities have no fake tenant.
type Reader interface {
	Cluster(context.Context, string) (*Cluster, error)
	// Clusters filters by configured provider connection references, never user grants.
	Clusters(context.Context, []string, string, int) ([]Cluster, error)
	Pool(context.Context, string) (*Pool, error)
	Pools(context.Context, string, string, int) ([]Pool, error)
	Group(context.Context, string) (*SupplyGroup, error)
	Groups(context.Context, string, string, int) ([]SupplyGroup, error)
	Profile(context.Context, string, uint64) (*Profile, error)
	Profiles(context.Context, string, SupplyMode, string, int) ([]Profile, error)
	Usage(context.Context, UsageRef) (*UsageProjection, error)
	Usages(context.Context, string, string, UsageState, string, int) ([]UsageProjection, error)
	Devices(context.Context, string, string, string, int) ([]DeviceRecord, error)
	Bindings(context.Context, BindingFilter, string, int) ([]Binding, error)
	DeviceBindings(context.Context, string) ([]Binding, error)
	SourceHealth(context.Context, string) ([]SourceWatermark, error)
	PodEvidence(context.Context, PodRef) ([]Claim, []ReleaseProof, error)
	OwnerClaims(context.Context, UsageRef) ([]Claim, error)
}
type Transaction interface {
	Reader
	Lock(context.Context, string) error
	Replay(context.Context, string, string, string) (*IdempotencyRecord, error)
	Remember(context.Context, IdempotencyRecord) error
	InsertCluster(context.Context, Cluster) error
	InsertPool(context.Context, Pool) error
	InsertGroup(context.Context, SupplyGroup) error
	UpdateGroup(context.Context, SupplyGroup, uint64) error
	InsertProfile(context.Context, Profile, string) error
	UpdateProfile(context.Context, Profile, uint64) error
	SaveUsage(context.Context, UsageProjection) (*SyncResult, error)
	SaveObservation(context.Context, ObservationBatch) error
}
type Repository interface {
	View(context.Context, func(Reader) error) error
	Transact(context.Context, func(Transaction) error) error
}
type IdempotencyRecord struct {
	Actor, Method, Key, Digest string
	Result                     []byte
}
type BindingFilter struct {
	ClusterID, DeviceID string
	Usage               *UsageRef
	Pod                 *PodRef
}

// Provider only reads infrastructure. A runtime adapter cannot create quota,
// workloads, nodes or admission certificates through this port.
type Provider interface {
	Inspect(context.Context, Cluster, SupplyGroup) (GroupInspection, error)
	Collect(context.Context, Cluster, []SupplyGroup) (ObservationBatch, error)
}
type RuntimeClassInspection struct {
	Name            string
	UID             string
	ResourceVersion string
	NodeSelector    map[string]string
}

type GroupInspection struct {
	RuntimeClass   RuntimeClassInspection
	BaselineDigest string
	Nodes          []InspectedNode
	QueueName      string
	ObservedAt     time.Time
}
type InspectedNode struct {
	Node               NodeRef
	ModelKey           string
	Factor, SplitCount uint32
	Labels             map[string]string
	DeviceBudgetsMiB   []int64
	ConfigDigest       string
}

type DeviceRecord struct {
	Device               PhysicalDevice `json:"device"`
	ProviderMemoryBlocks int64          `json:"provider_memory_blocks"`
	MemoryBlockMiB       uint32         `json:"memory_block_mib"`
	SafeMemoryBudgetMiB  int64          `json:"safe_memory_budget_mib"`
	BaselineDigest       string         `json:"baseline_digest"`
}
type SourceWatermark struct {
	ConsumedNodeRevision uint64    `json:"consumed_node_revision,omitempty"`
	ClusterID            string    `json:"cluster_id"`
	NodeUID              string    `json:"node_uid"`
	Source               string    `json:"source"`
	Cursor               string    `json:"cursor"`
	ObservedAt           time.Time `json:"observed_at"`
	ReceivedAt           time.Time `json:"received_at"`
	Complete             bool      `json:"complete"`
	Issue                string    `json:"issue"`
	// Cluster-wide pending demand is carried only by the first configured
	// node's Kubernetes watermark, avoiding per-node duplication.
	PendingByGroup map[string]int64 `json:"pending_by_group,omitempty"`
}
type Claim struct {
	Pod              PodRef          `json:"pod"`
	DeviceID         string          `json:"device_id"`
	NodeUID          string          `json:"node_uid"`
	Source           string          `json:"source"`
	Cursor           string          `json:"cursor"`
	ObservedAt       time.Time       `json:"observed_at"`
	ReceivedAt       time.Time       `json:"received_at"`
	State            AllocationState `json:"state"`
	MemoryMiB        int64           `json:"memory_mib"`
	CoreLimitPercent uint32          `json:"core_limit_percent"`
	BaselineDigest   string          `json:"baseline_digest"`
	MemoryBlockMiB   uint32          `json:"memory_block_mib"`
	EvidenceRef      string          `json:"evidence_ref"`
	// Only cryptographically verified owner assertions may populate these.
	OwnerRef        *UsageRef `json:"owner_ref"`
	ExecutionDigest string    `json:"execution_digest"`
}

// ReleaseProof covers one actual Pod/container/device and retires only claims
// at or before the proven observation time. A missing API object is not proof.
type ReleaseProof struct {
	CheckedNodeRevision uint64    `json:"checked_node_revision,omitempty"`
	Pod                 PodRef    `json:"pod"`
	DeviceID            string    `json:"device_id"`
	NodeUID             string    `json:"node_uid"`
	ObservedAt          time.Time `json:"observed_at"`
	ReceivedAt          time.Time `json:"received_at"`
	EvidenceRef         string    `json:"evidence_ref"`
	RuntimeExited       bool      `json:"runtime_exited"`
	AllocationAbsent    bool      `json:"allocation_absent"`
	NodeOnline          bool      `json:"node_online"`
	OwnerRef            *UsageRef `json:"owner_ref"`
	ExecutionDigest     string    `json:"execution_digest"`
}
type ObservationBatch struct {
	ConnectionRef string
	NodeRevisions map[string]uint64
	ClusterID     string
	Devices       []DeviceRecord
	Claims        []Claim
	Releases      []ReleaseProof
	Watermarks    []SourceWatermark
}

type ReleaseRecheckScope struct {
	Owner UsageRef
	Pods  []PodRef
}

type NodeFactProvider interface {
	CollectRelease(context.Context, Cluster, []SupplyGroup, *ReleaseRecheckScope) (ObservationBatch, error)
}

type ReleaseNodeLocker interface {
	LockReleaseNodes(context.Context, string, []string) (map[string]NodeTelemetryState, error)
}
