package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"

	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
)

func TestBuildAppRunsProductionComposition(t *testing.T) {
	grpcAddress := reserveAddress(t)
	adminAddress := reserveAddress(t)
	for adminAddress == grpcAddress {
		adminAddress = reserveAddress(t)
	}
	fixture := testkit.NewRuntime(t, grpcAddress, adminAddress)
	bc := fixture.Config
	var logs bytes.Buffer
	app, err := buildApp(bc, newRuntimeLogger(&logs))
	if err != nil {
		t.Fatalf("buildApp() error = %v", err)
	}

	runResult := make(chan error, 1)
	go func() { runResult <- app.Run() }()
	waitForHTTP(t, "http://"+adminAddress+"/readyz")
	assertProductionAdmin(t, adminAddress)
	assertProductionGRPCHealth(t, grpcAddress, fixture)

	if err := app.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("production composition did not stop within its timeout")
	}
	if !strings.Contains(logs.String(), `"service.name":"ani-accelerator-service"`) {
		t.Fatalf("structured service identity missing from logs: %s", logs.String())
	}
	if !regexp.MustCompile(`"trace_id":"[0-9a-f]{32}"`).MatchString(logs.String()) {
		t.Fatalf("request trace correlation missing from logs: %s", logs.String())
	}
	if !regexp.MustCompile(`"span_id":"[0-9a-f]{16}"`).MatchString(logs.String()) {
		t.Fatalf("request span correlation missing from logs: %s", logs.String())
	}
}

func TestBuildAppRejectsInvalidConfig(t *testing.T) {
	_, err := buildApp(&conf.Bootstrap{}, newRuntimeLogger(io.Discard))
	if err == nil {
		t.Fatal("buildApp() unexpectedly accepted an incomplete config")
	}
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved address: %v", err)
	}
	return address
}

func waitForHTTP(t *testing.T, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("endpoint did not become ready: %s", endpoint)
}

func assertProductionAdmin(t *testing.T, address string) {
	t.Helper()
	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + address + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read /healthz: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("GET /healthz = %d %q", response.StatusCode, body)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("GET /healthz content type = %q", contentType)
	}
}

func assertProductionGRPCHealth(t *testing.T, address string, fixture *testkit.Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(testkit.ClientTLS(t, fixture.PKI.CAFile, fixture.GovCert, fixture.GovKey))))
	if err != nil {
		t.Fatalf("dial production gRPC: %v", err)
	}
	defer connection.Close()
	response, err := grpc_health_v1.NewHealthClient(connection).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("production gRPC health: %v", err)
	}
	if response.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("production gRPC health = %s", response.Status)
	}
}
