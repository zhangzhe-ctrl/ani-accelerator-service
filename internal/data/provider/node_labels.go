package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type initializationLabelPatch struct {
	Operation string `json:"op"`
	Path      string `json:"path"`
	Value     any    `json:"value"`
}

func (p *Provider) EnsureInitializationLabels(ctx context.Context, cluster biz.Cluster, group biz.SupplyGroup, guard func(context.Context) error) error {
	reader := p.connections[cluster.ConnectionRef]
	if reader == nil || guard == nil || !biz.UUID(group.GroupID) || group.ClusterID != cluster.ClusterID || !biz.Label(group.ModelKey) || group.Baseline == nil || !biz.UUID(group.Baseline.BaselineID) {
		return biz.Invalid("INITIALIZATION_LABEL_SCOPE_INVALID")
	}
	nodes := append([]biz.NodeRef{}, group.Nodes...)
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].UID < nodes[right].UID })
	for _, target := range nodes {
		if !biz.Ref(target.UID) || !nodeIdentityDNSName(target.Name) {
			return biz.Invalid("INVALID_GROUP_NODES")
		}
		completed := false
		for attempt := 0; attempt < 3; attempt++ {
			if err := guard(ctx); err != nil {
				return err
			}
			var actual node
			path := "/api/v1/nodes/" + url.PathEscape(target.Name)
			if err := reader.get(ctx, path, &actual); err != nil {
				return err
			}
			patch, err := initializationNodePatch(target, actual, group)
			if err != nil {
				return err
			}
			if err = guard(ctx); err != nil {
				return err
			}
			if len(patch) > 2 {
				retry, err := reader.patchInitializationNodeLabels(ctx, target.Name, patch)
				if err != nil {
					return err
				}
				if retry {
					continue
				}
			}
			if err = guard(ctx); err != nil {
				return err
			}
			var verified node
			if err = reader.get(ctx, path, &verified); err != nil {
				return err
			}
			if verified.Metadata.UID != target.UID || verified.Metadata.Name != target.Name || verified.Metadata.DeletionTimestamp != "" {
				return biz.Conflict("NODE_IDENTITY_CONFLICT")
			}
			if verified.Metadata.Labels[biz.GroupLabel] != group.GroupID || verified.Metadata.Labels[biz.ModelLabel] != group.ModelKey || verified.Metadata.Labels[biz.BaselineLabel] != group.Baseline.BaselineID {
				return biz.Conflict("NODE_LABEL_CONFLICT")
			}
			if err = guard(ctx); err != nil {
				return err
			}
			completed = true
			break
		}
		if !completed {
			return biz.Precondition("NODE_RESOURCE_VERSION_UNSTABLE")
		}
	}
	return nil
}

func initializationNodePatch(target biz.NodeRef, actual node, group biz.SupplyGroup) ([]initializationLabelPatch, error) {
	if actual.Metadata.UID != target.UID || actual.Metadata.Name != target.Name || actual.Metadata.DeletionTimestamp != "" {
		return nil, biz.Conflict("NODE_IDENTITY_CONFLICT")
	}
	if actual.Metadata.ResourceVersion == "" {
		return nil, biz.Precondition("NODE_RESOURCE_VERSION_UNKNOWN")
	}
	labels := map[string]string{biz.GroupLabel: group.GroupID, biz.ModelLabel: group.ModelKey, biz.BaselineLabel: group.Baseline.BaselineID}
	patch := []initializationLabelPatch{{Operation: "test", Path: "/metadata/uid", Value: target.UID}, {Operation: "test", Path: "/metadata/resourceVersion", Value: actual.Metadata.ResourceVersion}}
	for key, value := range labels {
		if current, exists := actual.Metadata.Labels[key]; exists && current != value {
			return nil, biz.Conflict("NODE_LABEL_CONFLICT")
		}
	}
	if actual.Metadata.Labels == nil {
		patch = append(patch, initializationLabelPatch{Operation: "add", Path: "/metadata/labels", Value: labels})
		return patch, nil
	}
	keys := []string{biz.GroupLabel, biz.ModelLabel, biz.BaselineLabel}
	sort.Strings(keys)
	for _, key := range keys {
		if _, exists := actual.Metadata.Labels[key]; !exists {
			escaped := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			patch = append(patch, initializationLabelPatch{Operation: "add", Path: "/metadata/labels/" + escaped, Value: labels[key]})
		}
	}
	return patch, nil
}

func (k *kubeReader) patchInitializationNodeLabels(ctx context.Context, name string, patch []initializationLabelPatch) (bool, error) {
	payload, err := json.Marshal(patch)
	if err != nil {
		return false, biz.Invalid("INITIALIZATION_LABEL_PATCH_INVALID")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, k.config.APIServer+"/api/v1/nodes/"+url.PathEscape(name), bytes.NewReader(payload))
	if err != nil {
		return false, biz.Unavailable()
	}
	token, err := k.currentToken()
	if err != nil {
		return false, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json-patch+json")
	response, err := k.client.Do(request)
	if err != nil {
		return false, biz.Unavailable()
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusUnprocessableEntity {
		return true, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, biz.Unavailable()
	}
	return false, nil
}
