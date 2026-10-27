package server

import (
	"context"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestGpuMetricScopeDelegationKeepsAdminTenantBoundary(t *testing.T) {
	pki := testkit.NewPKI(t)
	cert, _ := pki.Issue(t, "gov", "ani-governance", false)
	base := verifiedContext(t, pki, cert)
	for _, tc := range []struct {
		name, method, action, tenant string
		req                          proto.Message
		allow                        bool
	}{
		{"admin", pb.AcceleratorAdminService_ListAdminGpuMetricScopes_FullMethodName, "ListAdminGpuMetricScopes", "", &pb.AdminGpuMetricScopesRequest{}, true},
		{"admin-with-tenant", pb.AcceleratorAdminService_ListAdminGpuMetricScopes_FullMethodName, "ListAdminGpuMetricScopes", biz.NewID(), &pb.AdminGpuMetricScopesRequest{}, false},
		{"tenant", pb.AcceleratorUsageService_ListTenantGpuMetricScopes_FullMethodName, "ListTenantGpuMetricScopes", biz.NewID(), &pb.TenantGpuMetricScopesRequest{}, true},
		{"tenant-without-tenant", pb.AcceleratorUsageService_ListTenantGpuMetricScopes_FullMethodName, "ListTenantGpuMetricScopes", "", &pb.TenantGpuMetricScopesRequest{}, false},
		{"tenant-as-admin", pb.AcceleratorAdminService_ListAdminGpuMetricScopes_FullMethodName, "ListTenantGpuMetricScopes", biz.NewID(), &pb.AdminGpuMetricScopesRequest{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pairs := []string{"x-ani-action", tc.action, "x-ani-actor-type", "user", "x-ani-actor-id", "actor"}
			if tc.tenant != "" {
				pairs = append(pairs, "x-ani-tenant-id", tc.tenant)
			}
			called := false
			_, err := Authorize(serviceSecurity())(metadata.NewIncomingContext(base, metadata.Pairs(pairs...)), tc.req, &grpc.UnaryServerInfo{FullMethod: tc.method}, func(context.Context, any) (any, error) { called = true; return nil, nil })
			if called != tc.allow || !tc.allow && status.Code(err) != codes.PermissionDenied {
				t.Fatalf("allow=%v called=%v err=%v", tc.allow, called, err)
			}
		})
	}
}
