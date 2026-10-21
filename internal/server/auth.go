package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"os"

	"github.com/go-kratos/kratos/v3/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func LoadServerTLS(c *conf.Security) (*tls.Config, error) {
	if c == nil || c.CaFile == "" || c.CertFile == "" || c.KeyFile == "" || c.ValidateServiceIdentities() != nil {
		return nil, biz.Invalid("MTLS_CONFIG_REQUIRED")
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, biz.Invalid("SERVER_CERTIFICATE_UNAVAILABLE")
	}
	ca, err := os.ReadFile(c.CaFile)
	if err != nil {
		return nil, biz.Invalid("CLIENT_CA_UNAVAILABLE")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, biz.Invalid("CLIENT_CA_INVALID")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, nil
}
func NewSecureGRPCServer(c *conf.Server_GRPC, security *conf.Security, middlewares ...middleware.Middleware) (*kratosgrpc.Server, error) {
	tlsConfig, err := LoadServerTLS(security)
	if err != nil {
		return nil, err
	}
	return kratosgrpc.NewServer(kratosgrpc.Network(c.Network), kratosgrpc.Address(c.Addr), kratosgrpc.Timeout(c.Timeout.AsDuration()), kratosgrpc.TLSConfig(tlsConfig), kratosgrpc.Middleware(middlewares...), kratosgrpc.DisableReflection(), kratosgrpc.UnaryInterceptor(Authorize(security)), kratosgrpc.StreamInterceptor(AuthorizeStream(security)), kratosgrpc.Options(grpc.MaxRecvMsgSize(9<<20))), nil
}

type rpcPolicy struct {
	action string
	admin  bool
}

var governanceRPCs = map[string]rpcPolicy{
	pb.AcceleratorAdminService_GetInitialization_FullMethodName:            {action: "GetInitialization", admin: true},
	pb.AcceleratorAdminService_ListDiscoveredNodes_FullMethodName:          {action: "ListDiscoveredNodes", admin: true},
	pb.AcceleratorAdminService_CreateInitialization_FullMethodName:         {action: "CreateInitialization", admin: true},
	pb.AcceleratorAdminService_GetInitializationOperation_FullMethodName:   {action: "GetInitializationOperation", admin: true},
	pb.AcceleratorAdminService_RetryInitializationOperation_FullMethodName: {action: "RetryInitializationOperation", admin: true},
	pb.AcceleratorAdminService_RegisterCluster_FullMethodName:              {action: "RegisterCluster", admin: true},
	pb.AcceleratorAdminService_ListClusters_FullMethodName:                 {action: "ListClusters", admin: true},
	pb.AcceleratorAdminService_CreatePool_FullMethodName:                   {action: "CreatePool", admin: true},
	pb.AcceleratorAdminService_ListPools_FullMethodName:                    {action: "ListPools", admin: true},
	pb.AcceleratorAdminService_AdoptSupplyGroup_FullMethodName:             {action: "AdoptSupplyGroup", admin: true},
	pb.AcceleratorAdminService_ListSupplyGroups_FullMethodName:             {action: "ListSupplyGroups", admin: true},
	pb.AcceleratorAdminService_SetAdmission_FullMethodName:                 {action: "SetAdmission", admin: true},
	pb.AcceleratorAdminService_CreateProfile_FullMethodName:                {action: "CreateProfile", admin: true},
	pb.AcceleratorAdminService_PreviewProfile_FullMethodName:               {action: "PreviewProfile", admin: true},
	pb.AcceleratorAdminService_SetProfileState_FullMethodName:              {action: "SetProfileState", admin: true},
	pb.AcceleratorAdminService_DeleteProfile_FullMethodName:                {action: "DeleteProfile", admin: true},
	pb.AcceleratorAdminService_AdminListProfiles_FullMethodName:            {action: "AdminListProfiles", admin: true},
	pb.AcceleratorAdminService_AdminListBindings_FullMethodName:            {action: "AdminListBindings", admin: true},
	pb.AcceleratorAdminService_ListDevices_FullMethodName:                  {action: "ListDevices", admin: true},
	pb.AcceleratorAdminService_AdminGetCapacity_FullMethodName:             {action: "AdminGetCapacity", admin: true},
	pb.AcceleratorCatalogService_ListProfiles_FullMethodName:               {action: "ListProfiles", admin: false},
	pb.AcceleratorCatalogService_GetProfile_FullMethodName:                 {action: "GetProfile", admin: false},
	pb.AcceleratorCatalogService_ResolveGpuRequest_FullMethodName:          {action: "ResolveGpuRequest", admin: false},
	pb.AcceleratorCatalogService_GetCapacity_FullMethodName:                {action: "GetCapacity", admin: false},
	pb.AcceleratorCatalogService_CheckGpuFit_FullMethodName:                {action: "CheckGpuFit", admin: false},
	pb.AcceleratorUsageService_GetGpuUsage_FullMethodName:                  {action: "GetGpuUsage", admin: false},
	pb.AcceleratorUsageService_ListGpuUsages_FullMethodName:                {action: "ListGpuUsages", admin: false},
	pb.AcceleratorUsageService_ListBindings_FullMethodName:                 {action: "ListBindings", admin: false},
}

