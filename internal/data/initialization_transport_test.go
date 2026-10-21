package data

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/service"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (external *initializationTestProvider) DiscoverInitialization(context.Context) (biz.InitializationDiscovery, error) {
	result := biz.InitializationDiscovery{Environment: external.environment, ConfigurationState: "READY"}
	for _, node := range external.environment.Nodes {
		result.Nodes = append(result.Nodes, biz.DiscoveredNode{Node: node.Node, GroupID: node.GroupID, Selectable: node.Selectable})
	}
	return result, nil
}

func TestPostgresInitializationAuthenticatedRPCAndWorker(t *testing.T) {
	fixture, external, usecase, input := initializationPGFixture(t)
	runtime, address := serveInitializationRPC(t, fixture.u, usecase)
	connection, err := grpc.NewClient(address, grpc.WithDisableRetry(), grpc.WithTransportCredentials(credentials.NewTLS(testkit.ClientTLS(t, runtime.PKI.CAFile, runtime.GovCert, runtime.GovKey))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := pb.NewAcceleratorAdminServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	callContext := func(action string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("x-ani-action", action, "x-ani-actor-type", adminActor.Type, "x-ani-actor-id", adminActor.ID))
	}
	read := &pb.AdminRead{RequestId: biz.NewID(), Actor: &pb.Actor{Type: adminActor.Type, Id: adminActor.ID}}
	if _, err = client.GetInitialization(ctx, &pb.GetInitializationRequest{Context: read}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service certificate bypassed required delegation: %v", err)
	}
	initial, err := client.GetInitialization(callContext("GetInitialization"), &pb.GetInitializationRequest{Context: read})
	if err != nil || !initial.Registered || initial.ClusterId != fixture.cluster.ClusterID {
		t.Fatalf("initialization summary RPC: %+v %v", initial, err)
	}
	discovered, err := client.ListDiscoveredNodes(callContext("ListDiscoveredNodes"), &pb.ListDiscoveredNodesRequest{Context: read, Page: 1, PageSize: 20})
	if err != nil || discovered.DiscoveryRevision != input.DiscoveryRevision {
		t.Fatalf("discovery RPC: %+v %v", discovered, err)
	}
	selected := input.Groups[0].NewGroup.Nodes[0]
	request := &pb.CreateInitializationRequest{Mutation: &pb.AdminMutation{Context: read, IdempotencyKey: biz.NewID()}, DiscoveryRevision: input.DiscoveryRevision,
		Pools:    []*pb.InitializationPoolInput{{ClientKey: "pool", Pool: &pb.InitializationPoolInput_NewPool{NewPool: &pb.InitializationNewPool{DisplayName: input.Pools[0].NewPool.DisplayName}}}},
		Groups:   []*pb.InitializationGroupInput{{ClientKey: "group", Group: &pb.InitializationGroupInput_NewGroup{NewGroup: &pb.InitializationNewGroup{PoolKey: "pool", DisplayName: input.Groups[0].NewGroup.DisplayName, Nodes: []*pb.NodeRef{{Uid: selected.UID, Name: selected.Name}}, BaselineRef: "baseline-1", QueueName: "default"}}}},
		Profiles: []*pb.InitializationProfileInput{{ClientKey: "profile", GroupKey: "group", DisplayName: "8GiB", MemoryMibPerDevice: 8192, CoreLimitPercent: 25, MaxDevicesPerReplica: 1}}}
	accepted, err := client.CreateInitialization(callContext("CreateInitialization"), request)
	if err != nil || accepted.State != "ACCEPTED" || len(accepted.Steps) == 0 {
		t.Fatalf("create operation RPC: %+v %v", accepted, err)
	}
	if replay, err := client.CreateInitialization(callContext("CreateInitialization"), request); err != nil || replay.OperationId != accepted.OperationId {
		t.Fatalf("RPC replay did not return same durable operation: %+v %v", replay, err)
	}
	var failed atomic.Bool
	external.labels = func(context.Context, biz.SupplyGroup) error {
		if failed.CompareAndSwap(false, true) {
			return biz.Unavailable()
		}
		return nil
	}
	worker := server.NewInitializationWorker(usecase, nil, 5*time.Millisecond)
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := worker.Stop(stop); err != nil {
			t.Error(err)
		}
	})
	observe := func(want string) *pb.InitializationOperation {
		t.Helper()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			operation, err := client.GetInitializationOperation(callContext("GetInitializationOperation"), &pb.GetInitializationOperationRequest{Context: read, OperationId: accepted.OperationId})
			if err != nil {
				t.Fatal(err)
			}
			if operation.State == want {
				return operation
			}
			if operation.State == "FAILED" || operation.State == "CONFLICT" {
				t.Fatalf("operation stopped unexpectedly: %+v", operation)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("operation did not reach %s: %+v", want, operation)
			case <-ticker.C:
			}
		}
	}
	blocked := observe("BLOCKED")
	if !blocked.Retryable || len(blocked.Groups) != 1 || blocked.Groups[0].RecordedGroupVersion == 0 {
		t.Fatalf("partial committed results missing: %+v", blocked)
	}
	retryRequest := &pb.RetryInitializationOperationRequest{Mutation: &pb.AdminMutation{Context: read, IdempotencyKey: biz.NewID()}, OperationId: accepted.OperationId, ExpectedVersion: blocked.Version}
	if _, err = client.RetryInitializationOperation(callContext("RetryInitializationOperation"), retryRequest); err != nil {
		t.Fatal(err)
	}
	completed := observe("SUCCEEDED")
	if len(completed.Profiles) != 1 || completed.Profiles[0].RecordedState != "PROFILE_ACTIVE" || completed.Groups[0].AutoOpenResult != "OPENED" || completed.Groups[0].Admission != "ADMISSION_OPEN" {
		t.Fatalf("persisted initializer result missing: %+v", completed)
	}
	for index, step := range completed.Steps {
		if step.StepId != accepted.Steps[index].StepId {
			t.Fatal("worker retry changed stable step identity")
		}
	}
	summary, err := client.GetInitialization(callContext("GetInitialization"), &pb.GetInitializationRequest{Context: read})
	if err != nil || summary.LatestOperation == nil || summary.LatestOperation.OperationId != accepted.OperationId || summary.LatestOperation.State != "SUCCEEDED" || !summary.ConfigurationComplete {
		t.Fatalf("summary lost durable operation or configured supply: %+v %v", summary, err)
	}
}

