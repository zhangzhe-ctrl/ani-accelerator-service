# GOV-ACC-V12-01 安全暂停记录

2026-09-23，按用户“到安全暂停点就暂停吧”停止。本任务未完成，不得自动续跑、发布或将组件通过记录升级为全部软件验收通过。

## 当前版本与工作区

两仓分支均为 `codex/governance-accelerator-v1.2`。

- Accelerator HEAD：`29226ecc303f9a8f9897cd243c7581dc82b9fccf`，暂停前已形成一个**本地**数据层候选提交。其后网络调用事务边界整改、并发测试和证据仍未提交。
- Governance HEAD：`0fbe1a69e49cc23dc7a1696b62f68c34a7c6a48a`，本批改动尚未提交，工作区必须保留。
- Gov 当前正式模块：`v0.0.0-20260923100416-9f9712198488`；尚未固定 Acc 新候选。
- 本轮没有推送、合并或部署。GitHub `gh repo view` 返回 HTTP 401；精确 SHA CI 尚未查询成功。Gov 默认 SSH 22 连接关闭，独立 `ssh.github.com:443` 的只读 ls-remote 成功，未更改 remote URL。

## 已验证与未完成

已有独立证据包括：Acc 全量 48 SQL 的 sqlc/pgx 整改、受限角色/no-RLS/显式租户与 mutation；Gov 原配额及 GPU 受限 PG 回归；20 个真实 HTTP BFF 与权限撤销修复、分页、预览只读、非空绑定脱敏；quota_lab 8 项及 Network/鉴权回归；持久 owner 主闭环；双 Gov 进程同键竞争；同步丢行/提交后 kill 恢复；10 个真实事务 kill 窗口、12 轮双进程取消/领取竞争及 race；填充数据库恢复比较。

主 joint 较早完整一轮 exit 0，71.73 秒：真实已知容量 0 与真实 freshness 超时后的 UNKNOWN 均可受理等待。构造观察经过 SaveObservation，不是 GPU 硬件采样。

恢复后优先处理这些**实际缺口**：

1. 新增混合 charge 恢复场景等待“DELETE 原集合不完整时转 UNKNOWN”超时，最新 joint exit 1。不能从较早单 charge 闭环推导 DELETE-04 全部通过。先核对生产领取/原集合校验的真实状态和测试屏障，再决定修实现或修错误断言。
2. 最新 PlanQuota 列表兼容扩展的空过滤构造 `$ ? (true)`，PG 报 SQLSTATE 42601；嵌套过滤/排序/分页仍需充分回归。
3. Acc 网络调用移出事务的六个真实 PG 场景及 A/22 RPC 普通回归通过，但 candidate-v5 完整 `make verify` 在 contract race 阶段按暂停要求终止，exit 143。新源码不能沿用 candidate-v4 总通过结论。
4. Gov 完整最终 `make verify-gpu` 未通过；需汇合全部源文件、更新格式 manifest、重新生成核对、完整审核及回归。
5. 正式 Gov 与正式 Acc 各自进程边界已有证据，A→A 同版本真实连接尚未完成。
6. 最终 owner 指南、完整逐 ID 验收汇总、精确受测 manifest、模块重新固定、审计/SBOM、提交/推送/CI均未完成。固定 cyclonedx 工具下载在暂停时被终止，exit 143；不能记 audit pass。

生产 Inference、真实 GPU、多 owner 与生产部署保持 `not_verified`。

## 暂停安全性

- 全部协作子任务已停止新增工作；应用/worker/helper、构建/测试及工具下载进程已结束。
- Acc B wrapper/server 与 A 只读 provider source 通过各自 `stop` 文件正常退出。
- Gov 故障场景移除的 charge 已由退出清理还原。暂停只读检查：原 GPU charge 集合不匹配 0、账户/charge 不平衡 0、其他未提交事务 0。
- 账本保留 1 条 DISPATCHING、1 条 QUEUED 待恢复事实，未人为 ACK/清账/退款。恢复 worker 前先读取这些记录及最新失败日志。
- Gov PG 与 Redis 使用 **Podman**，Acc PG 使用 **Docker**。只停止任务容器，没有删除数据库、volume、证书、缓存或备份。Redis 在停止前 SAVE 成功。

## 恢复资源

远端根目录：`fedora:/home/chabking/gov-acc-v12-01-20260923`。

暂停新增备份（0600，仅远端，不入仓库）：

- `evidence/pause/gov-joint-b.dump`，SHA256 `57d0426a26123f5f50d1e1c94409af1410bbd2e3dcc6e99bcfbca0fd501c13e0`。
- `evidence/pause/test-owner.dump`，SHA256 `eef0715f9512ac5e6098d26870d96bd95e247d35ac6393deed11573bb935c854`。
- `evidence/pause/acc-cluster.sql`，任务 Acc 集群备份，包含私有角色资料，只保留远端受限权限。

既有升级前/填充后 dump 仍位于 `evidence/acc/`、`evidence/gov-data/`；DSN、CA 和私钥仍在 `joint-b/`、`joint-a/`、`task/` 的受限文件中。不得将其复制到报告或 Git。

用户明确恢复后，先核对本地 dirty state 和远端 manifest，再按需要启动 `podman start gov-acc-v12-01-gov-pg gov-acc-v12-01-bff-redis` 与 `docker start gov-acc-v12-01-acc`。不要重跑 bootstrap 或自动迁移。原 helper 的 stop 文件已存在；重启前按 `docs/testing/gov-acc-v12-01-helpers.md` 检查配置/二进制/证书及停止标记，不能盲目启动旧候选。

所有后续生成、格式化、测试、审计和进程继续只在 Fedora，使用任务私有缓存、锁、`GOWORK=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=2`。

相关交接文件：Gov `docs/evidence/gov-acc-v12-01/data-pause.md`、`bff/paused-handoff.md`、`process/README.md`、`joint-software.md`；Acc `docs/evidence/gov-acc-v12-01/acc/candidate-v5/`。原失败日志保留；最新源码部分通过不能替代最终完整门禁。
