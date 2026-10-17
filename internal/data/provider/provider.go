package provider

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"k8s.io/apimachinery/pkg/api/resource"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type Provider struct {
	connections      map[string]*kubeReader
	inventory        inventoryReader
	evidence         *EvidenceStore
	ownerKey         ed25519.PublicKey
	resourceOwnerKey ed25519.PublicKey
	repo             biz.Repository
	freshness        time.Duration
	runtimeEvents    *runtimeEvents
	runtimeJournal   *journalRecoveryConfig
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

func New(config Config, evidence *EvidenceStore, ownerKey ed25519.PublicKey, repo biz.Repository, freshness time.Duration) (*Provider, error) {
	p := &Provider{connections: map[string]*kubeReader{}, inventory: nvmlInventory{}, evidence: evidence, ownerKey: ownerKey, repo: repo, freshness: freshness}
	if config.RuntimeEventsDirectory != "" {
		p.runtimeEvents = newRuntimeEvents(config.RuntimeEventsDirectory)
	}
	p.runtimeJournal = config.RuntimeJournal
	for _, c := range config.Connections {
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
func (p *Provider) Inspect(ctx context.Context, c biz.Cluster, g biz.SupplyGroup) (biz.GroupInspection, error) {
	k := p.connections[c.ConnectionRef]
	if k == nil {
		return biz.GroupInspection{}, biz.Precondition("CONNECTION_UNAVAILABLE")
	}
	ns, err := k.nodes(ctx)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	nodeDigest, err := k.configDigest(ctx, k.config.PluginNamespace, k.config.PluginConfigMap)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	schedulerDigest, err := k.schedulerConfigDigest(ctx, k.config.SchedulerNamespace, k.config.SchedulerConfigMap, g.Baseline.VolcanoVersion)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	if nodeDigest != g.Baseline.NodeConfigDigest || schedulerDigest != g.Baseline.SchedulerConfigDigest {
		return biz.GroupInspection{}, biz.Precondition("CONFIG_DRIFT")
	}
	plugins, err := k.selectedPods(ctx, k.config.PluginNamespace, k.config.PluginSelector)
	if err != nil {
		return biz.GroupInspection{}, err
	}
	schedulers, err := k.selectedPods(ctx, k.config.SchedulerNamespace, k.config.SchedulerSelector)
	if err != nil {
		return biz.GroupInspection{}, err
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
	inspection := biz.GroupInspection{BaselineDigest: g.Baseline.BaselineDigest, QueueName: g.QueueName, ObservedAt: time.Now().UTC()}
	var att *Attestation
	var physicalInventory []Inventory
	if g.Baseline.Verification != nil && g.Baseline.Verification.State == biz.Verified {
		att, err = p.evidence.Load(g.Baseline.Verification.EvidenceRef)
		if err != nil {
			return inspection, err
		}
		if att.ClusterID != c.ClusterID || att.GroupID != g.GroupID || att.BaselineDigest != g.Baseline.BaselineDigest || g.Baseline.Verification.BaselineDigest != g.Baseline.BaselineDigest {
			return inspection, biz.Precondition("ATTESTATION_SCOPE_MISMATCH")
		}
		wantNodes := map[string]bool{}
		for _, member := range g.Nodes {
			wantNodes[member.UID] = true
		}
		if len(att.NodeUIDs) != len(wantNodes) {
			return inspection, biz.Precondition("ATTESTATION_SCOPE_MISMATCH")
		}
		for _, uid := range att.NodeUIDs {
			if !wantNodes[uid] {
				return inspection, biz.Precondition("ATTESTATION_SCOPE_MISMATCH")
			}
			delete(wantNodes, uid)
		}
		physicalInventory, err = p.inventory.Read(ctx)
		if err != nil {
			return inspection, err
		}
	}
	for _, want := range g.Nodes {
		var found *node
		for i := range ns {
			if ns[i].Metadata.UID == want.UID && ns[i].Metadata.Name == want.Name {
				found = &ns[i]
				break
			}
		}
		if found == nil || !k.nodeOnline(ctx, *found, time.Now(), p.freshness) {
			return inspection, biz.Precondition("NODE_UNKNOWN")
		}
		if g.Baseline.VolcanoVersion == "v1.15.2" {
			// This scheduler ignores the register annotation unless all three
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
		if err != nil || time.Since(t) > p.freshness {
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
		if att != nil {
			// A signed qualification describes concrete physical devices. The
			// process-local NVML inventory cannot attest a remote node's bytes.
			if want.UID != k.config.LocalNodeUID {
				return inspection, biz.Precondition("NVML_UNAVAILABLE")
			}
			devices := map[string]HardwareDevice{}
			for _, device := range att.Devices {
				if device.NodeUID == want.UID {
					if _, duplicate := devices[device.VendorUUID]; duplicate || device.ModelKey != g.ModelKey || device.PhysicalBytes == 0 {
						return inspection, biz.Precondition("ATTESTATION_DEVICE_MISMATCH")
					}
					devices[device.VendorUUID] = device
				}
			}
			if len(devices) != len(ds) || len(physicalInventory) != len(ds) {
				return inspection, biz.Precondition("ATTESTATION_DEVICE_MISMATCH")
			}
			for _, registered := range ds {
				device, exists := devices[registered.UUID]
				matched := false
				for _, physical := range physicalInventory {
					if exists && physical.UUID == device.VendorUUID && modelKey(physical.Model) == device.ModelKey && physical.Bytes == device.PhysicalBytes {
						matched = true
					}
				}
				if !matched {
					return inspection, biz.Precondition("ATTESTATION_DEVICE_MISMATCH")
				}
			}
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
				factor, ok1 := argument(container.Args, "gpu-memory-factor")
				split, ok2 := argument(container.Args, "device-split-count")
				if ok1 && ok2 && factor == strconv.FormatUint(uint64(g.Baseline.MemoryBlockMiB), 10) && split == strconv.FormatUint(uint64(g.Baseline.SplitCount), 10) {
					loaded = true
				}
			}
		}
		if !loaded {
			return inspection, biz.Precondition("MEMORY_UNIT_UNVERIFIED")
		}
		inspection.Nodes = append(inspection.Nodes, biz.InspectedNode{Node: want, ModelKey: g.ModelKey, Factor: g.Baseline.MemoryBlockMiB, SplitCount: g.Baseline.SplitCount, Labels: found.Metadata.Labels, ConfigDigest: nodeDigest})
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
func (p *Provider) Collect(ctx context.Context, c biz.Cluster, groups []biz.SupplyGroup) (biz.ObservationBatch, error) {
	batch := biz.ObservationBatch{ClusterID: c.ClusterID}
	k := p.connections[c.ConnectionRef]
	if k == nil {
		return batch, biz.Precondition("CONNECTION_UNAVAILABLE")
	}
	now := time.Now().UTC()
	nodes, ne := k.nodes(ctx)
	pods, pe := k.pods(ctx)
	inspected := map[string]bool{}
	for _, g := range groups {
		_, err := p.Inspect(ctx, c, g)
		inspected[g.GroupID] = err == nil
	}
	pending := map[string]int64{}
	for _, pod := range pods {
		group := pod.Spec.NodeSelector[biz.GroupLabel]
		if pod.Spec.NodeName != "" || pod.Metadata.DeletionTimestamp != "" || pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" || !biz.UUID(group) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			value := container.Resources.Limits["volcano.sh/vgpu-number"]
			if value == "" {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n <= 0 {
				pe = biz.Invalid("PROVIDER_PENDING_INVALID")
				continue
			}
			pending[group], err = biz.Add(pending[group], n)
			if err != nil {
				pe = err
			}
		}
	}
	// Admission qualification and physical source availability are independent.
	// Only known running binaries and explicit plugin F/K can justify decoding
	// scheduler block units; runtime memory is read directly in MiB otherwise.
	plugins, pluginErr := k.selectedPods(ctx, k.config.PluginNamespace, k.config.PluginSelector)
	schedulers, schedulerErr := k.selectedPods(ctx, k.config.SchedulerNamespace, k.config.SchedulerSelector)
	var inventory []Inventory
	var inventoryErr error
	if k.config.LocalNodeUID != "" {
		inventory, inventoryErr = p.inventory.Read(ctx)
	}
	for sourceIndex, source := range k.config.Nodes {
		var n *node
		for i := range nodes {
			if nodes[i].Metadata.UID == source.UID && nodes[i].Metadata.Name == source.Name {
				n = &nodes[i]
				break
			}
		}
		nodeOK := ne == nil && pe == nil && n != nil && k.nodeOnline(ctx, *n, now, p.freshness)
		snapshot := readNode(ctx, source)
		releaseSnapshot := snapshot
		if p.runtimeEvents != nil {
			p.runtimeEvents.observeSnapshot(source, snapshot)
			releaseSnapshot = p.runtimeEvents.releaseSnapshot(source, snapshot)
		}
		for _, health := range []struct {
			name string
			ok   bool
		}{{"kubernetes", nodeOK}, {"podresources", snapshot.ResourcesOK}, {"cri", snapshot.RuntimeOK}} {
			issue := ""
			if !health.ok {
				issue = "SOURCE_UNKNOWN"
			}
			batch.Watermarks = append(batch.Watermarks, biz.SourceWatermark{ClusterID: c.ClusterID, NodeUID: source.UID, Source: health.name, Cursor: now.Format(time.RFC3339Nano), ObservedAt: snapshot.ObservedAt, ReceivedAt: time.Now().UTC(), Complete: health.ok, Issue: issue})
			if health.name == "kubernetes" && sourceIndex == 0 {
				batch.Watermarks[len(batch.Watermarks)-1].PendingByGroup = pending
			}
		}
		// Positive release evidence needs current Node UID + healthy CRI and
		// PodResources only. Registration, F/K, and admission proofs do not
		// participate in determining that an old allocation has disappeared.
		proofs, err := p.releaseProofs(ctx, c.ClusterID, source, releaseSnapshot, nodeOK, now)
		if err != nil {
			return batch, err
		}
		batch.Releases = append(batch.Releases, proofs...)
		if !nodeOK {
			continue
		}
		registered, registerErr := ParseNodeDevices(n.Metadata.Annotations[NodeRegister])
		handshake, handshakeErr := ParseHandshake(n.Metadata.Annotations[NodeHandshake])
		var group *biz.SupplyGroup
		for i := range groups {
			for _, member := range groups[i].Nodes {
				if member.UID == source.UID && member.Name == source.Name {
					group = &groups[i]
				}
			}
		}
		if group == nil {
			continue
		} // No trusted F exists for an unmanaged supply node.
		unitsTrusted := registerErr == nil && handshakeErr == nil && now.Sub(handshake) <= p.freshness && pluginErr == nil && schedulerErr == nil && collectionUnitsMatch(*group, source, registered, plugins, schedulers, k.config)
		if registerErr != nil || handshakeErr != nil {
			// Do not leave a recently complete cached record visible as free
			// capacity while current registration can no longer be interpreted.
			stale, err := p.unqualifiedDevices(ctx, c.ClusterID, group.GroupID, source.UID, now)
			if err != nil {
				return batch, err
			}
			batch.Devices = append(batch.Devices, stale...)
			registered = nil
		}
		for _, rd := range registered {
			var physical *Inventory
			if source.UID == k.config.LocalNodeUID && inventoryErr == nil {
				for i := range inventory {
					if inventory[i].UUID == rd.UUID && inventory[i].Model == rd.Model {
						physical = &inventory[i]
						break
					}
				}
			}
			bytes := uint64(0)
			if physical != nil {
				bytes = physical.Bytes
			}
			health := "UNHEALTHY"
			if rd.Healthy {
				health = "HEALTHY"
			}
			complete := inspected[group.GroupID] && unitsTrusted && physical != nil && snapshot.ResourcesOK && snapshot.RuntimeOK && now.Sub(handshake) <= p.freshness
			safe := int64(0)
			if group.Baseline.Verification != nil && group.Baseline.Verification.State == biz.Verified {
				if att, err := p.evidence.Load(group.Baseline.Verification.EvidenceRef); err == nil && att.BaselineDigest == group.Baseline.BaselineDigest && att.GroupID == group.GroupID {
					for _, v := range att.Devices {
						if v.NodeUID == source.UID && v.VendorUUID == rd.UUID && v.PhysicalBytes == bytes {
							safe = v.SafeMemoryMiB
						}
					}
				}
			}
			id := PhysicalID(c.ClusterID, source.UID, rd.UUID)
			issues := []string{}
			if physical == nil || !snapshot.ResourcesOK || !snapshot.RuntimeOK || now.Sub(handshake) > p.freshness {
				issues = append(issues, "SOURCE_UNKNOWN")
			}
			if !inspected[group.GroupID] {
				issues = append(issues, "QUALIFICATION_UNAVAILABLE")
			}
			if !unitsTrusted {
				rd.MemoryBlocks = 0 // Unknown F is never decoded with the old baseline.
				issues = append(issues, "MEMORY_UNIT_UNVERIFIED")
			}
			if modelKey(rd.Model) != group.ModelKey || rd.SplitCount != group.Baseline.SplitCount {
				complete = false
				issues = append(issues, "CONFIG_DRIFT")
			}
			batch.Devices = append(batch.Devices, biz.DeviceRecord{Device: biz.PhysicalDevice{DeviceID: id, ClusterID: c.ClusterID, GroupID: group.GroupID, Node: &biz.NodeRef{UID: source.UID, Name: source.Name}, VendorDeviceID: rd.UUID, ModelKey: modelKey(rd.Model), PhysicalMemoryBytes: bytes, Health: health, Observation: &biz.Observation{Complete: complete, ObservedAt: handshake.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano), Issues: issues, SnapshotRef: n.Metadata.ResourceVersion}}, ProviderMemoryBlocks: rd.MemoryBlocks, MemoryBlockMiB: group.Baseline.MemoryBlockMiB, SafeMemoryBudgetMiB: safe, BaselineDigest: group.Baseline.BaselineDigest})
		}
		for _, pod := range pods {
			if pod.Spec.NodeName != source.Name {
				continue
			}
			var claims []biz.Claim
			var err error
			if unitsTrusted {
				claims, err = p.podClaims(ctx, c.ClusterID, *group, pod, source, snapshot, registered, now)
			} else {
				claims, err = p.runtimeClaims(c.ClusterID, *group, pod, source, snapshot, batch.Devices, now)
			}
			if err != nil {
				batch.Watermarks[len(batch.Watermarks)-3].Complete = false
				batch.Watermarks[len(batch.Watermarks)-3].Issue = "PROVIDER_POD_INVALID"
				continue
			}
			batch.Claims = append(batch.Claims, claims...)
		}
	}
	return batch, nil
}

// collectionUnitsMatch establishes only the unit decoder, not eligibility for
// new allocations. A changed ConfigMap or expired qualification may invalidate
// admission while these actual running binaries still emit the known F/K units.
func collectionUnitsMatch(g biz.SupplyGroup, source NodeSource, registered []RegisteredDevice, plugins, schedulers []pod, config Connection) bool {
	if len(registered) == 0 {
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
	for _, plugin := range plugins {
		if plugin.Spec.NodeName != source.Name || !imageRunning(plugin, config.PluginContainer, g.Baseline.DevicePluginImageDigest) {
			continue
		}
		for _, container := range plugin.Spec.Containers {
			if container.Name != config.PluginContainer {
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

func (p *Provider) unqualifiedDevices(ctx context.Context, cluster, group, nodeUID string, now time.Time) ([]biz.DeviceRecord, error) {
	var devices []biz.DeviceRecord
	err := p.repo.View(ctx, func(r biz.Reader) error {
		after := ""
		for {
			page, err := r.Devices(ctx, cluster, group, after, 201)
			if err != nil {
				return err
			}
			for _, device := range page {
				if device.Device.Node == nil || device.Device.Node.UID != nodeUID {
					continue
				}
				observed := now.Format(time.RFC3339Nano)
				if device.Device.Observation != nil {
					observed = device.Device.Observation.ObservedAt
				}
				device.Device.Observation = &biz.Observation{Complete: false, ObservedAt: observed, ReceivedAt: now.Format(time.RFC3339Nano), Issues: []string{"MEMORY_UNIT_UNVERIFIED"}}
				devices = append(devices, device)
			}
			if len(page) < 201 {
				return nil
			}
			after = page[len(page)-1].Device.DeviceID
		}
	})
	return devices, err
}

// runtimeClaims records current occupancy in CRI's actual MiB units when the
// scheduler/plugin unit decoder is untrusted. It cannot establish a complete
// binding or free capacity, and never rewrites scheduler/allocation claims.
func (p *Provider) runtimeClaims(cluster string, g biz.SupplyGroup, pod pod, node NodeSource, snapshot nodeSnapshot, devices []biz.DeviceRecord, now time.Time) ([]biz.Claim, error) {
	var claims []biz.Claim
	for _, container := range pod.Spec.Containers {
		podRef := biz.PodRef{ClusterID: cluster, Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name, UID: pod.Metadata.UID, ContainerName: container.Name}
		statuses, ok := runtimeFor(snapshot, podRef.UID, podRef.ContainerName)
		if !ok {
			continue
		}
		for _, status := range statuses {
			if status.Status.State != cri.ContainerState_CONTAINER_RUNNING {
				continue
			}
			allocation, err := ParseContainerdInfo(status.Info["info"])
			if err != nil {
				// Non-GPU containers have no GPU runtime fields.
				if container.Resources.Limits["volcano.sh/vgpu-number"] == "" {
					continue
				}
				return nil, err
			}
			id := PhysicalID(cluster, node.UID, allocation.UUID)
			known := false
			for _, device := range devices {
				if device.Device.DeviceID == id {
					known = true
				}
			}
			if !known {
				continue // Never create a claim for an unobserved physical device.
			}
			ref, plan := p.verifyOwner(podRef, pod.Metadata.Labels, pod.Metadata.Annotations)
			claims = append(claims, biz.Claim{Pod: podRef, DeviceID: id, NodeUID: node.UID, Source: "runtime", ObservedAt: snapshot.ObservedAt, ReceivedAt: now, State: biz.AllocationUnknown, MemoryMiB: allocation.MemoryMiB, CoreLimitPercent: allocation.CorePercent, BaselineDigest: g.Baseline.BaselineDigest, MemoryBlockMiB: g.Baseline.MemoryBlockMiB, Cursor: status.Status.Id, EvidenceRef: "cri:" + status.Status.Id, OwnerRef: ref, PlanDigest: plan})
		}
	}
	return claims, nil
}

func (p *Provider) podClaims(ctx context.Context, cluster string, g biz.SupplyGroup, pod pod, node NodeSource, snapshot nodeSnapshot, registered []RegisteredDevice, now time.Time) ([]biz.Claim, error) {
	assignments, err := ParseAssignments(pod.Metadata.Annotations[AssignedIDs])
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		return nil, nil
	}
	assigned, err := podAssignedTime(pod.Metadata.Annotations)
	if err != nil {
		return nil, err
	}
	var containers []container
	for _, c := range pod.Spec.Containers {
		if c.Resources.Limits["volcano.sh/vgpu-number"] != "" || c.Resources.Limits["volcano.sh/vgpu-memory"] != "" {
			containers = append(containers, c)
		}
	}
	if len(containers) != len(assignments) {
		return nil, biz.Invalid("PROVIDER_CONTAINER_MAPPING")
	}
	var claims []biz.Claim
	for i, container := range containers {
		podRef := biz.PodRef{ClusterID: cluster, Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name, UID: pod.Metadata.UID, ContainerName: container.Name}
		var previous []biz.Claim
		if p.repo != nil {
			if err := p.repo.View(ctx, func(r biz.Reader) error {
				var err error
				previous, _, err = r.PodEvidence(ctx, podRef)
				return err
			}); err != nil {
				return nil, err
			}
		}
		for _, a := range assignments[i] {
			known := false
			for _, d := range registered {
				if d.UUID == a.UUID {
					known = true
				}
			}
			if !known {
				return nil, biz.Invalid("PROVIDER_DEVICE_IDENTITY")
			}
			ref, plan := p.verifyOwner(podRef, pod.Metadata.Labels, pod.Metadata.Annotations)
			m, err := biz.NormalizeMemory(a.MemoryBlocks, "BLOCKS", g.Baseline.MemoryBlockMiB)
			if err != nil {
				return nil, err
			}
			c := biz.Claim{Pod: podRef, DeviceID: PhysicalID(cluster, node.UID, a.UUID), NodeUID: node.UID, Source: "scheduler", ObservedAt: assigned, ReceivedAt: now, State: biz.Active, MemoryMiB: m, CoreLimitPercent: a.CorePercent, BaselineDigest: g.Baseline.BaselineDigest, MemoryBlockMiB: g.Baseline.MemoryBlockMiB, OwnerRef: ref, PlanDigest: plan}
			// Owner signing follows Pod UID creation and may arrive after the
			// scheduler fact was saved. Preserve that fact's original ownership;
			// current verified ownership belongs to fresh runtime/allocation facts.
			for _, prior := range previous {
				if prior.Source == "scheduler" && prior.Pod == podRef && prior.DeviceID == c.DeviceID && prior.NodeUID == c.NodeUID && prior.ObservedAt.Equal(assigned) {
					c.OwnerRef, c.PlanDigest = prior.OwnerRef, prior.PlanDigest
					break
				}
			}
			// The revision belongs to this historical allocation fact. Pod
			// status/resourceVersion and poll reception time may change freely.
			revision := c
			revision.ReceivedAt = time.Time{}
			c.Cursor, err = biz.Hash(revision)
			if err != nil {
				return nil, err
			}
			c.EvidenceRef = "scheduler:" + pod.Metadata.UID + ":" + c.Cursor
			claims = append(claims, c)
			resources, resourcesOK := resourcesFor(snapshot, podRef.Namespace, podRef.Name, podRef.ContainerName)
			runtime, runtimeOK := runtimeFor(snapshot, podRef.UID, podRef.ContainerName)
			for _, status := range runtime {
				if !runtimeOK || status.Status.State != cri.ContainerState_CONTAINER_RUNNING {
					continue
				}
				alloc, err := ParseContainerdInfo(status.Info["info"])
				if err != nil {
					return nil, err
				}
				if alloc.UUID != a.UUID {
					return nil, biz.Invalid("PROVIDER_DEVICE_CONFLICT")
				}
				c.OwnerRef, c.PlanDigest = ref, plan
				c.Source = "runtime"
				c.ObservedAt = snapshot.ObservedAt
				c.Cursor = status.Status.Id
				c.MemoryMiB = alloc.MemoryMiB
				c.CoreLimitPercent = alloc.CorePercent
				c.EvidenceRef = "cri:" + status.Status.Id
				claims = append(claims, c)
				if resourcesOK && hasGPUResources(resources) {
					c.Source = "allocation"
					c.MemoryMiB = m
					c.CoreLimitPercent = a.CorePercent
					c.EvidenceRef = "podresources:" + podRef.UID + ":" + podRef.ContainerName
					claims = append(claims, c)
				}
			}
		}
	}
	return claims, nil
}

func (p *Provider) verifyOwner(pod biz.PodRef, labels, annotations map[string]string) (*biz.UsageRef, string) {
	key := p.ownerKey
	if labels["accelerator.ani.io/owner-service"] == "ani-resource-service" {
		key = p.resourceOwnerKey
	}
	return VerifyOwnerAssertion(pod, labels, annotations, key)
}
func (p *Provider) releaseProofs(ctx context.Context, cluster string, node NodeSource, snapshot nodeSnapshot, nodeOnline bool, now time.Time) ([]biz.ReleaseProof, error) {
	if !nodeOnline || !snapshot.ResourcesOK || !snapshot.RuntimeOK {
		return nil, nil
	}
	var result []biz.ReleaseProof
	err := p.repo.View(ctx, func(r biz.Reader) error {
		after := ""
		for {
			bindings, err := r.Bindings(ctx, biz.BindingFilter{ClusterID: cluster}, after, 201)
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				if binding.Pod == nil || binding.State == biz.ReleaseConfirmed {
					continue
				}
				claims, _, err := r.PodEvidence(ctx, *binding.Pod)
				if err != nil {
					return err
				}
				var claim *biz.Claim
				for i := range claims {
					// Historical assignment can remain unknown when a running
					// container's GPU env was invalid. Current positive CRI exit
					// and allocation absence can still retire that exact claim;
					// a previous valid runtime allocation is not required.
					if claims[i].Pod == *binding.Pod && claims[i].DeviceID == binding.PhysicalDeviceID && claims[i].NodeUID == node.UID {
						claim = &claims[i]
						break
					}
				}
				if claim == nil {
					continue
				}
				resources, ok := resourcesFor(snapshot, binding.Pod.Namespace, binding.Pod.Name, binding.Pod.ContainerName)
				if !ok || hasGPUResources(resources) {
					continue
				}
				statuses, ok := runtimeFor(snapshot, binding.Pod.UID, binding.Pod.ContainerName)
				if !ok {
					continue
				}
				unmapped := snapshot.UnknownPodContainers[binding.Pod.UID]
				if config := p.runtimeJournal; config != nil && config.NodeUID == node.UID && (needsJournalExit(statuses) || len(unmapped) > 0) {
					recovered, complete, err := recoverJournalExit(ctx, *config, *binding.Pod)
					if err != nil {
						continue
					}
					if complete {
						statuses = mergeJournalExits(statuses, recovered, snapshot.HistoricalStatuses, *binding.Pod)
						covered := map[string]bool{}
						for _, status := range statuses {
							if status.Status.State == cri.ContainerState_CONTAINER_EXITED && status.Status.FinishedAt > 0 {
								covered[status.Status.Id] = true
							}
						}
						remaining := []string{}
						for _, id := range unmapped {
							if !covered[id] {
								remaining = append(remaining, id)
							}
						}
						unmapped = remaining
					}
				}
				if len(statuses) == 0 || len(unmapped) > 0 {
					continue
				}
				exited := true
				var ids []string
				for _, status := range statuses {
					if status.Status.State != cri.ContainerState_CONTAINER_EXITED || status.Status.FinishedAt <= 0 {
						exited = false
					}
					ids = append(ids, status.Status.Id+":"+strconv.FormatInt(status.Status.FinishedAt, 10)+":"+status.Info["journal_evidence"])
				}
				if !exited {
					continue
				}
				sort.Strings(ids)
				digest, _ := biz.Hash(ids)
				result = append(result, biz.ReleaseProof{Pod: *binding.Pod, DeviceID: binding.PhysicalDeviceID, NodeUID: node.UID, ObservedAt: snapshot.ObservedAt, ReceivedAt: now, EvidenceRef: "release:" + digest, RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: claim.OwnerRef, PlanDigest: claim.PlanDigest})
			}
			if len(bindings) < 201 {
				return nil
			}
			after = bindings[len(bindings)-1].BindingID
		}
	})
	return result, err
}

func needsJournalExit(statuses []*cri.ContainerStatusResponse) bool {
	if len(statuses) == 0 {
		return true
	}
	for _, status := range statuses {
		if status.Status.State != cri.ContainerState_CONTAINER_EXITED || status.Status.FinishedAt <= 0 {
			return true
		}
	}
	return false
}

func mergeJournalExits(current, recovered []*cri.ContainerStatusResponse, historical map[string]bool, pod biz.PodRef) []*cri.ContainerStatusResponse {
	byID := map[string]*cri.ContainerStatusResponse{}
	for _, status := range current {
		byID[status.Status.Id] = status
	}
	for _, status := range recovered {
		if status == nil || status.Status == nil || status.Status.Id == "" || status.Status.Metadata == nil || status.Status.Metadata.Name != pod.ContainerName || status.Status.Labels["io.kubernetes.pod.uid"] != pod.UID || status.Status.Labels["io.kubernetes.pod.namespace"] != pod.Namespace || status.Status.Labels["io.kubernetes.pod.name"] != pod.Name || status.Status.State != cri.ContainerState_CONTAINER_EXITED || status.Status.FinishedAt <= 0 {
			return current
		}
		id := status.Status.Id
		if _, exists := byID[id]; !exists || historical[id] {
			byID[id] = status // Never replace a currently observed running attempt.
		}
	}
	result := make([]*cri.ContainerStatusResponse, 0, len(byID))
	for _, status := range byID {
		result = append(result, status)
	}
	return result
}
func (p *Provider) String() string {
	return fmt.Sprintf("volcano-hami-factor-v1 (%d managed connections)", len(p.connections))
}
