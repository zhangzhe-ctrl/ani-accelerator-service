# Governance GPU 适配及无 GPU 软件验收计划

> 合同更新（2026-10-27）：本文中对外 `ObserveRelease` 的要求已由[接口下线说明](../releases/2026-10-27-observe-release-retirement.md)取代（superseded）；其余历史计划与结果保留，不代表本次消费方或环境验收。

批次：`GOV-ACC-V12-01`。编制日期：2026-09-23。状态：**获准执行，R0 进行中；尚无本批软件验收通过结论**。

本轮按用户 GOV-ACC-V12-01 Goal 连续执行 R0～R5，允许修改 Governance 与 Accelerator、在 Fedora 隔离资源显式迁移并运行验收、提交及向指定任务分支非强制推送。先完成数据层整改、业务适配及无 GPU 软件验收，再依据最终实现交付通用 GPU owner 接入与释放文档。Inference 实现不在本批范围，也不是本批完成的前置。

## 执行授权修订（2026-09-23）

本节覆盖旧计划中的只写计划、只改 Governance、禁止修改 Accelerator、手写参数化 SQL 等限制。历史运行记录保留当时事实，追加整改说明，不能改写为历史已使用 sqlc。

- 两仓业务数据禁用 RLS/FORCE RLS，清除残留租户 policy；所有租户关系和读写显式保留 tenant，包括非空约束、复合外键、JSON 一致性、幂等、游标及恢复。全局供给事实不伪造 tenant=0。
- Accelerator 全部生产数据库查询、Governance 完整通用配额账本及本批 GPU 持久化统一采用固定版本 sqlc + pgx。原非 GPU 和 quota_lab 共用整改后的账本，不保留该账本的 Ent 双路径。单次原子操作绑定同一 pgx.Tx。
- SQL 只存版本化查询源；生产 Go 不内嵌 SQL、不接收任意 SQL 字符串。Acc 正式 migrations 为 schema 权威，Gov 保留 Ent/Atlas 权威链，sqlc 消费其结果，不手工复制 schema。
- verify/CI 增加 sqlc compile、重新生成无差异、生产手写 SQL 扫描。真实 PG 使用非 owner、非超级用户、无 BYPASSRLS/DDL/TEMP 的受限运行角色，并执行关键租户谓词 mutation。
- 本地仅读写、传输及 Git；其余生成、格式、依赖、审计、运行及验收均在 ssh fedora 的任务独占目录与缓存。
- 发布目标均为 origin 的 `codex/governance-accelerator-v1.2`；Gov URL 为 `git@github.com:zhangzhe-ctrl/ani-governance.git`，Acc 为 `https://github.com/zhangzhe-ctrl/ani-accelerator-service.git`。不强推、不合并 main、不打 tag、不部署生产。Acc 候选依赖可先发布，Gov 必须消费精确 SHA 正式模块，无兄弟 replace。
- 原 84 项矩阵保持，追加 RLS/TENANT/SQLC 门禁。任一软件必验 fail/not_verified 均不能宣布完成。

执行盘点、完整查询迁移清单、命令证据及恢复入口见 [本批运行手册](../operations/governance-accelerator-v1.2.md)。

配套文件：[验收矩阵](governance-accelerator-v1.2-acceptance.md)。接口依据：[Accelerator 接入合同](../contracts/accelerator-consumer-contract.md)、[规范向量](../contracts/accelerator-vectors.json)、`api/protos/accelerator/`。v1.2 设计中的 SQL、路径和代码片段仅作设计依据，不能代替当前源码或直接执行。

## 1. 完成定义与现状

### 1.1 本批完成什么

1. Governance 能以真实身份访问 Accelerator 的管理、目录、容量、使用和绑定接口，并提供经过授权和脱敏的 BFF。
2. 在既有配额账本及投递系统上完成 GPU 计划冻结、一次占额、完整 charge 传递、创建/删除受理、可靠投递和可信累计释放的公共能力。
3. 完成独立、可重建的 `SyncGpuUsage` 同步；不把同步变成创建、删除或退额的前置。
4. 在 Fedora 通过真实 PostgreSQL、真实进程、mTLS、并发、断点恢复和隔离验收；GPU 正向消费证据使用严格隔离的测试装配。
5. 完成显式迁移、恢复演练、生成与构建边界、定向回归、证据、提交及条件发布。
6. 上述能力及验收冻结后，交付面向 GPU 使用服务的通用接入/释放指南、字段和错误合同、测试向量、准入清单。

完成状态分开记录：`governance_software`、`owner_contract_fixture`、`production_owner_integration`、`gpu_live`、`publication`。前两项须通过全部必验门禁；`production_owner_integration` 和 `gpu_live` 本批固定 `not_verified`；`publication` 按真实结果记录。不能只完成客户端或表结构就关闭本批。

### 1.2 冻结输入

| 仓库 | 本次只读核对基线 | 用途 |
|---|---|---|
| `/home/chabking/workspace/ani-governance` | `main`，`0fbe1a69e49cc23dc7a1696b62f68c34a7c6a48a`；核对时干净 | 配额数据层与 GPU 适配，遵守其 AGENTS.md |
| `/home/chabking/workspace/ani-accelerator-service` | `codex/accelerator-v1.2`，`9f9712198488aaa722dc78aeb444248df25b08ef`；核对时干净 | 固定下游、Proto、摘要向量、生产二进制和测试装配 |

执行 R0 必须重新核对 HEAD、工作区、remote、工具与环境。基线变化时列出影响并重新冻结，不覆盖别人的修改，也不把本计划中的旧行号当成最新位置。

