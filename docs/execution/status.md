# Accelerator 执行状态

## Node Agent / 初始化 B/C（2026-10-22，已交付可用部分，整体未完成）

### 主链与当前边界

- **真实共享主链 PASS**：Agent 事实 → 已有 ACTIVE Profile/OPEN Group → 显式启用规格 → 租户选择 → 单 T4 CUDA（3840MiB/25%，含超显存拒绝）→ Stop/Start → 原 DELETE → 正向物理释放 → 容量恢复 → Gov 原 CREATE 账本仅退一次。不是只验证 Pod Ready/HTTP 200，也未绕过认证、持久化、冻结执行或原退款 worker。
- 原实例 `d816b7f8-e090-4fc5-ab94-4968dc945cee`；CREATE `d0458241-e29a-4e4c-ae6f-a36dd19767c5`；DELETE `f4e76478-231d-4271-a8ef-2fee71693865`。原 charge `a7cd3905-bdeb-4719-a5cd-c7f15b07aba9` 的 3840 已全额释放，release_event receipt_count=1，Resource v4 desired/observed=deleted、清理及退额均完成。相同 DELETE key 重放 PASS。证据：Fedora 根下 `c4-preflight/delete-quarter-a.after-coverage.log`（退出 0）、`live-driver-evidence/quarter-a/state.json`；不要把日志里的原 CREATE operation_id 当 DELETE ID。
- Stop `c2b1e166-fd08-4139-a48f-d1a319d6fbfa`、Start `99f242c6-c2de-42b5-9457-47800dfdf019` 保持原冻结执行摘要 `8d2a75e5cc3268d55f7dff67df1b42487d0e3c04e3162d277dad1b10571b2366`、原 charge 与占用；沿用原流程补观测，没有另建实例或重复 Stop/Start。证据 `stop-start-resume-running.after-forward.log`（退出 0）。
- 真实初始化只执行 **pool-only** operation `c2b31eec-c9e3-4980-bf2d-6add03d2f3be`，SUCCEEDED/v6；新 pool `59574e75-93a8-4a3b-a0d0-fbb66c0bbd9c` 保留。不能据此宣称真实两批节点接管、新组自动 OPEN 或全初始化均已硬件验收。
- **当前五个 Gov BFF 不可用（404）**：其正式源码/联合链/历史线上五接口验收已通过；外部随后发布 main `323eb74`。用户明确要求保留外部发布，本任务未回滚。C3 前端代码由专人负责，本任务不修改、不冒充浏览器验收。
- **NOT_RUN / 整体阻塞**：当前只有 ani-03 一张 T4，缺第二物理 GPU 节点/第二独立批次；本批当前整卡模式真实验收未运行。未用假节点、多份虚拟容量或旧批次整卡结果计本批 PASS，未自动切插件模式。所缺硬件/人工安装基线与后续集成发布就绪前，不能称整个 Goal 完成。

### 固定来源与交付

