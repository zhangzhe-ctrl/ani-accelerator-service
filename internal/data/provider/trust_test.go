package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestOwnerTrustBoundaries(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	clusterID := biz.NewID()
	pod := biz.PodRef{ClusterID: clusterID, Namespace: "test", Name: "pod", UID: "pod-1", ContainerName: "inference"}
	ref := biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-inference", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	labels := map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}
	annotations := map[string]string{"accelerator.ani.io/execution-digest": digest}
	if got, _ := VerifyOwnerAssertion(pod, labels, annotations, pub); got != nil {
		t.Fatal("ordinary labels trusted")
	}
	payload, _ := biz.Canonical(OwnerAssertion{pod, ref, digest})
	annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v2\n"), payload...)))
	if got, _ := VerifyOwnerAssertion(pod, labels, annotations, pub); got == nil || *got != ref {
		t.Fatal("valid owner assertion failed")
	}
	for _, change := range []func(*biz.PodRef, map[string]string, map[string]string){
		func(p *biz.PodRef, _ map[string]string, _ map[string]string) { p.ClusterID = biz.NewID() },
		func(p *biz.PodRef, _ map[string]string, _ map[string]string) { p.Namespace = "other" },
		func(p *biz.PodRef, _ map[string]string, _ map[string]string) { p.ContainerName = "sidecar" },
		func(_ *biz.PodRef, l map[string]string, _ map[string]string) {
			l["accelerator.ani.io/resource-tenant-id"] = biz.NewID()
		},
		func(_ *biz.PodRef, l map[string]string, _ map[string]string) {
			l["accelerator.ani.io/resource-id"] = biz.NewID()
		},
		func(_ *biz.PodRef, l map[string]string, _ map[string]string) {
			l["accelerator.ani.io/create-operation-id"] = biz.NewID()
		},
		func(_ *biz.PodRef, _ map[string]string, a map[string]string) {
			a["accelerator.ani.io/execution-digest"] = strings.Repeat("b", 64)
		},
		func(_ *biz.PodRef, _ map[string]string, a map[string]string) {
			a["accelerator.ani.io/plan-digest"] = a["accelerator.ani.io/execution-digest"]
			delete(a, "accelerator.ani.io/execution-digest")
		},
	} {
		changedPod := pod
		changedLabels, changedAnnotations := map[string]string{}, map[string]string{}
		for k, v := range labels {
			changedLabels[k] = v
		}
		for k, v := range annotations {
			changedAnnotations[k] = v
		}
		change(&changedPod, changedLabels, changedAnnotations)
		if got, _ := VerifyOwnerAssertion(changedPod, changedLabels, changedAnnotations, pub); got != nil {
			t.Fatal("signature accepted for changed scope or removed contract")
		}
	}
	annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v1\n"), payload...)))
	if got, _ := VerifyOwnerAssertion(pod, labels, annotations, pub); got != nil {
		t.Fatal("old signature domain accepted")
	}
	resourcePub, resourceKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	provider := &Provider{ownerKey: pub}
	ref.OwnerService = "ani-resource-service"
	labels["accelerator.ani.io/owner-service"] = ref.OwnerService
	payload, _ = biz.Canonical(OwnerAssertion{pod, ref, digest})
	annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(resourceKey, append([]byte("acc-owner-pod-v2\n"), payload...)))
	if got, _ := provider.verifyOwner(pod, labels, annotations); got != nil {
		t.Fatal("missing Resource key trusted")
	}
	if err = provider.ConfigureResourceOwnerKey(resourcePub); err != nil {
		t.Fatal(err)
	}
	if got, _ := provider.verifyOwner(pod, labels, annotations); got == nil || *got != ref {
		t.Fatal("Resource owner assertion failed")
	}
	annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v2\n"), payload...)))
	if got, _ := provider.verifyOwner(pod, labels, annotations); got != nil {
		t.Fatal("Inference key authorized Resource owner")
	}
	annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(resourceKey, append([]byte("acc-owner-pod-v2\n"), payload...)))
	pod.UID = "recreated-pod"
	if got, _ := provider.verifyOwner(pod, labels, annotations); got != nil {
		t.Fatal("Pod UID replay accepted")
	}
	for _, raw := range []string{`{"connections":[],"connections":[]}`, `{"connections":[{"ref":"a","ref":"b"}]}`, `{"connections":[]} {}`, `{"unexpected":true}`} {
		var c Config
		if decodeStrict(strings.NewReader(raw), &c) == nil {
			t.Fatal("ambiguous or unknown config accepted", raw)
		}
	}
}

func TestModeldevOwnerUsesIndependentKeyAndExactTrainerPod(t *testing.T) {
	inferencePub, inferenceKey, _ := ed25519.GenerateKey(rand.Reader)
	resourcePub, resourceKey, _ := ed25519.GenerateKey(rand.Reader)
	modeldevPub, modeldevKey, _ := ed25519.GenerateKey(rand.Reader)
	p := &Provider{ownerKey: inferencePub, resourceOwnerKey: resourcePub}
	pod := biz.PodRef{ClusterID: biz.NewID(), Namespace: "training", Name: "trainjob-node-0", UID: "actual-trainer-pod", ContainerName: "node"}
	ref := biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-modeldev-service", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	digest := strings.Repeat("a", 64)
	labels := map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}
	annotations := map[string]string{"accelerator.ani.io/execution-digest": digest}
	payload, err := biz.Canonical(OwnerAssertion{pod, ref, digest})
	if err != nil {
		t.Fatal(err)
	}
	sign := func(key ed25519.PrivateKey) {
		annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v2\n"), payload...)))
	}
	sign(modeldevKey)
	if got, _ := p.verifyOwner(pod, labels, annotations); got != nil {
		t.Fatal("missing ModelDev key authorized ownership")
	}
	if p.ConfigureModeldevOwnerKey(nil) == nil {
		t.Fatal("invalid ModelDev key accepted")
	}
	for _, otherKey := range []ed25519.PublicKey{inferencePub, resourcePub} {
		if p.ConfigureModeldevOwnerKey(otherKey) == nil {
			t.Fatal("ModelDev configured with another owner's public key")
		}
	}
	if err = p.ConfigureModeldevOwnerKey(modeldevPub); err != nil {
		t.Fatal(err)
	}
	if got, actualDigest := p.verifyOwner(pod, labels, annotations); got == nil || *got != ref || actualDigest != digest {
		t.Fatal("ModelDev Trainer assertion rejected")
	}
	for _, key := range []ed25519.PrivateKey{inferenceKey, resourceKey} {
		sign(key)
		if got, _ := p.verifyOwner(pod, labels, annotations); got != nil {
			t.Fatal("another owner's key authorized ModelDev")
		}
	}
	sign(modeldevKey)
	for _, change := range []func(*biz.PodRef){
		func(p *biz.PodRef) { p.UID = "recreated-trainer-pod" },
		func(p *biz.PodRef) { p.ContainerName = "sidecar" },
		func(p *biz.PodRef) { p.Namespace = "another-tenant" },
		func(p *biz.PodRef) { p.ClusterID = biz.NewID() },
	} {
		other := pod
		change(&other)
		if got, _ := p.verifyOwner(other, labels, annotations); got != nil {
			t.Fatal("changed actual Trainer scope authorized")
		}
	}
	labels["accelerator.ani.io/create-operation-id"] = biz.NewID()
	if got, _ := p.verifyOwner(pod, labels, annotations); got != nil {
		t.Fatal("changed create scope authorized")
	}
}
