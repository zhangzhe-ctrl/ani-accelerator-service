package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/schedulerconfig"
)

type Connection struct {
	Ref                string `json:"ref"`
	APIServer          string `json:"api_server"`
	ServerName         string `json:"server_name"`
	CAFile             string `json:"ca_file"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	TokenFile          string `json:"token_file"`
	PluginNamespace    string `json:"plugin_namespace"`
	PluginSelector     string `json:"plugin_selector"`
	PluginContainer    string `json:"plugin_container"`
	PluginConfigMap    string `json:"plugin_config_map"`
	SchedulerNamespace string `json:"scheduler_namespace"`
	SchedulerSelector  string `json:"scheduler_selector"`
	SchedulerContainer string `json:"scheduler_container"`
	SchedulerConfigMap string `json:"scheduler_config_map"`
	// Exactly one node may use the process-local NVML library. Other nodes
	// retain inventory/occupancy but cannot claim complete local telemetry.
	LocalNodeUID string       `json:"local_node_uid"`
	Nodes        []NodeSource `json:"nodes"`
}
type NodeSource struct {
	UID                string `json:"uid"`
	Name               string `json:"name"`
	PodResourcesSocket string `json:"podresources_socket"`
	CRISocket          string `json:"cri_socket"`
}
type Config struct {
	Connections            []Connection           `json:"connections"`
	RuntimeEventsDirectory string                 `json:"runtime_events_directory"`
	RuntimeJournal         *journalRecoveryConfig `json:"runtime_journal"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, biz.Invalid("PROVIDER_CONFIG_UNAVAILABLE")
	}
	defer f.Close()
	if decodeStrict(f, &c) != nil {
		return c, biz.Invalid("PROVIDER_CONFIG_INVALID")
	}
	if c.RuntimeEventsDirectory != "" && !filepath.IsAbs(c.RuntimeEventsDirectory) {
		return c, biz.Invalid("PROVIDER_CONFIG_INVALID")
	}
	if j := c.RuntimeJournal; j != nil && (!filepath.IsAbs(j.Directory) || !filepath.IsAbs(j.JournalctlPath) || !biz.UUID(j.NodeUID) || !journalHex32.MatchString(j.MachineID)) {
		return c, biz.Invalid("PROVIDER_CONFIG_INVALID")
	}
	seen := map[string]bool{}
	for _, x := range c.Connections {
		u, err := url.Parse(x.APIServer)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !biz.Ref(x.Ref) || seen[x.Ref] || !filepath.IsAbs(x.CAFile) || x.ServerName == "" {
			return c, biz.Invalid("PROVIDER_CONFIG_INVALID")
		}
		seen[x.Ref] = true
		if !biz.Name(x.PluginNamespace) || !biz.Name(x.SchedulerNamespace) || !biz.Name(x.PluginContainer) || !biz.Name(x.SchedulerContainer) || !biz.Name(x.PluginConfigMap) || !biz.Name(x.SchedulerConfigMap) || x.PluginSelector == "" || x.SchedulerSelector == "" {
			return c, biz.Invalid("PROVIDER_CONFIG_INVALID")
		}
		nodes := map[string]bool{}
		for _, n := range x.Nodes {
			if !biz.Ref(n.UID) || !biz.Text(n.Name, 253) || nodes[n.UID] || !filepath.IsAbs(n.PodResourcesSocket) || !filepath.IsAbs(n.CRISocket) {
				return c, biz.Invalid("PROVIDER_NODE_CONFIG_INVALID")
			}
			nodes[n.UID] = true
		}
		if x.LocalNodeUID != "" && !nodes[x.LocalNodeUID] {
			return c, biz.Invalid("PROVIDER_NODE_CONFIG_INVALID")
		}
	}
	return c, nil
}

type kubeReader struct {
	config Connection
	client *http.Client
	token  string
}

func newKubeReader(c Connection) (*kubeReader, error) {
	ca, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, biz.Invalid("PROVIDER_CA_UNAVAILABLE")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, biz.Invalid("PROVIDER_CA_INVALID")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: c.ServerName}
	if c.CertFile != "" || c.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, biz.Invalid("PROVIDER_CERTIFICATE_UNAVAILABLE")
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	r := &kubeReader{config: c, client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: tc, MaxIdleConns: 8}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if c.TokenFile != "" {
		token, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return nil, biz.Invalid("PROVIDER_TOKEN_UNAVAILABLE")
		}
		r.token = strings.TrimSpace(string(token))
	}
	if len(tc.Certificates) == 0 && r.token == "" {
		return nil, biz.Invalid("PROVIDER_AUTH_REQUIRED")
	}
	return r, nil
}
func (k *kubeReader) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.config.APIServer+path, nil)
	if err != nil {
		return biz.Unavailable()
	}
	if k.token != "" {
		req.Header.Set("Authorization", "Bearer "+k.token)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return biz.Precondition("SOURCE_UNKNOWN")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return biz.Precondition("SOURCE_UNKNOWN")
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out) != nil {
		return biz.Precondition("PROVIDER_RESPONSE_INVALID")
	}
	return nil
}

