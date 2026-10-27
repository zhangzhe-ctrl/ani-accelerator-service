package service

import (
	"context"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type adminMetricInput struct {
	Context *biz.AdminRead `json:"context"`
	biz.MetricScopeFilter
}
type tenantMetricInput struct {
	Context *biz.TenantContext `json:"context"`
	biz.MetricScopeFilter
}

func (s *Accelerator) ListAdminGpuMetricScopes(ctx context.Context, req *pb.AdminGpuMetricScopesRequest) (*pb.GpuMetricScopesResponse, error) {
	return invoke[pb.GpuMetricScopesResponse](req, func(in *adminMetricInput) (*biz.MetricScopes, error) {
		return s.u.ListAdminGpuMetricScopes(ctx, in.Context, in.MetricScopeFilter)
	})
}
func (s *Accelerator) ListTenantGpuMetricScopes(ctx context.Context, req *pb.TenantGpuMetricScopesRequest) (*pb.GpuMetricScopesResponse, error) {
	return invoke[pb.GpuMetricScopesResponse](req, func(in *tenantMetricInput) (*biz.MetricScopes, error) {
		return s.u.ListTenantGpuMetricScopes(ctx, in.Context, in.MetricScopeFilter)
	})
}
