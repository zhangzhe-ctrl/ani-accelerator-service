# Gov / Acc / Inference v1.2 接线结果索引

批次：`GOV-ACC-INF-V12-WIRING-01`。这是本 Goal 唯一执行结果索引；[计划](governance-accelerator-inference-v1.2-plan.md)保留规划时事实，[Goal](governance-accelerator-inference-v1.2-goal.md)定义授权范围。

**真实软件主链已通过 R2、R3 与最终 R4d 联测：Gov 公共创建入口经权限、Acc Resolve 和事务占额，调用真实 Inference mTLS 接收器，将原业务、plan、全量 charges 和 ACK 保存到真实 PG，再从该快照调用生产参数渲染函数；DELETE 持久受理后，显式注入的完成通知经真实 Inference refund client 到 Gov mTLS receiver/事务账本完成一次累计退额。** R4d 还验证了丢响应重投、Inference 进程重启后的原 ACK 恢复、Acc 使用同步及真实退额 receiver 的完整负向调用。

完整 C02 仍 `blocked`：KServe v0.16 的 LLMI→LWS queue/模板 GPU metadata 同步接线未实现。Deployment 的共享/整卡冻结快照参数交付已验证；typed LWS helper 不能替代实际控制器接线。Gov 完整 `make verify-ci`、相关 vet/build、正式依赖校验均已通过；本 Goal 因 C02 尚不能标记 achieved。

本次是隔离环境中的 `software-only` 验证。模型/K8s 执行被暂停，完成通知由测试明确注入；真实 owner 清理完成、GPU/模型运行和生产可用性均为 `not_verified`。

## 基线与公开依赖

| 仓库 | 规划输入 base | 当前已知候选 / 最终回执 |
|---|---|---|
| Accelerator | `1d32dd9a9173b8869fa0ef2ae64e20b88f2ca0a3` | runtime/API 沿用该 base；本 Goal 仅合同导航、计划与结果文档。最终文档提交为本文所在提交，完整 SHA 见仓外 `publication-receipt.json`，避免自引用 |
| Inference | `01033dbbca64164e7a1ae52516a4b731e5858e66` | API provider `cbeb2a6c6fa177a19ebbc45419884b2145ce20d5`；已发布文档 HEAD `e60df7f9b82f2c197b4e47e15801686e419174d4` |
| Governance | `25a171cc676e8dc5f2a614da08f64c0dc5daff06` | quota API provider `1ab461604c74bdf62d1a71b9a310febcbab21dd0`；最终 consumer `dd1e772ac5d0718c00a3cc197c9e750c0b9fe896` |

R0 在线核实后，三仓实际执行 base 与表中规划输入一致。三仓任务分支均为 `codex/gov-acc-inf-v12-wiring-01`，非强制推送；未创建 tag/PR、未合并 main 或部署。旧 Gov 工作区 `refactor/quota-contract-convergence` 的 `d1a804f1d35d7294cb7eab48ee3f256d9d2482b5` 及无关 `_agent/` 保持原状；原 Inf main 与原 Acc main/未跟踪计划也保持原状。外置发布回执核对完整远端 SHA，不从移动分支推断。

| Provider | 正式发布版本 | Consumer / 关系 |
|---|---|---|
| `github.com/zhangzhe-ctrl/ani-accelerator-service`，provider `1d32dd9a9173` | `v0.0.0-20260924030150-1d32dd9a9173` | Gov 与 Inference 消费既有 plan、附件和 ACK 公共契约；Acc runtime 不变 |
| `github.com/zhangzhe-ctrl/ani-governance/api/quota`，provider `1ab461604c74` | `v0.0.0-20261009133356-1ab461604c74` | Gov receiver 与 Inference refund client 消费同一公开退额包 |
| `github.com/zhangzhe-ctrl/ani-inference-service`，API provider `cbeb2a6c6fa1` | `v0.0.0-20261009141630-cbeb2a6c6fa1` | Gov consumer `dd1e772ac5d0718c00a3cc197c9e750c0b9fe896` 锁 CREATE / DELETE / DurableOwnerAck；Inf 后续提交仅文档，API/runtime 与此版本一致 |

已核对 provider sums：

