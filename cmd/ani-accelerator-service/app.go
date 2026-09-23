package main

import (
	"context"
	"log/slog"
	"time"

	kratos "github.com/go-kratos/kratos/v3"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"

	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
)

func buildApp(bc *conf.Bootstrap, logger *slog.Logger) (*kratos.App, error) {
	if err := bc.Validate(); err != nil {
		return nil, err
	}
	readiness := server.NewReadiness()
	observability, err := server.NewObservability(Name, Version, readiness)
	if err != nil {
		return nil, err
	}
	middlewares := observability.ServerMiddleware(logger)
	grpcServer := server.NewGRPCServer(bc.Server.Grpc, middlewares...)
	adminServer := server.NewAdminServer(bc.Server.Admin, readiness, observability.Gatherer(), middlewares...)
	return newApp(logger, grpcServer, adminServer, readiness, observability, bc.Server.ShutdownTimeout.AsDuration()), nil
}

func newApp(
	logger *slog.Logger,
	grpcServer *kratosgrpc.Server,
	adminServer *kratoshttp.Server,
	readiness *server.Readiness,
	observability *server.Observability,
	stopTimeout time.Duration,
) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Logger(logger),
		kratos.Server(grpcServer, adminServer),
		kratos.AfterStart(func(context.Context) error {
			readiness.Set(true)
			return nil
		}),
		kratos.BeforeStop(func(context.Context) error {
			readiness.Set(false)
			return nil
		}),
		kratos.AfterStop(observability.Shutdown),
		kratos.StopTimeout(stopTimeout),
	)
}
