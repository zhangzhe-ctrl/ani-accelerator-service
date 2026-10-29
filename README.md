# ani-accelerator-service

Accelerator v1.2 独立 Go 服务，管理 GPU 供给目录、固定规格、设备与实际绑定、容量、使用投影和释放观察。实现 Admin 12、Catalog 5、Usage 5，共 22 个 mTLS gRPC RPC；使用真实 PostgreSQL 持久化及只读 provider 适配。

Go module：`github.com/zhangzhe-ctrl/ani-accelerator-service`，Go 版本以 `go.mod` 为准。运行不依赖生成本仓的 layout。`cmd/ani-accelerator-service` 显式装配；biz 保持纯领域规则，service 适配 protobuf，data 管 PG/外部读取，server 管身份与生命周期。

软件验收、真实 GPU 验证、外部服务集成和发布分别记账，见 [执行状态](docs/execution/status.md) 与 [验收矩阵](docs/plans/accelerator-v1.2-acceptance.md)。2026-10-17 已在单节点 Tesla T4 上完成共享、正式百分比整卡计划的真实 GPU 与 ACC 主链验证，并保留修复版 Volcano scheduler；[保留版回执](docs/evidence/gpu-live-20261017/retained-receipt.json)记录 GPU 原始验收，[baseline 资格回执](docs/evidence/gpu-live-20261017/baseline-qualification-receipt.json)记录无时间自动失效的 v2 资格及采集/释放解耦后的部署验证。生产没有测试绕过，未经有效硬件验收资格的供给保持 CLOSED/NOT_VERIFIED；资格持续匹配 baseline，运行健康独立按实时采样判断。受控回放不证明真实模型加载或推理，ACC 验收也不替代 Governance/Inference 产品链路验收。

- [接入合同：22 RPC、权限、摘要、幂等、owner 附件与释放责任](docs/contracts/accelerator-consumer-contract.md)
- [固定 baseline/plan/projection 测试向量](docs/contracts/accelerator-vectors.json)
- [运行、显式迁移、恢复与门禁](docs/runbooks/accelerator-runtime.md)
- [统一生产镜像构建入口与结果 JSON](docs/runbooks/production-image.md)
- [provider 版本、来源、配置与限制](docs/runbooks/provider-sources.md)
- [实施计划](docs/plans/accelerator-v1.2-implementation.md)及[原始设计快照](docs/design/v1.2/README.md)

活跃 API 在 `api/protos/accelerator/`，生成 Go 在 `api/gen/go/`，通过固定 `make api` 生成，不手改。正式迁移在 `migrations/`，启动不迁移、不播种；设计树 SQL 仍只是历史提案。

Governance 拥有配额、完整 charge 与异步 SyncGpuUsage；Inference 拥有 workload、模型 readiness、删除完成与可靠配额释放事实。创建无 Consumer 前置，ENDED 不清除活绑定，退出/重启不代表实例删除。本仓不写其他服务业务状态，也不写 Kubernetes 资源。

本任务本地仅阅读、编辑、传输和 Git；以下仅在 Fedora 隔离环境运行：

```bash
make tools api generate
# 必须先显式迁移隔离 PG 并配置无 DDL 运行角色
export ACC_TEST_DSN_FILE=/absolute/task/pg/runtime-dsn
make verify test-domain test-pg test-contract test-provider test-race
make supply-chain-tools audit
```

恢复演练另需明确任务容器与 evidence 目录，见运行手册。配置样例引用不存在的受管密钥/连接文件，不能直接当生产配置运行。`/healthz` 是存活，`/readyz` 包括 PG 探测，GPU 完整度由业务观测返回。

[上游 notice](THIRD_PARTY_NOTICES.go-kratos-layout.txt) 与 [来源记录](docs/scaffold/provenance.md) 保留。BOM 在 `docs/scaffold/bom.cdx.json`，由 Fedora 干净提交生成；本仓没有项目级 LICENSE，未替项目 owner 选择发布许可。