### 1.3 已存在与真实缺口

以下路径相对 Governance 根；均为源码事实，本次没有重新执行历史运行验收。

| 已核对位置 | 现状 | 本批增量 |
|---|---|---|
| `internal/data/quota_ledger_repo.go`（位于 `app/admin/service/`） | 已有 Occupy、Release、CancelUnsent、operation/charge/receipt | 复用事务账本；补完整集合、幂等重放、原子删除受理 |
| `internal/service/quota_dispatch_worker.go:169` | DELETE 回补只取第一笔 charge；失败后仍可能继续投递 | 获取完整集合；失败不调用 owner；保留账本并记录错误 |
| `internal/service/quota_adapter.go` | 编译注册 owner/action；Actor 是拼接字符串；Dispatch 返回 ACK JSON | 结构化 actor、GPU 附件构造和 ACK 验证；保留既有投递系统 |
| `cmd/server/wiring_quota_default.go:15` | 正式 adapter 注册表为空，正式退额 owner map 为 nil | 保持缺少真实 owner 时失败关闭；本批不假注册 Inference |
| `internal/data/quota_admin_repo.go:175` | 非旧三项统一 LAB_ONLY | 精确目录与能力映射，区分 NOT_ENABLED/ENFORCED |
| `api/protos/quota/service/v1/quota_release.proto`、`internal/server/quota_internal_server.go` | 已有累计释放 RPC，证书 DNS SAN 映射 owner | 复用服务和字段/tag；补 GPU 释放策略及可信 owner 注册接缝 |
| `internal/data/ent/schema/quota_operation.go` | 不可变 canonical、原创建引用、业务投递状态已存在 | GPU canonical 作为原操作快照；同步进度单独持久化 |
| `api/protos/identity/service/v1/module.proto` | 无 Accelerator 模块 | 统一登记新模块、API、套餐及权限 |
| `docs/evidence/quota-gpu-local-01/README.md` | 历史实验闭环报告，非正式 GPU 接入 | 仅作回归基线，不抄作本批 pass |

未发现正式 Accelerator 依赖/客户端、GPU plan 受理或 `SyncGpuUsage` worker。当前 `gpu.count` 是实验口径，不能改名或直接作为新的正式计量。

## 2. 范围、职责与 owner 延后规则

### 2.1 责任固定

| 参与者 | 权威职责 | 不承担 |
|---|---|---|
| Governance | 身份入口、套餐与额度、一次占额、不可变命令、投递恢复、可信事实退额、使用投影同步 | GPU 调度/父卡选择、Pod 写入、模型 readiness、凭退出推断删除 |
| Accelerator | 供给规格、设备和实际绑定、容量、投影接收、只读释放观察 | 业务额度、工作负载创建/删除、退款 owner |
| GPU owner（未来的 Inference 或其他服务） | 持久接受业务命令、实际渲染/执行、可信 Pod 身份、创建封闭、全实例清理、可靠释放通知 | 自报配额、绕过 Governance 受理、把 ACK 当业务完成 |
| 本批测试 owner | 独立 PG 持久接受/关闭/通知的合同试验装置 | 正式 Inference、真实 GPU 分配、生产可用 owner |

### 2.2 不依赖真实 Inference 的实施方式

- 复用 `QuotaDispatchAdapter`，增加明确的 GPU 接入用例及 owner 合同接缝；具体服务负责验证自身业务输入和生成稳定业务摘要。owner/action 由编译装配选择，不接受用户传 URL、RPC 名称、任意 owner 或任意 JSON 转发。
- 本批实现计划校验、附件构造、完整 charge 校验、ACK 验证、删除受理和恢复逻辑，使用测试二进制中注册的 adapter 调用独立测试 owner。
- 不发明 `InferenceService/AcceptCreate` 等尚未核对的业务 RPC，不新建用于生产的万能 GPU workload 创建接口。通用用例由未来真实业务 BFF 调用；本批测试入口仅存在于 `_test.go` 装配。
- 正式构建中，缺少真实 owner adapter/证书注册时，新业务创建必须在 Resolve/占额前拒绝，正式 GPU 目录显示 `NOT_ENABLED`。管理与只读 BFF 可以独立启用。测试成功不得使正式环境变为 `ENFORCED`。
- 恢复时 adapter 缺失不能删除 operation/charge 或自动退额；既有操作保留，明确报告并退避。治理暂停只阻止新受理，可信 owner 的释放通知不因套餐到期或新建能力关闭而停收。

### 2.3 当前单 owner 边界

冻结的 Accelerator 在 ref 校验、使用查询和 ObserveRelease 中固定支持 `ani-inference`，provider 使用受管 owner 验证公钥。这不是任意 owner 的配置化注册系统。

本批允许整改 Accelerator 数据层、实际合同/鉴权/持久化及联调缺陷，不把测试 owner 改成新的正式身份绕过校验。隔离测试可以使用专用测试 CA 下的合同身份 `ani-inference`，证据必须标为测试身份。通用指南明确当前单 owner 限制。第二个真实服务接入需另批完成 owner 身份/验证密钥/查询作用域扩展及 Governance 注册。

### 2.4 明确排除

不改 Inference/其他兄弟仓库业务，不部署生产，不安装 GPU 驱动/Volcano/HAMi/Kubernetes，不引入预留/Consumer 强注册、不写第二套业务 dispatch，不重构全平台 IAM/证书/配额，也不实现前端页面、自动伸缩、多 GPU 容器或混合整卡共享计费。除任务隔离资源外，不接触共享服务和数据库。

