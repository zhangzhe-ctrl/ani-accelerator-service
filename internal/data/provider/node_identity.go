package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type NodeAgentIdentityConfig struct {
	Namespace      string `json:"namespace"`
	ServiceAccount string `json:"service_account"`
	DaemonSet      string `json:"daemon_set"`
	Audience       string `json:"audience"`
}

func validateNodeAgentIdentityConfig(config *NodeAgentIdentityConfig) error {
	if config == nil {
		return nil
	}
	if !biz.Name(config.Namespace) || !nodeIdentityDNSName(config.ServiceAccount) || !nodeIdentityDNSName(config.DaemonSet) || !biz.Text(config.Audience, 253) || strings.ContainsAny(config.Audience, " \t") {
		return biz.Invalid("NODE_AGENT_IDENTITY_CONFIG_INVALID")
	}
	return nil
}

func nodeIdentityDNSName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if !biz.Name(part) {
			return false
		}
	}
	return true
}

type nodeTokenReview struct {
	Status struct {
		Authenticated bool     `json:"authenticated"`
		Audiences     []string `json:"audiences"`
		Error         string   `json:"error"`
		User          struct {
			Username string              `json:"username"`
			UID      string              `json:"uid"`
			Extra    map[string][]string `json:"extra"`
		} `json:"user"`
	} `json:"status"`
}

func (k *kubeReader) reviewNodeToken(ctx context.Context, token, audience string) (nodeTokenReview, error) {
	var review nodeTokenReview
	payload, err := json.Marshal(struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Spec       struct {
			Token     string   `json:"token"`
			Audiences []string `json:"audiences"`
		} `json:"spec"`
	}{APIVersion: "authentication.k8s.io/v1", Kind: "TokenReview", Spec: struct {
		Token     string   `json:"token"`
		Audiences []string `json:"audiences"`
	}{Token: token, Audiences: []string{audience}}})
	if err != nil {
		return review, biz.Denied()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, k.config.APIServer+"/apis/authentication.k8s.io/v1/tokenreviews", bytes.NewReader(payload))
	if err != nil {
		return review, biz.Unavailable()
	}
	credential, err := k.currentToken()
	if err != nil {
		return review, err
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := k.client.Do(request)
	if err != nil {
		return review, biz.Unavailable()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return review, biz.Unavailable()
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&review) != nil {
		return nodeTokenReview{}, biz.Unavailable()
	}
	if !review.Status.Authenticated || review.Status.Error != "" || len(review.Status.Audiences) != 1 || review.Status.Audiences[0] != audience {
		return nodeTokenReview{}, biz.Denied()
	}
	return review, nil
}

