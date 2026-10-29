# 生产服务镜像构建

ACC 试行统一入口 `make image`，只构建并加载一个平台的主服务镜像。主程序是
`./cmd/ani-accelerator-service`；不执行生成、测试、数据库迁移、Harbor 推送或集群更新。
质量门禁仍按仓库原有要求执行。默认在 Fedora 隔离目录运行；本轮用户明确授权的
`.40` 真实构建例外与实际结果见[执行状态](../execution/status.md)。

```bash
make image \
  IMAGE=docker.changqingyun.cn/ani/ani-accelerator-service:<commit> \
  VERSION=<commit> PLATFORM=linux/amd64 TARGET=service
```

依赖 Python 3、Docker Engine 和 Buildx。`IMAGE` 必须带标签，支持单个平台
`linux/amd64` 或 `linux/arm64`；本入口使用 `--load`，多平台索引留给发布工具。
`DOCKER` 可指定 Docker CLI 路径。`IMAGE_OUTPUT_DIR` 默认 `.work/images`。
Go 编译固定 `GOWORK=off`、`GOTOOLCHAIN=local`、`CGO_ENABLED=0`，明确编译一个 main 包。
Go 版本要求以 `go.mod` 为准，默认 builder 使用 Go 1.26.10。

公司内网构建通过参数选择已有的基础镜像与 Go 代理：

```bash
make image IMAGE=<Harbor/project/name:tag> VERSION=<commit> \
  BUILDER_IMAGE=<Harbor/project/golang@sha256:digest> \
  RUNTIME_IMAGE=<Harbor/project/static-debian12@sha256:digest> \
  IMAGE_GOPROXY=https://goproxy.cn,direct
```

生产发布使用经过核验的基础镜像 digest；默认公共标签便于准备环境，不保证长期可复现。
builder 需提供符合 `go.mod` 的 Go；runtime 需为包含系统 CA 的静态 Go 运行镜像。
基础镜像参数不接受登录信息，Harbor 凭证由 Docker/后续发布工具提供。

## 构建输入和结果

构建脚本先复制 Dockerfile、`.dockerignore`、`go.mod`、`go.sum`、上游 notice，以及
`api/gen/go`、`cmd`、`internal`、`pkg` 中的非测试 Go 源码到独占目录，再交给 Docker。
配置、凭据、Git、测试、缓存及本地二进制不进入该目录。符号链接输入被拒绝；将来新增
生产 `go:embed` 资源时，需要显式扩展构建输入清单。

每次构建保留 `.work/images/image-*/context`、`source-manifest.json`、BuildKit 原始元数据，
成功后写入 `result.json` 并向 stdout 输出同一个 JSON。Docker 进度写 stderr。
结果包含 schema_version、service、源码 revision、构建输入 snapshot 摘要、版本、target、
平台、镜像地址、本地 image ID、本地描述符 digest/media type 与基础镜像引用。
描述符可能指向单个平台的 manifest，也可能指向附带 provenance 的 index。
`local_config_digest` 仅在 BuildKit 提供时记录，否则为 null；兼容 Docker 传统存储和
containerd 镜像存储，不能假定 `docker image inspect` 的 ID 永远等于 config digest。
`BUILDX_METADATA_PROVENANCE=max` 在原始元数据中记录 BuildKit 提供的构建来源，
字段语义见 [Docker Buildx 官方说明](https://docs.docker.com/reference/cli/docker/buildx/build/#write-build-result-metadata-to-a-file---metadata-file)。
源码 Git 工作区有修改时 `source_dirty=true`，不会把 HEAD 冒充完整构建输入。
无 `.git` 的源码导出必须传 `SOURCE_REVISION=<完整40位commit>`，此时
`source_dirty=null`，以单独记录的构建输入摘要标识实际内容。

加载后再次核对 image ID、源码/版本标签、平台及启动配置。任何构建或核对失败都以非零退出，
该任务不产生成功 `result.json`。历史任务记录不会被覆盖。
本地结果固定 `pushed=false`、`registry_digest=null`；本地 image ID 或描述符 digest
均不作为 Harbor 发布回执。发布工具需另行推送并从目标 registry 核对 digest。

## 运行约定

镜像内程序统一为 `/app/bin/server`，默认参数 `-conf /app/configs`，用户为 `10001:10001`。
镜像只含程序、基础运行文件及上游 notice。部署时挂载配置目录，以及配置引用的独立
DSN、mTLS、cursor key 和 provider 文件；非 root 用户必须具有相应读取权限。
配置示例与权限要求见[运行手册](accelerator-runtime.md#配置与启动)。
现有 Deployment 若覆盖了 `command`，采用该镜像时需同步调整程序路径。
gRPC/admin 的监听地址与端口继续由运行配置决定；`EXPOSE` 不修改监听地址。
数据库迁移仍由已有[显式迁移流程](accelerator-runtime.md#显式-postgresql-迁移)执行。

```bash
docker run --rm --network=none <本地镜像:tag> -h
make test-image
```

`-h` 仅验证真实二进制可执行、参数入口可用。`test-image` 使用明确的 Docker 外部边界替身，
覆盖输入隔离、源码冻结、失败停止、标签竞争及结果语义，不证明真实镜像构建或业务启动。
实际环境验证结果只记录在[执行状态](../execution/status.md)。