## 3. Governance 实现合同

### 3.1 目录、计量与执行能力

- 新增 `gpu.physical.count`（unit=`gpu`，CONCURRENT）和 `gpu.shared_memory_mib`（unit=`MiB`，CONCURRENT），使用独立版本数据迁移；不改历史实验目录脚本，不默认给所有套餐额度。
- 保留现有 enum 数值；计划增量为 `ENFORCED=3`、`NOT_ENABLED=4`。旧三项维持 LEGACY_CONFIG_ONLY；`gpu.count` 仅 LAB_ONLY；未登记编码不可因“非旧项”自动变 LAB_ONLY。
- `ENFORCED` 仅表示受控 owner/action 的正式占额执行已装配并启用，不表示硬件可用、容量充足或真实 GPU 验收通过。当前下游短时故障用可用性错误表达，不能关闭配额检查。
- 本批没有生产 owner adapter，因此默认两个正式 GPU code 均 NOT_ENABLED。能力查询须包含适用 owner/action，不能因一个 owner 注册而给所有业务入口放行。
- 共享数量为 `replicas × devices_per_replica × shared_memory_mib`；整卡为 `replicas × devices_per_replica`。首期每副本一个 GPU 容器、每容器一物理设备，副本范围按冻结 Accelerator 合同 1～16。
- F 是 provider 编码粒度，不是 Governance 配额单位。共享按 MiB 扣额，整卡按张数；两种模式互斥，不双计。所有乘加检查 int64 溢出，非法 shape/粒度在占额前失败。
- 当前每个合法单模式 GPU plan 对应一个 GPU quota code；“完整 charge 向量”同时要求原业务操作所有维度完整、GPU 子集精确完整，不得为凑多项测试给同一计划同时扣共享和整卡。

### 3.2 授权、依赖与摘要

- 固定消费 Accelerator Git SHA 对应的 Go 模块版本；在 Fedora 用 `GOWORK=off`、任务缓存从已确认上游获取，记录解析版本/校验和。禁止兄弟目录 `replace`、导入对方 `internal/`、自造 file-GOPROXY 代替正式消费。
- acc-c14n-v1 在 Governance 按公开合同实现，逐字匹配固定向量；不直接 hash protojson，不仅比较两个同源错误实现的结果。
- 使用现有可信 Principal 和持久 tenant→resource UUID 映射。actor_type/actor_id 分列；不拆拼接 Actor 字符串猜身份，不把数字 tenant 0 传为下游租户。
- Governance→Accelerator 使用精确 URI SAN 身份及 TLS 1.3；12个 Admin 和8个租户查询/解析 RPC 正确填充 `x-ani-action`、actor、必要 tenant，且与对应 protobuf context 一致。平台接口使用明确平台 grant 和 cluster 范围，不附加虚构 tenant。
- 上述20个委托 RPC 的静态 actor/action/tenant/cluster grant 是额外必需条件。Governance 角色授权不能代替下游 grant；本批提供受控配置和拒绝案例，不新增运行时自授权 RPC。
- `SyncGpuUsage` 不含上述用户委托 context：只验证精确 Governance 服务身份和持久原 ref/plan/账本事实。用户权限撤销、套餐到期或新建停用不能阻止已受理操作的后台投影收敛。`ObserveRelease` 使用直接 owner 身份及 tenant，不套用 Governance 的委托 grant。
- owner→Governance 现有退额接口按精确受管 DNS SAN 映射 owner；它与 Accelerator 的 URI SAN 合同不同。保留当前映射规则：至少命中一个已登记 owner，不能同时命中不同 owner；同 owner 的多个 SAN 不等于歧义，不把代码注释当成“所有多 SAN 均拒绝”。冻结双向身份矩阵及证书验收，不假设同一张证书必然兼容，也不借本批广泛更换证书体系。
- 新 BFF 首期仅接受用户 JWT；本批不扩展 AK/SK 的业务路由白名单。已有 AK/SK/Network 能力保持并做定向回归；若后续开放 GPU AK/SK，另按真实 query/body/重放合同验收。

### 3.3 不可变创建受理

顺序固定为：可信身份及当前权限、可信业务 owner/action 绑定 → 按当前作用域查原幂等键 → 校验原请求摘要并返回已存结果 → 新请求才检查 owner/action 执行能力并做业务校验/Resolve → 验证 plan/计算 charge → 单 PG 事务 Occupy → 原业务 worker 投递。不能因为 adapter 临时撤下或新建能力关闭而拒绝有权限的历史同内容重放；后台恢复缺 adapter 时仍保留账本并报错。

1. request_hash 基于可信 tenant/actor/owner/action 和规范业务输入，包含会改变业务的全部字段；不包含解析时间、新 resource UUID、实时容量或原 plan 的重新解析结果。
2. 有权限的同内容重放复用原 operation/resource/plan/charge，不再访问 Accelerator；不能因规格关闭、F 改变或当前容量不足而重新扣额。权限已经撤回时不泄漏原记录；套餐到期也不得成为匿名或越权重放后门。
3. 新请求 Resolve 在数据库事务外。保存版本化 GPU canonical：原规范业务输入、原 GPU 请求、完整 plan、plan digest、business payload digest、计量版本、可信 ref/actor。不得存 Bearer、证书私钥、任意未校验 workload 文本。
4. 并发相同幂等键仍在 Occupy 事务内重新判定；最终胜出的快照成为唯一原计划，失败者返回已存结果或冲突，不能用自己另一次 Resolve 的计划覆盖。
5. 把重复受理和新占额政策检查分开；新占额以 PG 事务中的统一数据库时钟判断到期，边界相等视为到期。保留 tenant/plan/账户锁与原子向量扣额。
6. 容量零/未知是参考状态；合法 plan、有额度且 owner 可用时可受理等待。配置漂移、供给 CLOSED 或未验证导致 Resolve 失败则拒绝新请求。
7. charge 引用按 quota_code/charge_id 匹配，不依赖数组位置。原操作完整 charge 集合与附件中的完整 GPU 子集分别校验；整卡和共享不混淆。
8. worker 只在具体 adapter 验证 `DurableOwnerAck.operation_id/resource_id/accepted` 后记录 ACKED。ACKED 不等于 workload Ready，不等于删除完成。