type nodeIdentityPod struct {
	Metadata struct {
		metadata
		OwnerReferences []struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Name       string `json:"name"`
			UID        string `json:"uid"`
			Controller bool   `json:"controller"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		NodeName           string `json:"nodeName"`
		ServiceAccountName string `json:"serviceAccountName"`
	} `json:"spec"`
}

func (p *Provider) VerifyNodeIdentity(ctx context.Context, request biz.NodeIdentityRequest) (biz.NodeIdentity, error) {
	var identity biz.NodeIdentity
	reader := p.connections[request.ConnectionRef]
	if reader == nil || reader.config.NodeAgentIdentity == nil {
		return identity, biz.Denied()
	}
	config := reader.config.NodeAgentIdentity
	if validateNodeAgentIdentityConfig(config) != nil || request.PodNamespace != config.Namespace || !nodeIdentityDNSName(request.PodName) || !nodeIdentityDNSName(request.NodeName) || !biz.UUID(request.PodUID) || (request.NodeUID != "" && !biz.UUID(request.NodeUID)) || !biz.UUID(request.InstanceID) || !biz.UUID(request.LogID) || !biz.UUID(request.BootID) || len(request.Token) == 0 || len(request.Token) > 128<<10 {
		return identity, biz.Denied()
	}
	review, err := reader.reviewNodeToken(ctx, request.Token, config.Audience)
	if err != nil {
		return identity, err
	}
	username := "system:serviceaccount:" + config.Namespace + ":" + config.ServiceAccount
	if review.Status.User.Username != username || !singleIdentityClaim(review.Status.User.Extra, "authentication.kubernetes.io/pod-name", request.PodName) || !singleIdentityClaim(review.Status.User.Extra, "authentication.kubernetes.io/pod-uid", request.PodUID) {
		return identity, biz.Denied()
	}
	expiresAt, err := verifiedNodeTokenExpiry(request.Token)
	if err != nil {
		return identity, err
	}
	var currentPod nodeIdentityPod
	var serviceAccount struct {
		Metadata metadata `json:"metadata"`
	}
	var daemonSet struct {
		Metadata metadata `json:"metadata"`
		Spec     struct {
			Template struct {
				Spec struct {
					ServiceAccountName string `json:"serviceAccountName"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	var currentNode struct {
		Metadata metadata `json:"metadata"`
	}
	namespacePath := "/api/v1/namespaces/" + url.PathEscape(config.Namespace)
	reads := []struct {
		path   string
		output any
	}{
		{namespacePath + "/pods/" + url.PathEscape(request.PodName), &currentPod},
		{namespacePath + "/serviceaccounts/" + url.PathEscape(config.ServiceAccount), &serviceAccount},
		{"/apis/apps/v1/namespaces/" + url.PathEscape(config.Namespace) + "/daemonsets/" + url.PathEscape(config.DaemonSet), &daemonSet},
		{"/api/v1/nodes/" + url.PathEscape(request.NodeName), &currentNode},
	}
	readContext, cancelReads := context.WithCancel(ctx)
	defer cancelReads()
	readErrors := make([]error, len(reads))
	var workers sync.WaitGroup
	for index, read := range reads {
		workers.Go(func() {
			readErrors[index] = reader.get(readContext, read.path, read.output)
			if readErrors[index] != nil {
				cancelReads()
			}
		})
	}
	workers.Wait()
	for _, readError := range readErrors {
		if readError != nil {
			return identity, readError
		}
	}
	if currentPod.Metadata.UID != request.PodUID || currentPod.Metadata.Name != request.PodName || currentPod.Metadata.Namespace != config.Namespace || currentPod.Metadata.DeletionTimestamp != "" || currentPod.Spec.ServiceAccountName != config.ServiceAccount || currentPod.Spec.NodeName != request.NodeName {
		return identity, biz.Denied()
	}
	if serviceAccount.Metadata.Name != config.ServiceAccount || serviceAccount.Metadata.Namespace != config.Namespace || serviceAccount.Metadata.UID == "" || serviceAccount.Metadata.UID != review.Status.User.UID || serviceAccount.Metadata.DeletionTimestamp != "" {
		return identity, biz.Denied()
	}
	if daemonSet.Metadata.Name != config.DaemonSet || daemonSet.Metadata.Namespace != config.Namespace || daemonSet.Metadata.UID == "" || daemonSet.Metadata.DeletionTimestamp != "" || daemonSet.Spec.Template.Spec.ServiceAccountName != config.ServiceAccount {
		return identity, biz.Denied()
	}
	owners := 0
	for _, owner := range currentPod.Metadata.OwnerReferences {
		if owner.Controller {
			owners++
			if owner.APIVersion != "apps/v1" || owner.Kind != "DaemonSet" || owner.Name != config.DaemonSet || owner.UID != daemonSet.Metadata.UID {
				return identity, biz.Denied()
			}
		}
	}
	if owners != 1 {
		return identity, biz.Denied()
	}
	if currentNode.Metadata.Name != request.NodeName || !biz.UUID(currentNode.Metadata.UID) || (request.NodeUID != "" && currentNode.Metadata.UID != request.NodeUID) || currentNode.Metadata.DeletionTimestamp != "" || !expiresAt.After(time.Now()) {
		return identity, biz.Denied()
	}
	return biz.NodeIdentity{ConnectionRef: request.ConnectionRef, NodeUID: currentNode.Metadata.UID, NodeName: currentNode.Metadata.Name, PodUID: currentPod.Metadata.UID, PodNamespace: config.Namespace, PodName: currentPod.Metadata.Name, ServiceAccount: config.ServiceAccount, DaemonSetUID: daemonSet.Metadata.UID, InstanceID: request.InstanceID, LogID: request.LogID, BootID: request.BootID, TokenExpiresAt: expiresAt}, nil
}

func singleIdentityClaim(extra map[string][]string, key, expected string) bool {
	values := extra[key]
	return len(values) == 1 && values[0] == expected
}

func verifiedNodeTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, biz.Denied()
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, biz.Denied()
	}
	var claims struct {
		Expiry json.Number `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return time.Time{}, biz.Denied()
	}
	expiry, err := strconv.ParseInt(string(claims.Expiry), 10, 64)
	if err != nil || expiry <= 0 {
		return time.Time{}, biz.Denied()
	}
	expiresAt := time.Unix(expiry, 0).UTC()
	if !expiresAt.After(time.Now()) {
		return time.Time{}, biz.Denied()
	}
	return expiresAt, nil
}
