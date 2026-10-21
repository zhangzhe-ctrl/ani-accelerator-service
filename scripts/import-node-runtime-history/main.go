package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/accdb"
)

type legacyState struct {
	ID, PodUID, Namespace, PodName, ContainerName string
	State                                         uint32
	FinishedAt, ObservedAt                        int64
}

type legacyFile struct {
	NodeUID, NodeName, Socket string
	States                    map[string]legacyState
}

type importScope struct {
	ClusterID, ConnectionRef, NodeUID, NodeName string
	Digest                                      string
	Attempts                                    []biz.RuntimeAttempt
}

type importReceipt struct {
	SourceSHA256, ClusterID, ConnectionRef, NodeUID string
	Read, Inserted, Existing                        int
	RevisionBefore, RevisionAfter                   uint64
	Applied                                         bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run() error {
	dsnFile := flag.String("dsn-file", "", "private restricted runtime DSN file; never a literal DSN")
	source := flag.String("source", "", "final legacy JSON captured after the old writer stopped")
	digest := flag.String("sha256", "", "reviewed original file SHA256")
	cluster := flag.String("cluster-id", "", "existing cluster UUID")
	connection := flag.String("connection-ref", "", "existing managed connection reference")
	node := flag.String("node-uid", "", "actual source Node UID")
	name := flag.String("node-name", "", "actual source Node name")
	socket := flag.String("source-socket", "", "exact socket value in the original file")
	kubectl := flag.String("kubectl", "/home/chabking/ani-four-service-deploy-20261012/tools/kubectl", "existing read-only cluster client")
	apply := flag.Bool("apply", false, "commit missing attempts; default validates and rolls back")
	sourceOnly := flag.Bool("validate-source-only", false, "validate the original file without DB or cluster access; never imports")
	flag.Parse()
	if !biz.UUID(*node) || *name == "" || *source == "" || *socket == "" || *sourceOnly && *apply || !*sourceOnly && (!biz.UUID(*cluster) || !biz.Ref(*connection) || *dsnFile == "") {
		return errors.New("IMPORT_SCOPE_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	scope, err := readLegacy(*source, *digest, *connection, *cluster, *node, *name, *socket, time.Now().UTC())
	if err != nil {
		return err
	}
	if *sourceOnly {
		return json.NewEncoder(os.Stdout).Encode(struct {
			SourceSHA256, NodeUID string
			ValidatedAttempts     int
			Imported              bool
		}{scope.Digest, scope.NodeUID, len(scope.Attempts), false})
	}
	if err := requireLegacyWriterStopped(ctx, *kubectl); err != nil {
		return err
	}
	repository, err := data.OpenPostgres(ctx, *dsnFile)
	if err != nil {
		return errors.New("PRODUCTION_REPOSITORY_CHECK_FAILED")
	}
	defer repository.Close()
	receipt, err := importLegacy(ctx, repository, *dsnFile, scope, *apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}

func readLegacy(path, digest, connection, cluster, node, name, socket string, now time.Time) (importScope, error) {
	scope := importScope{ClusterID: cluster, ConnectionRef: connection, NodeUID: node, NodeName: name, Digest: digest}
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return scope, errors.New("SOURCE_SHA256_REQUIRED")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return scope, errors.New("SOURCE_FILE_INVALID")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return scope, errors.New("SOURCE_FILE_UNAVAILABLE")
	}
	actual := sha256.Sum256(raw)
	if hex.EncodeToString(actual[:]) != digest {
		return scope, errors.New("SOURCE_DIGEST_MISMATCH")
	}
	var source legacyFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil || decoder.Decode(new(any)) != io.EOF || source.NodeUID != node || source.NodeName != name || source.Socket != socket || source.States == nil || len(source.States) > 65536 {
		return scope, errors.New("LEGACY_SOURCE_IDENTITY_OR_FORMAT_INVALID")
	}
	for id, state := range source.States {
		validName := biz.Name(state.ContainerName) || state.ContainerName == "" && state.State == 3 && state.FinishedAt == 0
		if id != state.ID || !biz.Ref(id) || !biz.Ref(state.PodUID) || !biz.Name(state.Namespace) || !biz.Name(state.PodName) || !validName || state.State > 3 || state.ObservedAt <= 0 || state.ObservedAt > now.Add(5*time.Second).UnixNano() || state.State == 2 && (state.FinishedAt <= 0 || state.FinishedAt > state.ObservedAt+int64(5*time.Second)) {
			return scope, errors.New("LEGACY_ATTEMPT_INVALID")
		}
		attempt := biz.RuntimeAttempt{ContainerID: id, PodUID: state.PodUID, PodNamespace: state.Namespace, PodName: state.PodName, ContainerName: state.ContainerName, State: state.State + 1, ObservedAt: time.Unix(0, state.ObservedAt).UTC(), Issues: []string{"LEGACY_LOCAL_EVENT_HISTORY_SHA256:" + digest}}
		if state.FinishedAt > 0 {
			attempt.FinishedAt = time.Unix(0, state.FinishedAt).UTC()
		}
		scope.Attempts = append(scope.Attempts, attempt)
	}
	sort.Slice(scope.Attempts, func(left, right int) bool {
		return scope.Attempts[left].ContainerID < scope.Attempts[right].ContainerID
	})
	return scope, nil
}

func validateScope(ctx context.Context, repository biz.Repository, scope importScope) ([]string, error) {
	var groups []string
	err := repository.View(ctx, func(reader biz.Reader) error {
		cluster, err := reader.Cluster(ctx, scope.ClusterID)
		if err != nil || cluster.ConnectionRef != scope.ConnectionRef {
			return errors.New("EXISTING_CLUSTER_SCOPE_MISMATCH")
		}
		for poolCursor := ""; ; {
			pools, err := reader.Pools(ctx, scope.ClusterID, poolCursor, 200)
			if err != nil {
				return errors.New("EXISTING_POOLS_UNAVAILABLE")
			}
			for _, pool := range pools {
				for groupCursor := ""; ; {
					page, err := reader.Groups(ctx, pool.PoolID, groupCursor, 200)
					if err != nil {
						return errors.New("EXISTING_GROUPS_UNAVAILABLE")
					}
					for _, group := range page {
						for _, node := range group.Nodes {
							if node.UID != scope.NodeUID && node.Name != scope.NodeName {
								continue
							}
							if node.UID != scope.NodeUID || node.Name != scope.NodeName || group.Admission != biz.Closed || group.ClusterID != scope.ClusterID {
								return errors.New("SOURCE_NODE_NOT_CLOSED_IN_EXISTING_GROUP")
							}
							groups = append(groups, group.GroupID)
						}
					}
					if len(page) < 200 {
						break
					}
					groupCursor = page[len(page)-1].GroupID
				}
			}
			if len(pools) < 200 {
				break
			}
			poolCursor = pools[len(pools)-1].PoolID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, errors.New("SOURCE_NODE_NOT_IN_EXISTING_GROUP")
	}
	sort.Strings(groups)
	return groups, nil
}

func importLegacy(ctx context.Context, repository biz.Repository, dsnFile string, scope importScope, apply bool) (importReceipt, error) {
	receipt := importReceipt{SourceSHA256: scope.Digest, ClusterID: scope.ClusterID, ConnectionRef: scope.ConnectionRef, NodeUID: scope.NodeUID, Read: len(scope.Attempts)}
	groups, err := validateScope(ctx, repository, scope)
	if err != nil {
		return receipt, err
	}
	dsn, err := os.ReadFile(dsnFile)
	if err != nil {
		return receipt, errors.New("DATABASE_SECRET_UNAVAILABLE")
	}
	connection, err := pgx.Connect(ctx, strings.TrimSpace(string(dsn)))
	if err != nil {
		return receipt, errors.New("DATABASE_UNAVAILABLE")
	}
	defer connection.Close(context.Background())
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return receipt, errors.New("IMPORT_TRANSACTION_UNAVAILABLE")
	}
	defer transaction.Rollback(context.Background())
	queries := accdb.New(transaction)
	for _, id := range groups {
		if queries.LockWrite1(ctx, "group:"+id) != nil {
			return receipt, errors.New("GROUP_LOCK_FAILED")
		}
		payload, err := queries.GroupData1(ctx, id)
		var group biz.SupplyGroup
		if err != nil || json.Unmarshal(payload, &group) != nil || group.Admission != biz.Closed || group.ClusterID != scope.ClusterID {
			return receipt, errors.New("GROUP_ADMISSION_CHANGED")
		}
		member := false
		for _, node := range group.Nodes {
			member = member || node.UID == scope.NodeUID && node.Name == scope.NodeName
		}
		if !member {
			return receipt, errors.New("GROUP_NODE_MEMBERSHIP_CHANGED")
		}
	}
	payload, err := queries.LockNodeTelemetryState(ctx, scope.ConnectionRef, scope.NodeUID)
	var state biz.NodeTelemetryState
	if err != nil || json.Unmarshal(payload, &state) != nil || state.Identity.NodeUID != scope.NodeUID || state.Identity.NodeName != scope.NodeName || state.Identity.ConnectionRef != scope.ConnectionRef || !biz.UUID(state.Identity.PodUID) || !biz.UUID(state.Identity.LogID) || state.LastSequence == 0 || state.Snapshot == nil || state.Revoked || !time.Now().Before(state.LeaseExpiresAt) {
		return receipt, errors.New("REAL_AUTHENTICATED_NODE_FACT_REQUIRED")
	}
	receipt.RevisionBefore = state.CurrentRevision
	rows, err := queries.ListNodeRuntimeAttempts(ctx, scope.ConnectionRef, scope.NodeUID)
	if err != nil {
		return receipt, errors.New("EXISTING_ATTEMPTS_UNAVAILABLE")
	}
	existing := map[string]biz.RuntimeAttempt{}
	for _, row := range rows {
		var attempt biz.RuntimeAttempt
		if json.Unmarshal(row, &attempt) != nil {
			return receipt, errors.New("EXISTING_ATTEMPT_INVALID")
		}
		existing[attempt.ContainerID] = attempt
	}
	for _, incoming := range scope.Attempts {
		if prior, found := existing[incoming.ContainerID]; found {
			if prior.PodUID != incoming.PodUID || prior.PodNamespace != incoming.PodNamespace || prior.PodName != incoming.PodName || prior.ContainerName != incoming.ContainerName {
				return receipt, errors.New("LEGACY_ATTEMPT_IDENTITY_CONFLICT")
			}
			receipt.Existing++
			continue
		}
		encoded, err := json.Marshal(incoming)
		if err != nil || queries.SaveNodeRuntimeAttempt(ctx, scope.ConnectionRef, scope.NodeUID, incoming.ContainerID, encoded) != nil {
			return receipt, errors.New("LEGACY_ATTEMPT_INSERT_FAILED")
		}
		receipt.Inserted++
	}
	if receipt.Inserted > 0 {
		state.CurrentRevision++
		encoded, err := json.Marshal(state)
		if err != nil || queries.UpdateNodeTelemetryState(ctx, scope.ConnectionRef, scope.NodeUID, state.Epoch, state.Identity.LogID, state.LastSequence, state.LastPayloadDigest, state.CurrentRevision, state.ConsumedRevision, encoded) != nil {
			return receipt, errors.New("IMPORT_REVISION_UPDATE_FAILED")
		}
	}
	receipt.RevisionAfter = state.CurrentRevision
	if apply {
		if transaction.Commit(ctx) != nil {
			return receipt, errors.New("IMPORT_COMMIT_FAILED_RECHECK_RECEIPT")
		}
		receipt.Applied = true
	}
	return receipt, nil
}

func requireLegacyWriterStopped(ctx context.Context, kubectl string) error {
	response, err := exec.CommandContext(ctx, kubectl, "-n", "ani-accelerator", "get", "deployment", "ani-accelerator-service", "-o", "json").Output()
	var deployment struct {
		Spec struct {
			Replicas *int32 `json:"replicas"`
		} `json:"spec"`
		Status struct {
			Replicas int32 `json:"replicas"`
		} `json:"status"`
	}
	if err != nil || json.Unmarshal(response, &deployment) != nil || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 || deployment.Status.Replicas != 0 {
		return errors.New("LEGACY_COLLECTOR_MUST_BE_STOPPED")
	}
	response, err = exec.CommandContext(ctx, kubectl, "-n", "ani-accelerator", "get", "pods", "-l", "app.kubernetes.io/name=ani-accelerator-service", "-o", "json").Output()
	var pods struct {
		Items []json.RawMessage `json:"items"`
	}
	if err != nil || json.Unmarshal(response, &pods) != nil || len(pods.Items) != 0 {
		return errors.New("LEGACY_COLLECTOR_PODS_MUST_BE_GONE")
	}
	return nil
}
