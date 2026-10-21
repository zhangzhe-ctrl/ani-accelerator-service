package service

import (
	"context"
	"sort"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/protobuf/proto"
)

func (handler *Accelerator) GetInitialization(ctx context.Context, request *pb.GetInitializationRequest) (*pb.Initialization, error) {
	if err := handler.checkInitializationRequest(request); err != nil {
		return nil, err
	}
	result, err := handler.initialization.GetInitialization(ctx, initializationRead(request.Context))
	if err != nil {
		return nil, transportError(err)
	}
	if result == nil {
		return nil, transportError(biz.Unavailable())
	}
	output := &pb.Initialization{Registered: result.Registered, ConnectionRef: result.ConnectionRef, ClusterId: result.ClusterID, DisplayName: result.DisplayName, ConfigurationState: result.ConfigurationState, LatestOperation: initializationOperation(result.LatestOperation), ConfigurationComplete: result.ConfigurationComplete}
	for _, check := range result.Checks {
		output.Checks = append(output.Checks, &pb.InitializationCheck{Key: check.Key, Source: check.Source, State: check.State, Reason: check.Reason})
	}
	return output, nil
}

func (handler *Accelerator) ListDiscoveredNodes(ctx context.Context, request *pb.ListDiscoveredNodesRequest) (*pb.DiscoveredNodes, error) {
	if err := handler.checkInitializationRequest(request); err != nil {
		return nil, err
	}
	result, err := handler.initialization.ListDiscoveredNodes(ctx, initializationRead(request.Context), request.Page, request.PageSize)
	if err != nil {
		return nil, transportError(err)
	}
	if result == nil {
		return nil, transportError(biz.Unavailable())
	}
	output := &pb.DiscoveredNodes{Total: result.Total, DiscoveryRevision: result.DiscoveryRevision}
	for _, node := range result.Items {
		mapped := &pb.DiscoveredNode{Node: &pb.NodeRef{Uid: node.Node.UID, Name: node.Node.Name}, GroupId: node.GroupID, AgentOnline: node.AgentOnline, AgentLeaseExpiresAt: initializationTime(node.AgentLeaseExpiresAt), Selectable: node.Selectable, BlockingReasons: node.BlockingReasons}
		for _, gpu := range node.GPUs {
			mapped.Gpus = append(mapped.Gpus, &pb.DiscoveredGPU{Uuid: gpu.UUID, ModelKey: gpu.ModelKey, PhysicalMemoryBytes: gpu.PhysicalMemoryBytes, SafeMemoryBudgetMib: gpu.SafeMemoryBudgetMiB, BudgetReason: gpu.BudgetReason})
		}
		for _, baseline := range node.Baselines {
			mapped.Baselines = append(mapped.Baselines, &pb.DiscoveredBaseline{BaselineRef: baseline.BaselineRef, Mode: pb.SupplyMode(baseline.Mode).String(), Source: baseline.Source})
		}
		for _, source := range node.Sources {
			mapped.Sources = append(mapped.Sources, &pb.NodeSource{Source: source.Name, ObservedAt: initializationTime(source.ObservedAt), ReceivedAt: initializationTime(source.ReceivedAt), Complete: source.Complete, Issues: source.Issues})
		}
		output.Items = append(output.Items, mapped)
	}
	return output, nil
}

func (handler *Accelerator) checkInitializationRequest(request proto.Message) error {
	if request == nil || !request.ProtoReflect().IsValid() || unknownFields(request.ProtoReflect()) {
		return transportError(biz.Invalid("INVALID_INITIALIZATION_REQUEST"))
	}
	if handler.initialization == nil {
		return transportError(biz.Unavailable())
	}
	return nil
}

func initializationRead(input *pb.AdminRead) *biz.AdminRead {
	if input == nil {
		return nil
	}
	output := &biz.AdminRead{RequestID: input.RequestId}
	if input.Actor != nil {
		output.Actor = &biz.Actor{Type: input.Actor.Type, ID: input.Actor.Id}
	}
	return output
}

func initializationMutation(input *pb.AdminMutation) *biz.AdminMutation {
	if input == nil {
		return nil
	}
	return &biz.AdminMutation{Context: initializationRead(input.Context), IdempotencyKey: input.IdempotencyKey}
}