### 3.4 删除、未发撤销与释放

- 提供一个内部 `AcceptGpuDelete` 用例，由未来真实 owner BFF 调用；请求使用原资源、可信主体及 DELETE 幂等键。原 CREATE、完整冻结 plan 和 charges 从本库读取，不信任调用者重传计量。
- 增加 `sys_gpu_delete_acceptances` 保存不可变 DELETE 幂等结果。本表不带执行 worker，不成为第二份 dispatch。键包含 tenant/actor/action/idempotency_key，所有引用保留 tenant。
- 同一 PG 事务锁住原 CREATE 并和 worker 领取串行化。仅 `QUEUED && attempt_count==0` 可原子取消、退还完整原集合并记录 `CANCELED_BEFORE_DISPATCH`；不伪造 owner ACK。
- 原 CREATE 已是 `CANCELED_UNSENT` 时，新的 DELETE key 仍记录 `CANCELED_BEFORE_DISPATCH`，核对原退款完成后返回；不再退一次、不建立发往 owner 的 DELETE。若状态和账本不一致则保留错误，不把新请求当修复后门。
- 已有任何发送尝试、UNKNOWN 或 ACKED 均走 owner 关闭协议：同事务保存 DELETE acceptance 和既有 quota DELETE operation，结果为 `QUEUED_FOR_OWNER`。不新建 charge；原集合读取/校验失败则不投递。
- DELETE 重放返回原受理结果；不同 DELETE key 仍指向同一原 CREATE/资源，由 owner 封闭语义保证不会迟到重建或重复退款。
- `ReportQuotaRelease` 保留已有服务、tag、int64 累计语义。对包含 GPU item 的通知，按持久原操作类型核验完整原 GPU 子集，charge/code/数量/ref/owner 一致，GPU 部分只接受整实例完成后的全量累计值。混合业务操作可以先单独通知非 GPU charge 的合法累计部分释放；这不结束 GPU 投影，也不强迫提前退还 GPU。
- GPU owner 的全量通知必须能关联 Governance 已持久的明确 DELETE acceptance/operation 及原 CREATE/资源；不要求 DELETE 已 ACKED，允许通知早于 ACK 回写。不能只凭 reason 字符串推断实例删除。未曾分配但已被 owner 封闭的分支也需此关联；纯本地未发撤销走本地事务。
- 现有释放报文没有 GpuUsageRef/DELETE ID/清理证明。Governance 从自身账本和 DELETE 受理记录做上述关联，不改变既有 tag，不把仅用于审计的 resource_refs 升级为授权或清理证据。实际范围完整、未来不再创建和清理真实性依赖可信 owner 完成事实，本批 fixture 不能证明真实工作负载已经删除。
- 身份来自验证过的 owner 证书，租户来自原 charge。超量、漏项、重复、异 owner、异 tenant、异原 CREATE、同事件异内容均拒绝且不改变余额；重复相同通知只退一次。
- “允许本地撤销”与“owner 报告未曾分配但已封闭”分开记录。后者属于 owner 合同，不要求用空 Pod 集合伪造 ObserveRelease 成功。
- 普通退出、失败、重启、删除 ACK、API NotFound、部分 Pod 清理、超时和失联均不能退款。Governance 不把 ObserveRelease 当作自动退款权威；该方法由 owner 调用，且只证明指定范围。
- tenant 到期/降额/新建能力关闭不能阻止可信旧操作退款；公开删除若被既有全局到期策略限制，不擅改全局闸门，以既有显式运维权限处理并记录边界。

### 3.5 使用投影与恢复

- 新增 `sys_gpu_usage_sync`，只存派生 payload/摘要、desired/acked revision、独立重试/租约/错误。仍以不可变原 CREATE 和完整 charge/receipt 为可靠来源。
- 不在 Occupy/Release 事务中要求同步表写入，不引入同步 outbox 强依赖；占额刚提交、还没有同步行就崩溃，也必须能补齐。
- 空 charge、缺少 GPU 子集、非法 canonical、摘要或计量不符必须保留错误，不能通过空集合 all 条件产生终态。
- 原 CREATE 安全未发撤销且原退款完整 → revision 2/CANCELED_UNSENT；原 GPU 子集全部释放且符合可信终结事实 → revision 2/OWNER_RESOURCE_RELEASED；否则 revision 1。不能因某一 receipt、部分 GPU 退款或 DELETE ACK 派生 ENDED。
- 必须核对原业务 charge 全集合完整性，并单独判定 GPU 子集的终结；GPU ENDED 不等待无关非 GPU charge 归零，也不由非 GPU 释放触发。GPU 终结仍要求明确删除和完整实例 GPU 清理合同成立，不能用计量子集概念允许运行中退 GPU。
- 两个 revision 都携带同一原 ref、完整原 plan 和原 CREATE 持久 created_at；source_fact_ref 使用合同确定性键。扫描时间、重试次数及 receipt 到达顺序不得改变同一 revision 的内容。
- 周期稳定分页扫描所有适用原 CREATE，并在一轮结束后重新开始；增量唤醒仅作优化。必须发现很久以前的操作后来释放，不能只按 created_at 水位扫描新增。
- 网络调用在同步领取事务外；回写以 generation 和本次 revision CAS，晚到 revision 1 ACK 不得确认尚未发送的 revision 2。两进程竞争不重复改变额度。
- 同 revision 异摘要为不可自动覆盖的冲突；保存证据并暂停该条。超时/Unavailable 重试原 payload。同步故障不修改业务 dispatch_state/attempt_count，不调用 owner 创建/删除，不改额度。