func TestPostgresInitializationDiscoveryPaginationAndPausedConfiguration(t *testing.T) {
	fixture, external, usecase, _ := initializationPGFixture(t)
	external.environment.Nodes = map[string]biz.InitializationNode{}
	for _, name := range []string{"node-z", "node-a", "node-m"} {
		uid := biz.NewID()
		external.environment.Nodes[uid] = biz.InitializationNode{Node: biz.NodeRef{UID: uid, Name: name}}
	}
	read := &biz.AdminRead{RequestID: biz.NewID(), Actor: &adminActor}
	page, err := usecase.ListDiscoveredNodes(adminContext("ListDiscoveredNodes"), read, 2, 1)
	if err != nil || page.Total != 3 || len(page.Items) != 1 || page.Items[0].Node.Name != "node-m" {
		t.Fatalf("discovery page must sort before taking requested window: %+v %v", page, err)
	}
	empty, err := usecase.ListDiscoveredNodes(adminContext("ListDiscoveredNodes"), read, 4, 1)
	if err != nil || empty.Total != 3 || len(empty.Items) != 0 {
		t.Fatalf("out-of-range page changed total: %+v %v", empty, err)
	}
	if _, err := usecase.ListDiscoveredNodes(adminContext("ListDiscoveredNodes"), read, 1, 201); err == nil {
		t.Fatal("discovery accepted an oversized page")
	}
	if _, err := fixture.u.SetAdmission(adminContext("SetAdmission"), adminMutation(), biz.AdmissionInput{GroupID: fixture.group.GroupID, ExpectedVersion: fixture.group.Version, Desired: biz.Closed, Reason: "explicit pause"}); err != nil {
		t.Fatal(err)
	}
	summary, err := usecase.GetInitialization(adminContext("GetInitialization"), read)
	if err != nil || !summary.ConfigurationComplete {
		t.Fatalf("explicit pause must not erase configuration completion: %+v %v", summary, err)
	}
	if _, err := usecase.GetInitialization(context.Background(), read); err == nil {
		t.Fatal("summary read bypassed trusted service authorization")
	}
}

func serveInitializationRPC(t *testing.T, catalog *biz.Usecase, usecase *biz.InitializationUsecase) (*testkit.Runtime, string) {
	t.Helper()
	runtime := testkit.NewRuntime(t, "127.0.0.1:0", "127.0.0.1:0")
	tlsConfig, err := server.LoadServerTLS(runtime.Config.Security)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secure := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.UnaryInterceptor(server.Authorize(runtime.Config.Security)))
	pb.RegisterAcceleratorAdminServiceServer(secure, service.NewAccelerator(catalog, usecase))
	go secure.Serve(listener)
	t.Cleanup(secure.Stop)
	return runtime, listener.Addr().String()
}

func TestPostgresInitializationJointHelper(t *testing.T) {
	exportFile := os.Getenv("ACC_INITIALIZATION_JOINT_EXPORT_FILE")
	if exportFile == "" {
		t.Skip("requires an explicitly requested isolated joint helper")
	}
	fixture, external, usecase, input := initializationPGFixture(t)
	var failed atomic.Bool
	external.labels = func(context.Context, biz.SupplyGroup) error {
		if failed.CompareAndSwap(false, true) {
			return biz.Unavailable()
		}
		return nil
	}
	runtime, address := serveInitializationRPC(t, fixture.u, usecase)
	worker := server.NewInitializationWorker(usecase, nil, 5*time.Millisecond)
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := worker.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	seed := struct {
		Address           string `json:"address"`
		ServerName        string `json:"server_name"`
		CAFile            string `json:"ca_file"`
		CertFile          string `json:"cert_file"`
		KeyFile           string `json:"key_file"`
		DiscoveryRevision string `json:"discovery_revision"`
		NodeUID           string `json:"node_uid"`
		NodeName          string `json:"node_name"`
		BaselineRef       string `json:"baseline_ref"`
	}{address, "accelerator.test", runtime.PKI.CAFile, runtime.GovCert, runtime.GovKey, input.DiscoveryRevision, input.Groups[0].NewGroup.Nodes[0].UID, input.Groups[0].NewGroup.Nodes[0].Name, "baseline-1"}
	encoded, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exportFile, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(exportFile) })
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(exportFile + ".stop"); err == nil {
			operation, err := fixture.db.LatestInitializationOperation(context.Background(), fixture.cluster.ConnectionRef)
			if err != nil || operation == nil || operation.State != biz.InitializationSucceeded {
				t.Fatalf("joint HTTP caller did not finish durable initialization: %+v %v", operation, err)
			}
			t.Log("real authenticated RPC/PG/worker initialization completed; Kubernetes discovery and labels are external boundary fixtures")
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("joint HTTP caller did not stop helper before deadline")
		case <-ticker.C:
		}
	}
}