- 固定入口 `ANI-node-agent-initialization-GOAL-v1.2-20261021.md`；主方案 SHA256 `f6668bcbc264d2df6e22f0033ccf38e46d24e678e40d07dcb2353dfa78d0bfa7`。基线 ACC `9d45df6e88bea0d4ed4a0fd1619a0dc96960a762`、Gov `323eb749b8a3d607b26687797b372919052895c7`、Resource `6bff99f1747a495fe4b278844c20ee72b1ce2ffc`。任务树根 `/home/chabking/.codex/worktrees/node-agent-initialization-bc`，未覆盖用户原工作区。
- ACC 代码 `e9dfd62ffef20c09059fe4b0fbf7018d5d462f79`，分支 `codex/node-agent-initialization-bc`，SSH push 退出 0，[PR #3](https://github.com/zhangzhe-ctrl/ani-accelerator-service/pull/3)。本索引随后为单独文档提交，不改变受测运行源。
- Gov `cbfce6e6b8288c080fd285d434e38a4fb8aeb1a4`，同名分支、SSH 推送，[PR #33](https://github.com/zhangzhe-ctrl/ani-governance/pull/33)。独立 Agent 实现 `8f49e36fcf8de2b1546ac8f718ad2728daaf5dd7`，同名分支已 SSH 推送；经用户允许的空 main `d3c93b332d712d35e51cdbd39cd1c0cc493e9909` 建立 [PR #1](https://github.com/zhangzhe-ctrl/ani-node-agent/pull/1)，审查分支 `codex/node-agent-initialization-review` HEAD `c0cb3bb4e8bb37122fa0617f3523109a1a6e6329`，tree 与实现完全相同。三个 PR 均 open、未合并；无实现直推 main、force push 或 bypass。
- Git SSH 远端均为 `git@github.com:zhangzhe-ctrl/<service>.git`，本任务复用已验证 SSH 443/HostKey，不更改全局安全配置。Agent/Gov 正式消费 ACC `v0.0.0-20261021055754-9969638c907a`，Origin.Hash `9969638c907a806e895751ac71740d52f8592954`，GOWORK=off、无 replace；Go 1.26.10。
- P1 保留原 Collect/SaveObservation 事务、worker 与五秒释放预算。最终修复仅让完整、同 Pod/全已知尝试、严格三阶段正向 journal 的不可变采样时间覆盖已闭合历史 gap；普通相同 EXITED 只保留旧覆盖，不伪造刷新。覆盖参与 semantic revision；Running/UNKNOWN、身份/退出变化、开放或更新 gap 仍使旧证明失效。历史 UNKNOWN/gap 不删除，不提高 freshness/超时，不增加公共 Proto 或表。

### T01–T12 与软件门禁

| 验收索引 | 实际结果及边界 |
|---|---|
| T01 身份/轮换/fencing | 软件 PASS：真实 Agent 进程、mTLS、TokenReview 与实际对象绑定的 K8s 外部边界；真实单节点认证入库 PASS，多物理 GPU 节点 NOT_RUN。 |
| T02 durable/replay/ACK | 软件 PASS；真实同 LogID 持久 ACK、重启续传 PASS，未清日志造成功。 |
| T03 三源/尝试/UID/env | 软件 PASS；实际 NVML/PodResources/CRI 已新鲜，保留未知尝试，sandbox DELETED 不伪造 container attempt。 |
| T04 断连/旧快照/Running | 软件 PASS：断连不假造采集 gap，旧快照不刷新事实，新 Running/UNKNOWN 胜过旧退出。 |
| T05 原子/CAS/持续上报 | 修订后的双节点持续正常快照、真实变更拒绝旧 revision、合法清理五秒内完成软件 PASS；原真实 DELETE 最终由既有 worker 自动完成。 |
| T06 用途完整性 | 软件 PASS：NVML/配置故障不额外否决已有充分物理释放证明；真实正向 journal 与物理释放 PASS。 |
| T07 初始化持久/恢复 | 真实 PG + K8s 外部边界的软件 PASS：幂等、部分成功/重启/Retry、非法引用图；实际 pool-only SUCCEEDED。 |
| T08 版本/人工意图/OPEN | 软件 PASS：新组版本链/ACTIVE Profile 自动 OPEN，已有 CLOSED 和外部干预不越过；实际新两批组 OPEN NOT_RUN。 |
| T09 发现/UID/三标签 | 软件 PASS：稳定 discovery_revision 与 UID/resourceVersion 条件三键 patch；K8s 发现/标签为明确外部边界，不冒充真实新节点接管。 |
| T10 五方法/DTO/权限 | 正式 handler/JWT/缓存/PG策略/Casbin→mTLS→ACC/PG/worker 联合 PASS（八项最小权限，匿名401/租户403），历史实际五 BFF PASS；当前五端点404，按用户选择保留外部发布。 |
| T11 容器/原账/回收 | 本批真实共享主链 PASS；整卡与两物理 GPU 节点/独立两批硬件 NOT_RUN。 |
| T12 Gov共享受理/Inference | Compute/Instance/Inference 接线及 CPU 无 GPU charges 相关软件回归 PASS；未新增真实 Inference/训练任务。 |

- 所有生成、格式、构建、测试、DB、镜像及集群工作均经 `ssh fedora`；本地仅编辑/Git/传输。唯一证据根 `fedora:/home/chabking/codex-runs/node-agent-initialization-bc`，下述最终日志相对 `c4-preflight/`。最终七文件 SHA256 本地与 Fedora `final-verify` 一致。
- 最终 `make verify` 实际 **退出2**，日志 `final-closed-gap-coverage.make-verify.log`/`.log.rc`：生成、SQLC、测试、vet、build、mod verify、design、race、租户 mutation 均通过；恢复 gate 的 `TestAll22RPCsMTLSAndRecovery` 在 `SetAdmission` 返回503/DEPENDENCY_UNAVAILABLE，原因未确认，不改写成整体 PASS。
- 未改源后仅重跑失败 gate：`make test-recovery` **退出0**，`final-closed-gap-coverage.recovery-recheck.log`/`.log.rc`，17张关系恢复/回滚与完整 RPC contract PASS。随后 `make vuln secrets` 首次因官方漏洞源 EOF 失败，原日志保留；同组件复验 **退出0**，`final-closed-gap-coverage.remaining-gates-recheck.log`/`.log.rc`，0可达漏洞、git/dir敏感信息扫描无泄漏。所需组件均通过，不声称最后整条 make verify 重跑为0。
- 最终7项 helper/环境 SKIP 不计 PASS：`TestPostgresInitializationJointHelper`、`TestJointFormalProviderSource`、`TestInstanceQuantityBoundaryRuntime`、`TestJointObservationFixture`、`TestJointCatalogSeed`、`TestJointPersistentRuntime`、`TestContractServerProcess`。正式联合/真实进程门禁另有独立已执行证据。
- 聚焦红绿保留：`closed-gap-coverage-red.log` 退出1 → `closed-gap-coverage-green.log` 退出0；旧 Pod journal/持久复用修复及其原失败一并保留。此前 `ed88893`、`fdec2b3`、`3e5ae43` 对应完整 verify 退出0，不代替最终差异检查。早期工具127/race fixture/Go1.26.9漏洞、Gov candidate.sum误报、镜像执行位700、PF/503等失败记录未删除；未用放宽断言/忽略规则制造通过。
- Gov/Agent 正式依赖与既有专属联合验证均退出0；Gov audit 原整体退出1后仅敏感扫描组件退出0，未冒充整脚本重跑 PASS。具体正式模块/最小权限联合证据在 `initialization-joint/evidence/*formal-minimal-role.*`、`evidence/gov/` 与 Agent `agent-event-classification*.green.log`。

### 运行态、清理和恢复

- 目标 `kubernetes-admin@ani-lab`，API `172.16.101.10:6443`；ani-01/02/03 分别 .10/.11/.12，Node UID 分别 `201e41ac-c50d-4c77-b118-72d1ed93e111`、`73564fa7-98ad-45d4-9576-be59f6d5d245`、`af992ed9-cbe1-4f64-8fac-7433808bfab2`。用户恢复后全节点 Ready/可调度，**不恢复旧 cordon**。只有 ani-03 T4 `GPU-a86cb9fe-53ca-e474-5375-b662a54e8878`/15360MiB；Volcano虚拟计数不当作物理卡。
- ACC 现唯一中心位于 ani-system，non-root10001，无 hostPath/GPU env/runtimeClass/hostname 固定；旧 ani-accelerator 中心 replicas0/无Pod，旧 Service ExternalName 指向新中心并保留原长DNS SNI。显式0005/0006迁移到schema6，保留旧目录/Claim/Binding/冻结执行；无启动自动迁移。
- ACC 当前镜像 `172.16.101.10:5000/ani/accelerator@sha256:52e9a52c56843f1444c9eb759de7b3aa7d0d53cbe38c48762826a5fd8d8044f8`，二进制SHA256 `b89606cda946946c9863f856eded221c18634456d5ddc06b36f7ef6730ea2bf7`；实际构建 version `3e5ae43-closed-gap-coverage`，内容等价代码提交 e9dfd62，非冒称新ldflags。`acc-closed-gap-coverage-source-equivalence.receipt.json` 确认 Fedora HEAD/index/工作区全树相等且clean，build/rollout回执已补正式commit。Agent 8f49e36f 镜像 `sha256:ad9f90c811d70ae7d9a9490b4bd3eaf2b30a7317904f87d457ac0f4e354bea4e` 保留原稳定 LogID/journal。最终新事实 current=consumed=2083，来源新鲜、队列清空。外部 registry 不可写/目标digest404；镜像经精确缓存导入三个节点，不宣称远端可拉。
- 外部 Gov 当前 `20261021-gov-main-323eb74@sha256:0d39969893fb8402f704a2ec98f978007066b06da060dbb7880953496cf07ff6` 保留。共享组 `69e6d9cf-7855-4311-b3a4-3dbcdba0e3ed` OPEN/v12，整卡组 `38348ed3-dce9-4b2f-85a3-3ba2ae64793e` CLOSED/v4，未自动开放人工暂停组。
- 最终只读回执 `final-runtime-readonly-20261021T172555Z/result.json`：容量完整、committed3840、剩余3份3840/25%；只保留原用户GPU实例 `59423463-191d-435e-92f7-8a27a035c22b` 原Pod/STS；原CPU predictor Pod/Deployment Ready且UID保持。旧通用 service_uid 与当前 InferenceService UID 不同，不能据此声称CPU Service/IS完整UID未变，也不能无证认定本任务改了它。
- 原历史导入587条：201插入/386沿用；重放0插入/587已有、revision53→53。原文件SHA256 `09da57fa2ad261402f8883ff872dcda9f305cb5c5a2b1f110eae797734cb7a72`，原宿主文件、epoch/log/receipt及历史未知事实保留，临时只读挂载已移除。
- 清理 PASS：合法管理员 **root/ID2** 经正常会话/CSRF将自身roles恢复[1]，任务role16删除，原role1/他人角色与产品API336–340、权限507–510保持；回执 `gov-task-role-cleanup-20261021T172741302498Z/report.json`。初次超时/旧会话失败零写入，原记录保留，无自造JWT或DB授权改写。
- 精确清理 PASS/退出0：47个临时token/cookie/CSRF/DSN/key/client/Secret副本、PF105981、独占测试PG `acc-node-agent-bc-20261021` 和唯一匿名卷；逐项身份/hash/inode guard，无扩范围。回执 `task-temporaries-cleanup-20261021T173745950805Z.report.json`。测试PG已删除，重现需用当前 migrations 与官方 test-pg-grants 重建，旧DSN不可复用。生产PG/Secret/业务Pod、Agent持久日志、外部凭据源、公钥、kubeconfig和备份未删。
- 恢复入口为受限 `c4-preflight/`（目录0700/私有文件0600）的变更前对象、迁移/单写切换/客户端patch及 `ani_accelerator.closed.before.dump`。先关闭相关新准入、确保唯一writer，再按schema6兼容关系恢复；**不得把旧dump覆盖现有账本/绑定，也不得以不兼容旧binary启动，不自动重开人工CLOSED**。早期专用 acc_runtime DSN误输出已窄轮换，真实PodIP SCRAM确认新可用/旧拒绝；未改共享ani_app、HBA/CA或全局认证，临时凭据副本现已清理。
- 固定延期：D退役/解绑/换组、P2容量revision新门禁、自动安装维修/通用插件/远程执行平台、新训练/真实Inference验收、新配额模型。本批未夹带实现；现有主链/权限/租户隔离保护继续保留。

## 既往批次记录

2026-10-17 已部署绑定 baseline、无时间自动失效的硬件验收资格 v2，并解除运行采集/释放与准入资格的耦合。两组资格用原真实实验 manifest 显式签发，保留原 baseline/spec/Node UID/设备预算/verified_at，不含 expires_at；新 ConfigMap 挂载与新 ACC 镜像一起部署，原 v1 文件、ConfigMap 和回执保留。数据库、profile 与 baseline 未替换。当前镜像与完整来源摘要见[baseline 资格回执](../evidence/gpu-live-20261017/baseline-qualification-receipt.json)。

本轮单 T4 顺序执行的最小真实主链均通过：整卡正式百分比计划 Resolve→Iris CUDA→ACTIVE 绑定→CRI 正向退出→正常删除→RELEASE_CONFIRMED/ALLOCATION_RELEASED，容量 `1→0→1`；共享同样以真实 CUDA 和绑定达到 `10→9`，随后在受保护 scheduler ConfigMap 加入未被 scheduler 消费的额外 data 键制造漂移。漂移时 Resolve 实际拒绝 CONFIG_DRIFT，同一活绑定仍产生新的完整观测；漂移仍存在时正常退出/删除后确认释放。最后 CAS 移除该键、逐字节恢复原 data，Resolve 恢复、完整可用容量回到 10。调度配置正文、scheduler 镜像与运行 Pod 未为此实验切换。

资格签名/作用域/预算与固定时钟推进 30 天的定向回归通过；真实 PG provider 回放验证资格缺失/损坏/旧 v1 过期、配置/镜像/F/注册/显存漂移与实际 CRI/PodResources 失联的区别。软件时间推进不称为真实等待 30 天；原完整硬件实验复用不称为重新训练或重跑满载矩阵。当前 K=10/F=256，共享 OPEN/free10，整卡 CLOSED/副本 0；修复版 scheduler `ba5120f9…` 与 Helm revision 3 持续保留。

当前 reader 自动核对的漂移边界及宿主驱动/HAMi 库变更流程见 [provider 合同](../runbooks/provider-sources.md)。本轮没有写 Gov usage 投影或配额、没有接入真实外部 owner 产品 API、没有提交/推送；这些边界与多物理卡验收仍分别记账。下述保留调度器批次的 v1 到期时间属于历史记录，不再控制当前 v2 资格。

## 保留修复版调度器与 v1 证明（历史阶段）

2026-10-17 已按用户要求保留修复版 scheduler，Helm `ani-volcano` revision 3 固定镜像 `sha256:ba5120f9edf947bccf9da85b25b3a11181d8c7c55a373b521209f632e018b442`，三个可调度节点均已导入同一镜像。升级保留既有 admission 证书及 Webhook CA。已知有问题的原版不再作为当前部署或恢复目标。新共享/整卡 baseline、证明、精确镜像及原始证据摘要见[保留版回执](../evidence/gpu-live-20261017/retained-receipt.json)。

在同一张 T4 上顺序重新采集了两种模式的真实硬件实验并签署新证明；新目录分别持久化于 `acc_live_v2`、`acc_whole_v2`，原数据库与首轮证据保留。共享通过两个独立模型推理、显存限制、10 份满载及第 11 份 Pending/释放回填；整卡通过正式 `percentage=100` 计划及竞争者 Pending/释放回填。两者均完成 Publish/OPEN/Resolve、真实 owner Pod、绑定、ACC 重启保持占用、CRI 正向退出与正常删除后释放，容量分别为 `10→9→10`、`1→0→1`。

本轮另修复了历史 RELEASE_CONFIRMED 在 120 秒后被查询误降为 UNKNOWN 的问题。定向红→绿后部署新 ACC 查询镜像，真实 AdminListBindings 复核共享 12 条、整卡 3 条历史记录，每条与列表均完整；相同 Pod UID、绑定、设备、baseline、最终部署前的原证明时间/引用均保留。原错误响应和旧 collector 在部署前合法推进共享正式记录时间的证据分别保留。GPU 主链执行镜像与最终查询镜像在回执中明确区分；ACTIVE 过期/失联和新于证明的分配仍保守计量。

该阶段结束时节点为 K=10/F=256，共享组 OPEN、完整可用容量 10，整卡组 CLOSED、整卡实例副本为 0，避免同卡重复开放供给。原 v1 文件包含联调工具默认的 24 小时有效期，后续显式迁移为 v2；该旧文件及回执保留，不再作为长期资格策略。Gov/Inference 使用受控 mTLS/owner 客户端，未写入 Gov usage 投影、配额或其他服务业务状态；租户关联历史绑定、真实外部服务链路、多物理卡等仍 `not_verified`。本轮仅运行对应定向回归与真实主链，没有提交、推送、合并或重跑全量软件门禁。

## 首轮临时 GPU 联调（历史）

2026-10-17 首轮：基于最新非 main 候选 `c1242866d9877d4fd5dee934ddc80e8ec8b23ad9`，在 `kubernetes-admin@ani-lab` 的 `ani-03`（172.16.101.12、单 Tesla T4）部署真实生产入口、独立 PostgreSQL、mTLS 和真实 Kubernetes/NVML/PodResources/CRI reader。真实实验与阶段回执见 [首轮 GPU 联调证据](../evidence/gpu-live-20261017/receipt.json)。本批仅运行对应 provider 定向回归及真实主链，不重复历史全量软件门禁；没有提交、推送或合并。

共享规格已完成真实硬件实验、签名证明校验、Publish/OPEN/Resolve、按冻结计划创建实际 CUDA 工作负载、三源合并绑定、进程重启持久性和正常删除释放，可用容量为 `10→9→10`。正式 Whole 百分比计划在原版 scheduler 被 Queue 拒绝；应用[独立最小补丁](../evidence/gpu-live-20261017/volcano-percentage-only.patch)后，以新 baseline/证明完成真实模型竞争、Publish/OPEN/Resolve 和同样的绑定/重启/正常删除链，可用容量为 `1→0→1`。这不是原版 v1.15.2 Whole 通过声明；[兼容说明](../runbooks/volcano-v1.15.2-compatibility.md)与同一回执记录原失败、派生镜像和恢复结果。

Gov/Inference 调用方由明确的受控 mTLS/owner 测试客户端替代；ACC 业务、PG 持久化、provider 及 GPU 执行均真实。没有伪造 Gov 使用投影或配额账本，租户绑定关联与外部占退额仍 `not_verified`。单 T4 不覆盖多物理卡，F=256 恰好整除该卡显存，不能宣称量化尾差硬件场景已验。原始来源含节点级敏感信息，保留在仓外受限目录；Git 仅保存脱敏回执和最小补丁。

首轮结束曾保留共享 ACC 生产入口与 PG，并恢复原 scheduler spec/image、插件 spec/ConfigMap 原字符串和 Node 三标签，K=10/F=256，共享组 OPEN、完整可用容量 10，GPU 0 MiB 且无计算进程。整卡组 CLOSED、整卡实例副本为 0；两组数据库保留。这是历史结束状态，原版仍有上述 Whole 百分比缺陷，已由后续保留修复版部署替代。

## ACC-V12-01 历史软件批次

更新：2026-09-23，批次 `ACC-V12-01`，工作分支 `codex/accelerator-v1.2`。

R0～R5 在本批无 GPU 软件范围内完成（completed）。发布等待明确 remote。完整结果见 [本批验收记录](acc-v12-01-acceptance.md)，实施依据为 [实施计划](../plans/accelerator-v1.2-implementation.md) 与 [验收矩阵](../plans/accelerator-v1.2-acceptance.md)。最终 SHA 验证与清理回执存于仓外，需与本记录一起读取。

| 项目 | 结果 | 已完成行为 |
|---|---|---|
| R0 环境与输入 | pass | 初始候选 Fedora verify 后提交 b898fd8，独占目录/PG/缓存/端口/证据 |
| R1 领域与合同 | pass | 整卡/共享计量、静态 plan、规范摘要、权限及字段级接入合同/固定向量 |
| R2 PG 与供给目录 | pass | 显式迁移、无 DDL 运行角色、事务幂等、目录/组/规格、发布与 OPEN 证据门槛 |
| R3 provider/绑定/容量 | pass | 固定来源 reader、真实 PG、三源去重、局部冲突、未知/恢复与可信解除 |
| R4 使用投影/释放/22 RPC | pass | 真实 mTLS 进程正负向、终态幂等、绑定独立、只读 owner 释放观察 |
| R5 软件门禁 | pass | verify、领域/PG/contract/provider/restore/race 全部退出 0，修复后漏洞扫描无发现 |
| SHIP-01 Git/BOM/审计 | pass | 首个真实提交 7046c4f audit 通过，41 个依赖许可证据齐全；最终 SHA verify/audit、BOM 无差异与资源清理的完整回执见仓外 final-receipt.txt |
| SHIP-02 推送 | not_verified | 当前无 remote，不猜目标、不强推 |
| SHIP-03 CI | not_verified | 尚无最终 SHA CI 结果 |
| GPU-LIVE-* | not_verified | 无 GPU；未进行硬件消费、模型推理和满载/解除实测 |
| EXT-* | not_verified | 仅交付接入合同，未修改或等待 Governance/Inference |

正式构建没有测试绕过；无有效硬件证明的供给保持 CLOSED/NOT_VERIFIED。ENDED 不清活绑定，退出/重启不是实例删除，本服务不写配额或工作负载。

仓外证据根：`fedora:/home/chabking/acc-v12-01-20260923-bdtawbnM/evidence`。`software-final-01` 保存完整源码 manifest、真实命令/时间/退出码/日志；`precommit-*` 保存收口候选门禁，`source-audit-02.log` 是首个真实提交审计。最终提交后的 verify/audit、BOM 无差异、推送/CI/清理与代码 SHA 单独写仓外 `final-receipt.txt`，避免改写 BOM 来源。原失败日志保留，当前 pass 不抹去失败历史。
