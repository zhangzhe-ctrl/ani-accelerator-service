# Accelerator v1.2 运行与恢复

历史 ACC-V12-01 批次仅在 Fedora 隔离环境进行软件验证；2026-10-17 的真实单节点 GPU 部署与回执见[执行状态](../execution/status.md)。生产二进制只装配 PostgreSQL、固定 provider 读取器、mTLS 与签名证明校验器；测试注入位于 `_test.go`，没有配置绕过。无 GPU 的安装应保持无正式供给或 CLOSED/NOT_VERIFIED。

2026-10-17 用户要求保留修复版 Volcano scheduler，调度器保留结果见[保留版回执](../evidence/gpu-live-20261017/retained-receipt.json)，后续资格政策迁移及当前 ACC 镜像以[baseline 资格回执](../evidence/gpu-live-20261017/baseline-qualification-receipt.json)为准。固定调度镜像 digest 为 `sha256:ba5120f9edf947bccf9da85b25b3a11181d8c7c55a373b521209f632e018b442`，源码和[最小补丁](../evidence/gpu-live-20261017/volcano-percentage-only.patch)见兼容说明。首轮临时试验恢复原版的步骤只属历史，不能再作为当前恢复目标；已知有 Whole 百分比缺陷的 `sha256:836554c1c3286742fb6fcc721f5d3515e0e3e304a30d9cb733635f555f5ef671` 不可替换当前修复版。

baseline 不可原位替换。切换 scheduler 镜像时，先关闭旧供给、停止旧写入，保留原数据库；用新数据库显式执行已有迁移并生成新 cluster/group/baseline/profile，重新采集绑定实际新镜像的硬件实验、签署证明并 Publish/OPEN/Resolve，再验证真实工作负载。不能修改旧 JSON/SQL 或把旧实验重新贴上新镜像摘要。单 T4 的 K=1 整卡与 K=10 共享分别验收，结束时只开放与实际节点配置匹配的组，避免重复计算物理供给。

硬件验收资格采用无时间自动失效的 v2，绑定实际 baseline/规格/节点/物理设备；关键环境变化才按新 baseline 重新验收。运行健康继续按实时采样判断，资格不通过不停止占用和释放事实采集。旧 v1 文件仍保持其原到期语义；迁移由受控签名者核对原实验及当前配置后显式签发 v2，保留真实验收时间和原始实验，不改写旧文件冒充续期或重验。该迁移不改业务数据库、profile 或 baseline。mTLS 证书仍按证书期限更新，不能与硬件资格混为一谈。当前资格、证书期限与镜像投递范围记录在回执中；仅在节点导入镜像时，不称为 registry 发布。

迁移使用新 ConfigMap 并更新受管挂载，保留原 ConfigMap 和签名。恢复 ACC 时同时核对二进制支持的资格格式与实际挂载，旧版仅支持 v1 的二进制不能直接搭配 v2 文件；调度器仍保留上述修复镜像。

## 配置与启动

