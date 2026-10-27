package biz

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MetricScope contains internal, verified selectors. Governance must remove
// all infrastructure identity fields from its public tenant response.
type MetricScope struct {
	ScopeID           string    `json:"scope_id"`
	Ref               *UsageRef `json:"ref"`
	ResourceKind      string    `json:"resource_kind"`
	ClusterID         string    `json:"cluster_id"`
	Node              *NodeRef  `json:"node"`
	Namespace         string    `json:"namespace"`
	PodName           string    `json:"pod_name"`
	ContainerName     string    `json:"container_name"`
	GPUUUID           string    `json:"gpu_uuid"`
	VdeviceID         string    `json:"vdevice_id"`
	PodUID            string    `json:"pod_uid"`
	DeviceID          string    `json:"device_id"`
	BindingID         string    `json:"binding_id"`
	ValidFrom         string    `json:"valid_from"`
	ValidTo           string    `json:"valid_to"`
	EvidenceAt        string    `json:"evidence_at"`
	EvidenceRef       string    `json:"evidence_ref"`
	OwnershipState    string    `json:"ownership_state"`
	CandidateComplete bool      `json:"candidate_complete"`
	Reasons           []string  `json:"reasons"`
}

type MetricScopeEvent struct {
	Revision   uint64
	RecordedAt time.Time
	SourceKey  string
	Scope      MetricScope
	Active     bool
}
type MetricScopeFilter struct {
	ClusterID  string    `json:"cluster_id"`
	TenantID   string    `json:"tenant_id"`
	Ref        *UsageRef `json:"ref"`
	Start      string    `json:"start"`
	End        string    `json:"end"`
	Page       *Page     `json:"page"`
	SnapshotID string    `json:"snapshot_id"`
}
type MetricScopes struct {
	Items          []MetricScope `json:"items"`
	NextToken      string        `json:"next_token"`
	SnapshotID     string        `json:"snapshot_id"`
	IndexStartedAt string        `json:"index_started_at"`
	EvidenceAt     string        `json:"evidence_at"`
	IndexComplete  bool          `json:"index_complete"`
	Reasons        []string      `json:"reasons"`
}

// Kept separate from Reader so unrelated repository implementations cannot
// accidentally manufacture monitoring ownership through their generic reads.
type MetricScopeReader interface {
	MetricScopeEvents(context.Context, []string, MetricScopeFilter) ([]MetricScopeEvent, uint64, time.Time, error)
}

func (u *Usecase) ListAdminGpuMetricScopes(ctx context.Context, a *AdminRead, f MetricScopeFilter) (*MetricScopes, error) {
	if err := AuthorizeAdmin(ctx, a, "ListAdminGpuMetricScopes"); err != nil {
		return nil, err
	}
	return u.metricScopes(ctx, f, "ListAdminGpuMetricScopes")
}
func (u *Usecase) ListTenantGpuMetricScopes(ctx context.Context, a *TenantContext, f MetricScopeFilter) (*MetricScopes, error) {
	if err := AuthorizeTenant(ctx, a, "ListTenantGpuMetricScopes"); err != nil {
		return nil, err
	}
	if f.TenantID != "" && f.TenantID != a.TenantID {
		return nil, Denied()
	}
	f.TenantID = a.TenantID
	if f.Ref != nil && f.Ref.TenantID != a.TenantID {
		return nil, Denied()
	}
	return u.metricScopes(ctx, f, "ListTenantGpuMetricScopes")
}
func (u *Usecase) metricScopes(ctx context.Context, f MetricScopeFilter, method string) (*MetricScopes, error) {
	start, e1 := time.Parse(time.RFC3339Nano, f.Start)
	end, e2 := time.Parse(time.RFC3339Nano, f.End)
	if e1 != nil || e2 != nil || !end.After(start) || end.Sub(start) > 30*24*time.Hour || end.After(time.Now().Add(time.Minute)) || start.Before(time.Now().Add(-30*24*time.Hour-time.Minute)) {
		return nil, Invalid("INVALID_METRIC_TIME_RANGE")
	}
	if f.ClusterID != "" && !UUID(f.ClusterID) || f.TenantID != "" && !UUID(f.TenantID) {
		return nil, Invalid("INVALID_METRIC_SCOPE")
	}
	if f.Ref != nil {
		if err := ValidateRef(f.Ref); err != nil {
			return nil, err
		}
		if f.TenantID != "" && f.TenantID != f.Ref.TenantID {
			return nil, Denied()
		}
		f.TenantID = f.Ref.TenantID
	}
	if f.Page != nil && f.Page.Token != "" && f.SnapshotID == "" {
		return nil, Invalid("METRIC_SNAPSHOT_REQUIRED")
	}
	out := &MetricScopes{Items: []MetricScope{}}
	err := u.repo.View(ctx, func(r Reader) error {
		mr, ok := r.(MetricScopeReader)
		if !ok {
			return Precondition("METRIC_INDEX_UNAVAILABLE")
		}
		if f.ClusterID != "" {
			if err := u.authorizeCluster(ctx, r, f.ClusterID); err != nil {
				return err
			}
		}
		events, rev, started, err := mr.MetricScopeEvents(ctx, u.connectionRefs(), f)
		if err != nil {
			return err
		}
		out.SnapshotID = strconv.FormatUint(rev, 10)
		if f.SnapshotID != "" && f.SnapshotID != out.SnapshotID {
			return Conflict("METRIC_SNAPSHOT_CHANGED")
		}
		out.IndexStartedAt = started.UTC().Format(time.RFC3339Nano)
		evidenceAt := started
		for _, event := range events {
			if event.RecordedAt.After(evidenceAt) {
				evidenceAt = event.RecordedAt
			}
		}
		out.EvidenceAt = evidenceAt.UTC().Format(time.RFC3339Nano)
		out.IndexComplete = !start.Before(started)
		if !out.IndexComplete {
			out.Reasons = append(out.Reasons, "HISTORY_UNAVAILABLE")
		}
		// Unknown current associations are disclosed as a coverage gap, without
		// extending an old owner into that interval or exposing another identity.
		nextEvidence := map[string]time.Time{}
		for i := len(events) - 1; i >= 0; i-- {
			event := events[i]
			until := nextEvidence[event.Scope.BindingID]
			nextEvidence[event.Scope.BindingID] = event.RecordedAt
			if event.Active && event.Scope.Ref == nil && event.RecordedAt.Before(end) && (until.IsZero() || until.After(start)) {
				out.IndexComplete = false
				out.Reasons = append(out.Reasons, "OWNERSHIP_UNKNOWN")
				break
			}
		}
		filter := f
		filter.Page = nil
		filter.SnapshotID = out.SnapshotID
		pageScope, err := PageScope(ctx, method, filter)
		if err != nil {
			return err
		}
		after, size, err := u.cursor.Decode(f.Page, pageScope)
		if err != nil {
			return err
		}
		items := ResolveMetricScopes(events, f, start, end)
		for _, item := range items {
			if item.ScopeID <= after {
				continue
			}
			if len(out.Items) == size {
				out.NextToken = u.cursor.Encode(pageScope, out.Items[size-1].ScopeID)
				break
			}
			out.Items = append(out.Items, item)
			if !item.CandidateComplete {
				out.IndexComplete = false
			}
		}
		return nil
	})
	return out, err
}

