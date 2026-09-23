# ani-accelerator-service

GPU 资源管理服务，面向静态整卡与固定规格的逻辑 GPU。当前交付是基于 ANI Kratos layout 生成的独立服务骨架、GPU v1.2 合同及相关设计资料；供给管理、容量观测、使用关联和释放观察等业务尚未实现。

Go module：`github.com/zhangzhe-ctrl/ani-accelerator-service`。工具链基准：Go `1.26.7`。构建和运行不依赖生成它的 layout 仓库；脚手架来源见 [docs/scaffold/provenance.md](docs/scaffold/provenance.md)。

实施入口：[v1.2 首期独立实施计划](docs/plans/accelerator-v1.2-implementation.md)、[验收矩阵](docs/plans/accelerator-v1.2-acceptance.md)、[完整 Goal 提示词](docs/plans/accelerator-v1.2-goal.md)、[执行状态](docs/execution/status.md)。本批完整实现 Accelerator，Governance/Inference 只交付接入合同；本地仅阅读、编辑、传输和 Git 提交推送，生成/格式化/构建/测试/数据库等全部在 `fedora`。当前无 GPU 环境，真实 GPU 验证另列后续且保持 `not_verified`。

## 目录与职责

| 位置 | 内容 |
|---|---|
| `cmd/ani-accelerator-service/` | 服务装配入口 |
| `internal/service/`、`internal/biz/`、`internal/data/` | 传输适配、业务与外部依赖适配边界，按具体业务逐步实现 |
| `internal/server/`、`internal/conf/`、`configs/` | 运行骨架、类型化配置与本地配置样例 |
| `api/protos/accelerator/v1/` | 本服务正式维护的 GPU 合同源 |
| `api/protos/accelerator/integration/v1/` | 嵌入 Inference 创建/删除合同的 GPU 消息附件；不定义替代 Inference 的业务服务 |
| `api/gen/go/` | 通过固定工具生成的 Go API，禁止手改 |
| [docs/design/v1.2/](docs/design/v1.2/README.md) | v1.2 设计来源快照、SQL 提案、示例与纯计算回归测试 |

v1.2 的职责边界：Governance 保有配额账本并向 Inference 投递业务命令，Inference 持有业务实例和执行生命周期，本服务管理 GPU 供给、规格和实际绑定读模型。Governance 异步调用 `SyncGpuUsage` 同步使用关联，创建不需要 `RegisterConsumer`。Usage 结束不删除实际 Binding，也不触发退额。Inference 沿用正常重启策略，仅在明确删除且完整实例清理完成后向 Governance 报告原 GPU charge 的全量累计释放。

合同中的租户委托、Governance/Inference 精确 mTLS 身份及动作权限是待实现的接入要求；生成 SDK 不代表已实现或启用这些权限。当前没有装配 GPU 业务 RPC 的实现。

## 开发命令

从仓库根目录执行：

```bash
make tools
make api
make design-test
make verify
go run ./cmd/ani-accelerator-service -conf ./configs
```

`make tools` 安装固定版本工具，`make api` 生成业务 API，`make design-test` 执行迁入的 Python 设计模型测试，`make verify` 执行本仓质量检查。首次工具安装和依赖获取需要对应依赖源可达。设计模型测试只验证单位换算、容量计算、使用投影与累计释放算法，不证明真实设备或数据库行为。

配置默认仅监听本机回环地址。后续部署配置通过类型化的 `ANI` 环境配置覆盖；不得把当前样例当成生产配置。

## 当前运行能力与验证边界

骨架提供 Kratos 生命周期和优雅退出、gRPC 与独立管理 HTTP 服务、结构化脱敏日志、tracing、metrics 及中间件。`/healthz` 表示进程存活；`/readyz` 仅表示本地运行启动钩子完成；`/metrics` 导出本进程指标。引入真实依赖时，再补充相应的就绪检查。

尚未接入 PostgreSQL、集群、GPU provider、模型加载、真实 Inference 创建/删除、Governance 退额及使用关联同步。SQL 文件全部是设计提案，未作为启动迁移运行。本轮未部署、发布或推送服务；骨架启动或设计测试通过不等于真实 GPU 业务验收通过。

## 来源许可与供应链

[THIRD_PARTY_NOTICES.go-kratos-layout.txt](THIRD_PARTY_NOTICES.go-kratos-layout.txt) 保留上游模板的 MIT notice。本仓当前没有项目级 `LICENSE`，发布前需由项目 owner 确定。

首次源码提交后运行 `make supply-chain-tools` 与 `make audit`，审查并提交 `docs/scaffold/bom.cdx.json`。CI 会检查运行时 SBOM 是否存在且匹配，并执行漏洞、密钥、notice 完整性及许可证证据检查。来源设计包的历史验证记录不替代本仓 SBOM 或本轮验证。
