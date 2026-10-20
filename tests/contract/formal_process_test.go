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
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jackc/pgx/v5"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// A-layer gate runs an ordinary go-built production executable. Its only
// external substitutes are the authenticated Kubernetes, local runtime and NVML
// sources. No test business implementation or injection RPC is linked into the
// executable. This is software coverage, not actual GPU validation.
func TestFormalProductionWithoutTestProof(t *testing.T) {
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
	nvmlDirectory := formalNVMLLibrary(t, work)
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
	// The optional long-lived A integration uses the same independent CA and
	// ordinary executable; certificate aliases are transport fixture plumbing.
	fixture.GovCert, fixture.GovKey = jointCertificate(t, fixture.PKI, "formal-gov", []string{"ani-governance"})
	fixture.Config.Security.CertFile, fixture.Config.Security.KeyFile = jointCertificate(t, fixture.PKI, "formal-acc", []string{"ani-accelerator", "accelerator.test"})
	fixture.Config.Database.DsnFile = dsn
	fixture.Config.Accelerator.ProviderConfigFile = input.ProviderConfig
	productionLog := filepath.Join(work, "production.log")
	productionPID := 0
	start := func() func() {
		t.Helper()
		cfg := fixture.WriteConfig(t)
		cmd := exec.Command(binary, "-conf", cfg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOMAXPROCS=2", "LD_LIBRARY_PATH="+nvmlDirectory)
		stop := startFormalCommand(t, cmd, productionLog)
		productionPID = cmd.Process.Pid
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
	profile := new(pb.GpuProfile)
	rpc("CreateProfile", &pb.CreateProfileRequest{Mutation: mutation(), DisplayName: "no-test-proof", Spec: &pb.ProfileSpec{GroupId: group.GroupId, Mode: pb.SupplyMode_SHARED_FIXED, ModelKey: input.Model, MemoryMibPerDevice: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}, ExpectedGroupVersion: 1}, profile)
	rpc("SetAdmission", &pb.SetAdmissionRequest{Mutation: mutation(), GroupId: group.GroupId, ExpectedVersion: 2, Desired: pb.AdmissionState_ADMISSION_OPEN, Reason: "formal source wiring"}, group)
	// The tenant is supplied by Gov; ACC needs no tenant/actor configuration.
	tenant := biz.NewID()
	stop()
	stop = start()
	defer stop()
	request := &pb.ResolveGpuRequestRequest{Context: tenantContext(tenant), Gpu: &pb.GpuRequest{ClusterId: cluster.ClusterId, PoolId: pool.PoolId, ProfileId: profile.ProfileId, ProfileVersion: 1, Replicas: 1, DevicesPerReplica: 1, ContainerName: "inference"}}
	resolved := new(pb.ResolvedGpuRequest)
	if err := invoke("ResolveGpuRequest", tenant, request, resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.GetExecution().GetResources()["volcano.sh/vgpu-memory"] != "6144" || resolved.GetExecution().GetRuntimeClassName() != "nvidia" {
		t.Fatal(resolved)
	}
	profiles := new(pb.ListProfilesResponse)
	rpc("AdminListProfiles", &pb.AdminListProfilesRequest{Context: adminRead(), ClusterId: cluster.ClusterId}, profiles)
	if len(profiles.Items) != 1 || profiles.Items[0].ProfileId != profile.ProfileId {
		t.Fatal("created profile did not survive restart", profiles)
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
		if stored.Admission != biz.Open || stored.Version != 3 {
			t.Fatal("created and opened supply did not persist", stored)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	record := map[string]any{"status": "PASS", "layer": "ordinary production executable with external source substitutes", "binary_sha256": formalFileHash(t, binary), "cluster_id": cluster.ClusterId, "group_id": group.GroupId, "profile_id": profile.ProfileId, "create": "PASS", "open": "PASS", "resolve": "PASS", "profile_count": 1, "group_version": 3, "admission": "OPEN", "execution_digest": resolved.Execution.ExecutionDigest, "hardware": "NOT_RUN", "source": "constructed authenticated Kubernetes, Unix CRI/PodResources and NVML library; production business, persistence and readers unchanged"}
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
	t.Log("ordinary production executable: Create/OPEN/Resolve succeed without test-proof configuration; persisted across restart; external source substitutes only; hardware NOT_RUN")
	if hold := os.Getenv("ACC_FORMAL_HOLD_DIR"); hold != "" {
		if !filepath.IsAbs(hold) {
			t.Fatal("absolute task-owned hold directory required")
		}
		if _, e := os.Stat(filepath.Join(hold, "ready.json")); e == nil {
			t.Fatal("existing A helper must be reviewed before reuse")
		}
		if e := os.MkdirAll(hold, 0700); e != nil {
			t.Fatal(e)
		}
		writeJointJSON(t, filepath.Join(hold, "ready.json"), map[string]any{"address": fixture.Config.Server.Grpc.Addr, "server_name": "ani-accelerator", "ca_file": fixture.PKI.CAFile, "gov_cert_file": fixture.GovCert, "gov_key_file": fixture.GovKey, "config_file": fixture.WriteConfig(t), "binary": binary, "binary_sha256": formalFileHash(t, binary), "production_pid": productionPID, "cluster_id": cluster.ClusterId, "pool_id": pool.PoolId, "group_id": group.GroupId, "actor_type": "user", "actor_id": "1", "source_manifest": manifest, "record": record, "scope": "ordinary production executable; external source substitutes; actual hardware NOT_RUN"})
		for {
			if _, e := os.Stat(filepath.Join(hold, "stop")); e == nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
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

// This shared library replaces only the external NVML ABI in a separate ordinary
// production executable. No eligibility, rendering or persistence logic is faked.
func formalNVMLLibrary(t *testing.T, root string) string {
	t.Helper()
	directory := filepath.Join(root, "nvml-external-source")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "nvml.c")
	code := `#include <stdint.h>
#include <stdio.h>
typedef struct { uint64_t total, free, used; } memory;
int nvmlInit(void) { return 0; }
int nvmlInit_v2(void) { return 0; }
int nvmlShutdown(void) { return 0; }
int nvmlDeviceGetCount_v2(unsigned int *count) { *count = 1; return 0; }
int nvmlDeviceGetHandleByIndex_v2(unsigned int i, void **device) { if (i) return 2; *device=(void*)1; return 0; }
int nvmlDeviceGetUUID(void *device, char *text, unsigned int length) { snprintf(text,length,"GPU-01234567-89ab-cdef-0123-456789abcdef"); return 0; }
int nvmlDeviceGetName(void *device, char *text, unsigned int length) { snprintf(text,length,"Test"); return 0; }
int nvmlDeviceGetMemoryInfo(void *device, memory *info) { info->total = 24576ULL << 20; info->free=info->total; info->used=0; return 0; }
const char *nvmlErrorString(int result) { return "external source fixture"; }
`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cc", "-shared", "-fPIC", "-o", filepath.Join(directory, "libnvidia-ml.so.1"), source)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external NVML fixture: %v\n%s", err, out)
	}
	return directory
}
