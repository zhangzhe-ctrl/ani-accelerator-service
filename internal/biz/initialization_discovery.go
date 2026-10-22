package biz

import (
	"context"
	"sort"
	"time"
)

type InitializationCheck struct {
	Key    string
	Source string
	State  string
	Reason string
}

type Initialization struct {
	Registered            bool
	ConnectionRef         string
	ClusterID             string
	DisplayName           string
	ConfigurationState    string
	Checks                []InitializationCheck
	LatestOperation       *InitializationOperation
	ConfigurationComplete bool
}

type DiscoveredGPU struct {
	UUID                string
	ModelKey            string
	PhysicalMemoryBytes uint64
	SafeMemoryBudgetMiB *int64
	BudgetReason        string
}

type DiscoveredBaseline struct {
	BaselineRef string
	Mode        SupplyMode
	Source      string
}

type DiscoveredNode struct {
	Node                NodeRef
	GroupID             string
	GPUs                []DiscoveredGPU
	Baselines           []DiscoveredBaseline
	AgentOnline         bool
	AgentLeaseExpiresAt time.Time
	Sources             []TelemetrySource
	Selectable          bool
	BlockingReasons     []string
}

type DiscoveredNodes struct {
	Items             []DiscoveredNode
	Total             uint64
	DiscoveryRevision string
}

type InitializationDiscovery struct {
	Environment        InitializationEnvironment
	Nodes              []DiscoveredNode
	Checks             []InitializationCheck
	ConfigurationState string
}

type InitializationDiscoveryProvider interface {
	DiscoverInitialization(context.Context) (InitializationDiscovery, error)
}

type InitializationSummaryRepository interface {
	LatestInitializationOperation(context.Context, string) (*InitializationOperation, error)
}

func (usecase *InitializationUsecase) discovery(ctx context.Context) (InitializationDiscovery, error) {
	provider, ok := usecase.provider.(InitializationDiscoveryProvider)
	if !ok {
		return InitializationDiscovery{}, Unavailable()
	}
	discovery, err := provider.DiscoverInitialization(ctx)
	if err != nil {
		return discovery, err
	}
	if discovery.Environment.ConnectionRef != "" && !usecase.catalog.connections[discovery.Environment.ConnectionRef] {
		return InitializationDiscovery{}, NotFound()
	}
	return discovery, nil
}

func (usecase *InitializationUsecase) GetInitialization(ctx context.Context, read *AdminRead) (*Initialization, error) {
	if err := AuthorizeAdmin(ctx, read, "GetInitialization"); err != nil {
		return nil, err
	}
	discovery, err := usecase.discovery(ctx)
	if err != nil {
		return nil, err
	}
	output := &Initialization{ConnectionRef: discovery.Environment.ConnectionRef, DisplayName: discovery.Environment.ClusterDisplayName, ConfigurationState: discovery.ConfigurationState, Checks: discovery.Checks}
	if output.ConfigurationState == "" {
		output.ConfigurationState = "UNKNOWN"
	}
	if output.ConnectionRef == "" {
		output.ConfigurationState = "CONFIGURATION_REQUIRED"
		return output, nil
	}
	err = usecase.catalog.repo.View(ctx, func(reader Reader) error {
		clusters, err := reader.Clusters(ctx, []string{output.ConnectionRef}, "", 2)
		if err != nil {
			return err
		}
		if len(clusters) > 1 {
			return Precondition("INITIALIZATION_SCOPE_INVALID")
		}
		if len(clusters) == 0 {
			return nil
		}
		output.Registered = true
		output.ClusterID = clusters[0].ClusterID
		output.DisplayName = clusters[0].DisplayName
		cursor := ""
		for {
			profiles, err := reader.Profiles(ctx, output.ClusterID, 0, cursor, 200)
			if err != nil {
				return err
			}
			for _, profile := range profiles {
				if profile.Spec == nil || profile.State != ProfileActive && profile.State != ProfileDisabled {
					continue
				}
				group, err := reader.Group(ctx, profile.Spec.GroupID)
				if err != nil {
					return err
				}
				if group.ClusterID == output.ClusterID {
					output.ConfigurationComplete = true
					return nil
				}
			}
			if len(profiles) < 200 {
				return nil
			}
			next := profiles[len(profiles)-1].ProfileID
			if next == cursor {
				return Precondition("INITIALIZATION_SCOPE_INVALID")
			}
			cursor = next
		}
	})
	if err != nil {
		return nil, err
	}
	if summaries, ok := usecase.repo.(InitializationSummaryRepository); ok {
		output.LatestOperation, err = summaries.LatestInitializationOperation(ctx, output.ConnectionRef)
		if err != nil {
			return nil, err
		}
	}
	return output, nil
}

func (usecase *InitializationUsecase) ListDiscoveredNodes(ctx context.Context, read *AdminRead, page, size uint32) (*DiscoveredNodes, error) {
	if err := AuthorizeAdmin(ctx, read, "ListDiscoveredNodes"); err != nil {
		return nil, err
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	if size > 200 {
		return nil, Invalid("INVALID_DISCOVERY_PAGE")
	}
	discovery, err := usecase.discovery(ctx)
	if err != nil {
		return nil, err
	}
	nodes := append([]DiscoveredNode{}, discovery.Nodes...)
	sort.Slice(nodes, func(first, second int) bool {
		if nodes[first].Node.Name != nodes[second].Node.Name {
			return nodes[first].Node.Name < nodes[second].Node.Name
		}
		return nodes[first].Node.UID < nodes[second].Node.UID
	})
	result := &DiscoveredNodes{Items: []DiscoveredNode{}, Total: uint64(len(nodes)), DiscoveryRevision: discovery.Environment.DiscoveryRevision}
	start := (uint64(page) - 1) * uint64(size)
	if start < uint64(len(nodes)) {
		end := start + uint64(size)
		if end > uint64(len(nodes)) {
			end = uint64(len(nodes))
		}
		result.Items = append(result.Items, nodes[start:end]...)
	}
	return result, nil
}
