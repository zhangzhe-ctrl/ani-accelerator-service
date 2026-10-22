package contract_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/provider"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/service"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type nodeWireFault struct {
	grpc.ServerStream
	drop     *atomic.Bool
	dropped  chan<- *nodepb.Ack
	welcomes chan<- *nodepb.Welcome
	reports  *sync.Map
}

func (stream *nodeWireFault) SendMsg(message any) error {
	if center, ok := message.(*nodepb.CenterMessage); ok {
		if welcome := center.GetWelcome(); welcome != nil {
			select {
			case stream.welcomes <- proto.Clone(welcome).(*nodepb.Welcome):
			default:
			}
		}
		if ack := center.GetAck(); ack != nil && stream.drop.CompareAndSwap(false, true) {
			stream.dropped <- proto.Clone(ack).(*nodepb.Ack)
			<-stream.Context().Done()
			return stream.Context().Err()
		}
	}
	return stream.ServerStream.SendMsg(message)
}

func (stream *nodeWireFault) RecvMsg(message any) error {
	if err := stream.ServerStream.RecvMsg(message); err != nil {
		return err
	}
	if incoming, ok := message.(*nodepb.AgentMessage); ok && incoming.GetReport() != nil {
		record := incoming.GetReport().Record
		encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(record)
		if err != nil {
			return err
		}
		stream.reports.Store(record.Sequence, encoded)
	}
	return nil
}

type nodeProcess struct {
	command *exec.Cmd
	done    chan error
	stopped bool
	logPath string
}

func startNodeProcess(testContext *testing.T, binary string, environment []string, root string) *nodeProcess {
	testContext.Helper()
	logPath := filepath.Join(root, "agent-"+biz.NewID()+".log")
	file, err := os.Create(logPath)
	if err != nil {
		testContext.Fatal(err)
	}
	command := exec.Command(binary)
	command.Env = append(os.Environ(), environment...)
	command.Stdout = file
	command.Stderr = file
	if err = command.Start(); err != nil {
		file.Close()
		testContext.Fatal(err)
	}
	process := &nodeProcess{command: command, done: make(chan error, 1), logPath: logPath}
	go func() { process.done <- command.Wait(); file.Close() }()
	testContext.Cleanup(func() { process.stop() })
	return process
}

func (process *nodeProcess) stop() {
	if process.stopped {
		return
	}
	_ = process.command.Process.Kill()
	<-process.done
	process.stopped = true
}

