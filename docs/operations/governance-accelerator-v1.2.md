# GOV-ACC-V12-01 执行与恢复手册

状态：R0 进行中。本文件只记录当前执行约束和已核对事实；没有软件完成声明。

## 输入和环境

- Governance 初始 HEAD：`0fbe1a69e49cc23dc7a1696b62f68c34a7c6a48a`，工作区干净，origin 为 `git@github.com:zhangzhe-ctrl/ani-governance.git`。
- Accelerator 初始 HEAD：`9f9712198488aaa722dc78aeb444248df25b08ef`；两份 governance-accelerator-v1.2 计划未跟踪，作为本批输入保留。origin 为 `https://github.com/zhangzhe-ctrl/ani-accelerator-service.git`。
- 两仓任务分支：`codex/governance-accelerator-v1.2`。仅允许非强制推送，不合并 main/tag/生产部署。
- 2026-09-23 通过 ssh 核对 Fedora 44、hostname `fedora`、Go `go1.26.7-X:nodwarf5`、8 CPU、约 16 GiB RAM、rootless Podman，home 可用约 373 GiB。
- 远端任务根 `/home/chabking/gov-acc-v12-01-20260923`；acc、gov、gov-bff 副本分开；任务私有 cache/locks/evidence/bin。每个任务固定 `GOWORK=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=2`。
- 回环 PG 端口预分配 Acc 25432、Gov 25433；各执行者必须实际检查占用并记录容器摘要/标签。此处预分配不是数据库已创建或受限角色已通过的证据。

本地只读写文件、传输和 Git；所有工具安装、生成、格式化、审计、构建、测试、PG、证书与进程在 Fedora。远端失败不回退本地。

## 数据迁移清单与权威

| 范围 | 原实现 | 目标及 schema 权威 |
|---|---|---|
| Acc internal/data/postgres.go 的全部持久读写 | pgx 内嵌 SQL | migrations 正式链 → 固定 sqlc 查询源/生成 pgx 层 |
| Gov QuotaLedgerRepo | Ent 事务及手写查询；占额/释放/撤销/领取/ACK/重试/删除 | Ent schema → 固定导出 schema.sql / Atlas → sqlc；统一单 pgx.Tx |
| Gov QuotaAdminRepo、PlanQuotaRepo | Ent 目录/账户/套餐额度 | 同一权威生成链及 sqlc，保留无关 Ent 模块 |
| Gov 新 GPU delete acceptance、usage sync | 尚未实现 | 加入同一 Ent/Atlas 链，tenant 复合关系、独立租约/CAS |

细粒度查询清单、源码扫描及迁移覆盖由 R1 实际盘点追加；没有覆盖完整前不得标记 SQLC-01 通过。生产 SQL 禁止在 Go 拼接，迁移、运维和测试 SQL 各自集中版本化。历史 quota/lab 报告仍代表旧 Ent 实现，不追写历史 sqlc 结论。

## 执行顺序及证据

R0 冻结输入、全量查询/租户关系和角色；R1 完成 pgx/sqlc、no-RLS、显式租户与门禁；R2 完成业务与持久恢复；R3 A/B/C 独立联调；R4 完整矩阵与故障/恢复；R5 最终指南、候选验证、提交/发布/CI及清理。

每次运行保存命令、开始结束时间、退出码、SHA/源码 manifest、schema/query/config/generated 摘要、工具与镜像 digest、角色/RLS断言、fixture与故障点、数据库前后断言。失败原始日志保留，修复复跑另存。不保存凭据明文。

A 正式构建层不能挂入测试证据私钥。B 仅 `_test.go` 替换硬件证据，独立持久 owner/PG/mTLS；观察由实际 SaveObservation 写入，非空绑定才能证明脱敏。C 生产 reader TLS/Unix 来源回放仍是构造样本。所有层均 `hardware_observed=false`。

## 升级、回退和清理

1. 用旧版本 quota/lab/套餐/tenant fixture 先做 populated 备份；显式运行版本迁移，不重跑 bootstrap、不启动自动迁移。
2. 限制运行角色，无 owner/superuser/BYPASSRLS/DDL/TEMP；实际检查 RLS/FORCE/policy 关闭和复合约束。
3. 失败迁移事务完整回滚；独立库恢复备份并逐表比较，再恢复 worker 验证收敛。
4. 回退先停新受理与相关 worker。旧二进制不能识别新模块/canonical/schema 时不能直接切回；在任务隔离库还原备份或用兼容恢复工具。已有新业务数据不得自动 DROP/DOWN/清库。
5. 清理只作用于本任务路径、标签、角色、进程、证书和缓存。保留脱敏证据、版本锁、恢复备份。不得操作共享数据库/服务。

当前上述运行验收均为 `not_verified`，后续只凭真实命令和断言更新。
