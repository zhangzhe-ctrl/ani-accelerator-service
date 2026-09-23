package biz

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestContractVectors(t *testing.T) {
	b := baselineForTest(1024, Shared)
	p := testPlan(t, Shared, 1024)
	projection := &UsageProjection{Ref: &UsageRef{testID, "ani-inference", testID, testID}, Revision: 2, State: Ended, Plan: p, EndReason: OwnerReleased, SourceFactRef: "quota-operation:" + testID + ":gpu-fully-released", SourceOperationCreatedAt: "2026-09-23T00:00:00Z"}
	projection.PayloadDigest, _ = ProjectionDigest(projection)
	vector := map[string]any{"schema": "acc-c14n-v1", "baseline": b, "plan": p, "projection": projection, "construction": "deterministic software fixture; no hardware verification"}
	canonical, err := Canonical(map[string]any{"z": int64(9223372036854775807), "a": "<&中文>", "b": true})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := Hash(map[string]any{"z": int64(9223372036854775807), "a": "<&中文>", "b": true})
	if err != nil {
		t.Fatal(err)
	}
	vector["canonical_example"] = string(canonical)
	vector["canonical_example_sha256"] = digest
	bytes, err := json.MarshalIndent(vector, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	bytes = append(bytes, '\n')
	if output := os.Getenv("ACC_VECTOR_OUTPUT"); output != "" {
		if err = os.WriteFile(output, bytes, 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	expected, err := os.ReadFile("../../docs/contracts/accelerator-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != string(bytes) {
		t.Fatal("frozen contract vectors differ; review the semantic change before regenerating on Fedora")
	}
}

func TestCursorBoundToPrincipalAndFilter(t *testing.T) {
	codec, err := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	c := Caller{Service: "ani-governance", TenantID: testID, Actor: Actor{Type: "user", ID: "one"}, Action: "ListBindings", ClusterIDs: []string{testID}}
	scope, err := PageScope(WithCaller(context.Background(), c), "ListBindings", testID)
	if err != nil {
		t.Fatal(err)
	}
	token := codec.Encode(scope, testID)
	if after, _, err := codec.Decode(&Page{Size: 1, Token: token}, scope); err != nil || after != testID {
		t.Fatal(after, err)
	}
	for _, mutate := range []func(*Caller){func(c *Caller) { c.TenantID = NewID() }, func(c *Caller) { c.Actor.ID = "two" }, func(c *Caller) { c.ClusterIDs = []string{NewID()} }} {
		other := c
		mutate(&other)
		different, err := PageScope(WithCaller(context.Background(), other), "ListBindings", testID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = codec.Decode(&Page{Token: token}, different); err == nil {
			t.Fatal("cursor replay across scope")
		}
	}
	other, _ := PageScope(WithCaller(context.Background(), c), "ListBindings", NewID())
	if _, _, err = codec.Decode(&Page{Token: token}, other); err == nil {
		t.Fatal("cursor replay across filter")
	}
	if _, _, err = codec.Decode(&Page{Token: token + "x"}, scope); err == nil {
		t.Fatal("tampered cursor")
	}
}