| Module | Module sum | go.mod sum |
|---|---|---|
| Accelerator 上述版本 | `h1:lXrsyPsOQ0M8Tsy6aWcw8k5IL4h59uhy3Ysf62palrU=` | `h1:3mSqLUcRMc++eiZuqICo56tFwoKZ9jr34tepPvWcOsM=` |
| Gov quota 上述版本 | `h1:cfxpluUd6AVt8vBb6Z4vb5FQg2/3fK67BGDRmzbUFIA=` | `h1:OmpTNUUTOEMzeXx5cOUdOYqNxrmA+KBthlt44mfZ4s8=` |
| Inference API 上述版本 | `h1:am8snqLVePSLLurw+wSGdFlPkAxIGv/2BsB7obyGLSw=` | `h1:XHxBcsdlAk/nFgUmG4a1lfcd8gwL7Ye/ccV6BYzusWE=` |

发布顺序已采用 Gov quota API→Inference 锁正式 quota/Acc 版本并发布 API→Gov adapter 锁 Inference API。依赖关系为 `Gov runtime → Inference API → Gov quota API`；Gov 根 module 保持 `go-wind-admin`。唯一退额 Proto 源仍在 Gov `api/protos/quota/service/v1/quota_release.proto`，公开生成包为 `api/quota/gen/go/quota/service/v1`，保留 wire package/RPC/tag/enum；旧重复 Release descriptor 退出使用。正式验证采用 `GOWORK=off`，不使用本地 `replace`、兄弟仓 internal 或复制生成包。

## 必验矩阵

`pass` 只覆盖该行明确列出的软件证据；C02 的具体阻塞单独标记。源码、软件接线与真实硬件/业务验收不互相替代。

| ID | 当前结论 | 实际证据及界限 |
|---|---|---|
| C01 | `pass` | 首次 R2：实际 Gov HTTP JWT/Redis/Casbin 入口→既有 Resolve/Occupy→真实 Inference mTLS/service/PG/ACK→实际 PG 快照的生产 renderer。单 Pod 共享 6144 MiB，F=1024、q=6；模型/K8s 执行暂停 |
| C02 | `blocked` | Deployment 共享/整卡、固定 2 副本的真实 PG→生产 renderer 定向测试 `pass`；实际 joint 为单 Pod shared。KServe v0.16 LLMI→LWS top-level queue/两模板 GPU metadata 接线未实现，不能将 typed helper 记为 C02 `pass` |
| C03 | `pass` | R4d CREATE 实际调用 2 次，首次 ACK 响应丢失；原 operation/resource/charge/ACK 重放不另占额。Inference 真实 PG 并发命令单 ACK、原子回滚和同内容重放通过；相关 race 通过 |
| C04 | `pass` | 实际 BFF 无认证/无 grant/仅 create grant 无 delete grant、撤权后重放、跨租户拒绝；Inference 真实 PG 8 项 forged caller/tenant/owner/业务及请求摘要/错误单位/漏全量 charge/缺 plan 拒绝，且无持久 ACK。额度并发上限由 Gov 必需 PG 门禁核验 |
| C05 | `pass` | R4d：实际 Inference owner 进程停止/重启，Gov 重投从真实 PG 恢复原 CREATE、resource、charge 和 ACK |
| C06 | `pass` | R4d 实际 DELETE 原关联及 ACK 持久化；真实 PG `DeleteFirstClosesLateCreate`、`DeletePreservesAcceptedCreateAndRejectsTopologyMutation` 通过。Gov 未发撤销/领取竞态沿用当前事务账本必需门禁。DELETE ACK 仅表示受理/关闭意图，不证明释放 |
| Q01 | `pass` | 首次 R3：先有真实持久 DELETE，再显式注入完成通知；真实 Inf PG 原上下文→实际 mTLS refund client→Gov receiver/Ent PG，一次退原 charge，重放零增量 |
| Q02 | `pass` | R4d Gov 首次退款事务已提交但响应丢失；同 event/内容重试 delta=0，同一 receipt 仅 1 条。既有真实 receiver/PG 累计与不同 event 重复通知门禁验证通用累计账本；GPU 部分退额仍拒绝，不实现通知触发器 |
| Q03 | `pass` | R4d 真实 client 与 receiver 无 DELETE 拒绝；持久 DELETE 后直调真实 receiver 拒绝错原 CREATE、漏 GPU、部分/超量、错 DNS owner/CA；每次 charge/账户/receipt 不变。同 event 异内容由真实 receiver 拒绝 |
| Q04 | `pass` | `TestManagedGPUIsolationFromLegacyQuotaAndResolve` 及真实 PG lifecycle 测试通过：受管 stop/start/restart/update/delete 不触碰旧 Reserve/Confirm/Release，缺原 plan 不二次 Resolve；新 reporter 只由显式完成通知端口调用，未自动接入普通退出或异常 |
| A01 | `pass` | R4d 实际 Acc Sync DECLARED/ENDED revisions=1/2；5 个真实受限 PG 测试通过，ENDED/Missing poll 保留 allocation。实际 Gov→Acc Resolve/Sync caller、原 ref/plan 和持久 acked revision 均核验，未新增 Inference→Resolve/Sync |
| D01 | `pass` | Gov quota / Inf API 按真实发布版本和 sums 正式消费；两方生成可重现、vet/build、相关 race 及 Gov 完整 gate 通过。61 个 Gov 变化路径的 staged Git blob 与 Fedora 受测 source 完全一致；公开 module 下载由官方 proxy/sumdb 核对 |
| H01 | `pass`（交接文档范围） | 现有三方合同导航与 Inference 手册包含可信来源、PG/ACK、完整真实 CREATE/DELETE/ACK 消息文件、共享/整卡字段映射、配置/调用、错误/重试及负责人边界；最终版本/证据回执在本文收敛 |