## 4. BFF、权限与错误合同

计划新增 `ACCELERATOR=13` 模块，保留所有旧枚举值；R0 若最新统一登记已占用 13，先记录冲突并在同一登记中选定未占用值，不能静默重编号。只增加 Accelerator 模块，不提前新增 Inference 模块。

下表是本批建议冻结的完整路由集。执行 R1 将其逐项登记到 Governance 的 `docs/interface-integration-register.md`，复用已有条目，不建立平行登记。新请求保留 `{data:{...}}` 包装；新增字段显式固定 JSON 名称，采用 snake_case；已有 QuotaAccountView 等消息维持原 JSON 合同。64 位数量按对应 Proto 使用十进制字符串。

| 方法/路径 | 下游/用途 | 权限 |
|---|---|---|
| GET `/admin/v1/accelerator/clusters` | ListClusters | `accelerator:cluster:list` |
| POST `/admin/v1/accelerator/clusters` | RegisterCluster | `accelerator:cluster:register` |
| GET `/admin/v1/accelerator/pools` | ListPools | `accelerator:pool:list` |
| POST `/admin/v1/accelerator/pools` | CreatePool | `accelerator:pool:create` |
| GET `/admin/v1/accelerator/supply-groups` | ListSupplyGroups | `accelerator:supply-group:list` |
| POST `/admin/v1/accelerator/supply-groups:adopt` | AdoptSupplyGroup | `accelerator:supply-group:adopt` |
| POST `/admin/v1/accelerator/supply-groups/{group_id}:set-admission` | SetAdmission | `accelerator:supply-group:set-admission` |
| GET `/admin/v1/accelerator/profiles` | AdminListProfiles | `accelerator:profile:admin-list` |
| POST `/admin/v1/accelerator/profiles` | PublishProfile | `accelerator:profile:publish` |
| GET `/admin/v1/accelerator/devices` | ListDevices | `accelerator:device:list` |
| GET `/admin/v1/accelerator/bindings` | AdminListBindings | `accelerator:binding:admin-list` |
| GET `/admin/v1/accelerator/capacity` | AdminGetCapacity | `accelerator:capacity:admin-get` |
| GET `/api/v1/accelerator/profiles` | ListProfiles | `accelerator:profile:list` |
| GET `/api/v1/accelerator/profiles/{profile_id}` | GetProfile，必须具体 version | `accelerator:profile:get` |
| GET `/api/v1/accelerator/capacity` | GetCapacity，具体 profile/version | `accelerator:capacity:get` |
| POST `/api/v1/accelerator/admission-preview` | ResolveGpuRequest + CheckGpuFit + 自身额度 | `accelerator:admission:preview` |
| GET `/api/v1/accelerator/usages` | ListGpuUsages | `accelerator:usage:list` |
| GET `/api/v1/accelerator/usages/{owner_service}/{resource_id}` | 本租户原 CREATE 构造 ref → GetGpuUsage | `accelerator:usage:get` |
| GET `/api/v1/accelerator/usages/{owner_service}/{resource_id}/bindings` | 同上 → ListBindings | `accelerator:binding:list` |
| GET `/api/v1/me/quota-accounts` | 既有账本，Principal 自身 tenant | `quota:account:self-read` |

前 12 项为明确平台权限，不借 tenant=0 获得下游任意访问。租户 Accelerator 路由要求租户状态、ACCELERATOR 套餐模块、角色权限和下游 grant；自身额度查询复用通用配额权限，不强制购买 GPU 模块。不同层拒绝分别验收。

- 所有写路由的 idempotency_key/expected_version 按真实下游消息映射；入口禁止主体/tenant/connection 凭据注入。只接受已配置 connection_ref，不允许任意地址。
- 下游游标作为 opaque token 传递，新增 cursor 接口使用 page_size/page_token；不把 token 伪装成 offset/page number。不伪造 total，响应提供真实 next_page_token。既有分页接口不改风格。
- Preview 不写 operation、charge 或占位，不返回可供调用方绕过校验的“授权计划”。输出 requested_quota_items、quota_checks、gpu_fit、容量 complete/known、owner execution readiness；CPU/网络/存储明确 NOT_CHECKED。NOT_ENABLED 时仍可做只读诊断，但响应不声称可正式提交。
- 租户 DTO 不透传完整 plan/runtime、source_fact_ref、证书配置、物理 UUID、Pod 身份和原始证据。使用投影不代表业务 Ready，缺投影不代表资源不存在；实际未关联占用仅在授权管理侧可见。
- 未授权 401/403、跨租户与不存在统一 404；非法参数 400；幂等/版本冲突 409；已知前置条件失败 412；依赖不可用 503；已连接调用超时 504。保留稳定机器 reason，不泄漏凭据/原始下游内容；未知 gRPC 错误不得返回假成功。
- SyncGpuUsage 仅内部 worker 调用，ObserveRelease 仅 owner 直连，二者均不新增公网 BFF。全部 21 个 Governance 可调用的 Accelerator RPC 要有使用定位：20 个由上述 BFF/组合调用覆盖，1 个由同步 worker 覆盖。
- API 同步先 dry-run，再显式增量同步；目录存在不等于角色已授权。同步模块枚举、Ent/schema CHECK、API 分类、套餐关联、策略刷新、OpenAPI；不自动给全部租户角色授予新权限。

