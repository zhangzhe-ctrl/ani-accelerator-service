# Accelerator v1.2 运行与恢复

本批只在 Fedora 隔离环境进行软件验证，不部署正式服务。生产二进制只装配 PostgreSQL、固定 provider 读取器、mTLS 与签名证明校验器；测试注入位于 `_test.go`，没有配置绕过。无 GPU 的安装应保持无正式供给或 CLOSED/NOT_VERIFIED。

## 配置与启动

`configs/config.yaml` 是不可直接启动的安全示例：所有密钥、数据库及 provider 配置采用绝对路径引用，缺失即启动失败。将 `database.dsn_file` 指向只读、权限 0600 的 libpq DSN 文件；生产 PG 连接使用可验证的 TLS，测试 PG 仅绑定任务回环端口。证书使用受管 CA、服务器 DNS SAN、独立服务 URI SAN。客户端精确 URI 和 actor/action/tenant/cluster grants 见 [接入合同](../contracts/accelerator-consumer-contract.md)。服务不自动创建 grants、密钥、目录数据或迁移。

持久 cursor key 至少 32 字节，多副本一致；硬件证明和 owner 分别使用独立 Ed25519 公钥（标准 Base64 原始 32 字节）。私钥不交给 Accelerator。secret/provider 文件更新通过受管部署流程重启装配；不要期待文件热更新。

`make build` 后运行 `bin/ani-accelerator-service -conf <配置目录>`。本次任务这些命令只能在 fedora 执行。业务入口仅 mTLS gRPC；独立管理 HTTP 默认回环，`/healthz` 为存活，`/readyz` 为进程就绪且 PG 最近探测成功，`/metrics` 为进程指标。provider 失联不会假装全部资源耗尽，也不必导致整个服务失去查询能力：通过业务 observation.complete/issues 表达。readiness 不证明 GPU、模型 readiness 或外部服务已集成。

## 显式 PostgreSQL 迁移

执行前停下本服务写入，确认目标数据库与迁移 owner，使用 `pg_dump -Fc` 保存恢复点。将 PGHOST/PGPORT/PGDATABASE/PGUSER 和 PGPASSFILE 配置到操作者环境，不把密码放命令行或日志。由独立迁移 owner 执行 `bash scripts/migrate 0001`；`migrations/0001_accelerator.sql` 在单一事务中建 schema，版本 1，不对已有 schema 做猜测性升级。失败回滚后检查版本和恢复点，不重复绕过错误。

运行角色与 schema/table owner 分开，禁止 SUPERUSER/CREATEDB/CREATEROLE 与数据库/schema CREATE，以及任何表所有权。示例授权使用已审核的实际角色名：

```sql
REVOKE CREATE, TEMPORARY ON DATABASE accelerator FROM PUBLIC;
GRANT CONNECT ON DATABASE accelerator TO acc_runtime;
GRANT USAGE ON SCHEMA accelerator TO acc_runtime;
GRANT SELECT ON ALL TABLES IN SCHEMA accelerator TO acc_runtime;
GRANT INSERT ON ALL TABLES IN SCHEMA accelerator TO acc_runtime;
REVOKE INSERT ON accelerator.schema_migrations FROM acc_runtime;
GRANT UPDATE ON accelerator.supply_groups, accelerator.usages,
  accelerator.devices, accelerator.claims, accelerator.release_proofs,
  accelerator.bindings, accelerator.source_watermarks TO acc_runtime;
```

不授予 profiles/目录 UPDATE、DELETE、TRUNCATE 或迁移权限。启动会检查 schema 版本及危险运行角色，但操作者仍需审核 PUBLIC/角色继承权限。PG JSONB 保存不可变领域快照，关系列负责索引、唯一键、版本与 tenant-preserving FK；手写参数化 SQL 集中在 data，不需要 sqlc 或启动 DDL。

管理幂等键和业务写共用 PG 事务；usage 锁按 tenant/owner/resource；binding 键按 cluster/Pod UID/container/device；观察按 cluster 串行持久化。数据库冲突返回稳定 reason，事务错误不会输出原始 SQL/DSN。投影历史与当前终态持久化，服务重启不靠重播模拟数据。

## 恢复

保留失败日志与迁移前备份。恢复到单独数据库，用 `pg_restore --exit-on-error` 恢复**原备份数据**；比较目录、规格、幂等记录、usages/revisions、devices/claims/proofs/bindings/watermarks，再由操作者切换受管 DSN。不能清库重跑迁移冒充恢复。切换前停止旧写入，回滚可切回保留的原库；不自动 drop 原库。

本批 `make test-recovery` 要求明确 `ACC_TEST_PG_CONTAINER=acc-v12-01-*` 且容器 `ani.task=ACC-V12-01-*`，以及 `ACC_TEST_EVIDENCE_DIR`；仅在该任务容器中新建独立恢复库，比较 13 张表，最后删除恢复库。它保存 populated dump 与注入失败日志，不删除源库。`tests/contract` 在真实 PG 事务提交前、提交后响应送达前杀真实服务进程，验证 rollback、耐久终态与原报文重试。

采集失联不删除历史 occupancy。新的失败接收保留最后可信 observed_at；received_at 不能当新观测。重连从数据库的 binding/claim/水位继续；解除必须覆盖指定 Pod/container/device、Node UID，且有在线节点、CRI 正向退出与 PodResources 无分配。API NotFound、单次无记录、普通退出/重启不构成业务实例删除或退款。

## 软件门禁与证据

仅 Fedora：`make tools api generate verify supply-chain-tools audit`；真实 PG 环境设置 `ACC_TEST_DSN_FILE` 后执行 `make test-domain test-pg test-contract test-provider test-race`。`make test-provider/test-pg/test-contract/test-race` 缺 DSN 时直接失败；普通 `go test` 的 helper SKIP 不是软件验收通过。`make test-recovery` 必须在其他写入测试完成后执行，避免比较期间发生合法写入。

冻结源码和报告后先 verify，再提交；真实提交在 Fedora 干净 checkout audit，回传 BOM；含 BOM 候选 verify 后 BOM-only 提交；最终 SHA verify/audit 且 BOM 无差异。发布回执放仓外，避免改变源码快照引起 BOM 漂移。失败原日志永不改写为 pass。

## 后续真实硬件门禁

完整 provider 来源/部署限制见 [provider 读取合同](provider-sources.md)。GPU-LIVE-01～04 必须在另行授权的真实环境证明 F>1 全链单位、同卡两个独立模型真实推理、K 满后 Pending、整卡排他及真实解除恢复。硬件消费证明必须引用上述独立原始实验 manifest，指定 cluster/group/baseline/spec/Node UID/物理 UUID 和预算，并经离线受控签名。管理员不能通过 RPC 自报通过。外部 Gov/Inference 接入与退款闭环独立验收，本批不执行。
