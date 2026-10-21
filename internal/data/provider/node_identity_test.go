package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestVerifyNodeIdentityBindsReviewedTokenToLiveObjects(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*biz.NodeIdentityRequest, map[string]map[string]any)
		allow  bool
	}{
		{name: "bound_pod", allow: true},
		{name: "derive_node_uid", allow: true, change: func(request *biz.NodeIdentityRequest, _ map[string]map[string]any) { request.NodeUID = "" }},
		{name: "wrong_request_namespace", change: func(request *biz.NodeIdentityRequest, _ map[string]map[string]any) { request.PodNamespace = "default" }},
		{name: "unauthenticated", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			identityTestStatus(objects)["authenticated"] = false
		}},
		{name: "wrong_audience", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			identityTestStatus(objects)["audiences"] = []string{"other"}
		}},
		{name: "missing_pod_binding", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			delete(identityTestUser(objects)["extra"].(map[string]any), "authentication.kubernetes.io/pod-uid")
		}},
		{name: "wrong_pod_binding", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			identityTestUser(objects)["extra"].(map[string]any)["authentication.kubernetes.io/pod-uid"] = []string{biz.NewID()}
		}},
		{name: "wrong_service_account", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			identityTestUser(objects)["username"] = "system:serviceaccount:ani-node-system:other"
		}},
		{name: "recreated_service_account", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["serviceaccounts"]["metadata"].(map[string]any)["uid"] = biz.NewID()
		}},
		{name: "recreated_pod", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["pods"]["metadata"].(map[string]any)["uid"] = biz.NewID()
		}},
		{name: "deleting_pod", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["pods"]["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
		}},
		{name: "wrong_actual_node", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["pods"]["spec"].(map[string]any)["nodeName"] = "ani-02"
		}},
		{name: "recreated_daemonset", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["daemonsets"]["metadata"].(map[string]any)["uid"] = biz.NewID()
		}},
		{name: "noncontroller_owner", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["pods"]["metadata"].(map[string]any)["ownerReferences"].([]any)[0].(map[string]any)["controller"] = false
		}},
		{name: "recreated_node", change: func(_ *biz.NodeIdentityRequest, objects map[string]map[string]any) {
			objects["nodes"]["metadata"].(map[string]any)["uid"] = biz.NewID()
		}},
		{name: "expired_token", change: func(request *biz.NodeIdentityRequest, _ map[string]map[string]any) {
			request.Token = identityTestToken(time.Now().Add(-time.Second))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, objects := identityTestObjects()
			if test.change != nil {
				test.change(&request, objects)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
				if incoming.Header.Get("Authorization") != "Bearer center-token" {
					t.Error("missing center credential")
					writer.WriteHeader(http.StatusUnauthorized)
					return
				}
				var object map[string]any
				switch incoming.URL.Path {
				case "/apis/authentication.k8s.io/v1/tokenreviews":
					if incoming.Method != http.MethodPost {
						t.Error("TokenReview did not use POST")
					}
					var body struct {
						Spec struct {
							Token     string   `json:"token"`
							Audiences []string `json:"audiences"`
						} `json:"spec"`
					}
					if json.NewDecoder(incoming.Body).Decode(&body) != nil || body.Spec.Token != request.Token || len(body.Spec.Audiences) != 1 || body.Spec.Audiences[0] != "ani-node-agent" {
						t.Error("TokenReview request does not match configured audience and token")
					}
					object = objects["tokenreviews"]
				case "/api/v1/namespaces/ani-node-system/pods/ani-node-agent-pod":
					object = objects["pods"]
				case "/api/v1/namespaces/ani-node-system/serviceaccounts/ani-node-agent":
					object = objects["serviceaccounts"]
				case "/apis/apps/v1/namespaces/ani-node-system/daemonsets/ani-node-agent":
					object = objects["daemonsets"]
				case "/api/v1/nodes/ani-03":
					object = objects["nodes"]
				default:
					t.Errorf("unexpected identity request path: %s", incoming.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				if err := json.NewEncoder(writer).Encode(object); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			reader := &kubeReader{config: Connection{Ref: "local", APIServer: server.URL, NodeAgentIdentity: &NodeAgentIdentityConfig{Namespace: "ani-node-system", ServiceAccount: "ani-node-agent", DaemonSet: "ani-node-agent", Audience: "ani-node-agent"}}, client: server.Client(), token: "center-token"}
			provider := &Provider{connections: map[string]*kubeReader{"local": reader}}
			identity, err := provider.VerifyNodeIdentity(context.Background(), request)
			if test.allow {
				if err != nil {
					t.Fatal(err)
				}
				if identity.NodeUID != objects["nodes"]["metadata"].(map[string]any)["uid"] || identity.PodUID != request.PodUID || identity.LogID != request.LogID || identity.DaemonSetUID != objects["daemonsets"]["metadata"].(map[string]any)["uid"] || !identity.TokenExpiresAt.After(time.Now()) {
					t.Fatal("verified identity lost its live object or expiry binding")
				}
			} else if err == nil {
				t.Fatal("unbound identity accepted")
			}
		})
	}
}

func TestKubeReaderReloadsTokenFileForReadsAndReviews(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("first-token"), 0600); err != nil {
		t.Fatal(err)
	}
	var credentials []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		credentials = append(credentials, request.Header.Get("Authorization"))
		if request.Method == http.MethodPost {
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": map[string]any{"authenticated": true, "audiences": []string{"ani-node-agent"}}})
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	reader := &kubeReader{config: Connection{APIServer: server.URL, TokenFile: tokenFile}, client: server.Client(), token: "obsolete-token"}
	var object map[string]any
	if err := reader.get(context.Background(), "/api/v1/nodes", &object); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("second-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.reviewNodeToken(context.Background(), "pod-token", "ani-node-agent"); err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 || credentials[0] != "Bearer first-token" || credentials[1] != "Bearer second-token" {
		t.Fatal("rotated service credential was not reloaded")
	}
	if err := os.Remove(tokenFile); err != nil {
		t.Fatal(err)
	}
	if err := reader.get(context.Background(), "/api/v1/nodes", &object); err == nil {
		t.Fatal("missing rotated token silently reused old credential")
	}
	if len(credentials) != 2 {
		t.Fatal("request was sent after token file became unavailable")
	}
}

func identityTestToken(expiry time.Time) string {
	claims, _ := json.Marshal(map[string]any{"exp": expiry.Unix()})
	return "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func identityTestObjects() (biz.NodeIdentityRequest, map[string]map[string]any) {
	request := biz.NodeIdentityRequest{ConnectionRef: "local", PodNamespace: "ani-node-system", PodName: "ani-node-agent-pod", PodUID: biz.NewID(), NodeUID: biz.NewID(), NodeName: "ani-03", InstanceID: biz.NewID(), LogID: biz.NewID(), BootID: biz.NewID(), Token: identityTestToken(time.Now().Add(time.Minute))}
	serviceAccountUID := biz.NewID()
	daemonSetUID := biz.NewID()
	objects := map[string]map[string]any{
		"tokenreviews":    {"status": map[string]any{"authenticated": true, "audiences": []string{"ani-node-agent"}, "user": map[string]any{"username": "system:serviceaccount:ani-node-system:ani-node-agent", "uid": serviceAccountUID, "extra": map[string]any{"authentication.kubernetes.io/pod-name": []string{request.PodName}, "authentication.kubernetes.io/pod-uid": []string{request.PodUID}}}}},
		"pods":            {"metadata": map[string]any{"namespace": request.PodNamespace, "name": request.PodName, "uid": request.PodUID, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "ani-node-agent", "uid": daemonSetUID, "controller": true}}}, "spec": map[string]any{"serviceAccountName": "ani-node-agent", "nodeName": request.NodeName}},
		"serviceaccounts": {"metadata": map[string]any{"namespace": request.PodNamespace, "name": "ani-node-agent", "uid": serviceAccountUID}},
		"daemonsets":      {"metadata": map[string]any{"namespace": request.PodNamespace, "name": "ani-node-agent", "uid": daemonSetUID}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"serviceAccountName": "ani-node-agent"}}}},
		"nodes":           {"metadata": map[string]any{"name": request.NodeName, "uid": request.NodeUID}},
	}
	return request, objects
}

func identityTestStatus(objects map[string]map[string]any) map[string]any {
	return objects["tokenreviews"]["status"].(map[string]any)
}

func identityTestUser(objects map[string]map[string]any) map[string]any {
	return identityTestStatus(objects)["user"].(map[string]any)
}