R4d `pass` 指本次真实联测运行通过，不代表 C02 的实际 LWS 接线已通过。

## 已执行验证与保留失败

所有软件执行在授权 Fedora，任务根目录为 `/home/chabking/gov-acc-inf-v12-wiring-01-20261009`，使用任务独占 PG/角色/端口/证书/缓存/TMPDIR。下列日志在仓外 `evidence/`；脱敏副本为本地 `/home/chabking/.codex/attachments/gov-acc-inf-v12-01-results/`，发布/清理回执同目录。私有 DSN/key 不进入本文或公开日志。

| 验证 | 结果 | 证据索引 |
|---|---|---|
| 首次实际 R2 / R3 | `pass`，exit 0 | `r2-r3-actual-inference-joint.log`；[实际持久消息文件](https://github.com/zhangzhe-ctrl/ani-inference-service/blob/e60df7f9b82f/docs/contracts/managed-gpu-owner-examples.json)保留正式 API 解码后的 CREATE / DELETE / response / ACK |
| 实际 R4d `TestActualInferenceOwnerWiring` | `pass`，2.80 s，exit 0 | `r4d-actual-inference-joint.log`；脱敏数值结果 `actual-inference-wiring-result.json`。资源 `94372da8-14d7-4af1-ac61-28d0b1e9d9ac`；不同 owner OS PID，同原 ACK/charge；单 receipt、重试 delta 0 |
| Inference 相关真实 PG、受影响包、race、生成可重现、vet/build | `pass`，exit 0 | `r2-inference-candidate.log` / `r4-inference-managed-race.log`；生成两遍 SHA 清单。真实 PG 使用任务 DSN，缺 DSN 的 skip 不计通过 |
| Acc 定向 5 个真实受限 PG 测试 | `pass`，无 skip | `r5-accelerator-actual-pg.log`，精确测试名如下 |
| Gov 完整 `make verify-ci` | `pass`，exit 0 | `r5-governance-verify-ci-fresh.log` / `.rc`；必需 PG 22 项、data race 3 项/service race 1 项、两遍 make gen 的 865 managed files/214 inputs/7 roots 一致、gow tests 和工具集成均通过 |
| Gov 相关 vet/build / 正式依赖 | `pass`，exit 0 | `r5-governance-vet-build-modverify.log`；Go 1.26.9，GOWORK=off，all modules verified |
| 发布 / 清理 / 远端 CI | 见明确回执 | `publication-receipt.json` / `cleanup.log`；本次 CI 指 Fedora 已运行的完整命令，GitHub Actions 尚 `not_verified`。Acc 仅文档改动，未重跑全量 `make verify` |

Acc 的 5 个实际 PG 测试是：

- `TestPostgresRuntimeRoleAndAtomicIdempotency`
- `TestPostgresProjectionOrderConflictAndRestart`
- `TestPostgresBindingsBeforeProjectionAndScopedRelease`
- `TestPostgresConstraintsVersionRaceAndConcurrentAssociation`
- `TestPostgresCatalogDenialsAndStablePaging`

Inference 下列失败在 base `01033dbbca64164e7a1ae52516a4b731e5858e66` 独立复现，记录在 `r2-inference-baseline-known-failures.log`。本 Goal 没有修改历史 direct lifecycle/metrics 来消除它们；它们不能记为通过，也不由受管链定向 PASS 覆盖：

- `TestPostgresCreateLifecyclePreservesQuotaResources`：成功后期望额外 `observe_runtime`。
- `TestWorkStoreMetricsSnapshotIntegration`：插入 `NULL next_run_at`，却期望出现在 DueWork。

失败联测与门禁日志保留，不以重试覆盖：R4 初次使用旧环境导致 FK 错误，R4b 复用固定实例名导致 tenant/name 唯一约束冲突；修正仓外配置与测试唯一实例名后 R4c/d 通过。Gov 完整门禁曾因工具 fixture/include 输入、不兼容的 `REDIS_STATE_DIR` 外部设置，以及重复使用已种入固定岗位的 guard 库失败；原日志及 `.rc` 均保留。最终完整运行使用新建受限库，未删除旧行或更改业务逻辑制造成功。

## 显式迁移、生成与权限启用

Inference 增量迁移为 `migrations/000018_managed_gpu_commands.sql`，通过现有迁移链显式执行，服务启动不执行 DDL。资源表以 `(tenant_id, resource_id)` 保存 immutable 原 CREATE 上下文、完整 plan/charges 和 DELETE 关联；命令表以 `(tenant_id, operation_id)` 保存 tenant-preserving FK、规范原报文、hash、response/ACK 与执行/关闭意图。精确 SQL、调用和恢复规则引用 [Inference handoff](https://github.com/zhangzhe-ctrl/ani-inference-service/blob/e60df7f9b82f/docs/contracts/managed-gpu-owner-handoff.md)，不复制第二份字段表。Gov 继续使用原 Ent 账本，不改为 sqlc/pgx；Acc runtime/migrations 不变。

生成入口：Inference 新增 `scripts/generate-api.sh`，固定 Buf 1.60.0、protoc-gen-go 1.36.11 / go-grpc 1.6.0，通过固定发布 Acc 输入生成本仓 API；SQL 沿用 `sqlc.yaml` 与本次固定 sqlc 1.31.1。Gov 使用受管 `make gen`（包含 API/公开 quota、Ent 与 OpenAPI）；Acc 活跃 API 仍使用 `make api`，本次未修改其 API。生成文件不手改；最终具体命令、工具 manifest 和可重现比较由根执行者补入。

权限权威登记为 **`INFERENCE=15`**，独立于 `ACCELERATOR`。模块套餐资格（PlanModule）、配额限额（PlanQuota）与角色/action 的 create/delete 权限是分别核验的关系，模块可见不自动授予任一动作；不得以 ACCELERATOR 读权限授权推理写入。

Gov 提供显式 `scripts/ops/sql/register-inference-permissions.sql` 与 `scripts/ops/sql/enable-inference-access.sql`。公共入口为 `POST /api/v1/inference/services` 和 `POST /api/v1/inference/services/{data.resource_id}:delete`，保持 `data` 包装及当前可信 Principal；trusted attachment/plan/charges/tenant/actor 不由用户输入。隔离联测登记只作用于任务私有库。**没有启用生产租户的角色/套餐，没有自动为 FREE 或所有角色授权**；生产启用需另行选择具体 tenant/role/plan 后显式执行，经部署输入核实。

## C02 与负责人边界

详细源码结论和 pinned primary links 已集中在 [Inference handoff 的 C02 小节](https://github.com/zhangzhe-ctrl/ani-inference-service/blob/e60df7f9b82f/docs/contracts/managed-gpu-owner-handoff.md#kserve-v016-lws-projection-blocker-c02)。KServe v0.16 的 LLMI 输入没有支持所需 queue/模板 annotations 的完整通道，其 metadata whitelist 丢弃 Volcano/HAMI 键；LWS v0.10 初建 PodGroup 从 LWS 顶层读 queue。创建后补 patch 不能保证第一次调度，后续 KServe full update 也不保证保留投影。

因此实际 API 在 Gov Occupy 前拒绝 managed GPU LWS，Inference intake 和 LLMI renderer 同样拒绝，CPU LWS 保留。`ProjectManagedGPULeaderWorkerSet` 仅验证未来 pre-CREATE 类型投影，不被当前 KServe 创建流程调用，UPDATE 保留也未接线。C02 等待用户决定同步 CREATE/UPDATE interception 的授权 scope/实现路线；未决前不能声明完整参数交付、LWS 可用或 Goal achieved。

推理负责人仍需实现和真实验收：模型加载/推理、GPU 调度与物理限制、可信 Pod 签名和完整历史范围、封闭全部创建点并核清在途写、全量清理及 `ObserveRelease`、持久关闭事实、可靠通知 outbox、退额触发/重试调度与真实环境恢复。这些均 `not_verified`。本次 ACK 只证明持久受理；stop/restart/异常、NotFound、部分清理与 DELETE ACK 都不构成完成通知。Acc 只拥有供给/规格/绑定/容量和只读释放观察，ENDED 不抹掉 live Binding；Gov 拥有配额、累计退额与异步 Sync。

三方导航从本仓 [accelerator-consumer-contract](../contracts/accelerator-consumer-contract.md) 进入；Gov 的 `docs/contracts/gpu-owner-integration-guide.md` 与上述 Inference handoff 各自维护合同/负责人手册，本文只收敛本 Goal 结果，不复制运行日志或历史 PASS。

## 复现与剩余输入

Fedora 任务环境固定 `GOWORK=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=2`，生成/构建/测试持既有重任务锁。以下均在对应 `source/<repo>` 目录执行，连接配置仅存任务私有目录：

```sh
# Inference：显式执行既有迁移链和 000018，非服务启动路径。
for migration in migrations/*.sql; do
  PGUSER=inference_migrate psql -X -v ON_ERROR_STOP=1 -d inference_wiring -f "$migration"
done
bash scripts/generate-api.sh
sqlc generate  # task pinned v1.31.1
go test ./api/inference/v1 ./internal/biz/gpu ./internal/biz/inference ./internal/service ./internal/data/governance ./internal/data/kubernetes ./cmd/ani-inference-service
go test -count=1 ./internal/data/postgres -run '^TestPostgresManagedGPU'
go test -race -count=1 ./internal/data/postgres -run '^TestPostgresManagedGPU(CreateAtomicReplayAndOriginalContext|ConcurrentCommandHasOneACK|DeleteFirstClosesLateCreate)$'
go vet ./api/inference/v1 ./internal/biz/gpu ./internal/biz/inference ./internal/service ./internal/data/governance ./internal/data/kubernetes ./cmd/ani-inference-service
go build ./...
go mod verify
# Gov：显式八项迁移和 grant/restrict/audit，完整命令见仓外
# setup-final-ci-db.sh；每轮 guard fixture 使用新建受限库。
make verify-ci
go vet ./app/admin/service/internal/service ./app/admin/service/internal/data ./app/admin/service/internal/server ./app/admin/service/cmd/server ./pkg/constants ./tools/localdeps/gow/internal/buf
go build ./app/admin/service/cmd/server ./app/admin/service/cmd/admin
go mod verify
# Joint：编译当前实际 Inf/Acc helpers，再运行正式 Gov→Inf adapter 测试。
go test -c -tags=gpu_joint -ldflags='-s -w' -o "$WIRING_RUN/bin/gov-gpu-contract.test" ./app/admin/service/tests/contracts/gpu
"$WIRING_RUN/bin/gov-gpu-contract.test" -test.run='^TestActualInferenceOwnerWiring$' -test.timeout=180s -test.v
```

公共退额生成固定 Buf 1.60.0 / Go plugin 1.36.11 / gRPC plugin 1.6.0；Gov 其它受管模板按原锁使用 gRPC 1.6.2，protoc 29.3 和对应标准 include。安全扫描结果为 0 个被当前代码调用的漏洞、0 泄漏；仍有 imported/module 层提示，未在本 Goal 扩大依赖升级。

唯一范围内剩余阻塞是 C02 的同步 CREATE/UPDATE interception 路线/范围决定。未获明确决定前，维持 GPU LWS 拒绝，不缩减原验收要求；不公开私有 DSN/key，不触及共享/生产资源。日志、缓存、脚本和任务 worktree 保留恢复，临时运行资源与密钥的实际清理以回执为准。

只有范围内必验项及最终交付回执完成，才可标记本 Goal achieved；负责人后续真实环境验收不由软件接线结果推导。
