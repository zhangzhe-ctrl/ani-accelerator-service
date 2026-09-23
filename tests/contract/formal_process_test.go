package contract_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jackc/pgx/v5"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// A-layer gate runs an ordinary go-built production executable. Its only
// external substitute is a separately authenticated HTTP provider source. No
// test evidence implementation, hardware proof or production injection RPC is
// linked into that executable. The A DSN must name a separate database.
func TestFormalProductionConsumptionGate(t *testing.T) {
	dsn := os.Getenv("ACC_FORMAL_DSN_FILE")
	if dsn == "" {
		t.Skip("requires independent A-layer PostgreSQL DSN")
	}
	if formalDatabase(t, dsn) == formalDatabase(t, os.Getenv("ACC_TEST_DSN_FILE")) {
		t.Fatal("A and software fixture DSNs must be separate")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("repository source unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	work := t.TempDir()
	sourceDir := filepath.Join(work, "source")
	if err := os.Mkdir(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	sourceBinary := filepath.Join(work, "source.test")
	binary := filepath.Join(work, "ani-accelerator-service")
	for _, args := range [][]string{{"test", "-c", "-o", sourceBinary, "./internal/data/provider"}, {"build", "-trimpath", "-o", binary, "./cmd/ani-accelerator-service"}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %v: %v\n%s", args, err, out)
		}
	}
	source := exec.Command(sourceBinary, "-test.run=^TestJointFormalProviderSource$", "-test.timeout=0", "-test.v")
	source.Env = append(os.Environ(), "ACC_FORMAL_SOURCE_DIR="+sourceDir, "GOMAXPROCS=2")
	sourceLog := filepath.Join(work, "source.log")
	stopSource := startFormalCommand(t, source, sourceLog)
	defer func() {
		_ = os.WriteFile(filepath.Join(sourceDir, "stop"), nil, 0600)
		time.Sleep(300 * time.Millisecond)
		stopSource()
	}()
	manifest := filepath.Join(sourceDir, "source.json")
	waitFormalFile(t, manifest)
	var input struct {
		ProviderConfig string                `json:"provider_config_file"`
		Connection     string                `json:"connection_ref"`
		Baseline       *biz.ProviderBaseline `json:"baseline"`
		Node           *biz.NodeRef          `json:"node"`
		GroupFile      string                `json:"group_id_file"`
		Model          string                `json:"model_key"`
		Queue          string                `json:"queue_name"`
	}
	readJointJSON(t, manifest, &input)
	fixture := testkit.NewRuntime(t, address(t), address(t))
	fixture.Config.Database.DsnFile = dsn
	fixture.Config.Accelerator.ProviderConfigFile = input.ProviderConfig
	adminActions := []string{}
	for action := range server.AdminActions {
		adminActions = append(adminActions, action)
	}
	sort.Strings(adminActions)
	fixture.Config.Security.Grants = []*conf.DelegationGrant{{ActorType: "user", ActorId: "contract-user", Actions: adminActions, Platform: true, ClusterIds: []string{"*"}}}
	productionLog := filepath.Join(work, "production.log")
	start := func() func() {
		t.Helper()
		cfg := fixture.WriteConfig(t)
		cmd := exec.Command(binary, "-conf", cfg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
		stop := startFormalCommand(t, cmd, productionLog)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			response, e := (&http.Client{Timeout: 250 * time.Millisecond}).Get("http://" + fixture.Config.Server.Admin.Addr + "/readyz")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return stop
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		logs, _ := os.ReadFile(productionLog)
		t.Fatalf("formal process did not become ready: %s", logs)
		return stop
	}
	stop := start()
	client := conn(t, fixture, fixture.GovCert, fixture.GovKey)
	invoke := func(method, tenant string, in, out proto.Message) error {
		ctx, cancel := context.WithTimeout(callContext(method, tenant), 5*time.Second)
		defer cancel()
		return client.Invoke(ctx, methodPath(method), in, out)
	}
	rpc := func(method string, in, out proto.Message) {
		t.Helper()
		if e := invoke(method, "", in, out); e != nil {
			t.Fatal(method, e)
		}
	}
	cluster := new(pb.Cluster)
	rpc("RegisterCluster", &pb.RegisterClusterRequest{Mutation: mutation(), DisplayName: "formal-A-no-hardware", ConnectionRef: input.Connection}, cluster)
	pool := new(pb.Pool)
	rpc("CreatePool", &pb.CreatePoolRequest{Mutation: mutation(), ClusterId: cluster.ClusterId, DisplayName: "formal-A"}, pool)
	group := new(pb.SupplyGroup)
	rpc("AdoptSupplyGroup", &pb.AdoptSupplyGroupRequest{Mutation: mutation(), PoolId: pool.PoolId, DisplayName: "formal-A", Mode: pb.SupplyMode_SHARED_FIXED, Nodes: []*pb.NodeRef{copyAs[pb.NodeRef](t, input.Node)}, ModelKey: input.Model, Baseline: copyAs[pb.ProviderBaseline](t, input.Baseline), QueueName: input.Queue}, group)
	testkit.Write(t, input.GroupFile, []byte(group.GroupId))
	publish := &pb.PublishProfileRequest{Mutation: mutation(), DisplayName: "must-not-persist", Spec: &pb.ProfileSpec{GroupId: group.GroupId, Mode: pb.SupplyMode_SHARED_FIXED, ModelKey: input.Model, SharedMemoryMib: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}, ExpectedGroupVersion: 1, VerificationRef: "formal:missing-consumption-proof"}
	publishErr := invoke("PublishProfile", "", publish, new(pb.GpuProfile))
	assertFormalReason(t, publishErr, codes.FailedPrecondition, "CONSUMPTION_NOT_VERIFIED")
	openErr := invoke("SetAdmission", "", &pb.SetAdmissionRequest{Mutation: mutation(), GroupId: group.GroupId, ExpectedVersion: 1, Desired: pb.AdmissionState_ADMISSION_OPEN, Reason: "formal A gate"}, new(pb.SupplyGroup))
	assertFormalReason(t, openErr, codes.FailedPrecondition, "PROFILE_NOT_ACTIVE")
	// A tenant grant is bound to the actual registered cluster before Resolve.
	tenant := biz.NewID()
	fixture.Config.Security.Grants = append(fixture.Config.Security.Grants, &conf.DelegationGrant{ActorType: "user", ActorId: "contract-user", Actions: []string{"ResolveGpuRequest"}, TenantIds: []string{tenant}, ClusterIds: []string{cluster.ClusterId}})
	stop()
	stop = start()
	defer stop()
	request := &pb.ResolveGpuRequestRequest{Context: tenantContext(tenant), Gpu: &pb.GpuRequest{ClusterId: cluster.ClusterId, PoolId: pool.PoolId, ProfileId: biz.NewID(), ProfileVersion: 1, Replicas: 1, DevicesPerReplica: 1, ContainerName: "inference"}}
	resolveErr := invoke("ResolveGpuRequest", tenant, request, new(pb.ResolvedGpuPlan))
	assertFormalReason(t, resolveErr, codes.NotFound, "NOT_FOUND")
	profiles := new(pb.ListProfilesResponse)
	rpc("AdminListProfiles", &pb.AdminListProfilesRequest{Context: adminRead(), ClusterId: cluster.ClusterId}, profiles)
	if len(profiles.Items) != 0 {
		t.Fatal("failed Publish persisted profile")
	}
	db, e := data.OpenPostgres(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.View(context.Background(), func(reader biz.Reader) error {
		stored, e := reader.Group(context.Background(), group.GroupId)
		if e != nil {
			return e
		}
		if stored.Admission != biz.Closed || stored.Version != 1 || stored.Baseline.Verification == nil || stored.Baseline.Verification.State != biz.NotVerified {
			t.Fatal("failed Publish or OPEN mutated durable supply", stored)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	files, e := os.ReadDir(fixture.Config.Accelerator.AttestationsDir)
	if e != nil || len(files) != 0 {
		t.Fatal("A attestation directory was not empty", e)
	}
	record := map[string]any{"status": "pass", "layer": "A formal production executable", "binary_sha256": formalFileHash(t, binary), "cluster_id": cluster.ClusterId, "group_id": group.GroupId, "publish": publishErr.Error(), "publish_reason": formalReason(publishErr), "open": openErr.Error(), "open_reason": formalReason(openErr), "resolve": resolveErr.Error(), "resolve_reason": formalReason(resolveErr), "profile_count": 0, "group_version": 1, "admission": "CLOSED", "verification": "NOT_VERIFIED", "attestation_file_count": 0, "hardware": "not_verified", "source": "constructed TLS provider source; production reader and verifier unchanged"}
	if out := os.Getenv("ACC_TEST_EVIDENCE_DIR"); out != "" {
		if e = os.MkdirAll(out, 0700); e != nil {
			t.Fatal(e)
		}
		writeJointJSON(t, filepath.Join(out, "formal-a.json"), record)
		for name, path := range map[string]string{"formal-a-production.log": productionLog, "formal-a-source.log": sourceLog} {
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			testkit.Write(t, filepath.Join(out, name), b)
		}
	}
	t.Logf("A formal process: Publish=%s, OPEN=%s, Resolve=%s; zero profiles; CLOSED/NOT_VERIFIED/version1 retained across restart; hardware=not_verified", formalReason(publishErr), formalReason(openErr), formalReason(resolveErr))
}
func startFormalCommand(t *testing.T, command *exec.Cmd, logPath string) func() {
	t.Helper()
	f, e := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		t.Fatal(e)
	}
	command.Stdout = f
	command.Stderr = f
	if e = command.Start(); e != nil {
		f.Close()
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait(); f.Close() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
	t.Cleanup(stop)
	return stop
}
func waitFormalFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("source process did not publish manifest")
}
func formalReason(err error) string {
	if err == nil {
		return ""
	}
	return kerrors.FromError(err).Reason
}
func assertFormalReason(t *testing.T, err error, code codes.Code, reason string) {
	t.Helper()
	if status.Code(err) != code || formalReason(err) != reason {
		t.Fatalf("expected %s/%s; got %v reason=%s", code, reason, err, formalReason(err))
	}
}
func formalFileHash(t *testing.T, path string) string {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	hash := sha256.New()
	if _, e = io.Copy(hash, f); e != nil {
		t.Fatal(e)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func formalDatabase(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test database secret reference unavailable")
	}
	config, err := pgx.ParseConfig(string(contents))
	if err != nil {
		t.Fatal("test database configuration invalid")
	}
	return config.Database
}
