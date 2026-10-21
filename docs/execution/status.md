# Accelerator 执行状态

## Node Agent / 初始化 B/C（2026-10-21，执行中）

- 固定入口：`ANI-node-agent-initialization-GOAL-v1.2-20261021.md`；主方案 SHA256 `f6668bcbc264d2df6e22f0033ccf38e46d24e678e40d07dcb2353dfa78d0bfa7`。基线 ACC `9d45df6`、Gov `323eb749`、Resource `6bff99f`，任务工作树根 `/home/chabking/.codex/worktrees/node-agent-initialization-bc`，分支 `codex/node-agent-initialization-bc`。原工作区未覆盖。
- 用户追加范围：本批不修改前端代码，前端由专人接入五个 BFF；不再把页面交付作为本批实现项。D 退役/解绑/换组、P2 容量 revision 新门禁、自动安装平台、新训练/真实 Inference 验收继续延期。
- 软件 PASS：真实 Agent 进程经 mTLS、TokenReview 与实际对象绑定的外部 K8s 边界进入生产 ACC/PG；ACK 丢失、重启、原记录重放、journal 正向补证与旧退出阻断均已运行。外部 NVML/socket/K8s 测试边界与真实 GPU 验收分开。
- 软件 PASS：两节点正常连续上报经生产 Collect/SaveObservation 消费；真实 Running/UNKNOWN 拒绝旧 revision，合法清理在五秒预算内完成 ObserveRelease。初始化真实 PG 核心验证幂等、部分成功/重启/Retry、版本链 OPEN 和人工干预冲突。真实 Gov JWT/缓存/PG 策略/Casbin → 五 HTTP → mTLS → ACC/PG/worker 联合链通过；最小八项权限身份完成 Create/重放 → BLOCKED → Retry → SUCCEEDED，匿名 401、租户 403。K8s 发现/标签仍为明确外部边界。
- Fedora 唯一证据/恢复根：`/home/chabking/codex-runs/node-agent-initialization-bc`。`postgres/environment.sh` 对应本任务独占容器 `acc-node-agent-bc-20261021`；另有明确隔离的 C1/B3 库，不修改共享应用角色。原失败日志保留，运行结果以各 evidence 的退出码为准。
- 门禁 PASS：Fedora `integrated` 的 `make verify` 退出 0，覆盖正式生成/SQLC、测试、race、构建、恢复 17 张关系、漏洞及敏感信息扫描；日志 `evidence/integrated-verify.log`。保留原失败：固定工具路径缺失退出 127、早期 provider race 释放断言一次失败，以及 Go 1.26.9 标准库 GO-2026-6809；已用正确工具路径、真实 telemetry fixture 和 Go 1.26.10 完成集成门禁。最后窄差异另按受影响行为核验，不重复全量。
- 实际环境只读核实：`kubernetes-admin@ani-lab`，API `172.16.101.10:6443`，节点 ani-01/02/03 为 `.10/.11/.12`。单张 T4 位于 ani-03；ani-02/03 当前不可调度且存在既有运行实例，不自动解禁、清理或切插件模式。两批 GPU 节点/缺少模式的硬件验收目前 NOT_RUN。
- 运行切换尚未执行：活动中心在 `ani-accelerator`，`ani-system` 同名部署为零副本；必须按保留历史、单一权威和恢复快照受控切换。预检中发生一次 ACC DSN 误输出，凭据按已暴露处理；切换前核验专用角色并实施窄轮换，不复述原值、不修改共享 `ani_app`。
- 独立 Agent 工程已建立；用户建立 `git@github.com:zhangzhe-ctrl/ani-node-agent.git` 后 SSH 重试成功，原远端缺失已解除。当前准备提交/SSH 推送固定 API；Agent/Gov 候选联合验证已通过，正式固定模块无 replace 复验尚待发布。

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