// TLS verifies the chain; ACC additionally requires one exact DNS SAN and no
// alternate SAN type. Identity is never inferred from CN or URI aliases.
func authenticatedService(ctx context.Context, config *conf.Security) (string, error) {
	if config.ValidateServiceIdentities() != nil {
		return "", status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
	}
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "MTLS_REQUIRED")
	}
	t, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(t.State.VerifiedChains) == 0 || len(t.State.PeerCertificates) == 0 {
		return "", status.Error(codes.Unauthenticated, "MTLS_REQUIRED")
	}
	leaf := t.State.PeerCertificates[0]
	if len(leaf.DNSNames) != 1 || len(leaf.URIs) != 0 || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 || !uniqueDNSSAN(leaf) {
		return "", status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
	}
	switch leaf.DNSNames[0] {
	case config.GovernanceDnsName:
		return "ani-governance", nil
	case config.InferenceDnsName:
		return "ani-inference", nil
	case config.ResourceDnsName:
		return "ani-resource-service", nil
	case "ani-node-agent":
		if config.NodeAgentDnsName == "ani-node-agent" {
			return "ani-node-agent", nil
		}
		return "", status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
	case config.ModeldevDnsName:
		return "ani-modeldev-service", nil
	default:
		return "", status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
	}
}
func uniqueDNSSAN(leaf *x509.Certificate) bool {
	for _, ext := range leaf.Extensions {
		if !ext.Id.Equal(asn1.ObjectIdentifier{2, 5, 29, 17}) {
			continue
		}
		var sequence asn1.RawValue
		rest, err := asn1.Unmarshal(ext.Value, &sequence)
		if err != nil || len(rest) != 0 || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
			return false
		}
		var name asn1.RawValue
		rest, err = asn1.Unmarshal(sequence.Bytes, &name)
		return err == nil && len(rest) == 0 && name.Class == asn1.ClassContextSpecific && name.Tag == 2 && !name.IsCompound && string(name.Bytes) == leaf.DNSNames[0]
	}
	return false
}
func AuthorizeStream(config *conf.Security) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		service, err := authenticatedService(stream.Context(), config)
		if err != nil {
			return err
		}
		if !((service == "ani-governance" && info.FullMethod == healthpb.Health_Watch_FullMethodName) || (service == "ani-node-agent" && info.FullMethod == nodepb.NodeTelemetryService_Connect_FullMethodName)) {
			return status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		return next(srv, stream)
	}
}
func Authorize(config *conf.Security) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		service, err := authenticatedService(ctx, config)
		if err != nil {
			return nil, err
		}
		if service == "ani-governance" && (info.FullMethod == healthpb.Health_Check_FullMethodName || info.FullMethod == healthpb.Health_List_FullMethodName) {
			return next(ctx, request)
		}
		policy, delegated := governanceRPCs[info.FullMethod]
		sync := info.FullMethod == pb.AcceleratorUsageService_SyncGpuUsage_FullMethodName
		release := info.FullMethod == pb.AcceleratorUsageService_ObserveRelease_FullMethodName
		if !(service == "ani-governance" && (delegated || sync)) && !((service == "ani-inference" || service == "ani-resource-service" || service == "ani-modeldev-service") && release) {
			return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		if message, ok := request.(proto.Message); !ok || hasUnknown(message.ProtoReflect()) {
			return nil, status.Error(codes.InvalidArgument, "UNKNOWN_PROTO_FIELD")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		read := func(k string) string {
			values := md.Get(k)
			if len(values) != 1 {
				return ""
			}
			return values[0]
		}
		caller := biz.Caller{Service: service, Action: policy.action, TenantID: read("x-ani-tenant-id")}
		if sync {
			caller.Action = "SyncGpuUsage"
			// Durable background facts do not impersonate a live user or tenant.
			for _, key := range []string{"x-ani-actor-type", "x-ani-actor-id", "x-ani-tenant-id"} {
				if len(md.Get(key)) != 0 {
					return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
				}
			}
			if len(md.Get("x-ani-action")) != 0 && read("x-ani-action") != caller.Action {
				return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
			}
			return next(biz.WithCaller(ctx, caller), request)
		}
		if release {
			caller.Action = "ObserveRelease"
			if !biz.UUID(caller.TenantID) || read("x-ani-action") != caller.Action {
				return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
			}
			return next(biz.WithCaller(ctx, caller), request)
		}
		caller.Actor = biz.Actor{Type: read("x-ani-actor-type"), ID: read("x-ani-actor-id")}
		if read("x-ani-action") != policy.action || biz.ValidateActor(&caller.Actor) != nil || (policy.admin && len(md.Get("x-ani-tenant-id")) != 0) || (!policy.admin && !biz.UUID(caller.TenantID)) {
			return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		return next(biz.WithCaller(ctx, caller), request)
	}
}
func hasUnknown(m protoreflect.Message) bool {
	if len(m.GetUnknown()) > 0 {
		return true
	}
	found := false
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Message() == nil {
			return true
		}
		if f.IsList() {
			l := v.List()
			for i := 0; i < l.Len(); i++ {
				if hasUnknown(l.Get(i).Message()) {
					found = true
					return false
				}
			}
		} else if f.IsMap() {
			if f.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool { found = hasUnknown(v.Message()); return !found })
			}
		} else {
			found = hasUnknown(v.Message())
		}
		return !found
	})
	return found
}
