package provider

import (
	"context"
	"regexp"
	"sort"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type InstallationBaseline struct {
	Mode     biz.SupplyMode        `json:"mode"`
	Baseline *biz.ProviderBaseline `json:"baseline"`
}

var discoveryIssueCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)

func (p *Provider) InitializationEnvironment(ctx context.Context) (biz.InitializationEnvironment, error) {
	discovery, err := p.DiscoverInitialization(ctx)
	if err != nil {
		return discovery.Environment, err
	}
	if discovery.Environment.ConnectionRef == "" {
		return discovery.Environment, biz.Precondition("CONFIGURATION_REQUIRED")
	}
	return discovery.Environment, nil
}

func (p *Provider) DiscoverInitialization(ctx context.Context) (biz.InitializationDiscovery, error) {
	result := biz.InitializationDiscovery{ConfigurationState: "CONFIGURATION_REQUIRED", Environment: biz.InitializationEnvironment{ClusterDisplayName: "本集群", Baselines: map[string]biz.InitializationBaseline{}, Nodes: map[string]biz.InitializationNode{}}}
	if len(p.connections) != 1 {
		result.Checks = append(result.Checks, biz.InitializationCheck{Key: "connection", Source: "MANUAL_DECLARATION", State: "BLOCKED", Reason: "MANAGED_CONNECTION_REQUIRED"})
		return result, nil
	}
	var reader *kubeReader
	for reference, configured := range p.connections {
		result.Environment.ConnectionRef = reference
		reader = configured
	}
	nodes, err := reader.nodes(ctx)
	if err != nil {
		return result, err
	}
	if err := reader.validatePluginConfigNodes(nodes); err != nil {
		return result, err
	}
	views, err := p.loadTelemetry(ctx, result.Environment.ConnectionRef)
	if err != nil {
		return result, err
	}
	groups, err := p.initializationMembership(ctx, result.Environment.ConnectionRef)
	if err != nil {
		return result, err
	}
	for _, installed := range reader.config.InstallationBaselines {
		if err := biz.ValidateBaseline(installed.Baseline, installed.Mode); err != nil {
			return result, err
		}
		baseline := *installed.Baseline
		reference := baseline.BaselineID + ":" + baseline.BaselineDigest
		result.Environment.Baselines[reference] = biz.InitializationBaseline{Mode: installed.Mode, Baseline: &baseline}
	}
	result.Checks = append(result.Checks, biz.InitializationCheck{Key: "connection", Source: "AUTOMATIC_OBSERVATION", State: "PASS"})
	if len(result.Environment.Baselines) == 0 {
		result.Checks = append(result.Checks, biz.InitializationCheck{Key: "installation_baselines", Source: "MANUAL_DECLARATION", State: "UNKNOWN", Reason: "INSTALLATION_BASELINE_REQUIRED"})
	} else {
		result.ConfigurationState = "READY"
		result.Checks = append(result.Checks, biz.InitializationCheck{Key: "installation_baselines", Source: "MANUAL_DECLARATION", State: "PASS"})
	}
	plugins, pluginError := reader.selectedPods(ctx, reader.config.PluginNamespace, reader.config.PluginSelector)
	schedulers, schedulerError := reader.selectedPods(ctx, reader.config.SchedulerNamespace, reader.config.SchedulerSelector)
	for _, check := range []struct {
		key string
		ok  bool
	}{{"device_plugin", pluginError == nil && len(plugins) > 0}, {"scheduler", schedulerError == nil && len(schedulers) > 0}} {
		state, reason := "PASS", ""
		if !check.ok {
			state, reason = "UNKNOWN", "SOURCE_UNKNOWN"
		}
		result.Checks = append(result.Checks, biz.InitializationCheck{Key: check.key, Source: "AUTOMATIC_OBSERVATION", State: state, Reason: reason})
	}
	schedulerDigests := map[string]string{}
	for _, baseline := range result.Environment.Baselines {
		if _, found := schedulerDigests[baseline.Baseline.VolcanoVersion]; !found {
			digest, err := reader.schedulerConfigDigest(ctx, reader.config.SchedulerNamespace, reader.config.SchedulerConfigMap, baseline.Baseline.VolcanoVersion)
			if err == nil {
				schedulerDigests[baseline.Baseline.VolcanoVersion] = digest
			}
		}
	}
	type structuralNode struct {
		Node      biz.NodeRef
		GPUs      []biz.DiscoveredGPU
		Baselines []biz.DiscoveredBaseline
		GroupID   string
		Labels    map[string]string
	}
	structure := []structuralNode{}
	now := time.Now().UTC()
	sort.Slice(nodes, func(left, right int) bool {
		if nodes[left].Metadata.Name == nodes[right].Metadata.Name {
			return nodes[left].Metadata.UID < nodes[right].Metadata.UID
		}
		return nodes[left].Metadata.Name < nodes[right].Metadata.Name
	})
	for _, actual := range nodes {
		configMap, overridden := reader.config.pluginConfigMap(actual.Metadata.UID, actual.Metadata.Name)
		nodeDigest, nodeConfigError := reader.pluginConfigDigest(ctx, configMap, overridden)
		view, found := views[actual.Metadata.UID]
		item := biz.DiscoveredNode{Node: biz.NodeRef{UID: actual.Metadata.UID, Name: actual.Metadata.Name}, GroupID: groups[actual.Metadata.UID]}
		block := func(reason string) {
			for _, existing := range item.BlockingReasons {
				if existing == reason {
					return
				}
			}
			item.BlockingReasons = append(item.BlockingReasons, reason)
		}
		if !found || view.State.Identity.NodeName != actual.Metadata.Name {
			block("AGENT_UNOBSERVED")
		} else {
			item.AgentLeaseExpiresAt = view.State.LeaseExpiresAt
			item.AgentOnline = !view.State.Revoked && !view.State.Degraded && now.Before(view.State.LeaseExpiresAt)
			if !item.AgentOnline {
				block("AGENT_OFFLINE")
			}
			for _, sourceName := range []string{"nvml", "podresources", "cri"} {
				source, complete := telemetrySource(view, sourceName, now, p.freshness)
				if source.ObservedAt.IsZero() && view.State.Snapshot != nil {
					for _, raw := range view.State.Snapshot.Sources {
						if raw.Name == sourceName {
							source = raw
							break
						}
					}
				}
				source.Name = sourceName
				source.Complete = complete
				source.Issues = append([]string(nil), source.Issues...)
				for index, issue := range source.Issues {
					if !discoveryIssueCode.MatchString(issue) {
						source.Issues[index] = "SOURCE_UNKNOWN"
					}
				}
				if gap, found := view.State.Gaps[sourceName]; found {
					if gap.End.IsZero() || gap.End.After(source.ObservedAt) {
						source.Complete = false
					}
					source.Issues = append(source.Issues, "SOURCE_GAP")
				}
				if !source.Complete {
					source.Issues = append(source.Issues, "SOURCE_UNKNOWN")
					block("SOURCE_UNKNOWN")
				}
				item.Sources = append(item.Sources, source)
			}
		}
		if !reader.nodeOnline(ctx, actual, now, p.freshness) || actual.Metadata.DeletionTimestamp != "" {
			block("NODE_NOT_READY")
		}
		if item.GroupID != "" {
			block("NODE_ALREADY_ADOPTED")
		}
		if label := actual.Metadata.Labels[biz.GroupLabel]; label != "" && label != item.GroupID {
			block("NODE_LABEL_CONFLICT")
		}
		registered, registrationError := ParseNodeDevices(actual.Metadata.Annotations[NodeRegister])
		models := map[string]bool{}
		physical := map[string]biz.TelemetryDevice{}
		inventoryConflict := false
		if view.State.Snapshot != nil {
			for _, device := range view.State.Snapshot.Devices {
				if physicalUUID.MatchString(device.UUID) && device.PhysicalMemoryBytes > 0 {
					if _, duplicate := physical[device.UUID]; duplicate {
						inventoryConflict = true
						block("NVML_DEVICE_CONFLICT")
					}
					physical[device.UUID] = device
					models[modelKey(device.Model)] = true
					item.GPUs = append(item.GPUs, biz.DiscoveredGPU{UUID: device.UUID, ModelKey: modelKey(device.Model), PhysicalMemoryBytes: device.PhysicalMemoryBytes, BudgetReason: "SAFE_BUDGET_UNKNOWN"})
				}
			}
		}
		model := ""
		if len(models) == 1 {
			for value := range models {
				model = value
			}
		} else {
			block("NODE_MODEL_UNKNOWN")
		}
		if label := actual.Metadata.Labels[biz.ModelLabel]; label != "" && label != model {
			block("NODE_LABEL_CONFLICT")
		}
		budgetByDevice := map[string]int64{}
		if !inventoryConflict && registrationError == nil && pluginError == nil && schedulerError == nil && nodeConfigError == nil && model != "" && len(physical) == len(registered) && len(physical) > 0 {
			for reference, baseline := range result.Environment.Baselines {
				if label := actual.Metadata.Labels[biz.BaselineLabel]; label != "" && label != baseline.Baseline.BaselineID {
					continue
				}
				probe := biz.SupplyGroup{Mode: baseline.Mode, ModelKey: model, Baseline: baseline.Baseline}
				if nodeDigest != baseline.Baseline.NodeConfigDigest || schedulerDigests[baseline.Baseline.VolcanoVersion] != baseline.Baseline.SchedulerConfigDigest || !collectionUnitsMatch(probe, NodeSource{UID: item.Node.UID, Name: item.Node.Name}, registered, plugins, schedulers, reader.config) {
					continue
				}
				budgets := map[string]int64{}
				valid := true
				for _, device := range registered {
					inventory, ok := physical[device.UUID]
					if !ok || !device.Healthy || modelKey(inventory.Model) != modelKey(device.Model) {
						valid = false
						break
					}
					budget, err := deviceMemoryBudget(device.MemoryBlocks, baseline.Baseline.MemoryBlockMiB, inventory.PhysicalMemoryBytes)
					if err != nil || budget <= 0 {
						valid = false
						break
					}
					budgets[device.UUID] = budget
				}
				if valid {
					item.Baselines = append(item.Baselines, biz.DiscoveredBaseline{BaselineRef: reference, Mode: baseline.Mode, Source: "MANUAL_DECLARATION_WITH_OBSERVATION"})
					for uuid, budget := range budgets {
						budgetByDevice[uuid] = budget
					}
				}
			}
		}
		if len(item.GPUs) == 0 {
			block("NVML_UNAVAILABLE")
		}
		if len(item.Baselines) == 0 {
			block("INSTALLATION_UNVERIFIED")
		}
		for index := range item.GPUs {
			if budget, known := budgetByDevice[item.GPUs[index].UUID]; known {
				value := budget
				item.GPUs[index].SafeMemoryBudgetMiB = &value
				item.GPUs[index].BudgetReason = ""
			}
		}
		sort.Slice(item.GPUs, func(left, right int) bool { return item.GPUs[left].UUID < item.GPUs[right].UUID })
		sort.Slice(item.Baselines, func(left, right int) bool {
			return item.Baselines[left].BaselineRef < item.Baselines[right].BaselineRef
		})
		sort.Strings(item.BlockingReasons)
		item.Selectable = len(item.BlockingReasons) == 0
		candidate := biz.InitializationNode{Node: item.Node, ModelKey: model, GroupID: item.GroupID, Selectable: item.Selectable}
		if len(item.BlockingReasons) > 0 {
			candidate.BlockedReason = item.BlockingReasons[0]
		}
		for _, baseline := range item.Baselines {
			candidate.BaselineRefs = append(candidate.BaselineRefs, baseline.BaselineRef)
		}
		result.Environment.Nodes[item.Node.UID] = candidate
		labels := map[string]string{}
		for _, key := range []string{biz.GroupLabel, biz.ModelLabel, biz.BaselineLabel} {
			if value, found := actual.Metadata.Labels[key]; found {
				labels[key] = value
			}
		}
		structure = append(structure, structuralNode{Node: item.Node, GPUs: item.GPUs, Baselines: item.Baselines, GroupID: item.GroupID, Labels: labels})
		result.Nodes = append(result.Nodes, item)
	}
	revision, err := biz.Hash(struct {
		Connection struct{ Ref, APIServer, ServerName string }
		Baselines  map[string]biz.InitializationBaseline
		Nodes      []structuralNode
	}{struct{ Ref, APIServer, ServerName string }{result.Environment.ConnectionRef, reader.config.APIServer, reader.config.ServerName}, result.Environment.Baselines, structure})
	if err != nil {
		return result, err
	}
	result.Environment.DiscoveryRevision = revision
	return result, nil
}

func (p *Provider) initializationMembership(ctx context.Context, connection string) (map[string]string, error) {
	membership := map[string]string{}
	if p.repo == nil {
		return membership, biz.Unavailable()
	}
	err := p.repo.View(ctx, func(reader biz.Reader) error {
		clusters, err := reader.Clusters(ctx, []string{connection}, "", 2)
		if err != nil {
			return err
		}
		for _, cluster := range clusters {
			for poolAfter := ""; ; {
				pools, err := reader.Pools(ctx, cluster.ClusterID, poolAfter, 201)
				if err != nil {
					return err
				}
				for _, pool := range pools {
					for groupAfter := ""; ; {
						groups, err := reader.Groups(ctx, pool.PoolID, groupAfter, 201)
						if err != nil {
							return err
						}
						for _, group := range groups {
							for _, node := range group.Nodes {
								membership[node.UID] = group.GroupID
							}
						}
						if len(groups) < 201 {
							break
						}
						groupAfter = groups[len(groups)-1].GroupID
					}
				}
				if len(pools) < 201 {
					break
				}
				poolAfter = pools[len(pools)-1].PoolID
			}
		}
		return nil
	})
	return membership, err
}