func initializationInput(request *pb.CreateInitializationRequest) (biz.InitializationInput, error) {
	output := biz.InitializationInput{DiscoveryRevision: request.DiscoveryRevision}
	for _, pool := range request.Pools {
		if pool == nil {
			return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		mapped := biz.InitializationPoolInput{ClientKey: pool.ClientKey}
		switch target := pool.Pool.(type) {
		case *pb.InitializationPoolInput_ExistingPoolId:
			if target == nil {
				return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			mapped.ExistingPoolID = target.ExistingPoolId
		case *pb.InitializationPoolInput_NewPool:
			if target == nil || target.NewPool == nil {
				return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			mapped.NewPool = &biz.InitializationNewPool{DisplayName: target.NewPool.DisplayName}
		default:
			return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		output.Pools = append(output.Pools, mapped)
	}
	for _, group := range request.Groups {
		if group == nil {
			return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		mapped := biz.InitializationGroupInput{ClientKey: group.ClientKey}
		switch target := group.Group.(type) {
		case *pb.InitializationGroupInput_ExistingGroup:
			if target == nil || target.ExistingGroup == nil {
				return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			mapped.ExistingGroup = &biz.InitializationExistingGroup{GroupID: target.ExistingGroup.GroupId, ExpectedVersion: target.ExistingGroup.ExpectedVersion}
		case *pb.InitializationGroupInput_NewGroup:
			if target == nil || target.NewGroup == nil {
				return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			fresh := target.NewGroup
			mapped.NewGroup = &biz.InitializationNewGroup{PoolKey: fresh.PoolKey, DisplayName: fresh.DisplayName, BaselineRef: fresh.BaselineRef, QueueName: fresh.QueueName}
			for _, node := range fresh.Nodes {
				if node == nil {
					return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
				}
				mapped.NewGroup.Nodes = append(mapped.NewGroup.Nodes, biz.NodeRef{UID: node.Uid, Name: node.Name})
			}
		default:
			return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		output.Groups = append(output.Groups, mapped)
	}
	for _, profile := range request.Profiles {
		if profile == nil {
			return output, biz.Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		output.Profiles = append(output.Profiles, biz.InitializationProfileInput{ClientKey: profile.ClientKey, GroupKey: profile.GroupKey, DisplayName: profile.DisplayName, MemoryMiBPerDevice: profile.MemoryMibPerDevice, CoreLimitPercent: profile.CoreLimitPercent, MaxDevicesPerReplica: profile.MaxDevicesPerReplica})
	}
	return output, nil
}

func (handler *Accelerator) CreateInitialization(ctx context.Context, request *pb.CreateInitializationRequest) (*pb.InitializationOperation, error) {
	if err := handler.checkInitializationRequest(request); err != nil {
		return nil, err
	}
	input, err := initializationInput(request)
	if err != nil {
		return nil, transportError(err)
	}
	operation, err := handler.initialization.Create(ctx, initializationMutation(request.Mutation), input)
	if err != nil {
		return nil, transportError(err)
	}
	return initializationOperation(operation), nil
}

func (handler *Accelerator) GetInitializationOperation(ctx context.Context, request *pb.GetInitializationOperationRequest) (*pb.InitializationOperation, error) {
	if err := handler.checkInitializationRequest(request); err != nil {
		return nil, err
	}
	operation, err := handler.initialization.Get(ctx, initializationRead(request.Context), request.OperationId)
	if err != nil {
		return nil, transportError(err)
	}
	return initializationOperation(operation), nil
}

func (handler *Accelerator) RetryInitializationOperation(ctx context.Context, request *pb.RetryInitializationOperationRequest) (*pb.InitializationOperation, error) {
	if err := handler.checkInitializationRequest(request); err != nil {
		return nil, err
	}
	operation, err := handler.initialization.Retry(ctx, initializationMutation(request.Mutation), request.OperationId, request.ExpectedVersion)
	if err != nil {
		return nil, transportError(err)
	}
	return initializationOperation(operation), nil
}

func initializationOperation(input *biz.InitializationOperation) *pb.InitializationOperation {
	if input == nil {
		return nil
	}
	output := &pb.InitializationOperation{OperationId: input.OperationID, Version: input.Version, State: string(input.State), CreatedAt: initializationTime(input.CreatedAt), UpdatedAt: initializationTime(input.UpdatedAt), CurrentStep: input.CurrentStep, Retryable: input.Retryable, Reason: input.Reason, RequiredActions: input.RequiredActions, ClusterId: input.ClusterID}
	keys := make([]string, 0, len(input.PoolIDs))
	for key := range input.PoolIDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		output.Pools = append(output.Pools, &pb.InitializationPoolResult{ClientKey: key, PoolId: input.PoolIDs[key]})
	}
	keys = keys[:0]
	for key := range input.Groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := input.Groups[key]
		admission := input.Plan.Groups[key].Admission
		if group.AutoOpenResult == "OPENED" {
			admission = biz.Open
		}
		output.Groups = append(output.Groups, &pb.InitializationGroupResult{ClientKey: key, GroupId: group.GroupID, RecordedGroupVersion: group.RecordedGroupVersion, AutoOpenResult: group.AutoOpenResult, Admission: pb.AdmissionState(admission).String()})
	}
	keys = keys[:0]
	for key := range input.Profiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		profile := input.Profiles[key]
		output.Profiles = append(output.Profiles, &pb.InitializationProfileResult{ClientKey: key, ProfileId: profile.ProfileID, RecordedRevision: profile.Revision, RecordedState: pb.ProfileState(profile.State).String()})
	}
	for _, step := range input.Steps {
		output.Steps = append(output.Steps, &pb.InitializationStep{StepId: step.StepID, ObjectKey: step.ObjectKey, Kind: step.Kind, State: step.State, Reason: step.Reason, ResourceId: step.ResourceID, InputGroupVersion: step.InputGroupVersion, OutputGroupVersion: step.OutputGroupVersion})
	}
	return output
}

func initializationTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
