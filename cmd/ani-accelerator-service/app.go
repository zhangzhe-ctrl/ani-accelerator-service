package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	kratos "github.com/go-kratos/kratos/v3"

	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/provider"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/service"
)

func buildApp(bc *conf.Bootstrap, logger *slog.Logger) (*kratos.App, error) {
	if err := bc.ValidateRuntime(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := data.OpenPostgres(ctx, bc.Database.DsnFile)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			db.Close()
		}
	}()
	providerConfig, err := provider.LoadConfig(bc.Accelerator.ProviderConfigFile)
	if err != nil {
		return nil, err
	}
	ownerKey, err := provider.ReadPublicKey(bc.Accelerator.OwnerPublicKeyFile)
	if err != nil {
		return nil, err
	}
	reader, err := provider.New(providerConfig, ownerKey, db, bc.Accelerator.Freshness.AsDuration())
	if err != nil {
		return nil, err
	}
	if path := bc.Accelerator.ResourceOwnerPublicKeyFile; path != "" {
		key, err := provider.ReadPublicKey(path)
		if err != nil {
			return nil, err
		}
		if err = reader.ConfigureResourceOwnerKey(key); err != nil {
			return nil, err
		}
	}
	if path := bc.Accelerator.ModeldevOwnerPublicKeyFile; path != "" {
		key, err := provider.ReadPublicKey(path)
		if err != nil {
			return nil, err
		}
		if err = reader.ConfigureModeldevOwnerKey(key); err != nil {
			return nil, err
		}
	}
	key, err := os.ReadFile(bc.Security.CursorKeyFile)
	if err != nil {
		return nil, biz.Invalid("CURSOR_KEY_UNAVAILABLE")
	}
	codec, err := biz.NewCursorCodec(key)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for _, c := range providerConfig.Connections {
		refs = append(refs, c.Ref)
	}
	u, err := biz.NewUsecase(db, reader, codec, refs, bc.Accelerator.MaxReplicas, bc.Accelerator.Freshness.AsDuration())
	if err != nil {
		return nil, err
	}
	initialization, err := biz.NewInitializationUsecase(u, db, reader)
	if err != nil {
		return nil, err
	}
	telemetryServer := server.NewNodeTelemetryServer(db, reader)
	reader.ConfigureNodeExitRecovery(telemetryServer)
	readiness := server.NewReadiness()
	observability, err := server.NewObservability(Name, Version, readiness)
	if err != nil {
		return nil, err
	}
	middlewares := observability.ServerMiddleware(logger)
	grpcServer, err := server.NewSecureGRPCServer(bc.Server.Grpc, bc.Security, middlewares...)
	if err != nil {
		observability.Shutdown(context.Background())
		return nil, err
	}
	handler := service.NewAccelerator(u, initialization)
	pb.RegisterAcceleratorAdminServiceServer(grpcServer, handler)
	pb.RegisterAcceleratorCatalogServiceServer(grpcServer, handler)
	pb.RegisterAcceleratorUsageServiceServer(grpcServer, handler)
	nodepb.RegisterNodeTelemetryServiceServer(grpcServer, telemetryServer)
	adminServer := server.NewAdminServer(bc.Server.Admin, readiness, observability.Gatherer(), middlewares...)
	worker := server.NewWorker(u, db, readiness, logger, bc.Accelerator.CollectInterval.AsDuration())
	initializationWorker := server.NewInitializationWorker(initialization, logger, time.Second)
	app := kratos.New(kratos.ID(id), kratos.Name(Name), kratos.Version(Version), kratos.Logger(logger), kratos.Server(grpcServer, adminServer, worker, initializationWorker), kratos.BeforeStop(func(context.Context) error { readiness.Set(false); return nil }), kratos.AfterStop(func(ctx context.Context) error { db.Close(); return observability.Shutdown(ctx) }), kratos.StopTimeout(bc.Server.ShutdownTimeout.AsDuration()))
	complete = true
	return app, nil
}
