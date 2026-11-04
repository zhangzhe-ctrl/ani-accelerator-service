package provider

import (
	"context"
	"crypto/tls"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

const gpuUUID2 = "GPU-fedcba98-7654-3210-fedc-ba9876543210"

type providerRepository struct {
	biz.Repository
	reader *providerReader
}

func (r providerRepository) View(_ context.Context, read func(biz.Reader) error) error {
	return read(r.reader)
}

type providerReader struct {
	biz.Reader
	clusters   []biz.Cluster
	devices    []biz.DeviceRecord
	devicesErr error
}

func (r *providerReader) Clusters(context.Context, []string, string, int) ([]biz.Cluster, error) {
	return r.clusters, nil
}

func (r *providerReader) Pools(context.Context, string, string, int) ([]biz.Pool, error) {
	return nil, nil
}

func (r *providerReader) Devices(context.Context, string, string, string, int) ([]biz.DeviceRecord, error) {
	return r.devices, r.devicesErr
}

func tlsKeyPair(cert, key string) (*tls.Config, error) {
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}
