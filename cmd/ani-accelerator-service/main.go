package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/log"
	"go.uber.org/automaxprocs/maxprocs"

	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
)

// Name and Version can be overridden with -ldflags at build time.
var (
	Name     = "ani-accelerator-service"
	Version  = "dev"
	flagconf string
	id, _    = os.Hostname()
)

func init() {
	flag.StringVar(&flagconf, "conf", "configs", "config path, for example -conf configs/config.yaml")
}

func main() {
	flag.Parse()
	logger := newRuntimeLogger(os.Stdout)
	log.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("service terminated", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	undoMaxProcs, err := maxprocs.Set(maxprocs.Logger(func(format string, args ...interface{}) {
		log.Info("runtime CPU quota", "detail", fmt.Sprintf(format, args...))
	}))
	if err != nil {
		return fmt.Errorf("configure runtime CPU quota: %w", err)
	}
	defer undoMaxProcs()

	c := config.New(config.WithSource(
		file.NewSource(flagconf),
		env.NewSource("ANI"),
	))
	defer c.Close()
	if err := c.Load(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// Kratos deliberately discards unknown protobuf fields. Inspect the raw
	// section first so a removed static grant never becomes a silent migration.
	if err := rejectLegacySecurity(c); err != nil {
		return err
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		return fmt.Errorf("scan config: %w", err)
	}
	app, err := buildApp(&bc, logger)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}
	if err := app.Run(); err != nil {
		return fmt.Errorf("run app: %w", err)
	}
	return nil
}

func rejectLegacySecurity(c config.Config) error {
	values, err := c.Value("security").Map()
	if err != nil {
		return fmt.Errorf("security config is required: %w", err)
	}
	for _, key := range []string{"grants", "governance_uri", "governanceUri", "inference_uri", "inferenceUri", "resource_uri", "resourceUri"} {
		if _, exists := values[key]; exists {
			return fmt.Errorf("security.%s is obsolete; remove static grants and configure exact service DNS identities", key)
		}
	}
	return nil
}

func newRuntimeLogger(writer io.Writer) *slog.Logger {
	handler := log.NewHandler(
		log.WithWriter(writer),
		log.WithFormat(log.FormatJSON),
		log.WithLevel(log.LevelInfo),
		log.WithAddSource(true),
		log.WithExtractor(tracing.TraceAttrs),
		log.WithFilter(log.FilterKey(
			"args",
			"authorization",
			"cookie",
			"credential",
			"password",
			"private_key",
			"set-cookie",
			"token",
		)),
	)
	return slog.New(handler).With(
		slog.String("service.id", id),
		slog.String("service.name", Name),
		slog.String("service.version", Version),
	)
}
