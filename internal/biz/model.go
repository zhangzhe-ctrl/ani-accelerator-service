package biz

// Domain types deliberately have no dependency on transport or storage types.
// JSON names are also the versioned persistence snapshot field names.
type SupplyMode int32

const (
	Whole  SupplyMode = 1
	Shared SupplyMode = 2
)

type AdmissionState int32

const (
	Closed AdmissionState = 1
	Open   AdmissionState = 2
)

type UsageState int32

const (
	Declared UsageState = 1
	Ended    UsageState = 2
)

type EndReason int32

const (
	CanceledUnsent EndReason = 1
	OwnerReleased  EndReason = 2
)

type AllocationState int32

const (
	Active            AllocationState = 1
	ReleaseConfirmed  AllocationState = 2
	AllocationUnknown AllocationState = 3
)

type FitStatus int32

const (
	FitEstimate        FitStatus = 1
	WaitForCapacity    FitStatus = 2
	FitUnknown         FitStatus = 3
	RequestUnsupported FitStatus = 4
)

type ReleaseFinding int32

const (
	AllocationReleased ReleaseFinding = 1
	StillPresent       ReleaseFinding = 2
	ReleaseUnknown     ReleaseFinding = 3
)

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type TenantContext struct {
	RequestID string `json:"request_id"`
	TenantID  string `json:"tenant_id"`
	Actor     *Actor `json:"actor"`
}
type AdminRead struct {
	RequestID string `json:"request_id"`
	Actor     *Actor `json:"actor"`
}
type AdminMutation struct {
	Context        *AdminRead `json:"context"`
	IdempotencyKey string     `json:"idempotency_key"`
}
type Page struct {
	Size  uint32 `json:"size"`
	Token string `json:"token"`
}
type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type Observation struct {
	Complete    bool     `json:"complete"`
	ObservedAt  string   `json:"observed_at"`
	ReceivedAt  string   `json:"received_at"`
	Issues      []string `json:"issues"`
	SnapshotRef string   `json:"snapshot_ref"`
}
type ProviderBaseline struct {
	BaselineID              string `json:"baseline_id"`
	AdapterID               string `json:"adapter_id"`
	VolcanoVersion          string `json:"volcano_version"`
	VolcanoImageDigest      string `json:"volcano_image_digest"`
	DevicePluginVersion     string `json:"device_plugin_version"`
	DevicePluginImageDigest string `json:"device_plugin_image_digest"`
	HamiCoreRevision        string `json:"hami_core_revision"`
	MemoryBlockMiB          uint32 `json:"memory_block_mib"`
	MemoryEncoding          string `json:"memory_encoding"`
	SplitCount              uint32 `json:"split_count"`
	MemoryScalePercent      uint32 `json:"memory_scale_percent"`
	CoreScalePercent        uint32 `json:"core_scale_percent"`
	CoreBudgetAdditive      bool   `json:"core_budget_additive"`
	NodeConfigDigest        string `json:"node_config_digest"`
	SchedulerConfigDigest   string `json:"scheduler_config_digest"`
	RecipeVersion           string `json:"recipe_version"`
	RuntimeClassName        string `json:"runtime_class_name"`
	BaselineDigest          string `json:"baseline_digest"`
}
type NodeRef struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}
type Cluster struct {
	ClusterID     string `json:"cluster_id"`
	DisplayName   string `json:"display_name"`
	ConnectionRef string `json:"connection_ref"`
	Version       uint64 `json:"version"`
}
type Pool struct {
	PoolID      string `json:"pool_id"`
	ClusterID   string `json:"cluster_id"`
	DisplayName string `json:"display_name"`
}
type SupplyGroup struct {
	GroupID     string            `json:"group_id"`
	PoolID      string            `json:"pool_id"`
	ClusterID   string            `json:"cluster_id"`
	DisplayName string            `json:"display_name"`
	Mode        SupplyMode        `json:"mode"`
	Nodes       []NodeRef         `json:"nodes"`
	ModelKey    string            `json:"model_key"`
	Baseline    *ProviderBaseline `json:"baseline"`
	Admission   AdmissionState    `json:"admission"`
	QueueName   string            `json:"queue_name"`
	Version     uint64            `json:"version"`
	AdoptedAt   string            `json:"adopted_at"`
}
type ProfileSpec struct {
	GroupID              string     `json:"group_id"`
	Mode                 SupplyMode `json:"mode"`
	ModelKey             string     `json:"model_key"`
	MemoryMiBPerDevice   int64      `json:"memory_mib_per_device"`
	CoreLimitPercent     uint32     `json:"core_limit_percent"`
	MaxDevicesPerReplica uint32     `json:"max_devices_per_replica"`
	IsolationClass       string     `json:"isolation_class"`
}
type Profile struct {
	ProfileID      string       `json:"profile_id"`
	ProfileVersion uint64       `json:"profile_version"`
	DisplayName    string       `json:"display_name"`
	Spec           *ProfileSpec `json:"spec"`
	BaselineID     string       `json:"baseline_id"`
	SpecDigest     string       `json:"spec_digest"`
	State          ProfileState `json:"state"`
	Revision       uint64       `json:"revision"`
}