## 5. 数据、文件与迁移边界

### 5.1 新增数据

| 数据 | 保存内容 | 约束 |
|---|---|---|
| 原 quota operation canonical | 原业务输入、GPU plan/计量快照 | 不可变；原请求 hash 与 plan hash 分离；不新增余额副本 |
| `sys_gpu_usage_sync` | 可重建关联进度、冻结 payload、租约与错误 | tenant_id 非空；与原 CREATE 的 tenant 复合 FK；revision 单调及范围 CHECK |
| `sys_gpu_delete_acceptances` | DELETE 原始幂等结果及 dispatch 引用 | tenant/actor/action/key 唯一；原 CREATE/dispatch tenant 复合 FK；结果与引用一致 |
| 正式 quota 目录和模块/API 增量 | 两个 code、模块及可审查权限条目 | 版本数据脚本；重复执行无变更或明确一致性检查；不授予默认额度 |

遵循现有 `tenant_id bigint`、`operation_id varchar` 等真实类型；不照抄设计 SQL 的假定类型。全量 relation/query/background path 保持 tenant，不用跨租户通用 JSON 查询替代约束。全局 quota 目录是非租户元数据，不能误加伪 tenant。

### 5.2 预计修改位置（拟新增名称不是现有能力）

- `go.mod/go.sum`：固定 Accelerator 公共 API 模块依赖。
- `api/protos/`、定向 Buf 模板及生成文件：BFF/DTO、模块和 enforcement；不复制另一份下游 Proto 所有权。
- `internal/data/accelerator_client.go`、GPU sync/delete repo：下游通信与 PG；`internal/service/accelerator_service.go`、GPU 受理/同步用例：可信入口和业务编排。
- 既有 quota ledger/adapter/dispatch/internal release server：完整集合、结构化身份、ACK/释放策略和事务 helper；不删除旧实验支持。
- `ent/schema/`、生成代码、`app/admin/service/schema.sql`、Atlas `migrations/`：由固定工具生成；不手改生成文件。
- `cmd/server/wiring_*.go`、配置样例：生产默认关闭缺少 owner 的入口；worker Start/Stop 和清理按现有生命周期装配。
- `scripts/generate-accelerator-slice.sh`、`scripts/accelerator-acceptance/`、`tests`：拟新增定向生成/验收与隔离测试装配；生产镜像不包含测试 owner、注入端口或成功证明私钥。
- `docs/interface-integration-register.md`、本批执行报告、运行手册、后续通用指南。

以上 `internal/`、`cmd/` 均位于 Governance 的 `app/admin/service/` 下。Accelerator 必须完成全量查询迁移和必要合同修复，不能削弱验证掩盖不匹配。

### 5.3 迁移与回退

1. Fedora 建立旧版本真实 PG fixture，包含旧 quota/lab/Network/套餐/租户数据。先备份并记录数据摘要和恢复命令。
2. Ent/schema 生成 → Atlas 差异审查 → 显式迁移；运行角色不能 DDL/TEMP，启动保持 migrate=false。失败迁移必须完整回滚。
3. 验证旧数据、旧 code/tag、FK、索引、重复数据脚本及 schema 无漂移；不把“启动成功”视为兼容证明。
4. 另库还原 populated dump，逐表比对原操作/charge/receipt/两张新增表与租户关系，再恢复同步/业务投递；核对无重复扣退和终态倒退。
5. 软件回退先停止新 GPU 受理并停止新版本 worker，再切换明确兼容版本。若旧二进制不能读取新增模块/枚举或 GPU canonical，禁止直接回滚到旧版本；保留新版本恢复工具或在隔离环境恢复迁移前备份。
6. 存在新 GPU 业务写入后，不自动 DOWN/drop 新表、不擦掉未完成操作。真实环境恢复备份涉及数据损失，应另获明确授权；本批只在任务隔离库演练。

## 6. Fedora 执行及验收拓扑

本地仅阅读、编辑、文件传输和 Git。生成、格式化、依赖下载、构建、测试、审计、PostgreSQL、服务及证书生成全部在 `fedora`；远端失败不得回退本地。

R0 冻结 Fedora hostname/系统/Go/容器工具/可用内存与磁盘。只用任务独占目录、锁、回环端口、容器标签、测试 CA、PG 角色、缓存。默认 `GOWORK=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=2`，依据实测资源固定限制；不安装宿主包、不停共享资源、不切换共享 Kubernetes context。

### 6.1 三层实测分别记账

| 层 | 实际进程/来源 | 能证明 | 不证明 |
|---|---|---|---|
| A：正式构建边界 | 正式 Governance + 固定 Accelerator 生产二进制 + 独立 PG + 无消费证明的受控只读 provider 来源 | BFF/身份/目录、无证据时 CLOSED/NOT_VERIFIED、无 owner 时拒绝占额；测试入口不存在 | 真实采样、正向硬件验证、正式 owner 接入 |
| B：软件合同链 | Governance 同生产业务代码的测试进程 + 固定 Accelerator `tests/contract` 测试进程/下述仅测试叠加源 + 独立 owner 测试进程，各自真实 PG | 合法 Resolve→计划→占额→持久接受→同步→关闭/通知→退额、故障恢复 | GPU 真实分配、真实 Inference、未修改生产外部证据端口 |
| C：provider reader 回放 | 固定 Accelerator 生产 reader + TLS HTTP/Unix gRPC 构造来源 + 真 PG | 格式、归一化、未知/冲突、作用域、绑定与释放软件语义 | 真实 GPU/插件/推理或现场采样 |

