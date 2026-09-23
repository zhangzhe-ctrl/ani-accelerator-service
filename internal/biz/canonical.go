package biz

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Canonical encodes acc-c14n-v1: UTF-8 JSON with lexically ordered object keys,
// all integer values as decimal strings, booleans unchanged, no whitespace or
// HTML escaping. Arrays preserve order; semantic sets are sorted by callers.
// Floats are not a domain type. No generic map or raw JSON enters an RPC.
func Canonical(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, Invalid("CANONICAL_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err = d.Decode(&v); err != nil {
		return nil, Invalid("CANONICAL_INVALID")
	}
	v, err = canonicalNumbers(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	e.SetEscapeHTML(false)
	if err = e.Encode(v); err != nil {
		return nil, Invalid("CANONICAL_INVALID")
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}
func canonicalNumbers(v any) (any, error) {
	switch n := v.(type) {
	case json.Number:
		if _, err := n.Int64(); err != nil {
			return nil, Invalid("INTEGER_OVERFLOW")
		}
		return n.String(), nil
	case map[string]any:
		for k, x := range n {
			c, err := canonicalNumbers(x)
			if err != nil {
				return nil, err
			}
			n[k] = c
		}
	case []any:
		for i, x := range n {
			c, err := canonicalNumbers(x)
			if err != nil {
				return nil, err
			}
			n[i] = c
		}
	}
	return v, nil
}
func Hash(value any) (string, error) {
	b, err := Canonical(value)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte("acc-c14n-v1\n"), b...))
	return hex.EncodeToString(h[:]), nil
}
func BaselineDigest(b *ProviderBaseline) (string, error) {
	if b == nil {
		return "", Invalid("BASELINE_INCOMPLETE")
	}
	c := *b
	c.BaselineDigest = ""
	c.Verification = nil
	c.FactorEvidenceRef = ""
	return Hash(c)
}
func SpecDigest(s *ProfileSpec, baselineID string) (string, error) {
	return Hash(struct {
		Spec       *ProfileSpec `json:"spec"`
		BaselineID string       `json:"baseline_id"`
	}{s, baselineID})
}
func sortedFields(fields []KeyValue) ([]KeyValue, error) {
	c := append([]KeyValue{}, fields...)
	sort.Slice(c, func(i, j int) bool { return c[i].Key < c[j].Key })
	for i, x := range c {
		if x.Key == "" || x.Value == "" || (i > 0 && c[i-1].Key == x.Key) {
			return nil, Invalid("DUPLICATE_OR_EMPTY_KEY")
		}
	}
	return c, nil
}
func PlanDigest(p *ResolvedPlan) (string, error) {
	if p == nil || p.Profile == nil || p.Runtime == nil {
		return "", Invalid("INVALID_PLAN")
	}
	c := *p
	c.ResolutionDigest = ""
	profile := *p.Profile
	profile.Published = false
	c.Profile = &profile
	r := *p.Runtime
	c.Runtime = &r
	var err error
	if r.NodeLabels, err = sortedFields(r.NodeLabels); err != nil {
		return "", err
	}
	if r.PodAnnotations, err = sortedFields(r.PodAnnotations); err != nil {
		return "", err
	}
	if r.LimitsPerContainer, err = sortedFields(r.LimitsPerContainer); err != nil {
		return "", err
	}
	return Hash(c)
}
func ProjectionDigest(p *UsageProjection) (string, error) {
	if p == nil || p.Plan == nil {
		return "", Invalid("INVALID_PROJECTION")
	}
	c := *p
	c.PayloadDigest = ""
	// The validated plan digest commits to its canonical content independently
	// of array ordering and published state. Hash that content exactly once.
	return Hash(struct {
		Ref        *UsageRef  `json:"ref"`
		Revision   uint64     `json:"revision"`
		State      UsageState `json:"state"`
		PlanDigest string     `json:"plan_digest"`
		EndReason  EndReason  `json:"end_reason"`
		Fact       string     `json:"source_fact_ref"`
		Created    string     `json:"source_operation_created_at"`
	}{c.Ref, c.Revision, c.State, c.Plan.ResolutionDigest, c.EndReason, c.SourceFactRef, c.SourceOperationCreatedAt})
}
