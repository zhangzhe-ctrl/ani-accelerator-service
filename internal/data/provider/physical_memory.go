package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// PrometheusInventoryConfig refers to the existing NVML metric source. It is
// optional display metadata and never supplies allocation or device identity.
type PrometheusInventoryConfig struct {
	URL             string `json:"url"`
	CAFile          string `json:"ca_file,omitempty"`
	BearerTokenFile string `json:"bearer_token_file,omitempty"`
}

type PhysicalMemorySample struct {
	NodeUID       string
	CanonicalUUID string
	Bytes         uint64
}

type prometheusInventoryReader struct {
	config PrometheusInventoryConfig
	client *http.Client
}

func validatePrometheusInventoryConfig(config *PrometheusInventoryConfig) error {
	if config == nil {
		return nil
	}
	endpoint, err := url.Parse(config.URL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return biz.Invalid("PROMETHEUS_INVENTORY_CONFIG_INVALID")
	}
	if config.CAFile != "" && (!filepath.IsAbs(config.CAFile) || endpoint.Scheme != "https") || config.BearerTokenFile != "" && !filepath.IsAbs(config.BearerTokenFile) {
		return biz.Invalid("PROMETHEUS_INVENTORY_CONFIG_INVALID")
	}
	return nil
}

func newPrometheusInventoryReader(config PrometheusInventoryConfig) (*prometheusInventoryReader, error) {
	if err := validatePrometheusInventoryConfig(&config); err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, biz.Invalid("PROMETHEUS_INVENTORY_CA_UNAVAILABLE")
		}
		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(pem) {
			return nil, biz.Invalid("PROMETHEUS_INVENTORY_CA_INVALID")
		}
	}
	return &prometheusInventoryReader{
		config: config,
		client: &http.Client{
			Timeout:   2 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 90 * time.Second},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (r *prometheusInventoryReader) ReadPhysicalMemory(ctx context.Context, clusterID string) ([]PhysicalMemorySample, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	endpoint, err := url.Parse(r.config.URL)
	if err != nil {
		return nil, biz.Precondition("PROMETHEUS_INVENTORY_UNAVAILABLE")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/query"
	query := endpoint.Query()
	query.Set("query", "nvidia_smi_memory_total_bytes{ani_cluster_id="+strconv.Quote(clusterID)+",ani_gpu_source=\"nvml\"}")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, biz.Precondition("PROMETHEUS_INVENTORY_UNAVAILABLE")
	}
	if r.config.BearerTokenFile != "" {
		token, err := os.ReadFile(r.config.BearerTokenFile)
		if err != nil || strings.TrimSpace(string(token)) == "" {
			return nil, biz.Precondition("PROMETHEUS_INVENTORY_UNAVAILABLE")
		}
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, biz.Precondition("PROMETHEUS_INVENTORY_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, biz.Precondition("PROMETHEUS_INVENTORY_UNAVAILABLE")
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, (16<<20)+1))
	if decoder.Decode(&result) != nil || result.Status != "success" || result.Data.ResultType != "vector" {
		return nil, biz.Precondition("PROMETHEUS_INVENTORY_INVALID")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, biz.Precondition("PROMETHEUS_INVENTORY_INVALID")
	}
	byDevice := map[string]PhysicalMemorySample{}
	for _, sample := range result.Data.Result {
		if sample.Metric["ani_cluster_id"] != clusterID || sample.Metric["ani_gpu_source"] != "nvml" || !biz.Ref(sample.Metric["ani_node_uid"]) || len(sample.Value) != 2 {
			continue
		}
		uuid := biz.NormalizeGPUUUID(sample.Metric["uuid"])
		if !physicalUUID.MatchString("GPU-" + uuid) {
			continue
		}
		var raw string
		if json.Unmarshal(sample.Value[1], &raw) != nil {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value >= math.Exp2(64) || math.Trunc(value) != value {
			continue
		}
		key := sample.Metric["ani_node_uid"] + "\x00" + uuid
		if previous, exists := byDevice[key]; !exists || uint64(value) > previous.Bytes {
			byDevice[key] = PhysicalMemorySample{NodeUID: sample.Metric["ani_node_uid"], CanonicalUUID: uuid, Bytes: uint64(value)}
		}
	}
	samples := make([]PhysicalMemorySample, 0, len(byDevice))
	for _, sample := range byDevice {
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(left, right int) bool {
		if samples[left].NodeUID == samples[right].NodeUID {
			return samples[left].CanonicalUUID < samples[right].CanonicalUUID
		}
		return samples[left].NodeUID < samples[right].NodeUID
	})
	return samples, nil
}