`configs/config.yaml` 是不可直接启动的安全示例：所有密钥、数据库及 provider 配置采用绝对路径引用，缺失即启动失败。将 `database.dsn_file` 指向只读、权限 0600 的 libpq DSN 文件；生产 PG 连接使用可验证的 TLS，测试 PG 仅绑定任务回环端口。证书使用受管 CA 和服务器 DNS SAN；客户端证书必须只有一个精确 DNS SAN，Gov、Inference、Resource 分别为 `ani-governance`、`ani-inference-service`、`ani-resource-service`，不含其他 SAN。对应配置为 `security.governance_dns_name`、`inference_dns_name`、`resource_dns_name`。完整服务 RPC 清单与可信上下文要求见 [接入合同](../contracts/accelerator-consumer-contract.md#service-authorization-and-tenant-isolation)。

用户身份、角色、平台权限、套餐和配额准入由 Gov 统一管理；ACC 不再配置逐用户、逐租户 `security.grants`，也不新增集群清单。集群范围直接复用 `accelerator.provider_config_file` 指向的 `provider.json` `connections[].ref` 和持久的 `cluster.connection_ref`。新增已由 Gov 授权的用户或租户不需要修改或重启 ACC；基础设施连接或密钥变更仍遵守受管部署流程。

旧配置中的 `grants`、`governance_uri`、`inference_uri`、`resource_uri` 已移除且字段编号和名称保留，读取时明确拒绝旧字段。发布时需协同替换 Gov 与已启用 owner 的客户端证书及 ACC 配置；保留可匹配的旧二进制、配置和证书恢复材料。不能只更新 ACC 接收端，不能通过忽略旧字段或恢复用户白名单解决服务证书错误。本次代码改造不执行部署、目录播种或数据库迁移。

持久 cursor key 至少 32 字节，多副本一致；硬件证明和 owner 分别使用独立 Ed25519 公钥（标准 Base64 原始 32 字节）。私钥不交给 Accelerator。secret/provider 文件更新通过受管部署流程重启装配；不要期待文件热更新。

`make build` 后运行 `bin/ani-accelerator-service -conf <配置目录>`。本次任务这些命令只能在 fedora 执行。业务入口仅 mTLS gRPC；独立管理 HTTP 默认回环，`/healthz` 为存活，`/readyz` 为进程就绪且 PG 最近探测成功，`/metrics` 为进程指标。provider 失联不会假装全部资源耗尽，也不必导致整个服务失去查询能力：通过业务 observation.complete/issues 表达。readiness 不证明 GPU、模型 readiness 或外部服务已集成。

## 显式 PostgreSQL 迁移

执行前停下本服务写入，确认目标数据库与迁移 owner，使用 `pg_dump -Fc` 保存恢复点。将 PGHOST/PGPORT/PGDATABASE/PGUSER 和 PGPASSFILE 配置到操作者环境，不把密码放命令行或日志。由独立迁移 owner 执行 `bash scripts/migrate 0001`；`migrations/0001_accelerator.sql` 在单一事务中建 schema，版本 1，不对已有 schema 做猜测性升级。失败回滚后检查版本和恢复点，不重复绕过错误。

当前 ModelDev owner 接入的 runtime 要求 schema 7：新库依次显式执行 `0001` 至 `0007`；现有 schema 6 在保留恢复点后执行 `bash scripts/migrate 0007`，保留原 NodeAgent telemetry/initialization 的 0005/0006。0007 只将 usages/claims/release_proofs 的固定 owner 集合扩为 Inference、Resource 与 `ani-modeldev-service`，保留原身份 JSON、租户、原 CREATE、execution digest 和 FK 约束。应用不自动升级，不接受未迁移 schema；恢复旧二进制还需恢复与其匹配的数据库恢复点，不能只恢复启动配置或手改版本记录。

ModelDev 客户端证书唯一 DNS SAN 为 `ani-modeldev-service`，ACC 配置 `security.modeldev_dns_name` 同值。该身份只允许 `/accelerator.v1.AcceleratorUsageService/ObserveRelease`；不得共用其他 owner 的客户端身份或签名公钥。启用实际训练 ownership 采集时挂载独立 `accelerator.modeldev_owner_public_key_file`，标准 Base64 原始 32 字节 Ed25519 公钥。缺少该 key 时相关物理占用仍保留而不会被关联到 ModelDev。实际 Pod UID/container、签名域及独立 close UUID 规则见[接入合同](../contracts/accelerator-consumer-contract.md#ownership-usage-and-release)。

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

已完整确认的历史释放保持原状态及证明时间，不随实时采样 TTL 过期。2026-10-17 修复了绑定查询将这类历史记录误降为 UNKNOWN 的问题；旧失败响应保留，修复后用相同 binding/Pod UID、原证明引用和时间重新读取验证。活动绑定的新鲜度与来源健康检查继续生效。

## 软件门禁与证据

仅 Fedora：`make tools api generate verify supply-chain-tools audit`；真实 PG 环境设置 `ACC_TEST_DSN_FILE` 后执行 `make test-domain test-pg test-contract test-provider test-race`。`make test-provider/test-pg/test-contract/test-race` 缺 DSN 时直接失败；普通 `go test` 的 helper SKIP 不是软件验收通过。`make test-recovery` 必须在其他写入测试完成后执行，避免比较期间发生合法写入。

冻结源码和报告后先 verify，再提交；真实提交在 Fedora 干净 checkout audit，回传 BOM；含 BOM 候选 verify 后 BOM-only 提交；最终 SHA verify/audit 且 BOM 无差异。发布回执放仓外，避免改变源码快照引起 BOM 漂移。失败原日志永不改写为 pass。

## 后续真实硬件门禁

完整 provider 来源/部署限制见 [provider 读取合同](provider-sources.md)。GPU-LIVE-01～04 必须在另行授权的真实环境证明 F>1 全链单位、同卡两个独立模型真实推理、K 满后 Pending、整卡排他及真实解除恢复。硬件消费证明必须引用上述独立原始实验 manifest，指定 cluster/group/baseline/spec/Node UID/物理 UUID 和预算，并经离线受控签名。管理员不能通过 RPC 自报通过。外部 Gov/Inference 接入与退款闭环独立验收，本批不执行。