A 层需构造可到达“消费证明缺失”检查的合法 cluster/Node/baseline 来源，通过生产 reader 与真实管理 RPC 登记。该只读来源仍是构造样本，不是硬件实采；不提供有效硬件消费签名。Publish 必须真正到达证明门槛并失败，不创建 profile、不变 VERIFIED；不能用空配置的 INVALID_CONNECTION_REF 代替。Publish 被拒后没有已发布 profile，OPEN 的 PROFILE_NOT_ACTIVE 和 Resolve 的不存在拒绝按真实前置错误记录，不能另算二者的消费证明复核通过。已有 profile 的 OPEN/Resolve 证明失效场景由 B/C 的已标注测试装配或独立合同测试分别验证。无法准备 Publish 的有效前置输入时对应验收不可写pass。

B 层优先复用固定 Accelerator 的 `TestContractServerProcess`：Fedora 编译现有测试二进制，以 `-test.run=^TestContractServerProcess$` 和其既有 `ACC_CONTRACT_*` 参数启动。该装配使用真实 service/biz/PG/mTLS，仅 controlledProvider/controlledEvidence 在 `_test.go` 内替换。通过真实管理 RPC 初始化，不直接 UPDATE VERIFIED/OPEN。证据目录必须记录这些替换；若冻结版本入口变化，只调整测试编排，不给生产增加 skipVerification。

原 helper 的 Collect 返回空观察，不能靠空列表完成绑定和脱敏验收。本批按新授权在 Accelerator 自有 `tests/contract/` 版本化 `_test.go` 受控观察 helper，由同模块测试代码调用生产 SaveObservation/关联路径，载入与该次原 ref 匹配、含明确敏感字段的观察 fixture，再由真实 Governance BFF/mTLS 查询和同步验证。测试 helper 不进入正式二进制，不增加生产注入 RPC。

叠加源只能提供任务私有文件/进程控制，不添加生产 RPC，不直接更新供给为 VERIFIED/OPEN；原始 ref、fixture摘要、叠加文件摘要、基础SHA、完整测试源码manifest和测试二进制hash均入证据。报告分别写“原版测试helper”和“叠加测试装配”，不能把后者标成未经改动的上游二进制。该路径证明观察fixture→持久绑定→真实BFF映射/同步，不证明GPU采集全链；独立上游合同/provider测试承担采集与观察规则回归。

Governance 测试进程同样仅在 `_test.go` 中注册测试 owner/故障控制入口；测试 owner 通过独立 PG 事务提供持久接受、关闭墓碑、可靠 release outbox。其测试 RPC 命名明确 lab/test，只在验收命令使用，不能写入正式业务 API/OpenAPI/镜像。

每层使用不同 DSN/证书与明确标签。B/C 的密钥和合成证明绝不能挂入 A 层；除专用夹具库外不得产生伪造 VERIFIED/OPEN。所有日志和报告明确 `fixture` 与 `hardware_observed=false`；有测试叠加源还要列 overlay manifest。新增观察fixture装配必须真实完成并通过审查，不能将C层独立通过拼成不存在的BFF全链证据。

### 6.2 生成、验证和证据入口

执行时创建本批脚本与 `make verify-gpu`（**拟新增，当前并不存在**），统一封装固定版本定向生成无漂移、格式/vet/受影响包测试、正式与 quota_lab 构建、真实 PG/进程/mTLS、受影响 race、依赖与敏感信息审计。禁止运行含 `@latest` 的通用工具安装目标，禁止无关的广泛 gow api/Wire 或依赖升级。

Accelerator 使用扩展后的真实 `make verify`、`make test-provider` 及本批合同测试，包含全量 sqlc、no-RLS、显式隔离、mutation、恢复及必要审计。Governance 新链路的本批必验不能被 Accelerator 历史 pass 替代。

证据至少包含两仓 SHA/源码 manifest、工具和镜像 digest、测试装配类型、命令、开始结束时间、退出码、场景结果、数据库前后断言、进程 PID/重启点、证书身份摘要、清理及恢复入口；不收集 DSN/私钥/token 明文。`pass/fail/not_verified` 独立，失败原日志保留，修复后用新目录重跑。

## 7. 连续执行阶段与退出条件

| 阶段 | 必做工作 | 退出条件 |
|---|---|---|
| R0 输入与环境 | 固定源码/模块/合同/权限编号、Fedora/PG资源与版本；检查工作区/remote；建证据清单 | BASE 组通过；明确无 GPU/真实 owner；不依赖 Inference 提供实现 |
| R1 数据层整改 | Acc 全部查询、Gov 完整通用账本迁入 pgx/sqlc；唯一 schema、no-RLS、显式 tenant、生成及扫描门禁 | RLS/TENANT/SQLC 与非 GPU 兼容验证通过 |
| R2 业务适配与持久化 | 目录/enforcement、模块/BFF/授权、摘要计量、冻结计划受理、完整 charge、原子 DELETE、GPU release 策略、同步表及扫描/CAS；显式迁移 | CAT/AUTH/API/CAN/DB/CREATE/DELETE/RELEASE/SYNC 对应门禁通过 |
| R3 装配与软件链 | 生产依赖/worker生命周期；三层隔离拓扑；真实进程和测试 owner；完整 BFF正负向 | PROC/BOUND 通过；不能把测试 adapter 放入正式 registry |
| R4 故障、并发、恢复 | ACK丢失、杀进程、双worker、旧代次、政策竞争、dump restore、provider 回放、定向回归 | 矩阵所有软件必验通过；失败修复后复跑对应及受影响门禁 |
| R5 文档与交付 | 先冻结实现/验收，再编通用 owner 指南；生成对齐、审计、最终提交验证、条件推送/CI、资源清理 | DOC/SHIP通过或条件项如实记录；GPU/正式owner仍not_verified |

