package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/url"
	"os"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/dynamicpb"
)

func serviceSecurity() *conf.Security {
	return &conf.Security{GovernanceDnsName: "ani-governance", InferenceDnsName: "ani-inference-service", ResourceDnsName: "ani-resource-service", ModeldevDnsName: "ani-modeldev-service"}
}
func verifiedContext(t *testing.T, pki *testkit.PKI, certFile string) context.Context {
	t.Helper()
	b, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(pki.CACert)
	chains, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil {
		t.Fatal(err)
	}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: chains}}})
}
func TestServiceIdentityRequiresUniqueExactDNSSAN(t *testing.T) {
	pki := testkit.NewPKI(t)
	uri, _ := url.Parse("spiffe://ani.internal/service/ani-governance")
	for _, tc := range []struct {
		name   string
		dns    []string
		uris   []*url.URL
		ips    []net.IP
		emails []string
		allow  bool
	}{
		{name: "governance", dns: []string{"ani-governance"}, allow: true},
		{name: "resource", dns: []string{"ani-resource-service"}, allow: true},
		{name: "inference", dns: []string{"ani-inference-service"}, allow: true},
		{name: "modeldev", dns: []string{"ani-modeldev-service"}, allow: true},
		{name: "legacy-uri", uris: []*url.URL{uri}},
		{name: "mixed-uri", dns: []string{"ani-governance"}, uris: []*url.URL{uri}},
		{name: "mixed-ip", dns: []string{"ani-governance"}, ips: []net.IP{net.ParseIP("127.0.0.1")}},
		{name: "mixed-email", dns: []string{"ani-governance"}, emails: []string{"governance@ani.internal"}},
		{name: "multiple-dns", dns: []string{"ani-governance", "ani-resource-service"}},
		{name: "duplicate-dns", dns: []string{"ani-governance", "ani-governance"}},
		{name: "wildcard", dns: []string{"*.ani.internal"}},
		{name: "wrong-dns", dns: []string{"ani-governance.ani.internal"}},
		{name: "cn-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, _ := pki.IssueWithSAN(t, tc.name, tc.dns, tc.uris, tc.ips, tc.emails, false)
			_, err := authenticatedService(verifiedContext(t, pki, cert), serviceSecurity())
			if tc.allow && err != nil {
				t.Fatal(err)
			}
			if !tc.allow && status.Code(err) != codes.PermissionDenied {
				t.Fatalf("identity accepted: %v", err)
			}
		})
	}
	if _, err := authenticatedService(context.Background(), serviceSecurity()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing mTLS accepted: %v", err)
	}
}
func TestGeneratedRPCAllowlistAndTrustedDelegation(t *testing.T) {
	pki := testkit.NewPKI(t)
	for _, identity := range []string{"ani-governance", "ani-resource-service", "ani-inference-service", "ani-modeldev-service"} {
		cert, _ := pki.Issue(t, identity, identity, false)
		ctx := verifiedContext(t, pki, cert)
		services := pb.File_accelerator_v1_accelerator_proto.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			for j := 0; j < service.Methods().Len(); j++ {
				method := service.Methods().Get(j)
				t.Run(identity+"/"+string(method.Name()), func(t *testing.T) {
					full := "/" + string(service.FullName()) + "/" + string(method.Name())
					policy, delegated := governanceRPCs[full]
					want := identity == "ani-governance" && (delegated || method.Name() == "SyncGpuUsage") || identity != "ani-governance" && method.Name() == "ObserveRelease"
					pairs := []string{"x-ani-action", string(method.Name())}
					if method.Name() != "SyncGpuUsage" {
						if !policy.admin {
							pairs = append(pairs, "x-ani-tenant-id", biz.NewID())
						}
						pairs = append(pairs, "x-ani-actor-type", "user", "x-ani-actor-id", "new-user-without-config")
					}
					called := false
					_, err := Authorize(serviceSecurity())(metadata.NewIncomingContext(ctx, metadata.Pairs(pairs...)), dynamicpb.NewMessage(method.Input()), &grpc.UnaryServerInfo{FullMethod: full}, func(ctx context.Context, _ any) (any, error) {
						called = true
						c, ok := biz.CurrentCaller(ctx)
						if !ok || c.Action != string(method.Name()) {
							t.Fatalf("missing trusted caller %+v", c)
						}
						return nil, nil
					})
					if want && (err != nil || !called) {
						t.Fatalf("registered call rejected: %v", err)
					}
					if !want && (status.Code(err) != codes.PermissionDenied || called) {
						t.Fatalf("unregistered call allowed: %v", err)
					}
				})
			}
		}
		for _, full := range []string{"/accelerator.v1.FakeService/ListProfiles", "/accelerator.v1.AcceleratorUsageService/ListProfiles", "/accelerator.v1.AcceleratorCatalogService/Unknown", "/unknown.Service/SyncGpuUsage"} {
			_, err := Authorize(serviceSecurity())(ctx, &pb.ListProfilesRequest{}, &grpc.UnaryServerInfo{FullMethod: full}, func(context.Context, any) (any, error) { t.Fatal("unknown method reached handler"); return nil, nil })
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("unknown full method accepted: %v", err)
			}
		}
		for _, method := range []string{healthpb.Health_Check_FullMethodName, healthpb.Health_List_FullMethodName} {
			called := false
			_, err := Authorize(serviceSecurity())(ctx, &healthpb.HealthCheckRequest{}, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, any) (any, error) { called = true; return nil, nil })
			if identity == "ani-governance" {
				if err != nil || !called {
					t.Fatal(err)
				}
			} else if called || status.Code(err) != codes.PermissionDenied {
				t.Fatalf("owner health accepted: %v", err)
			}
		}
	}
}

