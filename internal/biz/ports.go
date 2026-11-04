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
	CurrentDeviceBindings(context.Context, string) ([]Binding, error)
	CollectionState(context.Context, string) (*CollectionState, error)
	CurrentBindingMetrics(context.Context, []string, time.Time, time.Time) ([]CurrentBindingMetric, error)
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

// ObservationBatch is one current Kubernetes/Volcano projection. A failed
// list is distinct from a complete, empty list and never ends occupancy.
type ObservationBatch struct {
	ClusterID                 string
	BaseObservationRevision   uint64
	ObservedAt                time.Time
	ReceivedAt                time.Time
	SnapshotRef               string
	PodListComplete           bool
	LivePodUIDs               []string
	IncompletePodUIDs         []string
	Bindings                  []ObservedBinding
	PendingByGroup            map[string]int64
	BindingIssues             []string
	NodeListComplete          bool
	LiveNodeUIDs              []string
	InventoryCompleteNodeUIDs []string
	Devices                   []DeviceRecord
}

// ObservedBinding contains one successfully decoded current assignment.
// Only cryptographically verified owner assertions may populate OwnerRef.
type ObservedBinding struct {
	Pod              PodRef
	NodeUID          string
	DeviceID         string
	MemoryMiB        int64
	CoreLimitPercent uint32
	BaselineDigest   string
	MemoryBlockMiB   uint32
	OwnerRef         *UsageRef
	ExecutionDigest  string
}

type CollectionState struct {
	Revision       uint64           `json:"-"`
	Observation    *Observation     `json:"observation"`
	PendingByGroup map[string]int64 `json:"pending_by_group"`
}

// BindingOwnerCandidate is private persistence metadata; it is not a tenant
// association until an immutable Usage projection matches it.
type BindingOwnerCandidate struct {
	Ref             UsageRef `json:"ref"`
	ExecutionDigest string   `json:"execution_digest"`
}

type CurrentBindingMetric struct {
	Pod      PodRef
	Node     NodeRef
	GPUUUID  string
	UsageRef UsageRef
}