type metadata struct {
	UID               string            `json:"uid"`
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	ResourceVersion   string            `json:"resourceVersion"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
	DeletionTimestamp string            `json:"deletionTimestamp"`
}
type node struct {
	Metadata metadata `json:"metadata"`
	Status   struct {
		Allocatable map[string]string `json:"allocatable"`
		Conditions  []struct {
			Type              string `json:"type"`
			Status            string `json:"status"`
			LastHeartbeatTime string `json:"lastHeartbeatTime"`
		} `json:"conditions"`
	} `json:"status"`
}
type container struct {
	Name      string   `json:"name"`
	Image     string   `json:"image"`
	Command   []string `json:"command"`
	Args      []string `json:"args"`
	Resources struct {
		Limits map[string]string `json:"limits"`
	} `json:"resources"`
}
type pod struct {
	Metadata metadata `json:"metadata"`
	Spec     struct {
		NodeName       string            `json:"nodeName"`
		NodeSelector   map[string]string `json:"nodeSelector"`
		Containers     []container       `json:"containers"`
		InitContainers []container       `json:"initContainers"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		ContainerStatuses []struct {
			Name    string `json:"name"`
			ImageID string `json:"imageID"`
			Ready   bool   `json:"ready"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

func (k *kubeReader) nodes(ctx context.Context) ([]node, error) {
	var l struct {
		Items    []node `json:"items"`
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
	}
	if err := k.get(ctx, "/api/v1/nodes", &l); err != nil {
		return nil, err
	}
	if l.Metadata.Continue != "" {
		return nil, biz.Precondition("SOURCE_INCOMPLETE")
	}
	return l.Items, nil
}
func (k *kubeReader) pods(ctx context.Context) ([]pod, error) {
	var l struct {
		Items    []pod `json:"items"`
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
	}
	if err := k.get(ctx, "/api/v1/pods", &l); err != nil {
		return nil, err
	}
	if l.Metadata.Continue != "" {
		return nil, biz.Precondition("SOURCE_INCOMPLETE")
	}
	return l.Items, nil
}
func (k *kubeReader) selectedPods(ctx context.Context, namespace, selector string) ([]pod, error) {
	var l struct {
		Items    []pod `json:"items"`
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
	}
	err := k.get(ctx, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods?labelSelector="+url.QueryEscape(selector), &l)
	if err == nil && l.Metadata.Continue != "" {
		return nil, biz.Precondition("SOURCE_INCOMPLETE")
	}
	return l.Items, err
}
func (k *kubeReader) configDigest(ctx context.Context, namespace, name string) (string, error) {
	var cm struct {
		Data       map[string]string `json:"data"`
		BinaryData map[string]string `json:"binaryData"`
	}
	if err := k.get(ctx, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/configmaps/"+url.PathEscape(name), &cm); err != nil {
		return "", err
	}
	return biz.Hash(cm)
}
func (k *kubeReader) schedulerConfigDigest(ctx context.Context, namespace, name, version string) (string, error) {
	var cm struct {
		Data       map[string]string `json:"data"`
		BinaryData map[string]string `json:"binaryData"`
	}
	if err := k.get(ctx, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/configmaps/"+url.PathEscape(name), &cm); err != nil {
		return "", err
	}
	digest, err := schedulerconfig.ConfigMapDigest(version, cm.Data, cm.BinaryData)
	if err != nil {
		return "", fmt.Errorf("%w: %v", biz.Precondition("SCHEDULER_CONFIG_INVALID"), err)
	}
	return digest, nil
}
func (k *kubeReader) nodeOnline(ctx context.Context, n node, now time.Time, ttl time.Duration) bool {
	for _, c := range n.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		if c.Status != "True" || ttl <= 0 || n.Metadata.DeletionTimestamp != "" {
			return false
		}
		t, err := time.Parse(time.RFC3339Nano, c.LastHeartbeatTime)
		if err != nil || t.After(now.Add(5*time.Second)) {
			return false
		}
		if now.Sub(t) <= ttl {
			return true
		}
		// With Node leases enabled, a healthy kubelet may leave Node status
		// unchanged for minutes. Bind its newer heartbeat to this exact Node UID.
		if n.Metadata.Name == "" || n.Metadata.UID == "" {
			return false
		}
		var lease struct {
			Metadata struct {
				Name              string `json:"name"`
				Namespace         string `json:"namespace"`
				DeletionTimestamp string `json:"deletionTimestamp"`
				OwnerReferences   []struct {
					APIVersion string `json:"apiVersion"`
					Kind       string `json:"kind"`
					Name       string `json:"name"`
					UID        string `json:"uid"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
			Spec struct {
				HolderIdentity       string `json:"holderIdentity"`
				LeaseDurationSeconds int32  `json:"leaseDurationSeconds"`
				RenewTime            string `json:"renewTime"`
			} `json:"spec"`
		}
		path := "/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases/" + url.PathEscape(n.Metadata.Name)
		if k.get(ctx, path, &lease) != nil || lease.Metadata.Name != n.Metadata.Name || lease.Metadata.Namespace != "kube-node-lease" || lease.Metadata.DeletionTimestamp != "" || lease.Spec.HolderIdentity != n.Metadata.Name || lease.Spec.LeaseDurationSeconds <= 0 {
			return false
		}
		ownerOK := false
		for _, owner := range lease.Metadata.OwnerReferences {
			if owner.APIVersion == "v1" && owner.Kind == "Node" && owner.Name == n.Metadata.Name && owner.UID == n.Metadata.UID {
				ownerOK = true
			}
		}
		if !ownerOK {
			return false
		}
		renewed, err := time.Parse(time.RFC3339Nano, lease.Spec.RenewTime)
		leaseTTL := min(ttl, time.Duration(lease.Spec.LeaseDurationSeconds)*time.Second)
		return err == nil && now.Sub(renewed) <= leaseTTL && !renewed.After(now.Add(5*time.Second))
	}
	return false
}