func TestNodeTelemetryAgentProcessDurableMTLSPostgres(testContext *testing.T) {
	binary := os.Getenv("ACC_NODE_AGENT_BINARY")
	dsnFile := os.Getenv("ACC_TEST_DSN_FILE")
	if binary == "" || dsnFile == "" {
		testContext.Skip("requires ACC_NODE_AGENT_BINARY and isolated ACC_TEST_DSN_FILE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	db, err := data.OpenPostgres(ctx, dsnFile)
	if err != nil {
		testContext.Fatal(err)
	}
	defer db.Close()
	connectionRef := "b2-node-" + biz.NewID()
	testContext.Log("task connection_ref", connectionRef)
	root := testContext.TempDir()
	pki := testkit.NewPKI(testContext)
	serverCert, serverKey := pki.Issue(testContext, "node-center", "", true)
	agentCert, agentKey := pki.Issue(testContext, "node-agent", "ani-node-agent", false)
	govCert, govKey := pki.Issue(testContext, "node-governance", "ani-governance", false)
	wrongCert, wrongKey := pki.Issue(testContext, "node-impostor", "ani-node-agent.other", false)
	nodeUID, podUID, daemonSetUID, serviceAccountUID := biz.NewID(), biz.NewID(), biz.NewID(), biz.NewID()
	podName := "ani-node-agent.pod"
	claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(5 * time.Minute).Unix()})
	token := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	var tokenReviews atomic.Uint64
	kube := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer center-credential" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		var result any
		switch request.URL.Path {
		case "/apis/authentication.k8s.io/v1/tokenreviews":
			var body struct {
				Spec struct {
					Token     string   `json:"token"`
					Audiences []string `json:"audiences"`
				} `json:"spec"`
			}
			if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&body) != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			tokenReviews.Add(1)
			if body.Spec.Token != token || len(body.Spec.Audiences) != 1 || body.Spec.Audiences[0] != "ani-node-agent" {
				result = map[string]any{"status": map[string]any{"authenticated": false}}
				break
			}
			result = map[string]any{"status": map[string]any{"authenticated": true, "audiences": []string{"ani-node-agent"}, "user": map[string]any{"username": "system:serviceaccount:ani-node-system:ani-node-agent", "uid": serviceAccountUID, "extra": map[string][]string{"authentication.kubernetes.io/pod-name": {podName}, "authentication.kubernetes.io/pod-uid": {podUID}}}}}
		case "/api/v1/namespaces/ani-node-system/pods/" + podName:
			result = map[string]any{"metadata": map[string]any{"namespace": "ani-node-system", "name": podName, "uid": podUID, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "ani-node-agent", "uid": daemonSetUID, "controller": true}}}, "spec": map[string]any{"serviceAccountName": "ani-node-agent", "nodeName": "b2-node"}}
		case "/api/v1/namespaces/ani-node-system/serviceaccounts/ani-node-agent":
			result = map[string]any{"metadata": map[string]any{"namespace": "ani-node-system", "name": "ani-node-agent", "uid": serviceAccountUID}}
		case "/apis/apps/v1/namespaces/ani-node-system/daemonsets/ani-node-agent":
			result = map[string]any{"metadata": map[string]any{"namespace": "ani-node-system", "name": "ani-node-agent", "uid": daemonSetUID}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"serviceAccountName": "ani-node-agent"}}}}
		case "/api/v1/nodes/b2-node":
			result = map[string]any{"metadata": map[string]any{"name": "b2-node", "uid": nodeUID}}
		default:
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(result)
	}))
	certificate, err := tls.LoadX509KeyPair(serverCert, serverKey)
	if err != nil {
		testContext.Fatal(err)
	}
	kube.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}
	kube.StartTLS()
	defer kube.Close()
	centerTokenFile := filepath.Join(root, "center.token")
	testkit.Write(testContext, centerTokenFile, []byte("center-credential"))
	ownerKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		testContext.Fatal(err)
	}
	nodeProvider, err := provider.New(provider.Config{Connections: []provider.Connection{{Ref: connectionRef, APIServer: kube.URL, ServerName: "accelerator.test", CAFile: pki.CAFile, TokenFile: centerTokenFile, NodeAgentIdentity: &provider.NodeAgentIdentityConfig{Namespace: "ani-node-system", ServiceAccount: "ani-node-agent", DaemonSet: "ani-node-agent", Audience: "ani-node-agent"}}}}, ownerKey, db, time.Minute)
	if err != nil {
		testContext.Fatal(err)
	}
	security := &conf.Security{CaFile: pki.CAFile, CertFile: serverCert, KeyFile: serverKey, GovernanceDnsName: "ani-governance", InferenceDnsName: "ani-inference-service", ResourceDnsName: "ani-resource-service", NodeAgentDnsName: "ani-node-agent"}
	transport, err := server.LoadServerTLS(security)
	if err != nil {
		testContext.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testContext.Fatal(err)
	}
	defer listener.Close()
	dropped := make(chan *nodepb.Ack, 1)
	welcomes := make(chan *nodepb.Welcome, 8)
	var drop atomic.Bool
	reports := new(sync.Map)
	fault := func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		return next(service, &nodeWireFault{ServerStream: stream, drop: &drop, dropped: dropped, welcomes: welcomes, reports: reports})
	}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(transport)), grpc.UnaryInterceptor(server.Authorize(security)), grpc.ChainStreamInterceptor(server.AuthorizeStream(security), fault), grpc.MaxRecvMsgSize(9<<20))
	nodeHandler := server.NewNodeTelemetryServer(db, nodeProvider)
	nodeDescription := nodepb.NodeTelemetryService_ServiceDesc
	nodeDescription.Streams = append(append([]grpc.StreamDesc(nil), nodeDescription.Streams...), grpc.StreamDesc{StreamName: "ConnectExtra", ServerStreams: true, ClientStreams: true, Handler: func(service any, stream grpc.ServerStream) error {
		return nodeHandler.Connect(&grpc.GenericServerStream[nodepb.AgentMessage, nodepb.CenterMessage]{ServerStream: stream})
	}})
	grpcServer.RegisterService(&nodeDescription, nodeHandler)
	cursor, err := biz.NewCursorCodec(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		testContext.Fatal(err)
	}
	usecase, err := biz.NewUsecase(db, nodeProvider, cursor, []string{connectionRef}, 16, time.Minute)
	if err != nil {
		testContext.Fatal(err)
	}
	pb.RegisterAcceleratorAdminServiceServer(grpcServer, service.NewAccelerator(usecase))
	go grpcServer.Serve(listener)
	defer grpcServer.Stop()
	clientFor := func(certFile, keyFile string) *grpc.ClientConn {
		testContext.Helper()
		connection, connectErr := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(testkit.ClientTLS(testContext, pki.CAFile, certFile, keyFile))))
		if connectErr != nil {
			testContext.Fatal(connectErr)
		}
		testContext.Cleanup(func() { connection.Close() })
		return connection
	}
	hello := func(candidateToken string) *nodepb.AgentMessage {
		return &nodepb.AgentMessage{Message: &nodepb.AgentMessage_Hello{Hello: &nodepb.AgentHello{ProtocolVersion: 1, ConnectionRef: connectionRef, PodNamespace: "ani-node-system", PodName: podName, PodUid: podUID, InstanceId: biz.NewID(), LogId: biz.NewID(), BootId: biz.NewID(), ServiceAccountToken: candidateToken, NodeName: "b2-node"}}}
	}
	for _, scenario := range []struct{ name, cert, key, token string }{{"impostor_SAN", wrongCert, wrongKey, token}, {"governance_stream", govCert, govKey, token}, {"wrong_token", agentCert, agentKey, "invalid-token"}} {
		testContext.Run(scenario.name, func(caseContext *testing.T) {
			callCtx, callCancel := context.WithTimeout(ctx, 3*time.Second)
			defer callCancel()
			stream, connectErr := nodepb.NewNodeTelemetryServiceClient(clientFor(scenario.cert, scenario.key)).Connect(callCtx)
			if connectErr != nil {
				caseContext.Fatal(connectErr)
			}
			_ = stream.Send(hello(scenario.token))
			_, connectErr = stream.Recv()
			if status.Code(connectErr) != codes.PermissionDenied {
				caseContext.Fatalf("identity boundary returned %s", status.Code(connectErr))
			}
		})
	}
	if _, err = pb.NewAcceleratorAdminServiceClient(clientFor(agentCert, agentKey)).AdminListProfiles(ctx, &pb.AdminListProfilesRequest{}); status.Code(err) != codes.PermissionDenied {
		testContext.Fatalf("Agent called business RPC: %s", status.Code(err))
	}
	wrongMethod, err := clientFor(agentCert, agentKey).NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, nodepb.NodeTelemetryService_Connect_FullMethodName+"Extra")
	if err != nil {
		testContext.Fatal(err)
	}
	_ = wrongMethod.SendMsg(hello(token))
	if err = wrongMethod.RecvMsg(new(nodepb.CenterMessage)); status.Code(err) != codes.PermissionDenied {
		testContext.Fatalf("nonexact stream method accepted: %s", status.Code(err))
	}
	views, err := db.NodeTelemetry(ctx, connectionRef)
	if err != nil || len(views) != 0 {
		testContext.Fatalf("denied caller created node state: %d %v", len(views), err)
	}
	criSocket, resourcesSocket := startNodeSourceSockets(testContext, root)
	tokenFile := filepath.Join(root, "agent.token")
	testkit.Write(testContext, tokenFile, []byte(token))
	bootFile := filepath.Join(root, "boot-id")
	testkit.Write(testContext, bootFile, []byte(biz.NewID()))
	stateDirectory := filepath.Join(root, "state")
	journalDirectory := filepath.Join(root, "journal")
	if err = os.Mkdir(journalDirectory, 0700); err != nil {
		testContext.Fatal(err)
	}
	journalData := filepath.Join(root, "journal.jsonl")
	writeNodeJournal(testContext, journalData, strings.Repeat("a", 64))
	journalCommand := filepath.Join(root, "journalctl")
	if err = os.WriteFile(journalCommand, []byte("#!/bin/sh\nexec cat '"+strings.ReplaceAll(journalData, "'", "'\\''")+"'\n"), 0700); err != nil {
		testContext.Fatal(err)
	}
	environment := []string{"ANI_ACC_ENDPOINT=" + listener.Addr().String(), "ANI_ACC_SERVER_DNS=accelerator.test", "ANI_TLS_CERT_FILE=" + agentCert, "ANI_TLS_KEY_FILE=" + agentKey, "ANI_TLS_CA_FILE=" + pki.CAFile, "ANI_SA_TOKEN_FILE=" + tokenFile, "ANI_CONNECTION_REF=" + connectionRef, "ANI_POD_NAMESPACE=ani-node-system", "ANI_POD_NAME=" + podName, "ANI_POD_UID=" + podUID, "ANI_NODE_NAME=b2-node", "ANI_NODE_UID=", "ANI_NODE_UID_FILE=", "ANI_STATE_DIRECTORY=" + stateDirectory, "ANI_CRI_SOCKET=" + criSocket, "ANI_POD_RESOURCES_SOCKET=" + resourcesSocket, "ANI_SOURCE_FRESHNESS=1m", "ANI_SNAPSHOT_INTERVAL=250ms", "ANI_BOOT_ID_FILE=" + bootFile, "ANI_JOURNAL_DIRECTORY=" + journalDirectory, "ANI_MACHINE_ID_FILE=", "ANI_MACHINE_ID=" + strings.Repeat("a", 32), "ANI_JOURNALCTL_PATH=" + journalCommand}
	first := startNodeProcess(testContext, binary, environment, root)
	var receipt *nodepb.Ack
	select {
	case receipt = <-dropped:
	case <-ctx.Done():
		logBytes, _ := os.ReadFile(first.logPath)
		testContext.Fatalf("Agent did not commit a Report before timeout: %s", logBytes)
	}
	nodeAwait(testContext, ctx, first, func() bool {
		_, statErr := os.Stat(filepath.Join(stateDirectory, fmt.Sprintf("%020d.record", receipt.Sequence+1)))
		return statErr == nil
	})
	var pending struct {
		Bytes []byte `json:"bytes"`
	}
	pendingPath := filepath.Join(stateDirectory, fmt.Sprintf("%020d.record", receipt.Sequence+1))
	readNodeJSON(testContext, pendingPath, &pending)
	var original nodepb.ImmutableRecord
	if err = proto.Unmarshal(pending.Bytes, &original); err != nil {
		testContext.Fatal(err)
	}
	first.stop()
	views, err = db.NodeTelemetry(ctx, connectionRef)
	if err != nil || len(views) != 1 || views[0].State.LastSequence != receipt.Sequence || views[0].State.LastPayloadDigest != receipt.PayloadDigest {
		testContext.Fatal("dropped ACK was not backed by committed PostgreSQL receipt", err)
	}
	var localReceipt struct {
		Sequence uint64 `json:"sequence"`
	}
	if ackBytes, readErr := os.ReadFile(filepath.Join(stateDirectory, "ack.json")); readErr == nil {
		if err = json.Unmarshal(ackBytes, &localReceipt); err != nil {
			testContext.Fatal(err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		testContext.Fatal(readErr)
	}
	if localReceipt.Sequence >= receipt.Sequence {
		testContext.Fatal("dropped ACK advanced local watermark")
	}
	second := startNodeProcess(testContext, binary, environment, root)
	var initialEpoch uint64
	for initialEpoch == 0 {
		select {
		case welcome := <-welcomes:
			if welcome.LastCommittedSequence == 0 {
				initialEpoch = welcome.Epoch
			}
		case <-ctx.Done():
			testContext.Fatal("missing initial Welcome")
		}
	}
	var replayWelcome *nodepb.Welcome
	for replayWelcome == nil {
		select {
		case welcome := <-welcomes:
			if welcome.Epoch > initialEpoch {
				replayWelcome = welcome
			}
		case <-ctx.Done():
			testContext.Fatal("restart did not reconnect")
		}
	}
	if replayWelcome.NodeUid != nodeUID || replayWelcome.ActiveLogId != receipt.LogId || replayWelcome.LastCommittedSequence != receipt.Sequence || replayWelcome.LastPayloadDigest != receipt.PayloadDigest {
		testContext.Fatalf("restart lost exact last receipt: %+v", replayWelcome)
	}
	nodeAwait(testContext, ctx, second, func() bool {
		current, readErr := db.NodeTelemetry(ctx, connectionRef)
		if readErr != nil || len(current) != 1 {
			return false
		}
		views = current
		return current[0].State.LastSequence > receipt.Sequence && current[0].State.Snapshot != nil && len(current[0].Attempts) == 2
	})
	actual, found := reports.Load(original.Sequence)
	if !found || !bytes.Equal(actual.([]byte), pending.Bytes) {
		testContext.Fatal("restart changed original pending record bytes or origin")
	}
	if views[0].State.Identity.InstanceID == original.OriginInstanceId {
		testContext.Fatal("process restart reused instance identity")
	}
	if views[0].State.Snapshot.CaptureEpoch != replayWelcome.Epoch {
		testContext.Fatal("new transport did not append a genuine current-epoch snapshot")
	}
	for _, attempt := range views[0].Attempts {
		if attempt.ContainerID == strings.Repeat("b", 64) && (attempt.State != 4 || attempt.ContainerName != "" || attempt.EventType == nil || *attempt.EventType != 2) {
			testContext.Fatal("missing event status metadata did not survive as UNKNOWN")
		}
		if attempt.ContainerID == strings.Repeat("a", 64) && (len(attempt.GPUEnvironment) != 5 || attempt.GPUEnvironment[2].Name != "CUDA_DEVICE_MEMORY_LIMIT_1" || attempt.GPUEnvironment[3].Name != "NVIDIA_VISIBLE_DEVICES") {
			testContext.Fatal("GPU env order, duplicate or unsupported memory index lost")
		}
	}
	sources := map[string]bool{}
	for _, source := range views[0].State.Snapshot.Sources {
		sources[source.Name] = source.Complete
	}
	if !sources["cri"] || !sources["podresources"] {
		testContext.Fatal("real Agent did not read source socket fixtures")
	}
	if sources["nvml"] {
		testContext.Log("NVML available on Fedora; this software case still does not create GPU supply")
	}
	if len(views[0].State.Snapshot.Allocations) != 1 || views[0].State.Snapshot.Allocations[0].DeviceIDs[0] != "GPU-fixture-0" {
		testContext.Fatal("raw PodResources allocation was changed")
	}
	if err = db.View(ctx, func(reader biz.Reader) error {
		clusters, listErr := reader.Clusters(ctx, []string{connectionRef}, "", 200)
		if listErr != nil {
			return listErr
		}
		if len(clusters) != 0 {
			return errors.New("pre-adoption telemetry created business supply")
		}
		return nil
	}); err != nil {
		testContext.Fatal(err)
	}
	nodeAwait(testContext, ctx, second, func() bool {
		readNodeJSON(testContext, filepath.Join(stateDirectory, "ack.json"), &localReceipt)
		return localReceipt.Sequence > receipt.Sequence
	})
	beforeJournal, err := db.NodeTelemetry(ctx, connectionRef)
	if err != nil {
		testContext.Fatal(err)
	}
	recoveryPod := biz.PodRef{ClusterID: biz.NewID(), UID: "11111111-1111-4111-8111-111111111111", Namespace: "tenant-b2", Name: "gpu-workload", ContainerName: "workspace"}
	oldExit, found, err := nodeHandler.RecoverNodeExit(ctx, connectionRef, nodeUID, recoveryPod, []string{strings.Repeat("a", 64)})
	if err != nil || found || len(oldExit) != 1 || oldExit[0].State == 3 {
		testContext.Fatalf("old journal exit overrode current Running: found=%v attempts=%+v err=%v", found, oldExit, err)
	}
	writeNodeJournal(testContext, journalData, strings.Repeat("d", 64))
	positive, found, err := nodeHandler.RecoverNodeExit(ctx, connectionRef, nodeUID, recoveryPod, []string{strings.Repeat("d", 64)})
	if err != nil || !found || len(positive) != 1 || positive[0].State != 3 || len(positive[0].JournalProvenance) == 0 {
		testContext.Fatalf("positive original journal chain did not commit: found=%v attempts=%+v err=%v", found, positive, err)
	}
	afterJournal, err := db.NodeTelemetry(ctx, connectionRef)
	if err != nil {
		testContext.Fatal(err)
	}
	if !reflect.DeepEqual(beforeJournal[0].State.Gaps, afterJournal[0].State.Gaps) {
		testContext.Fatal("local journal result cleared or rewrote global source gaps")
	}
	second.stop()
	testContext.Logf("PASS real Agent process → production mTLS/TokenReview/Pod-SA-DS-Node verifier → production stream/domain → PostgreSQL; dropped ACK seq=%d, restart exact receipt/original bytes, two runtime attempts, pre-adoption no supply; external fixtures: Kubernetes HTTP and CRI/PodResources Unix sockets; real GPU lifecycle NOT_RUN; TokenReviews=%d", receipt.Sequence, tokenReviews.Load())
}

func writeNodeJournal(testContext *testing.T, path, containerID string) {
	testContext.Helper()
	sandbox := strings.Repeat("c", 64)
	base := time.Now().Add(-5 * time.Second)
	messages := []string{`RunPodSandbox for name:"gpu-workload" uid:"11111111-1111-4111-8111-111111111111" namespace:"tenant-b2" returns sandbox id "` + sandbox + `"`, `CreateContainer within sandbox "` + sandbox + `" for name:"workspace" returns container id "` + containerID + `"`, `received container exit event container_id:"` + containerID + `" id:"` + containerID + `" exited_at:{seconds:` + strconv.FormatInt(base.Add(2*time.Second).Unix(), 10) + `}`}
	var output bytes.Buffer
	for index, message := range messages {
		record := map[string]string{"MESSAGE": "level=info msg=" + strconv.Quote(message), "_MACHINE_ID": strings.Repeat("a", 32), "_BOOT_ID": strings.Repeat("b", 32), "_SYSTEMD_UNIT": "containerd.service", "_COMM": "containerd", "_UID": "0", "_EXE": "/usr/local/bin/containerd", "_TRANSPORT": "stdout", "__CURSOR": fmt.Sprintf("journal-cursor-%d", index), "__REALTIME_TIMESTAMP": strconv.FormatInt(base.Add(time.Duration(index+1)*time.Second).UnixMicro(), 10), "__MONOTONIC_TIMESTAMP": strconv.Itoa((index + 1) * 100)}
		if err := json.NewEncoder(&output).Encode(record); err != nil {
			testContext.Fatal(err)
		}
	}
	testkit.Write(testContext, path, output.Bytes())
}

func nodeAwait(testContext *testing.T, ctx context.Context, process *nodeProcess, ready func() bool) {
	testContext.Helper()
	for !ready() {
		select {
		case err := <-process.done:
			process.stopped = true
			logBytes, _ := os.ReadFile(process.logPath)
			testContext.Fatalf("Agent exited %v: %s", err, logBytes)
		case <-ctx.Done():
			logBytes, _ := os.ReadFile(process.logPath)
			testContext.Fatalf("Agent condition timed out: %s", logBytes)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func readNodeJSON(testContext *testing.T, path string, value any) {
	testContext.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		testContext.Fatal(err)
	}
	if err = json.Unmarshal(data, value); err != nil {
		testContext.Fatal(err)
	}
}

type nodeCRI struct {
	cri.UnimplementedRuntimeServiceServer
	observed int64
}

func (source *nodeCRI) Version(context.Context, *cri.VersionRequest) (*cri.VersionResponse, error) {
	return &cri.VersionResponse{Version: "0.1.0", RuntimeName: "containerd", RuntimeVersion: "2.1.5", RuntimeApiVersion: "v1"}, nil
}
func (source *nodeCRI) ListContainers(context.Context, *cri.ListContainersRequest) (*cri.ListContainersResponse, error) {
	return &cri.ListContainersResponse{Containers: []*cri.Container{{Id: strings.Repeat("a", 64), PodSandboxId: strings.Repeat("c", 64), Metadata: &cri.ContainerMetadata{Name: "workspace", Attempt: 2}, Labels: map[string]string{"io.kubernetes.pod.uid": "11111111-1111-4111-8111-111111111111", "io.kubernetes.pod.namespace": "tenant-b2", "io.kubernetes.pod.name": "gpu-workload"}, CreatedAt: source.observed, State: cri.ContainerState_CONTAINER_RUNNING}}}, nil
}
func (source *nodeCRI) ContainerStatus(context.Context, *cri.ContainerStatusRequest) (*cri.ContainerStatusResponse, error) {
	return &cri.ContainerStatusResponse{Status: &cri.ContainerStatus{Id: strings.Repeat("a", 64), Metadata: &cri.ContainerMetadata{Name: "workspace", Attempt: 2}, Labels: map[string]string{"io.kubernetes.pod.uid": "11111111-1111-4111-8111-111111111111", "io.kubernetes.pod.namespace": "tenant-b2", "io.kubernetes.pod.name": "gpu-workload"}, State: cri.ContainerState_CONTAINER_RUNNING, CreatedAt: source.observed, StartedAt: source.observed}, Info: map[string]string{"info": `{"runtimeSpec":{"process":{"env":["SECRET=not-exported","NVIDIA_VISIBLE_DEVICES=GPU-11111111-1111-4111-8111-111111111111","CUDA_DEVICE_MEMORY_LIMIT_0=1024m","CUDA_DEVICE_MEMORY_LIMIT_1=2048m","NVIDIA_VISIBLE_DEVICES=GPU-22222222-2222-4222-8222-222222222222","CUDA_DEVICE_SM_LIMIT=25"]}}}`}}, nil
}
func (source *nodeCRI) GetContainerEvents(_ *cri.GetEventsRequest, stream cri.RuntimeService_GetContainerEventsServer) error {
	if err := stream.Send(&cri.ContainerEventResponse{ContainerId: strings.Repeat("b", 64), ContainerEventType: cri.ContainerEventType_CONTAINER_STARTED_EVENT, CreatedAt: source.observed, PodSandboxStatus: &cri.PodSandboxStatus{Id: strings.Repeat("c", 64), Metadata: &cri.PodSandboxMetadata{Uid: "11111111-1111-4111-8111-111111111111", Namespace: "tenant-b2", Name: "gpu-workload"}}}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

type nodePodResources struct {
	pr.UnimplementedPodResourcesListerServer
}

func (nodePodResources) List(context.Context, *pr.ListPodResourcesRequest) (*pr.ListPodResourcesResponse, error) {
	return &pr.ListPodResourcesResponse{PodResources: []*pr.PodResources{{Namespace: "tenant-b2", Name: "gpu-workload", Containers: []*pr.ContainerResources{{Name: "workspace", Devices: []*pr.ContainerDevices{{ResourceName: "volcano.sh/vgpu-number", DeviceIds: []string{"GPU-fixture-0"}}}}}}}}, nil
}

func startNodeSourceSockets(testContext *testing.T, root string) (string, string) {
	testContext.Helper()
	paths := []string{filepath.Join(root, "cri.sock"), filepath.Join(root, "podresources.sock")}
	for index, path := range paths {
		listener, err := net.Listen("unix", path)
		if err != nil {
			testContext.Fatal(err)
		}
		server := grpc.NewServer()
		if index == 0 {
			cri.RegisterRuntimeServiceServer(server, &nodeCRI{observed: time.Now().Add(-time.Second).UnixNano()})
		} else {
			pr.RegisterPodResourcesListerServer(server, nodePodResources{})
		}
		go server.Serve(listener)
		testContext.Cleanup(func() { server.Stop(); listener.Close() })
	}
	return paths[0], paths[1]
}
