ARG BUILDER_IMAGE=golang:1.26.10-bookworm
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM --platform=$BUILDPLATFORM ${BUILDER_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0 GOMAXPROCS=2 GOFLAGS=-p=2
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/gen/go/ ./api/gen/go/
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath \
      -ldflags="-s -w -X main.Name=ani-accelerator-service -X main.Version=${VERSION}" \
      -o /out/server ./cmd/ani-accelerator-service

FROM ${RUNTIME_IMAGE} AS service
ARG VERSION=dev
ARG SOURCE_REVISION
ARG SOURCE_SNAPSHOT
LABEL org.opencontainers.image.title="ani-accelerator-service" \
      org.opencontainers.image.source="https://github.com/zhangzhe-ctrl/ani-accelerator-service" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${SOURCE_REVISION}" \
      io.ani.source-snapshot="${SOURCE_SNAPSHOT}"
WORKDIR /app
COPY --from=build --chmod=0555 /out/server /app/bin/server
COPY --chmod=0444 THIRD_PARTY_NOTICES.go-kratos-layout.txt /app/notices/go-kratos-layout.txt
USER 10001:10001
EXPOSE 19090 19091
ENTRYPOINT ["/app/bin/server"]
CMD ["-conf", "/app/configs"]