func TestDelegatedMetadataFailsClosed(t *testing.T) {
	pki := testkit.NewPKI(t)
	cert, _ := pki.Issue(t, "gov", "ani-governance", false)
	ctx := verifiedContext(t, pki, cert)
	valid := metadata.Pairs("x-ani-action", "ListProfiles", "x-ani-actor-type", "user", "x-ani-actor-id", "new-user", "x-ani-tenant-id", biz.NewID())
	for _, key := range []string{"x-ani-action", "x-ani-actor-type", "x-ani-actor-id", "x-ani-tenant-id"} {
		for _, duplicate := range []bool{false, true} {
			md := valid.Copy()
			if duplicate {
				md.Append(key, md.Get(key)[0])
			} else {
				md.Delete(key)
			}
			_, err := Authorize(serviceSecurity())(metadata.NewIncomingContext(ctx, md), &pb.ListProfilesRequest{}, &grpc.UnaryServerInfo{FullMethod: pb.AcceleratorCatalogService_ListProfiles_FullMethodName}, func(context.Context, any) (any, error) { t.Fatal("invalid metadata reached handler"); return nil, nil })
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("missing or duplicate %s accepted: %v", key, err)
			}
		}
	}
}

type authenticationStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s authenticationStream) Context() context.Context { return s.ctx }
func TestStreamingRPCsUseTheSameServicePolicy(t *testing.T) {
	pki := testkit.NewPKI(t)
	for _, identity := range []string{"ani-governance", "ani-resource-service", "ani-inference-service"} {
		cert, _ := pki.Issue(t, identity, identity, false)
		stream := authenticationStream{ctx: verifiedContext(t, pki, cert)}
		for _, method := range []string{healthpb.Health_Watch_FullMethodName, "/grpc.health.v1.Fake/Watch", pb.AcceleratorCatalogService_ListProfiles_FullMethodName} {
			called := false
			err := AuthorizeStream(serviceSecurity())(nil, stream, &grpc.StreamServerInfo{FullMethod: method}, func(any, grpc.ServerStream) error { called = true; return nil })
			if identity == "ani-governance" && method == healthpb.Health_Watch_FullMethodName {
				if err != nil || !called {
					t.Fatal(err)
				}
			} else if called || status.Code(err) != codes.PermissionDenied {
				t.Fatalf("stream policy bypass: %s %s %v", identity, method, err)
			}
		}
	}
}