// ProfileScope is catalog metadata, deliberately separate from the immutable
// Profile embedded in a resolved plan and its digest.
type ProfileScope struct {
	ProfileID      string `json:"profile_id"`
	ProfileVersion uint64 `json:"profile_version"`
	SpecDigest     string `json:"spec_digest"`
	ClusterID      string `json:"cluster_id"`
	PoolID         string `json:"pool_id"`
}
type GpuRequest struct {
	ClusterID         string `json:"cluster_id"`
	PoolID            string `json:"pool_id"`
	ProfileID         string `json:"profile_id"`
	ProfileVersion    uint64 `json:"profile_version"`
	Replicas          uint32 `json:"replicas"`
	DevicesPerReplica uint32 `json:"devices_per_replica"`
	ContainerName     string `json:"container_name"`
}
type ProfileState int32

const (
	ProfileActive   ProfileState = 1
	ProfileDisabled ProfileState = 2
	ProfileDeleted  ProfileState = 3
)

type GpuExecution struct {
	SchedulerName    string            `json:"scheduler_name"`
	QueueName        string            `json:"queue_name"`
	RuntimeClassName string            `json:"runtime_class_name"`
	NodeSelector     map[string]string `json:"node_selector"`
	PodAnnotations   map[string]string `json:"pod_annotations"`
	Resources        map[string]string `json:"resources"`
	ExecutionDigest  string            `json:"execution_digest"`
}
type GpuDisplay struct {
	ProfileID      string     `json:"profile_id"`
	ProfileVersion uint64     `json:"profile_version"`
	ProfileName    string     `json:"profile_name"`
	ModelKey       string     `json:"model_key"`
	Mode           SupplyMode `json:"mode"`
}
type GpuQuotaItem struct {
	QuotaCode string `json:"quota_code"`
	Units     int64  `json:"units"`
}
type ResolvedGpuRequest struct {
	Execution  *GpuExecution  `json:"execution"`
	QuotaItems []GpuQuotaItem `json:"quota_items"`
	Selection  *GpuDisplay    `json:"selection"`
}
type PhysicalDevice struct {
	DeviceID            string       `json:"device_id"`
	ClusterID           string       `json:"cluster_id"`
	GroupID             string       `json:"group_id"`
	Node                *NodeRef     `json:"node"`
	VendorDeviceID      string       `json:"vendor_device_id"`
	ModelKey            string       `json:"model_key"`
	PhysicalMemoryBytes uint64       `json:"physical_memory_bytes"`
	Health              string       `json:"health"`
	Observation         *Observation `json:"observation"`
}
type UsageRef struct {
	TenantID          string `json:"tenant_id"`
	OwnerService      string `json:"owner_service"`
	ResourceID        string `json:"resource_id"`
	CreateOperationID string `json:"create_operation_id"`
}
type UsageProjection struct {
	Ref                      *UsageRef  `json:"ref"`
	Revision                 uint64     `json:"revision"`
	State                    UsageState `json:"state"`
	ProfileID                string     `json:"profile_id"`
	ProfileVersion           uint64     `json:"profile_version"`
	GroupID                  string     `json:"group_id"`
	BaselineID               string     `json:"baseline_id"`
	ClusterID                string     `json:"cluster_id"`
	ContainerName            string     `json:"container_name"`
	ExecutionDigest          string     `json:"execution_digest"`
	EndReason                EndReason  `json:"end_reason"`
	SourceFactRef            string     `json:"source_fact_ref"`
	SourceOperationCreatedAt string     `json:"source_operation_created_at"`
	PayloadDigest            string     `json:"payload_digest"`
}
type SyncResult struct {
	Ref             *UsageRef  `json:"ref"`
	AppliedRevision uint64     `json:"applied_revision"`
	State           UsageState `json:"state"`
	Replayed        bool       `json:"replayed"`
}
type PodRef struct {
	ClusterID     string `json:"cluster_id"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	UID           string `json:"uid"`
	ContainerName string `json:"container_name"`
}
type Binding struct {
	BindingID            string          `json:"binding_id"`
	VerifiedUsageRef     *UsageRef       `json:"verified_usage_ref"`
	Pod                  *PodRef         `json:"pod"`
	PhysicalDeviceID     string          `json:"physical_device_id"`
	State                AllocationState `json:"state"`
	MemoryMiB            int64           `json:"memory_mib"`
	CoreLimitPercent     uint32          `json:"core_limit_percent"`
	SourceBaselineDigest string          `json:"source_baseline_digest"`
	SourceMemoryBlockMiB uint32          `json:"source_memory_block_mib"`
	EvidenceRef          string          `json:"evidence_ref"`
	Observation          *Observation    `json:"observation"`
}
type DeviceCapacity struct {
	DeviceID             string   `json:"device_id"`
	NominalUnits         int64    `json:"nominal_units"`
	KnownCommittedUnits  int64    `json:"known_committed_units"`
	KnownFreeUnits       int64    `json:"known_free_units"`
	SafeMemoryBudgetMiB  int64    `json:"safe_memory_budget_mib"`
	CommittedMemoryMiB   int64    `json:"committed_memory_mib"`
	ProviderMemoryBlocks int64    `json:"provider_memory_blocks"`
	MemoryBlockMiB       uint32   `json:"memory_block_mib"`
	ReportedMemoryMiB    int64    `json:"reported_memory_mib"`
	Complete             bool     `json:"complete"`
	Issues               []string `json:"issues"`
}
type CapacityView struct {
	ProfileID              string           `json:"profile_id"`
	ProfileVersion         uint64           `json:"profile_version"`
	Mode                   SupplyMode       `json:"mode"`
	PhysicalDeviceCount    int64            `json:"physical_device_count"`
	NominalUnits           int64            `json:"nominal_units"`
	KnownCommittedUnits    int64            `json:"known_committed_units"`
	KnownFreeUnits         int64            `json:"known_free_units"`
	UnplacedRequestedUnits int64            `json:"unplaced_requested_units"`
	Observation            *Observation     `json:"observation"`
	Devices                []DeviceCapacity `json:"devices"`
}
type GpuFit struct {
	Status            FitStatus    `json:"status"`
	EstimatedReplicas int64        `json:"estimated_replicas"`
	EstimateKnown     bool         `json:"estimate_known"`
	Reasons           []string     `json:"reasons"`
	Observation       *Observation `json:"observation"`
}
type ReleaseObservation struct {
	EvidenceID        string         `json:"evidence_id"`
	Ref               *UsageRef      `json:"ref"`
	Finding           ReleaseFinding `json:"finding"`
	ActivePodUIDs     []string       `json:"active_pod_uids"`
	UncertainNodeUIDs []string       `json:"uncertain_node_uids"`
	Observation       *Observation   `json:"observation"`
	EvidenceDigest    string         `json:"evidence_digest"`
}