R1～R4 可以按依赖交叉推进，但不能跳过真实持久化/恢复只交付接口。遇到真实 blocker，保留命令/错误/影响 ID 并继续独立工作；缺少 GPU/Inference 本身不阻断本批。

需要停止受影响部分的情况：无法连接 Fedora、隔离资源/权限不足、固定模块无法可靠取得、源合同和接口产生不可兼容冲突、生成器产生无法解释的跨域修改、必须修改兄弟服务生产代码才能继续。不得猜身份、削弱鉴权/验证或伪造 pass。先完成所有不依赖该 blocker 的范围，最终列明剩余项。

## 8. 适配完成后交付的通用 GPU owner 文档

本轮只冻结该交付物要求，**不提前把待实现接口写成可用指南**。R5 以最终受测代码和 Proto 编写 `docs/contracts/gpu-owner-integration-guide.md`（建议落在 Governance 仓库并引用固定 Accelerator 合同），供 Inference 及后续 GPU 使用服务复用；不要求先完成任一真实 owner。

文档必须包含：

1. 版本/能力表：Governance/Accelerator 最低 SHA、当前支持 owner、未开放能力、软件/硬件证据边界。
2. 新 owner 准入：服务标识、真实业务 API、持久接受、身份 SAN、密钥与轮换、Gov adapter/退款映射、Accelerator owner/公钥/作用域扩展、正式 module/API登记。
3. 身份及字段：tenant/actor/resource/create_operation/delete_operation/charge 的来源和不可变性；服务身份与资源 owner 分开；URI SAN 和 DNS SAN 分别说明。
4. GPU 请求/计划/计量：整卡与共享、MiB/F、replica限制、规范摘要、完整 GPU 子集和业务总集合；固定通过/拒绝向量与64位JSON表示。
5. 创建流程：业务校验→冻结计划→一次占额→可靠投递→owner持久 ACK→异步执行；禁止重复占额、重新解析旧计划和强前置 RegisterConsumer。
6. owner 渲染责任：GPU init/sidecar/额外副本防绕过，静态受管选择器，可信 Pod UID/签名，API 写入未知时的对账。
7. 删除与关闭：DELETE先到、重复/并发DELETE、创建封闭、墓碑、全部历史/替换Pod集合、在途副作用、never-created 证明，退出/重启与实例删除的区别。
8. ObserveRelease：直接调用身份、完整实际范围、STILL_PRESENT/UNKNOWN/ALLOCATION_RELEASED；只证明范围解除，不证明完整清理/未来不再创建，不自动退款。
9. ReportQuotaRelease：复用既有 Proto，原 CREATE+完整原 GPU charge，全量累计、确定性事件/幂等、可信原因、outbox/ACK丢失重试；非GPU累计释放规则单独说明。
10. 查询与显示：使用关联/绑定/容量/业务状态来源，缺投影/不完整容量，租户脱敏，不把 ENDED 当设备空闲。
11. 失败恢复表：每个提交/RPC/ACK边界的权威存储、重试责任、相同ID/内容、错误分类、人工恢复入口。
12. 接入验收模板：可重用无GPU合同用例、真实owner专属用例和GPU-LIVE门禁；不可用 sleep/CPU/mock 证明硬件。

示例只使用公开附件和稳定公共协议。没有读取真实业务 API 前，用带标注的业务占位说明其接缝，不给出看似可以调用的虚构 RPC 或部署命令。文档必须让另一个团队明确知道“可复用什么、自己必须实现什么、当前还缺什么”，不能声明任意服务已接通。

## 9. 发布、清理和最终报告

- 本 Goal 已授权提交/非强制推送，分支 `codex/governance-accelerator-v1.2`；提交前在 Fedora 对精确候选运行本批完整门禁，生成结果逐文件与本地 index 对齐。
- 既有批准执行范围若包含发布，使用 R0 确认的准确 remote/分支，非强制推送；缺目标仅在发布前询问。没有明确授权不合并 main、不打发布 tag、不部署生产。
- 最终提交 SHA 要有 Fedora 定向 verify/audit 及生成无漂移证据；如 CI 可用，查精确 SHA 的真实结果和覆盖范围，CI绿不替代本矩阵。
- 停止本任务进程，删除本任务容器/角色/临时证书/缓存，保留脱敏日志、恢复备份和版本锁；先确认路径/标签归属，不清共享缓存或服务。
- 报告实现范围、所有必验ID结果、production owner未接入、GPU未验、代码/模块SHA、迁移与恢复证据、发布与CI、清理、通用指南位置。测试成功不是生产切换完成。

本批预期结论应为：**Governance GPU 公共接入能力及无 GPU 软件合同通过；通用 owner 接入指南交付；真实 owner 和 GPU 数据平面待各自接入验收。**
