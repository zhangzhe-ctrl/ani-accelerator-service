package provider

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type Provider struct {
	connections map[string]*kubeReader
	inventory   inventoryReader
	evidence    *EvidenceStore
	ownerKey    ed25519.PublicKey
	repo        biz.Repository
	freshness   time.Duration
}

func New(config Config, evidence *EvidenceStore, ownerKey ed25519.PublicKey, repo biz.Repository, freshness time.Duration) (*Provider, error) {
	p := &Provider{connections: map[string]*kubeReader{}, inventory: nvmlInventory{}, evidence: evidence, ownerKey: ownerKey, repo: repo, freshness: freshness}
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
	schedulerDigest, err := k.configDigest(ctx, k.config.SchedulerNamespace, k.config.SchedulerConfigMap)
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
	for _, want := range g.Nodes {
		var found *node
		for i := range ns {
			if ns[i].Metadata.UID == want.UID && ns[i].Metadata.Name == want.Name {
				found = &ns[i]
				break
			}
		}
		if found == nil || !online(*found, time.Now(), p.freshness) {
			return inspection, biz.Precondition("NODE_UNKNOWN")
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
		if g.Baseline.Verification != nil && g.Baseline.Verification.State == biz.Verified {
			att, err := p.evidence.Load(g.Baseline.Verification.EvidenceRef)
			if err != nil {
				return inspection, err
			}
			devices := map[string]bool{}
			for _, d := range att.Devices {
				if d.NodeUID == want.UID {
					devices[d.VendorUUID] = true
				}
			}
			if len(devices) != len(ds) {
				return inspection, biz.Precondition("ATTESTATION_DEVICE_MISMATCH")
			}
			for _, d := range ds {
				if !devices[d.UUID] {
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
		nodeOK := ne == nil && pe == nil && n != nil && online(*n, now, p.freshness)
		snapshot := readNode(ctx, source)
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
		if !nodeOK {
			continue
		}
		registered, err := ParseNodeDevices(n.Metadata.Annotations[NodeRegister])
		if err != nil {
			batch.Watermarks[len(batch.Watermarks)-3].Complete = false
			batch.Watermarks[len(batch.Watermarks)-3].Issue = "PROVIDER_NODE_FORMAT"
			continue
		}
		handshake, err := ParseHandshake(n.Metadata.Annotations[NodeHandshake])
		if err != nil {
			batch.Watermarks[len(batch.Watermarks)-3].Complete = false
			continue
		}
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
		if !inspected[group.GroupID] {
			batch.Watermarks[len(batch.Watermarks)-3].Complete = false
			batch.Watermarks[len(batch.Watermarks)-3].Issue = "CONFIG_DRIFT"
			continue // Never reinterpret old assignments using a drifted factor.
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
			complete := physical != nil && snapshot.ResourcesOK && snapshot.RuntimeOK && now.Sub(handshake) <= p.freshness
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
			if !complete {
				issues = append(issues, "SOURCE_UNKNOWN")
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
			claims, err := p.podClaims(c.ClusterID, *group, pod, source, snapshot, registered, now)
			if err != nil {
				batch.Watermarks[len(batch.Watermarks)-3].Complete = false
				batch.Watermarks[len(batch.Watermarks)-3].Issue = "PROVIDER_POD_INVALID"
				continue
			}
			batch.Claims = append(batch.Claims, claims...)
		}
		proofs, err := p.releaseProofs(ctx, c.ClusterID, source, snapshot, nodeOK, now)
		if err != nil {
			batch.Watermarks[len(batch.Watermarks)-1].Complete = false
		} else {
			batch.Releases = append(batch.Releases, proofs...)
		}
	}
	return batch, nil
}
func (p *Provider) podClaims(cluster string, g biz.SupplyGroup, pod pod, node NodeSource, snapshot nodeSnapshot, registered []RegisteredDevice, now time.Time) ([]biz.Claim, error) {
	assignments, err := ParseAssignments(pod.Metadata.Annotations[AssignedIDs])
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		return nil, nil
	}
	assigned, err := ParseAssignedTime(pod.Metadata.Annotations[AssignedTime])
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
			podRef := biz.PodRef{ClusterID: cluster, Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name, UID: pod.Metadata.UID, ContainerName: container.Name}
			ref, plan := VerifyOwnerAssertion(podRef, pod.Metadata.Labels, pod.Metadata.Annotations, p.ownerKey)
			m, err := biz.NormalizeMemory(a.MemoryBlocks, "BLOCKS", g.Baseline.MemoryBlockMiB)
			if err != nil {
				return nil, err
			}
			c := biz.Claim{Pod: podRef, DeviceID: PhysicalID(cluster, node.UID, a.UUID), NodeUID: node.UID, Source: "scheduler", Cursor: pod.Metadata.ResourceVersion, ObservedAt: assigned, ReceivedAt: now, State: biz.Active, MemoryMiB: m, CoreLimitPercent: a.CorePercent, BaselineDigest: g.Baseline.BaselineDigest, MemoryBlockMiB: g.Baseline.MemoryBlockMiB, EvidenceRef: "scheduler:" + pod.Metadata.UID + ":" + pod.Metadata.ResourceVersion, OwnerRef: ref, PlanDigest: plan}
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
				if binding.Pod == nil {
					continue
				}
				claims, _, err := r.PodEvidence(ctx, *binding.Pod)
				if err != nil {
					return err
				}
				var claim *biz.Claim
				for i := range claims {
					if claims[i].DeviceID == binding.PhysicalDeviceID && claims[i].NodeUID == node.UID && claims[i].Source == "runtime" {
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
				if !ok || len(statuses) == 0 {
					continue
				}
				exited := true
				var ids []string
				for _, status := range statuses {
					if status.Status.State != cri.ContainerState_CONTAINER_EXITED || status.Status.FinishedAt <= 0 {
						exited = false
					}
					ids = append(ids, status.Status.Id)
				}
				if !exited {
					continue
				}
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
func (p *Provider) String() string {
	return fmt.Sprintf("volcano-hami-factor-v1 (%d managed connections)", len(p.connections))
}
