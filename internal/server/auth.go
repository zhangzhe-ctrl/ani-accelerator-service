package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"

	"github.com/go-kratos/kratos/v3/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var AdminActions = map[string]bool{"RegisterCluster": true, "ListClusters": true, "CreatePool": true, "ListPools": true, "AdoptSupplyGroup": true, "ListSupplyGroups": true, "SetAdmission": true, "CreateProfile": true, "PreviewProfile": true, "SetProfileState": true, "DeleteProfile": true, "AdminListProfiles": true, "AdminListBindings": true, "ListDevices": true, "AdminGetCapacity": true}
var TenantActions = map[string]bool{"ListProfiles": true, "GetProfile": true, "ResolveGpuRequest": true, "GetCapacity": true, "CheckGpuFit": true, "GetGpuUsage": true, "ListGpuUsages": true, "ListBindings": true}

func LoadServerTLS(c *conf.Security) (*tls.Config, error) {
	if c == nil || c.CaFile == "" || c.CertFile == "" || c.KeyFile == "" || c.GovernanceUri == "" || c.InferenceUri == "" || c.ResourceUri == "" || c.GovernanceUri == c.InferenceUri || c.ResourceUri == c.GovernanceUri || c.ResourceUri == c.InferenceUri {
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
	return kratosgrpc.NewServer(kratosgrpc.Network(c.Network), kratosgrpc.Address(c.Addr), kratosgrpc.Timeout(c.Timeout.AsDuration()), kratosgrpc.TLSConfig(tlsConfig), kratosgrpc.Middleware(middlewares...), kratosgrpc.DisableReflection(), kratosgrpc.UnaryInterceptor(Authorize(security)), kratosgrpc.StreamInterceptor(AuthorizeStream(security))), nil
}

// Business RPCs are unary. Kratos also registers the streaming health Watch;
// it must not bypass Resource's sole-method allowlist.
func AuthorizeStream(config *conf.Security) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		p, ok := peer.FromContext(stream.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "MTLS_REQUIRED")
		}
		t, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok || len(t.State.VerifiedChains) == 0 || len(t.State.PeerCertificates) == 0 {
			return status.Error(codes.Unauthenticated, "MTLS_REQUIRED")
		}
		leaf := t.State.PeerCertificates[0]
		if len(leaf.URIs) != 1 {
			return status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
		}
		identity := leaf.URIs[0].String()
		if info.FullMethod != "/grpc.health.v1.Health/Watch" || (identity != config.GovernanceUri && identity != config.InferenceUri) {
			return status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		return next(srv, stream)
	}
}
func Authorize(config *conf.Security) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "MTLS_REQUIRED")
		}
		t, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok || len(t.State.VerifiedChains) == 0 || len(t.State.PeerCertificates) == 0 {
			return nil, status.Error(codes.Unauthenticated, "MTLS_REQUIRED")
		}
		leaf := t.State.PeerCertificates[0]
		if len(leaf.URIs) != 1 {
			return nil, status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
		}
		identity := leaf.URIs[0].String()
		service := ""
		switch identity {
		case config.GovernanceUri:
			service = "ani-governance"
		case config.InferenceUri:
			service = "ani-inference"
		case config.ResourceUri:
			service = "ani-resource-service"
		default:
			return nil, status.Error(codes.PermissionDenied, "SERVICE_IDENTITY_DENIED")
		}
		if info.FullMethod == "/grpc.health.v1.Health/Check" {
			if service == "ani-resource-service" {
				return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
			}
			return next(ctx, request)
		}
		if !strings.HasPrefix(info.FullMethod, "/accelerator.v1.") {
			return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		_, method, ok := strings.Cut(strings.TrimPrefix(info.FullMethod, "/"), "/")
		if !ok {
			return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		if message, ok := request.(proto.Message); !ok || hasUnknown(message.ProtoReflect()) {
			return nil, status.Error(codes.InvalidArgument, "UNKNOWN_PROTO_FIELD")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		read := func(k string) string {
			v := md.Get(k)
			if len(v) != 1 {
				return ""
			}
			return v[0]
		}
		caller := biz.Caller{Service: service, Action: method, TenantID: read("x-ani-tenant-id")}
		if method == "SyncGpuUsage" {
			if service != "ani-governance" {
				return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
			}
			return next(biz.WithCaller(ctx, caller), request)
		}
		if method == "ObserveRelease" {
			if (service != "ani-inference" && service != "ani-resource-service") || !biz.UUID(caller.TenantID) || read("x-ani-action") != method {
				return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
			}
			return next(biz.WithCaller(ctx, caller), request)
		}
		if service != "ani-governance" || read("x-ani-action") != method || (!AdminActions[method] && !TenantActions[method]) {
			return nil, status.Error(codes.PermissionDenied, "ACTION_DENIED")
		}
		caller.Actor = biz.Actor{Type: read("x-ani-actor-type"), ID: read("x-ani-actor-id")}
		for _, grant := range config.Grants {
			if grant.ActorType != caller.Actor.Type || grant.ActorId != caller.Actor.ID || !contains(grant.Actions, method) {
				continue
			}
			if AdminActions[method] {
				if !grant.Platform || caller.TenantID != "" {
					continue
				}
			} else {
				if grant.Platform || !biz.UUID(caller.TenantID) || !contains(grant.TenantIds, caller.TenantID) {
					continue
				}
			}
			caller.ClusterIDs = append([]string{}, grant.ClusterIds...)
			return next(biz.WithCaller(ctx, caller), request)
		}
		return nil, status.Error(codes.PermissionDenied, "DELEGATION_DENIED")
	}
}
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
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
