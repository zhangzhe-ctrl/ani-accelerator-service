package provider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"k8s.io/apimachinery/pkg/api/resource"
)

type Provider struct {
	connections      map[string]*kubeReader
	ownerKey         ed25519.PublicKey
	resourceOwnerKey ed25519.PublicKey
	modeldevOwnerKey ed25519.PublicKey
	repo             biz.Repository
	freshness        time.Duration
}

// ConfigureResourceOwnerKey is called once by composition before collection.
// A missing Resource key leaves its live allocations unassociated, still counted.
func (p *Provider) ConfigureResourceOwnerKey(key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return biz.Invalid("RESOURCE_OWNER_KEY_INVALID")
	}
	p.resourceOwnerKey = append(ed25519.PublicKey(nil), key...)
	return nil
}

// ConfigureModeldevOwnerKey installs the independent training-owner verifier.
// Without this key, ModelDev allocations remain counted and unassociated.
func (p *Provider) ConfigureModeldevOwnerKey(key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize || bytes.Equal(key, p.ownerKey) || bytes.Equal(key, p.resourceOwnerKey) {
		return biz.Invalid("MODELDEV_OWNER_KEY_INVALID")
	}
	p.modeldevOwnerKey = append(ed25519.PublicKey(nil), key...)
	return nil
}

func New(config Config, ownerKey ed25519.PublicKey, repo biz.Repository, freshness time.Duration) (*Provider, error) {
	p := &Provider{connections: map[string]*kubeReader{}, ownerKey: ownerKey, repo: repo, freshness: freshness}
	for _, c := range config.Connections {
		if err := validateNodePluginConfigMaps(c.NodePluginConfigMaps); err != nil {
			return nil, err
		}
		k, err := newKubeReader(c)
		if err != nil {
			return nil, err
		}
		if p.connections[c.Ref] != nil {
			return nil, biz.Invalid("DUPLICATE_CONNECTION")
		}
		p.connections[c.Ref] = k
	}
	return p, nil
}
func (p *Provider) Inspect(ctx context.Context, cluster biz.Cluster, group biz.SupplyGroup) (biz.GroupInspection, error) {
	reader := p.connections[cluster.ConnectionRef]
	if reader == nil {
		return biz.GroupInspection{}, biz.Precondition("CONNECTION_UNAVAILABLE")
	}
	nodes, err := reader.nodes(ctx)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	plugins, err := reader.selectedPods(ctx, reader.config.PluginNamespace, reader.config.PluginSelector)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	schedulers, err := reader.selectedPods(ctx, reader.config.SchedulerNamespace, reader.config.SchedulerSelector)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	return p.inspectGroup(ctx, reader, group, nodes, plugins, schedulers, time.Now().UTC())
}

