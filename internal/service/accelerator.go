package service

import (
	"context"
	"encoding/json"
	"errors"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Accelerator struct {
	pb.UnimplementedAcceleratorAdminServiceServer
	pb.UnimplementedAcceleratorCatalogServiceServer
	pb.UnimplementedAcceleratorUsageServiceServer
	u              *biz.Usecase
	initialization *biz.InitializationUsecase
}

func NewAccelerator(u *biz.Usecase, initialization ...*biz.InitializationUsecase) *Accelerator {
	handler := &Accelerator{u: u}
	if len(initialization) == 1 {
		handler.initialization = initialization[0]
	}
	return handler
}

// The transport bridge uses the standard Go JSON representation (numeric
// enums and integers), not protobuf JSON or canonical digest serialization.
// Domain validation is always performed after this copy. No generated type
// crosses the service/biz boundary, and no opaque JSON enters a use case.
func invoke[O any, I any, D any](request proto.Message, run func(*I) (*D, error)) (*O, error) {
	if request == nil || !request.ProtoReflect().IsValid() || unknownFields(request.ProtoReflect()) {
		return nil, kerrors.BadRequest("INVALID_REQUEST", "INVALID_REQUEST")
	}
	b, err := json.Marshal(request)
	if err != nil {
		return nil, kerrors.BadRequest("INVALID_REQUEST", "INVALID_REQUEST")
	}
	var input I
	if err = json.Unmarshal(b, &input); err != nil {
		return nil, kerrors.BadRequest("INVALID_REQUEST", "INVALID_REQUEST")
	}
	result, err := run(&input)
	if err != nil {
		return nil, transportError(err)
	}
	if result == nil {
		return nil, kerrors.InternalServer("INTERNAL_ERROR", "INTERNAL_ERROR")
	}
	b, err = json.Marshal(result)
	if err != nil {
		return nil, kerrors.InternalServer("INTERNAL_ERROR", "INTERNAL_ERROR")
	}
	out := new(O)
	if json.Unmarshal(b, out) != nil {
		return nil, kerrors.InternalServer("INTERNAL_ERROR", "INTERNAL_ERROR")
	}
	return out, nil
}
func transportError(err error) error {
	var e *biz.Error
	if !errors.As(err, &e) {
		return kerrors.InternalServer("INTERNAL_ERROR", "INTERNAL_ERROR")
	}
	code := int32(500)
	switch e.Kind {
	case "invalid":
		code = 400
	case "precondition":
		code = 412
	case "conflict":
		code = 409
	case "denied":
		code = 403
	case "not_found":
		code = 404
	case "unavailable":
		code = 503
	}
	// Stable reason is carried in google.rpc.ErrorInfo by Kratos.
	return kerrors.New(int(code), e.Reason, e.Reason)
}

type mutationInput struct {
	Mutation *biz.AdminMutation `json:"mutation"`
}
type adminInput struct {
	Context          *biz.AdminRead `json:"context"`
	Page             *biz.Page      `json:"page"`
	ClusterID        string         `json:"cluster_id"`
	PoolID           string         `json:"pool_id"`
	GroupID          string         `json:"group_id"`
	PhysicalDeviceID string         `json:"physical_device_id"`
	ProfileID        string         `json:"profile_id"`
	ProfileVersion   uint64         `json:"profile_version"`
	IncludeDevices   bool           `json:"include_devices"`
}
type tenantInput struct {
	Context        *biz.TenantContext `json:"context"`
	Page           *biz.Page          `json:"page"`
	ClusterID      string             `json:"cluster_id"`
	ProfileID      string             `json:"profile_id"`
	ProfileVersion uint64             `json:"profile_version"`
	Mode           biz.SupplyMode     `json:"mode"`
	Gpu            *biz.GpuRequest    `json:"gpu"`
	Ref            *biz.UsageRef      `json:"ref"`
	OwnerService   string             `json:"owner_service"`
	State          biz.UsageState     `json:"state"`
}

func (s *Accelerator) RegisterCluster(ctx context.Context, q *pb.RegisterClusterRequest) (*pb.Cluster, error) {
	type input struct {
		mutationInput
		DisplayName   string `json:"display_name"`
		ConnectionRef string `json:"connection_ref"`
	}
	return invoke[pb.Cluster](q, func(a *input) (*biz.Cluster, error) {
		return s.u.RegisterCluster(ctx, a.Mutation, a.DisplayName, a.ConnectionRef)
	})
}
func (s *Accelerator) ListClusters(ctx context.Context, q *pb.ListClustersRequest) (*pb.ListClustersResponse, error) {
	return invoke[pb.ListClustersResponse](q, func(a *adminInput) (*biz.ListResult[biz.Cluster], error) {
		return s.u.ListClusters(ctx, a.Context, a.Page)
	})
}
func (s *Accelerator) CreatePool(ctx context.Context, q *pb.CreatePoolRequest) (*pb.Pool, error) {
	type input struct {
		mutationInput
		DisplayName string `json:"display_name"`
		ClusterID   string `json:"cluster_id"`
	}
	return invoke[pb.Pool](q, func(a *input) (*biz.Pool, error) { return s.u.CreatePool(ctx, a.Mutation, a.ClusterID, a.DisplayName) })
}
func (s *Accelerator) ListPools(ctx context.Context, q *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	return invoke[pb.ListPoolsResponse](q, func(a *adminInput) (*biz.ListResult[biz.Pool], error) {
		return s.u.ListPools(ctx, a.Context, a.ClusterID, a.Page)
	})
}
func (s *Accelerator) AdoptSupplyGroup(ctx context.Context, q *pb.AdoptSupplyGroupRequest) (*pb.SupplyGroup, error) {
	type input struct {
		mutationInput
		biz.AdoptInput
	}
	return invoke[pb.SupplyGroup](q, func(a *input) (*biz.SupplyGroup, error) { return s.u.AdoptSupplyGroup(ctx, a.Mutation, a.AdoptInput) })
}
func (s *Accelerator) ListSupplyGroups(ctx context.Context, q *pb.ListSupplyGroupsRequest) (*pb.ListSupplyGroupsResponse, error) {
	return invoke[pb.ListSupplyGroupsResponse](q, func(a *adminInput) (*biz.ListResult[biz.SupplyGroup], error) {
		return s.u.ListSupplyGroups(ctx, a.Context, a.PoolID, a.Page)
	})
}
func (s *Accelerator) SetAdmission(ctx context.Context, q *pb.SetAdmissionRequest) (*pb.SupplyGroup, error) {
	type input struct {
		mutationInput
		biz.AdmissionInput
	}
	return invoke[pb.SupplyGroup](q, func(a *input) (*biz.SupplyGroup, error) { return s.u.SetAdmission(ctx, a.Mutation, a.AdmissionInput) })
}
func (s *Accelerator) CreateProfile(ctx context.Context, q *pb.CreateProfileRequest) (*pb.GpuProfile, error) {
	type input struct {
		mutationInput
		biz.CreateProfileInput
	}
	return invoke[pb.GpuProfile](q, func(a *input) (*biz.Profile, error) { return s.u.CreateProfile(ctx, a.Mutation, a.CreateProfileInput) })
}
func (s *Accelerator) AdminListProfiles(ctx context.Context, q *pb.AdminListProfilesRequest) (*pb.ListProfilesResponse, error) {
	return invoke[pb.ListProfilesResponse](q, func(a *adminInput) (*biz.ProfileListResult, error) {
		return s.u.AdminListProfiles(ctx, a.Context, a.ClusterID, a.Page)
	})
}
func (s *Accelerator) AdminListBindings(ctx context.Context, q *pb.AdminListBindingsRequest) (*pb.ListBindingsResponse, error) {
	return invoke[pb.ListBindingsResponse](q, func(a *adminInput) (*biz.ListResult[biz.Binding], error) {
		return s.u.AdminListBindings(ctx, a.Context, a.ClusterID, a.PhysicalDeviceID, a.Page)
	})
}
func (s *Accelerator) ListDevices(ctx context.Context, q *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	return invoke[pb.ListDevicesResponse](q, func(a *adminInput) (*biz.ListResult[biz.PhysicalDevice], error) {
		return s.u.ListDevices(ctx, a.Context, a.ClusterID, a.GroupID, a.Page)
	})
}
func (s *Accelerator) AdminGetCapacity(ctx context.Context, q *pb.AdminGetCapacityRequest) (*pb.CapacityView, error) {
	return invoke[pb.CapacityView](q, func(a *adminInput) (*biz.CapacityView, error) {
		return s.u.AdminGetCapacity(ctx, a.Context, a.ProfileID, a.ProfileVersion, a.IncludeDevices)
	})
}
func (s *Accelerator) ListProfiles(ctx context.Context, q *pb.ListProfilesRequest) (*pb.ListProfilesResponse, error) {
	return invoke[pb.ListProfilesResponse](q, func(a *tenantInput) (*biz.ProfileListResult, error) {
		return s.u.ListProfiles(ctx, a.Context, a.ClusterID, a.Mode, a.Page)
	})
}
func (s *Accelerator) GetProfile(ctx context.Context, q *pb.GetProfileRequest) (*pb.GpuProfile, error) {
	return invoke[pb.GpuProfile](q, func(a *tenantInput) (*biz.Profile, error) {
		return s.u.GetProfile(ctx, a.Context, a.ProfileID, a.ProfileVersion)
	})
}
func (s *Accelerator) ResolveGpuRequest(ctx context.Context, q *pb.ResolveGpuRequestRequest) (*pb.ResolvedGpuRequest, error) {
	return invoke[pb.ResolvedGpuRequest](q, func(a *tenantInput) (*biz.ResolvedGpuRequest, error) { return s.u.Resolve(ctx, a.Context, a.Gpu) })
}
func (s *Accelerator) GetCapacity(ctx context.Context, q *pb.GetCapacityRequest) (*pb.CapacityView, error) {
	return invoke[pb.CapacityView](q, func(a *tenantInput) (*biz.CapacityView, error) {
		return s.u.GetCapacity(ctx, a.Context, a.ProfileID, a.ProfileVersion)
	})
}
func (s *Accelerator) CheckGpuFit(ctx context.Context, q *pb.CheckGpuFitRequest) (*pb.GpuFit, error) {
	return invoke[pb.GpuFit](q, func(a *tenantInput) (*biz.GpuFit, error) { return s.u.CheckFit(ctx, a.Context, a.Gpu) })
}
func (s *Accelerator) SyncGpuUsage(ctx context.Context, q *pb.SyncGpuUsageRequest) (*pb.SyncGpuUsageResponse, error) {
	type input struct {
		RequestID  string               `json:"request_id"`
		Projection *biz.UsageProjection `json:"projection"`
	}
	return invoke[pb.SyncGpuUsageResponse](q, func(a *input) (*biz.SyncResult, error) { return s.u.SyncUsage(ctx, a.RequestID, a.Projection) })
}
func (s *Accelerator) GetGpuUsage(ctx context.Context, q *pb.GetGpuUsageRequest) (*pb.GpuUsageProjection, error) {
	return invoke[pb.GpuUsageProjection](q, func(a *tenantInput) (*biz.UsageProjection, error) { return s.u.GetUsage(ctx, a.Context, a.Ref) })
}
func (s *Accelerator) ListGpuUsages(ctx context.Context, q *pb.ListGpuUsagesRequest) (*pb.ListGpuUsagesResponse, error) {
	return invoke[pb.ListGpuUsagesResponse](q, func(a *tenantInput) (*biz.ListResult[biz.UsageProjection], error) {
		return s.u.ListUsages(ctx, a.Context, a.OwnerService, a.State, a.Page)
	})
}
func (s *Accelerator) ListBindings(ctx context.Context, q *pb.ListBindingsRequest) (*pb.ListBindingsResponse, error) {
	return invoke[pb.ListBindingsResponse](q, func(a *tenantInput) (*biz.ListResult[biz.Binding], error) {
		return s.u.ListBindings(ctx, a.Context, a.Ref, a.Page)
	})
}
func (s *Accelerator) PreviewProfile(ctx context.Context, q *pb.PreviewProfileRequest) (*pb.ProfilePreview, error) {
	type input struct {
		Context *biz.AdminRead `json:"context"`
		GroupID string         `json:"group_id"`
		Parts   uint32         `json:"parts"`
	}
	return invoke[pb.ProfilePreview](q, func(a *input) (*biz.ProfilePreview, error) {
		return s.u.PreviewProfile(ctx, a.Context, a.GroupID, a.Parts)
	})
}
func (s *Accelerator) SetProfileState(ctx context.Context, q *pb.SetProfileStateRequest) (*pb.GpuProfile, error) {
	type input struct {
		mutationInput
		ProfileID        string           `json:"profile_id"`
		ExpectedRevision uint64           `json:"expected_revision"`
		Desired          biz.ProfileState `json:"desired"`
	}
	return invoke[pb.GpuProfile](q, func(a *input) (*biz.Profile, error) {
		return s.u.SetProfileState(ctx, a.Mutation, a.ProfileID, a.ExpectedRevision, a.Desired)
	})
}
func (s *Accelerator) DeleteProfile(ctx context.Context, q *pb.DeleteProfileRequest) (*pb.GpuProfile, error) {
	type input struct {
		mutationInput
		ProfileID        string `json:"profile_id"`
		ExpectedRevision uint64 `json:"expected_revision"`
	}
	return invoke[pb.GpuProfile](q, func(a *input) (*biz.Profile, error) {
		return s.u.SetProfileState(ctx, a.Mutation, a.ProfileID, a.ExpectedRevision, biz.ProfileDeleted)
	})
}

func unknownFields(m protoreflect.Message) bool {
	if len(m.GetUnknown()) != 0 {
		return true
	}
	invalid := false
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.IsMap() {
			if f.MapValue().Kind() == protoreflect.MessageKind {
				v.Map().Range(func(_ protoreflect.MapKey, x protoreflect.Value) bool {
					invalid = unknownFields(x.Message())
					return !invalid
				})
			}
		} else if f.IsList() && f.Kind() == protoreflect.MessageKind {
			for i := 0; i < v.List().Len(); i++ {
				if unknownFields(v.List().Get(i).Message()) {
					invalid = true
					break
				}
			}
		} else if f.Kind() == protoreflect.MessageKind {
			invalid = unknownFields(v.Message())
		}
		return !invalid
	})
	return invalid
}
