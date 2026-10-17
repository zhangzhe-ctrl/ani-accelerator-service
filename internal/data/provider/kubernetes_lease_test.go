package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeOnlineUsesBoundFreshLeaseForStaleStatus(t *testing.T) {
	now := time.Date(2026, 10, 16, 17, 41, 29, 0, time.UTC)
	for _, test := range []struct {
		name        string
		change      func(map[string]any, map[string]any)
		status      int
		ttl         time.Duration
		wantOnline  bool
		wantRequest bool
	}{
		{name: "stale_status_fresh_lease", wantOnline: true, wantRequest: true},
		{name: "fresh_status_needs_no_lease", change: func(n, _ map[string]any) {
			leaseTestReady(n)["lastHeartbeatTime"] = now.Add(-time.Second).Format(time.RFC3339Nano)
		}, wantOnline: true},
		{name: "not_ready", change: func(n, _ map[string]any) { leaseTestReady(n)["status"] = "False" }},
		{name: "unknown_ready", change: func(n, _ map[string]any) { leaseTestReady(n)["status"] = "Unknown" }},
		{name: "missing_ready", change: func(n, _ map[string]any) { n["status"].(map[string]any)["conditions"] = []any{} }},
		{name: "lease_forbidden", status: http.StatusForbidden, wantRequest: true},
		{name: "lease_missing", status: http.StatusNotFound, wantRequest: true},
		{name: "lease_expired_by_duration", change: func(_, l map[string]any) {
			l["spec"].(map[string]any)["renewTime"] = now.Add(-41 * time.Second).Format(time.RFC3339Nano)
		}, wantRequest: true},
		{name: "lease_expired_by_freshness", ttl: 5 * time.Second, wantRequest: true},
		{name: "wrong_node_uid", change: func(_, l map[string]any) { leaseTestOwner(l)["uid"] = "recreated-node-uid" }, wantRequest: true},
		{name: "wrong_owner_name", change: func(_, l map[string]any) { leaseTestOwner(l)["name"] = "other-node" }, wantRequest: true},
		{name: "wrong_owner_kind", change: func(_, l map[string]any) { leaseTestOwner(l)["kind"] = "Pod" }, wantRequest: true},
		{name: "missing_owner", change: func(_, l map[string]any) { delete(l["metadata"].(map[string]any), "ownerReferences") }, wantRequest: true},
		{name: "wrong_lease_name", change: func(_, l map[string]any) { l["metadata"].(map[string]any)["name"] = "other-node" }, wantRequest: true},
		{name: "wrong_lease_namespace", change: func(_, l map[string]any) { l["metadata"].(map[string]any)["namespace"] = "default" }, wantRequest: true},
		{name: "wrong_holder", change: func(_, l map[string]any) { l["spec"].(map[string]any)["holderIdentity"] = "other-node" }, wantRequest: true},
		{name: "missing_renew_time", change: func(_, l map[string]any) { delete(l["spec"].(map[string]any), "renewTime") }, wantRequest: true},
		{name: "future_renew_time", change: func(_, l map[string]any) {
			l["spec"].(map[string]any)["renewTime"] = now.Add(time.Minute).Format(time.RFC3339Nano)
		}, wantRequest: true},
		{name: "invalid_duration", change: func(_, l map[string]any) { l["spec"].(map[string]any)["leaseDurationSeconds"] = 0 }, wantRequest: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodeJSON := map[string]any{
				"metadata": map[string]any{"uid": "node-uid", "name": "ani-03"},
				"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": now.Add(-6 * time.Minute).Format(time.RFC3339Nano)}}},
			}
			leaseJSON := map[string]any{
				"metadata": map[string]any{"name": "ani-03", "namespace": "kube-node-lease", "ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Node", "name": "ani-03", "uid": "node-uid"}}},
				"spec":     map[string]any{"holderIdentity": "ani-03", "leaseDurationSeconds": 40, "renewTime": now.Add(-7 * time.Second).Format(time.RFC3339Nano)},
			}
			if test.change != nil {
				test.change(nodeJSON, leaseJSON)
			}
			rawNode, err := json.Marshal(nodeJSON)
			if err != nil {
				t.Fatal(err)
			}
			var n node
			if err := json.Unmarshal(rawNode, &n); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Method != http.MethodGet || request.URL.Path != "/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases/ani-03" || request.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected lease request: %s %s", request.Method, request.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				if test.status != 0 {
					w.WriteHeader(test.status)
					return
				}
				if err := json.NewEncoder(w).Encode(leaseJSON); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			reader := &kubeReader{config: Connection{APIServer: server.URL}, client: server.Client(), token: "test-token"}
			ttl := test.ttl
			if ttl == 0 {
				ttl = 120 * time.Second
			}
			if got := reader.nodeOnline(context.Background(), n, now, ttl); got != test.wantOnline {
				t.Errorf("online = %v, want %v", got, test.wantOnline)
			}
			if got := requests.Load() > 0; got != test.wantRequest {
				t.Errorf("lease requested = %v, want %v", got, test.wantRequest)
			}
		})
	}
}

func leaseTestReady(n map[string]any) map[string]any {
	return n["status"].(map[string]any)["conditions"].([]any)[0].(map[string]any)
}

func leaseTestOwner(l map[string]any) map[string]any {
	return l["metadata"].(map[string]any)["ownerReferences"].([]any)[0].(map[string]any)
}