// inspectGroup shares this round's Kubernetes lists with collection. Device
// registration and verified plugin units define the allocation budget.
func (p *Provider) inspectGroup(ctx context.Context, k *kubeReader, g biz.SupplyGroup, ns []node, plugins, schedulers []pod, now time.Time) (biz.GroupInspection, error) {
	if g.Baseline == nil || !biz.SupportedMemoryFactor(g.Baseline.MemoryBlockMiB) {
		return biz.GroupInspection{}, biz.Precondition("MEMORY_UNIT_MISMATCH")
	}
	if err := k.validatePluginConfigNodes(ns); err != nil {
		return biz.GroupInspection{}, err
	}
	schedulerDigest, err := k.schedulerConfigDigest(ctx, k.config.SchedulerNamespace, k.config.SchedulerConfigMap, g.Baseline.VolcanoVersion)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	if schedulerDigest != g.Baseline.SchedulerConfigDigest {
		return biz.GroupInspection{}, biz.Precondition("CONFIG_DRIFT")
	}
	schedulerOK := false
	for _, pod := range schedulers {
		if imageRunning(pod, k.config.SchedulerContainer, g.Baseline.VolcanoImageDigest) {
			schedulerOK = true
		}
	}
	if !schedulerOK {
		return biz.GroupInspection{}, biz.Precondition("CONFIG_DRIFT")
	}
	var queue struct {
		Metadata metadata `json:"metadata"`
		Status   struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err = k.get(ctx, "/apis/scheduling.volcano.sh/v1beta1/queues/"+g.QueueName, &queue); err != nil || queue.Metadata.Name != g.QueueName || queue.Status.State != "Open" {
		return biz.GroupInspection{}, biz.Precondition("QUEUE_UNAVAILABLE")
	}
	runtimeClass, err := k.runtimeClass(ctx, g.Baseline.RuntimeClassName)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	inspection := biz.GroupInspection{RuntimeClass: runtimeClass, BaselineDigest: g.Baseline.BaselineDigest, QueueName: g.QueueName, ObservedAt: now}
	for _, want := range g.Nodes {
		var found *node
		for i := range ns {
			if ns[i].Metadata.UID == want.UID && ns[i].Metadata.Name == want.Name {
				found = &ns[i]
				break
			}
		}
		if found != nil {
			for key, value := range runtimeClass.NodeSelector {
				if found.Metadata.Labels[key] != value {
					return inspection, biz.Precondition("RUNTIME_CLASS_NODE_MISMATCH")
				}
			}
		}
		if found == nil || !k.nodeOnline(ctx, *found, now, p.freshness) {
			return inspection, biz.Precondition("NODE_UNKNOWN")
		}
		configMap, overridden := k.config.pluginConfigMap(found.Metadata.UID, found.Metadata.Name)
		nodeDigest, err := k.pluginConfigDigest(ctx, configMap, overridden)
		if err != nil {
			return inspection, err
		}
		if nodeDigest != g.Baseline.NodeConfigDigest {
			return inspection, biz.Precondition("CONFIG_DRIFT")
		}
		if g.Baseline.VolcanoVersion == "v1.15.2" || g.Baseline.VolcanoVersion == "v1.16.0" {
			// These schedulers ignore the register annotation unless all three
			// device-plugin resources are present and positive in Allocatable.
			for _, name := range []string{"volcano.sh/vgpu-number", "volcano.sh/vgpu-memory", "volcano.sh/vgpu-cores"} {
				quantity, err := resource.ParseQuantity(found.Status.Allocatable[name])
				value, exact := quantity.AsInt64()
				if err != nil || !exact || value <= 0 {
					return inspection, biz.Precondition("CONFIG_DRIFT")
				}
			}
		}
		ds, err := ParseNodeDevices(found.Metadata.Annotations[NodeRegister])
		if err != nil {
			return inspection, err
		}
		t, err := ParseHandshake(found.Metadata.Annotations[NodeHandshake])
		if err != nil || !sourceFresh(now, t, p.freshness) {
			return inspection, biz.Precondition("SOURCE_STALE")
		}
		if t.Before(inspection.ObservedAt) {
			inspection.ObservedAt = t
		}
		for _, d := range ds {
			if modelKey(d.Model) != g.ModelKey || d.SplitCount != g.Baseline.SplitCount {
				return inspection, biz.Precondition("CONFIG_DRIFT")
			}
		}
		budgets := make([]int64, 0, len(ds))
		for _, registered := range ds {
			if !registered.Healthy {
				return inspection, biz.Precondition("DEVICE_UNHEALTHY")
			}
			budget, err := deviceMemoryBudget(registered.MemoryBlocks, g.Baseline.MemoryBlockMiB)
			if err != nil || budget <= 0 {
				return inspection, biz.Precondition("CAPACITY_UNKNOWN")
			}
			budgets = append(budgets, budget)
		}
		loaded := false
		for _, plugin := range plugins {
			if plugin.Spec.NodeName != want.Name || !imageRunning(plugin, k.config.PluginContainer, g.Baseline.DevicePluginImageDigest) {
				continue
			}
			for _, container := range plugin.Spec.Containers {
				if container.Name != k.config.PluginContainer {
					continue
				}
				if overridden && !pluginConfigMounted(plugin, configMap, container) {
					continue
				}
				factor, ok1 := argument(container.Args, "gpu-memory-factor")
				split, ok2 := argument(container.Args, "device-split-count")
				if ok1 && ok2 && factor == strconv.FormatUint(uint64(g.Baseline.MemoryBlockMiB), 10) && split == strconv.FormatUint(uint64(g.Baseline.SplitCount), 10) {
					loaded = true
				}
			}
		}
		if !loaded {
			return inspection, biz.Precondition("MEMORY_UNIT_MISMATCH")
		}
		inspection.Nodes = append(inspection.Nodes, biz.InspectedNode{Node: want, ModelKey: g.ModelKey, Factor: g.Baseline.MemoryBlockMiB, SplitCount: g.Baseline.SplitCount, Labels: found.Metadata.Labels, ConfigDigest: nodeDigest, DeviceBudgetsMiB: budgets})
	}
	return inspection, nil
}
func argument(args []string, name string) (string, bool) {
	value := ""
	count := 0
	for i, a := range args {
		if a == "--"+name && i+1 < len(args) {
			value = args[i+1]
			count++
		}
		if strings.HasPrefix(a, "--"+name+"=") {
			value = strings.TrimPrefix(a, "--"+name+"=")
			count++
		}
	}
	return value, count == 1
}
func imageRunning(p pod, name, digest string) bool {
	if p.Status.Phase != "Running" || p.Metadata.DeletionTimestamp != "" {
		return false
	}
	_, hash, ok := strings.Cut(digest, "sha256:")
	if !ok || !biz.Digest(hash) {
		return false
	}
	for _, s := range p.Status.ContainerStatuses {
		if s.Name == name && s.Ready && strings.HasSuffix(s.ImageID, "sha256:"+hash) {
			return true
		}
	}
	return false
}
func modelKey(model string) string {
	v := strings.ToLower(strings.TrimSpace(model))
	v = strings.ReplaceAll(v, " ", "-")
	v = strings.ReplaceAll(v, "_", "-")
	return v
}
func (p *Provider) Collect(ctx context.Context, cluster biz.Cluster, groups []biz.SupplyGroup) (biz.ObservationBatch, error) {
	return p.collect(ctx, cluster, groups)
}

func (p *Provider) collect(ctx context.Context, cluster biz.Cluster, groups []biz.SupplyGroup) (biz.ObservationBatch, error) {
	batch := biz.ObservationBatch{ClusterID: cluster.ClusterID, ReceivedAt: time.Now().UTC()}
	reader := p.connections[cluster.ConnectionRef]
	if reader == nil {
		return batch, biz.Precondition("CONNECTION_UNAVAILABLE")
	}

	// Pod lifecycle remains usable even when inventory or configuration fails.
	pods, snapshotRef, podErr := reader.pods(ctx)
	if podErr == nil {
		batch.PodListComplete = true
		batch.ObservedAt = time.Now().UTC()
		batch.SnapshotRef = snapshotRef
		batch.PendingByGroup = map[string]int64{}
	} else {
		batch.BindingIssues = append(batch.BindingIssues, "SOURCE_UNKNOWN")
	}
	nodes, nodeErr := reader.nodes(ctx)
	plugins, pluginErr := reader.selectedPods(ctx, reader.config.PluginNamespace, reader.config.PluginSelector)
	schedulers, schedulerErr := reader.selectedPods(ctx, reader.config.SchedulerNamespace, reader.config.SchedulerSelector)
	now := time.Now().UTC()
	batch.NodeListComplete = nodeErr == nil
	actualNodes := map[string]node{}
	for _, actual := range nodes {
		batch.LiveNodeUIDs = append(batch.LiveNodeUIDs, actual.Metadata.UID)
		actualNodes[actual.Metadata.UID] = actual
	}

	groupByID := map[string]*biz.SupplyGroup{}
	members := map[string]biz.NodeRef{}
	inspected := map[string]bool{}
	for index := range groups {
		group := &groups[index]
		groupByID[group.GroupID] = group
		for _, member := range group.Nodes {
			members[member.UID] = member
		}
		if nodeErr == nil && pluginErr == nil && schedulerErr == nil {
			_, err := p.inspectGroup(ctx, reader, *group, nodes, plugins, schedulers, now)
			inspected[group.GroupID] = err == nil
		}
	}
	configNodesOK := nodeErr == nil && reader.validatePluginConfigNodes(nodes) == nil
	nodeUIDs := make([]string, 0, len(members))
	for uid := range members {
		nodeUIDs = append(nodeUIDs, uid)
	}
	sort.Strings(nodeUIDs)
	currentNodes := map[string]biz.NodeRef{}
	nodeGroups := map[string]*biz.SupplyGroup{}
	registeredByNode := map[string][]RegisteredDevice{}
	trustedUnits := map[string]bool{}
	type digestResult struct {
		digest string
		err    error
	}
	schedulerDigests := map[string]digestResult{}
	for _, uid := range nodeUIDs {
		member := members[uid]
		actual, found := actualNodes[uid]
		if !found || actual.Metadata.Name != member.Name {
			continue
		}
		currentNodes[member.Name] = member
		group := groupByID[actual.Metadata.Labels[biz.GroupLabel]]
		if group == nil || group.Baseline == nil {
			continue
		}
		memberOfGroup := false
		for _, configured := range group.Nodes {
			if configured == member {
				memberOfGroup = true
				break
			}
		}
		if !memberOfGroup {
			continue
		}
		nodeGroups[member.Name] = group
		registered, err := ParseNodeDevices(actual.Metadata.Annotations[NodeRegister])
		if err != nil {
			continue
		}
		registeredByNode[member.Name] = registered
		batch.InventoryCompleteNodeUIDs = append(batch.InventoryCompleteNodeUIDs, uid)
		configMap, overridden := reader.config.pluginConfigMap(member.UID, member.Name)
		nodeDigest, nodeConfigErr := reader.pluginConfigDigest(ctx, configMap, overridden)
		version := group.Baseline.VolcanoVersion
		schedulerConfig, loaded := schedulerDigests[version]
		if !loaded {
			schedulerConfig.digest, schedulerConfig.err = reader.schedulerConfigDigest(ctx, reader.config.SchedulerNamespace, reader.config.SchedulerConfigMap, version)
			schedulerDigests[version] = schedulerConfig
		}
		unitsOK := configNodesOK && pluginErr == nil && schedulerErr == nil && nodeConfigErr == nil && schedulerConfig.err == nil &&
			nodeDigest == group.Baseline.NodeConfigDigest && schedulerConfig.digest == group.Baseline.SchedulerConfigDigest &&
			actual.Metadata.Labels[biz.BaselineLabel] == group.Baseline.BaselineID && actual.Metadata.Labels[biz.ModelLabel] == group.ModelKey &&
			collectionUnitsMatch(*group, member, registered, plugins, schedulers, reader.config)
		trustedUnits[member.Name] = unitsOK
		handshake, handshakeErr := ParseHandshake(actual.Metadata.Annotations[NodeHandshake])
		fresh := handshakeErr == nil && sourceFresh(now, handshake, p.freshness)
		online := reader.nodeOnline(ctx, actual, now, p.freshness)
		for _, device := range registered {
			issues := []string{}
			if !online {
				issues = append(issues, "NODE_UNKNOWN")
			}
			if !fresh {
				issues = append(issues, "SOURCE_STALE")
			}
			if !inspected[group.GroupID] {
				issues = append(issues, "CONFIGURATION_UNAVAILABLE")
			}
			if !unitsOK {
				issues = append(issues, "MEMORY_UNIT_MISMATCH")
			}
			health := "HEALTHY"
			if !device.Healthy {
				health = "UNHEALTHY"
				issues = append(issues, "DEVICE_UNHEALTHY")
			}
			budget := int64(0)
			if unitsOK {
				budget, err = deviceMemoryBudget(device.MemoryBlocks, group.Baseline.MemoryBlockMiB)
				if err != nil {
					issues = append(issues, "CAPACITY_UNKNOWN")
				}
			}
			observedAt := ""
			if !handshake.IsZero() {
				observedAt = handshake.Format(time.RFC3339Nano)
			}
			nodeRef := member
			batch.Devices = append(batch.Devices, biz.DeviceRecord{
				Device: biz.PhysicalDevice{
					DeviceID: PhysicalID(cluster.ClusterID, uid, device.UUID), ClusterID: cluster.ClusterID, GroupID: group.GroupID,
					Node: &nodeRef, VendorDeviceID: device.UUID, ModelKey: modelKey(device.Model), Health: health,
					Observation: &biz.Observation{Complete: len(issues) == 0, ObservedAt: observedAt, ReceivedAt: now.Format(time.RFC3339Nano), Issues: issues, SnapshotRef: actual.Metadata.ResourceVersion},
				},
				ProviderMemoryBlocks: device.MemoryBlocks, MemoryBlockMiB: group.Baseline.MemoryBlockMiB,
				SafeMemoryBudgetMiB: budget, BaselineDigest: group.Baseline.BaselineDigest,
			})
		}
	}

	for _, workload := range pods {
		if workload.Status.Phase == "Failed" || workload.Status.Phase == "Succeeded" {
			continue
		}
		batch.LivePodUIDs = append(batch.LivePodUIDs, workload.Metadata.UID)
		assignments, err := ParseAssignments(workload.Metadata.Annotations[AssignedIDs])
		if err != nil {
			batch.IncompletePodUIDs = append(batch.IncompletePodUIDs, workload.Metadata.UID)
			batch.BindingIssues = append(batch.BindingIssues, "PROVIDER_POD_INVALID")
			continue
		}
		hasAllocation := false
		for _, assigned := range assignments {
			hasAllocation = hasAllocation || len(assigned) > 0
		}
		if !hasAllocation {
			if _, err := p.podBindings(cluster.ClusterID, biz.SupplyGroup{}, workload, biz.NodeRef{}, nil); err != nil {
				batch.IncompletePodUIDs = append(batch.IncompletePodUIDs, workload.Metadata.UID)
				batch.BindingIssues = append(batch.BindingIssues, "PROVIDER_POD_INVALID")
				continue
			}
			// A valid empty assignment ends old per-device allocations even if
			// the node/configuration cannot currently be read.
			groupID := workload.Spec.NodeSelector[biz.GroupLabel]
			if group := nodeGroups[workload.Spec.NodeName]; group != nil {
				groupID = group.GroupID
			}
			if groupByID[groupID] != nil {
				requested, requestErr := requestedGPUUnits(workload)
				if requestErr == nil {
					batch.PendingByGroup[groupID], requestErr = biz.Add(batch.PendingByGroup[groupID], requested)
				}
				if requestErr != nil {
					batch.BindingIssues = append(batch.BindingIssues, "PROVIDER_PENDING_INVALID")
				}
			}
			continue
		}
		group := nodeGroups[workload.Spec.NodeName]
		nodeRef, found := currentNodes[workload.Spec.NodeName]
		if !found || group == nil || !trustedUnits[workload.Spec.NodeName] {
			batch.IncompletePodUIDs = append(batch.IncompletePodUIDs, workload.Metadata.UID)
			batch.BindingIssues = append(batch.BindingIssues, "MEMORY_UNIT_MISMATCH")
			continue
		}
		bindings, err := p.podBindings(cluster.ClusterID, *group, workload, nodeRef, registeredByNode[workload.Spec.NodeName])
		if err != nil {
			batch.IncompletePodUIDs = append(batch.IncompletePodUIDs, workload.Metadata.UID)
			batch.BindingIssues = append(batch.BindingIssues, "PROVIDER_POD_INVALID")
			continue
		}
		batch.Bindings = append(batch.Bindings, bindings...)
	}

	// The optional metadata query never changes allocation or inventory truth.
	if reader.inventory != nil && len(batch.Devices) > 0 {
		if samples, err := reader.inventory.ReadPhysicalMemory(ctx, cluster.ClusterID); err == nil {
			byDevice := map[string]uint64{}
			for _, sample := range samples {
				byDevice[sample.NodeUID+"\x00"+sample.CanonicalUUID] = sample.Bytes
			}
			for index := range batch.Devices {
				device := &batch.Devices[index].Device
				device.PhysicalMemoryBytes = byDevice[device.Node.UID+"\x00"+biz.NormalizeGPUUUID(device.VendorDeviceID)]
			}
		}
	}
	sort.Strings(batch.LivePodUIDs)
	sort.Strings(batch.IncompletePodUIDs)
	sort.Strings(batch.LiveNodeUIDs)
	batch.BindingIssues = uniqueIssues(batch.BindingIssues)
	batch.ReceivedAt = time.Now().UTC()
	return batch, nil
}

func requestedGPUUnits(workload pod) (int64, error) {
	var total int64
	for _, container := range workload.Spec.Containers {
		value := container.Resources.Limits["volcano.sh/vgpu-number"]
		if value == "" && container.Resources.Limits["volcano.sh/vgpu-memory"] == "" {
			continue
		}
		count, err := strconv.ParseInt(value, 10, 64)
		if err != nil || count <= 0 {
			return 0, biz.Invalid("PROVIDER_PENDING_INVALID")
		}
		total, err = biz.Add(total, count)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func uniqueIssues(issues []string) []string {
	sort.Strings(issues)
	result := make([]string, 0, len(issues))
	for _, issue := range issues {
		if len(result) == 0 || result[len(result)-1] != issue {
			result = append(result, issue)
		}
	}
	return result
}

// collectionUnitsMatch checks actual running binaries and plugin F/K. Configuration
// identity never grants permission to decode a different memory unit.
func collectionUnitsMatch(g biz.SupplyGroup, source biz.NodeRef, registered []RegisteredDevice, plugins, schedulers []pod, config Connection) bool {
	if g.Baseline == nil || !biz.SupportedMemoryFactor(g.Baseline.MemoryBlockMiB) || len(registered) == 0 {
		return false
	}
	for _, device := range registered {
		if modelKey(device.Model) != g.ModelKey || device.SplitCount != g.Baseline.SplitCount {
			return false
		}
	}
	schedulerOK := false
	for _, scheduler := range schedulers {
		if imageRunning(scheduler, config.SchedulerContainer, g.Baseline.VolcanoImageDigest) {
			schedulerOK = true
		}
	}
	if !schedulerOK {
		return false
	}
	configMap, overridden := config.pluginConfigMap(source.UID, source.Name)
	for _, plugin := range plugins {
		if plugin.Spec.NodeName != source.Name || !imageRunning(plugin, config.PluginContainer, g.Baseline.DevicePluginImageDigest) {
			continue
		}
		for _, container := range plugin.Spec.Containers {
			if container.Name != config.PluginContainer {
				continue
			}
			if overridden && !pluginConfigMounted(plugin, configMap, container) {
				continue
			}
			factor, hasFactor := argument(container.Args, "gpu-memory-factor")
			split, hasSplit := argument(container.Args, "device-split-count")
			if hasFactor && hasSplit && factor == strconv.FormatUint(uint64(g.Baseline.MemoryBlockMiB), 10) && split == strconv.FormatUint(uint64(g.Baseline.SplitCount), 10) {
				return true
			}
		}
	}
	return false
}

func (p *Provider) podBindings(clusterID string, group biz.SupplyGroup, workload pod, nodeRef biz.NodeRef, registered []RegisteredDevice) ([]biz.ObservedBinding, error) {
	assignments, err := ParseAssignments(workload.Metadata.Annotations[AssignedIDs])
	if err != nil || len(assignments) == 0 {
		return nil, err
	}
	var containers []container
	for _, container := range workload.Spec.Containers {
		if container.Resources.Limits["volcano.sh/vgpu-number"] != "" || container.Resources.Limits["volcano.sh/vgpu-memory"] != "" {
			containers = append(containers, container)
		}
	}
	if len(containers) != len(assignments) {
		return nil, biz.Invalid("PROVIDER_CONTAINER_MAPPING")
	}
	hasAllocation := false
	seenContainers := map[string]bool{}
	for index, container := range containers {
		podRef := biz.PodRef{ClusterID: clusterID, Namespace: workload.Metadata.Namespace, Name: workload.Metadata.Name, UID: workload.Metadata.UID, ContainerName: container.Name}
		if biz.ValidatePod(podRef) != nil || seenContainers[container.Name] {
			return nil, biz.Invalid("PROVIDER_CONTAINER_MAPPING")
		}
		seenContainers[container.Name] = true
		hasAllocation = hasAllocation || len(assignments[index]) > 0
	}
	if !hasAllocation {
		return nil, nil
	}
	if nodeRef.UID == "" || nodeRef.Name != workload.Spec.NodeName {
		return nil, biz.Invalid("PROVIDER_NODE_IDENTITY")
	}
	if group.Baseline == nil || !biz.SupportedMemoryFactor(group.Baseline.MemoryBlockMiB) {
		return nil, biz.Precondition("MEMORY_UNIT_MISMATCH")
	}
	known := map[string]bool{}
	for _, device := range registered {
		known[device.UUID] = true
	}
	var bindings []biz.ObservedBinding
	for index, container := range containers {
		podRef := biz.PodRef{ClusterID: clusterID, Namespace: workload.Metadata.Namespace, Name: workload.Metadata.Name, UID: workload.Metadata.UID, ContainerName: container.Name}
		ref, execution := p.verifyOwner(podRef, workload.Metadata.Labels, workload.Metadata.Annotations)
		if encodedFactor, present := workload.Metadata.Annotations["accelerator.ani.io/memory-block-mib"]; present {
			if encodedFactor != strconv.FormatUint(uint64(group.Baseline.MemoryBlockMiB), 10) {
				return nil, biz.Precondition("MEMORY_UNIT_MISMATCH")
			}
		} else if ref != nil && group.Baseline.MemoryBlockMiB != 1 {
			return nil, biz.Precondition("MEMORY_UNIT_MISMATCH")
		}
		for _, allocation := range assignments[index] {
			if !known[allocation.UUID] {
				return nil, biz.Invalid("PROVIDER_DEVICE_IDENTITY")
			}
			memory, err := biz.NormalizeMemory(allocation.MemoryBlocks, "BLOCKS", group.Baseline.MemoryBlockMiB)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, biz.ObservedBinding{
				Pod: podRef, NodeUID: nodeRef.UID, DeviceID: PhysicalID(clusterID, nodeRef.UID, allocation.UUID),
				MemoryMiB: memory, CoreLimitPercent: allocation.CorePercent,
				BaselineDigest: group.Baseline.BaselineDigest, MemoryBlockMiB: group.Baseline.MemoryBlockMiB,
				OwnerRef: ref, ExecutionDigest: execution,
			})
		}
	}
	return bindings, nil
}

func (p *Provider) verifyOwner(pod biz.PodRef, labels, annotations map[string]string) (*biz.UsageRef, string) {
	key := p.ownerKey
	if labels["accelerator.ani.io/owner-service"] == "ani-resource-service" {
		key = p.resourceOwnerKey
	}
	if labels["accelerator.ani.io/owner-service"] == "ani-modeldev-service" {
		key = p.modeldevOwnerKey
	}
	return VerifyOwnerAssertion(pod, labels, annotations, key)
}
func (p *Provider) String() string {
	return fmt.Sprintf("volcano-hami-mib-v1 (%d managed connections)", len(p.connections))
}

// F is verified from the managed plugin configuration before this budget is used.
func deviceMemoryBudget(blocks int64, observedF uint32) (int64, error) {
	if !biz.SupportedMemoryFactor(observedF) || blocks <= 0 {
		return 0, biz.Precondition("MEMORY_UNIT_MISMATCH")
	}
	return biz.Multiply(blocks, int64(observedF))
}

func sourceFresh(now, observed time.Time, freshness time.Duration) bool {
	return !observed.IsZero() && !observed.After(now.Add(5*time.Second)) && now.Sub(observed) <= freshness
}
