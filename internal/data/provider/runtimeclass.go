package provider

import (
	"context"
	"encoding/json"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// Read the baseline's registered RuntimeClass through the existing managed
// cluster connection. Missing identity or unsupported Pod mutations fail closed.
func (k *kubeReader) runtimeClass(ctx context.Context, name string) (biz.RuntimeClassInspection, error) {
	var raw struct {
		Metadata metadata `json:"metadata"`
		Handler  string   `json:"handler"`
		Overhead struct {
			PodFixed map[string]json.RawMessage `json:"podFixed"`
		} `json:"overhead"`
		Scheduling struct {
			NodeSelector map[string]string `json:"nodeSelector"`
			Tolerations  []json.RawMessage `json:"tolerations"`
		} `json:"scheduling"`
	}
	if !biz.Name(name) {
		return biz.RuntimeClassInspection{}, biz.Precondition("RUNTIME_CLASS_UNAVAILABLE")
	}
	if err := k.get(ctx, "/apis/node.k8s.io/v1/runtimeclasses/"+name, &raw); err != nil {
		return biz.RuntimeClassInspection{}, err
	}
	if raw.Metadata.Name != name || !biz.UUID(raw.Metadata.UID) || !biz.Ref(raw.Metadata.ResourceVersion) || !biz.Name(raw.Handler) {
		return biz.RuntimeClassInspection{}, biz.Precondition("RUNTIME_CLASS_UNAVAILABLE")
	}
	if len(raw.Overhead.PodFixed) > 0 || len(raw.Scheduling.Tolerations) > 0 {
		return biz.RuntimeClassInspection{}, biz.Precondition("RUNTIME_CLASS_UNSUPPORTED")
	}
	if err := biz.ValidateRuntimeClassNodeSelector(raw.Scheduling.NodeSelector); err != nil {
		return biz.RuntimeClassInspection{}, err
	}
	return biz.RuntimeClassInspection{Name: name, UID: raw.Metadata.UID, ResourceVersion: raw.Metadata.ResourceVersion, NodeSelector: raw.Scheduling.NodeSelector}, nil
}