// Conflict candidates are built from the entire source history BEFORE applying
// tenant/resource/time output filters. Retired cross-resource owners never
// expire merely because their business interval ended or is older than 30d.
func ResolveMetricScopes(events []MetricScopeEvent, f MetricScopeFilter, start, end time.Time) []MetricScope {
	sort.Slice(events, func(i, j int) bool { return events[i].Revision < events[j].Revision })
	refs := map[string]map[string]bool{}
	unknown := map[string]map[string]bool{}
	knownUID := map[string]map[string]bool{}
	physical := map[string]map[string]bool{}
	for _, e := range events {
		if refs[e.SourceKey] == nil {
			refs[e.SourceKey] = map[string]bool{}
		}
		if unknown[e.SourceKey] == nil {
			unknown[e.SourceKey] = map[string]bool{}
		}
		if knownUID[e.SourceKey] == nil {
			knownUID[e.SourceKey] = map[string]bool{}
		}
		if physical[e.SourceKey] == nil {
			physical[e.SourceKey] = map[string]bool{}
		}
		if e.Scope.Node != nil {
			physical[e.SourceKey][e.Scope.Node.UID+"/"+e.Scope.DeviceID] = true
		}
		if e.Scope.Ref != nil {
			r := e.Scope.Ref
			refs[e.SourceKey][fmt.Sprintf("%s/%s/%s/%s", r.TenantID, r.OwnerService, r.ResourceID, r.CreateOperationID)] = true
			knownUID[e.SourceKey][e.Scope.PodUID] = true
		} else {
			unknown[e.SourceKey][e.Scope.PodUID] = true
		}
	}
	next := map[string]time.Time{}
	result := []MetricScope{}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		s := e.Scope
		until := next[s.BindingID]
		next[s.BindingID] = e.RecordedAt
		if s.Ref == nil || !e.Active || !e.RecordedAt.Before(end) || !until.IsZero() && !until.After(start) {
			continue
		}
		// Only now apply the authenticated output scope.
		if f.TenantID != "" && s.Ref.TenantID != f.TenantID || f.Ref != nil && *s.Ref != *f.Ref || f.ClusterID != "" && s.ClusterID != f.ClusterID {
			continue
		}
		s.ValidFrom = e.RecordedAt.UTC().Format(time.RFC3339Nano)
		if !until.IsZero() {
			s.ValidTo = until.UTC().Format(time.RFC3339Nano)
		}
		if s.OwnershipState == "" {
			s.CandidateComplete = true
			s.OwnershipState = "VERIFIED"
		}
		if len(refs[e.SourceKey]) > 1 {
			s.OwnershipState = "OWNERSHIP_AMBIGUOUS"
			s.CandidateComplete = false
			s.Reasons = []string{"UNRESOLVED_SOURCE_REUSE"}
		} else if len(physical[e.SourceKey]) > 1 {
			s.OwnershipState = "OWNERSHIP_UNKNOWN"
			s.CandidateComplete = false
			s.Reasons = []string{"DEVICE_IDENTITY_REUSED"}
		} else {
			for uid := range unknown[e.SourceKey] {
				if uid != s.PodUID && !knownUID[e.SourceKey][uid] {
					s.OwnershipState = "OWNERSHIP_UNKNOWN"
					s.CandidateComplete = false
					s.Reasons = []string{"CANDIDATE_HISTORY_INCOMPLETE"}
				}
			}
		}
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ScopeID < result[j].ScopeID })
	return result
}

func NormalizeGPUUUID(v string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "gpu-")
}
