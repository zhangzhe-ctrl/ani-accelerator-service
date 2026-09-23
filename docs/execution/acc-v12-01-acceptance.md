# ACC-V12-01 软件验收记录

2026-09-23，执行主机 **fedora**（Fedora 44），任务根 `/home/chabking/acc-v12-01-20260923-bdtawbnM`，源码目录 `src`。本地仅阅读、编辑、传输和 Git。没有运行 GPU/外部服务验收，没有修改兄弟仓库或共享集群。

## 候选与可追溯证据

仓外 evidence 为上述任务根的 `evidence/`。统一验收执行 `bash scripts/acceptance`，环境指向该任务的 `pg/runtime-dsn`、`acc-v12-01-bdtawbnm-pg` 及 `evidence/software-final-01`。该目录含 `source.sha256`（包括未跟踪文件的完整候选）、host/source-directory/base-commit/git-state，以及每个 target 的 `.command/.started/.exit/.log`。下表每项共享此身份元组；日志简称 D/P/C/O/R/X/V 对应该目录的实际日志，全部退出码 **0**。

| 简称 | 实际命令 | 层次 |
|---|---|---|
| V | `make verify` | 固定生成无漂移、tidy、Go 全包测试/vet/build/mod verify、48 项历史模型回归、diff 检查 |
| D | `make test-domain` | 领域、规范向量与作用域游标 |
| P | `make test-pg` | PostgreSQL 17.11，真实约束/并发/事务/持久化 |
| C | `make test-contract` | 真实独立进程、mTLS、22 RPC、故障中断及生产生命周期 |
| O | `make test-provider` | 生产 HTTPS/Unix gRPC reader，构造来源回放、真实 PG、签名门槛 |
| R | `make test-recovery` | populated dump 还原，13 张业务表逐行一致，失败迁移回滚 |
| X | `make test-race` | biz/data/provider/server/service/contract 受影响并发路径 |

源码提交前另在 Fedora `precommit` 门禁记录完整候选 manifest；最终 SHA、audit/BOM、清理和发布回执保存在仓外 `final-receipt.txt`，不把 Git SHA 自引用写回 BOM 输入。本文件是源码冻结时的验收记录，后续最终 SHA 门禁仍必须执行。

## 按验收 ID 的实际结果

| ID | 结果 | 实际断言/证据 |
|---|---|---|
| BASE-01 | pass | R0 初始 manifest/index export 逐文件一致，初始 verify 后基线 b898fd8；当前 software-final-01/source.sha256；来源设计未改 |
| BASE-02 | pass | R0 记录独占 PG/回环端口/缓存/源码/证据；所有运行在 Fedora |
| UNIT-01 | pass | D TestUNIT01：6144 MiB 与 F=1/256/1024、每容器/实例总量、整卡百分比 |
| UNIT-02 | pass | D TestUNIT02：F=10 发布规格/渲染拒绝；不涉及 Gov 扣额 |
| UNIT-03 | pass | D TestUNIT03：逐卡 floor，不跨卡拼量化余量；O 双设备聚合 |
| UNIT-04 | pass | D TestUNIT04、O RuntimeLimits：BLOCKS/MIB/BYTES 一次归一化 |
| UNIT-05 | pass | D TestUNIT05：未知 F/不完整 baseline 拒绝；O factor 漂移及检查失败 |
| UNIT-06 | pass | D TestUNIT06：旧 plan 原 F 不变；O factor 漂移不重解释已存绑定 |
| UNIT-07 | pass | D TestUNIT07：整数边界、非法容器/副本/设备数、重复受管键；O strict JSON |
| UNIT-08 | pass | D TestUNIT08/ContractVectors：排序/观察状态不改摘要，渲染改变必识别；固定 JSON 向量逐字验证 |
| DB-01 | pass | r2-migrate/grants，P RuntimeRole：schema 与 TEMP DDL 均拒绝；生产启动只检查版本 |
| DB-02 | pass | C ACK 后进程杀死重启仍有投影与去重；P/O 持久目录、规格、绑定、水位；R 原数据恢复 |
| DB-03 | pass | P AtomicIdempotency：同键 12 并发同结果，异内容冲突，回滚无半条写入 |
| DB-04 | pass | P Constraints：Node UID 唯一、失败组事务无残留、profile UPDATE 拒绝、跨 tenant binding 约束 |
| DB-05 | pass | P 版本 CAS 竞争、并发投影/绑定与重复同步；X；锁修复后专项 race 连续 10 次 |
| DB-06 | pass | R：迁移前 dump 保留，故意除零 migration exit=3 后无 probe 表；另库 restore 比对原 populated 数据 |
| AUTH-01 | pass | C 无证书、外 CA、同 CA 错身份拒绝；cmd 缺配置失败；生产仅 mTLS |
| AUTH-02 | pass | C 22 方法 wrong-service 负向，包括 Gov-only Sync、Inference-only ObserveRelease |
| AUTH-03 | pass | C 配置明确 platform actor/actions，业务/metadata 一致；无 tenant=0；P 当前 scope 重放检查 |
| AUTH-04 | pass | C forged tenant 拒绝；P 同 resource 跨租户查询/FK 负向；O 无签名标签不建立 owner |
| AUTH-05 | pass | C tenant binding 无物理 UUID/Pod UID/原证据；P 稳定 PG keyset 分页；D 游标跨主体/筛选/权限范围拒绝 |
| AUTH-06 | pass | C 未授权请求无业务调用；P 幂等重放不能绕过当前 scope；受管 connection_ref、参数化 SQL 与日志脱敏测试 |
| CAT-01 | pass | C 目录 4 RPC 内容断言；P 持久父子关系；登记只写目录 |
| CAT-02 | pass | C/P Adopt CLOSED/NOT_VERIFIED；O Node UID 重建不继承、只有 GET/读取 socket |
| CAT-03 | pass | O 实际配置/版本/F/镜像/Node 来源检查，签名过期/错 scope/预算负向；P 自报 VERIFIED 拒绝 |
| CAT-04 | pass | C 测试装配完整 Adopt→Publish→OPEN→Resolve；O 真实验证器只在测试密钥下接受构造证据；生产无 skip 开关 |
| CAT-05 | pass | P immutable profile、CLOSED 拒绝新 Resolve、冻结 plan 仍 Sync；D 渲染篡改拒绝 |
| CAT-06 | pass | P 分页/具体版本/不可见/未知 FIT；D shape；C 正常 capacity/FIT；非法配置是错误 |
| OBS-01 | pass | O pinned parsers、生产 Kubernetes/NVML/PodResources/CRI 读取实现；provider-sources.md 列来源与权限 |
| OBS-02 | pass | O TLS HTTP + Unix gRPC 实际生产路径；parse_test/replay_test 构造样本模板摘要见 source.sha256；无硬件实采 |
| OBS-03 | pass | O PhysicalID/格式负向，PodResources NVIDIA-0 不当物理 UUID；身份包含 cluster 与 Node UID |
| OBS-04 | pass | P/O 三源同实际键合并一次，Pod UID/容器/设备独立键；关联可以晚到 |
| OBS-05 | pass | O stale/offline/runtime failure、API 删除、factor 漂移未知；PG 保留旧 observed_at，不用 received_at 冒充 |
| OBS-06 | pass | O 重建适配器后继续已有事实并正向解除；C 真实进程恢复；R 还原水位；查询无写副作用 |
| CAP-01 | pass | P/O 三源只计 1；usage 未到仍占 1；未放置请求单列 1，不落父卡 |
| CAP-02 | pass | O 一卡 runtime 8192 vs scheduler 6144 冲突只令该卡未知，另一卡仍 free=4、汇总 complete=false |
| CAP-03 | pass | P/O scoped release 退休旧来源，双卡 free 由 7 回到 8；单次缺项不清占用 |
| CAP-04 | pass | P ENDED/missing poll 保留 ACTIVE；D 预算冲突明确未知，不截零掩盖；Node UID 互斥阻止整卡/共享重叠 |
| SYNC-RX-01 | pass | P/C 同 ref/revision 内容多进程/并发幂等 |
| SYNC-RX-02 | pass | P 同 revision 异摘要冲突、原终态保留 |
| SYNC-RX-03 | pass | P/C 2 先到、旧 1 无回退、更换 create operation 不复活 |
| SYNC-RX-04 | pass | D 结束原因/事实形态、plan摘要；P 持久不可变 plan/create time 检查；C 精确 Gov 身份 |
| SYNC-RX-05 | pass | P 先绑定后投影可关联、ENDED 不清；C ACK 耐久与重启；未实现退款/K8s 写端口 |
| REL-01 | pass | P/C/O 无 usage 时凭可信实际范围返回 STILL_PRESENT/UNKNOWN/解除 |
| REL-02 | pass | P/O 正向范围解除；摘要绑定 ref/DELETE/Pod 集合/来源事实与水位 |
| REL-03 | pass | P/C 任一实际使用 ACTIVE 返回 STILL_PRESENT；集合聚合不以部分解除替代整体 |
| REL-04 | pass | C 空列表/伪造租户/错误服务拒绝；O 节点失败、陈旧、API 强删和冲突 UNKNOWN |
| REL-05 | pass | ObserveRelease 在 PG read-only snapshot 中执行；无退款、投影终结或 workload 写入 |
| RUN-01 | pass | C TestAll22RPCsMTLSAndRecovery：所有 22 方法正向响应内容及 wrong-service 负向 |
| RUN-02 | pass | C 生产 main SIGTERM/监听关闭；V worker PG readiness/重试/停止，来源未知保持结构化 |
| RUN-03 | pass | C 真实事务写入后提交前杀进程回滚、提交后丢响应重放；P/X 并发绑定/投影收敛 |
| RUN-04 | pass | V、X 与 P/C/O/R 全部 exit=0；没有本地替代执行或降低原断言 |
| HANDOFF-01 | pass | consumer-contract.md 22 RPC/身份/字段/错误/幂等/附件/charge；accelerator-vectors.json |
| HANDOFF-02 | pass | runtime/provider runbooks：版本、配置、显式迁移、恢复、GPU 后续门禁 |
| SHIP-01 | pass | 首个真实提交 7046c4f 已经完整 audit；后续冻结文档/版本边界收口，按相同门禁交付 BOM-only 提交，最终 SHA/无差异/清理详见仓外 final-receipt.txt |
| SHIP-02 | not_verified | 当前无 remote，尚未推送；不从 module 地址猜目标 |
| SHIP-03 | not_verified | 尚无 final-SHA 远端 CI；Fedora pass 不等于 CI |

## 22 RPC 进程覆盖索引

C 的 `TestAll22RPCsMTLSAndRecovery` 对每个下列方法执行实际 mTLS Invoke、响应内容断言和 wrong-service 拒绝，累计覆盖数必须等于 22；独立进程连接同一真实 PG。

| 族 | 已调用方法 |
|---|---|
| Admin（12） | RegisterCluster、ListClusters、CreatePool、ListPools、AdoptSupplyGroup、ListSupplyGroups、SetAdmission、PublishProfile、AdminListProfiles、AdminListBindings、ListDevices、AdminGetCapacity |
| Catalog（5） | ListProfiles、GetProfile、ResolveGpuRequest、GetCapacity、CheckGpuFit |
| Usage（5） | SyncGpuUsage、GetGpuUsage、ListGpuUsages、ListBindings、ObserveRelease |

## 失败保留与修复

- `r3-replay-01.log` fail：测试 Unix socket 路径超长；改为任务 TMPDIR 内短名称，后续回放 pass。
- `r4-runtime-01.log` fail：样例 Protobuf Duration `2m` 非法；改 `120s`，完整回归 pass。
- `r5-recovery-race-01.log` fail：恢复脚本未处理官方 PG 镜像默认用户变量为空；明确默认 postgres，真实还原 pass。故意 migration 除零 exit=3 是 R 的预期负向，不是门禁失败。
- `r5-tools-vuln-01.log` fail：gRPC 1.82.1/pgx 5.7.6 可达漏洞；`r5-lock-order-01.log` 仍发现 gRPC 1.83 分支需 1.83.2。最终 gRPC 1.83.2、pgx 5.9.2，`r5-vuln-fixed-02.log` exit=0、No vulnerabilities found；扫描库时间 2026-09-16T18:00:43Z。
- `r5-patched-candidate-01.log` fail：并发观察/投影死锁，原始 PG 等待图见 `r5-pg-deadlock.log`。统一 cluster→usage→binding 锁顺序，跨 cluster 不错误关联。专项 `go test -race -count=10 ./internal/data -run TestPostgresConstraintsVersionRaceAndConcurrentAssociation` pass；统一 X 再通过。
- `precommit-01.log` fail：补强外键 SQLSTATE 断言时，测试 channel 名遮蔽标准库 errors；改名后重新执行完整提交前门禁，不删除断言。
- `precommit-index-match.log` fail：初始清单包含被 Git 忽略的 `.work/bootstrap-verify.txt`；保留全文件清单，候选清单按门禁既定规则排除 .work/.tmp，逐文件匹配 pass（precommit-index-match-02.log），未覆盖或提交该运行产物。
- `source-audit-01.log` fail：远端 `.tools` 顶层软链接被 Git 视为未跟踪文件，干净工作区门禁正确阻止 BOM；改成被既有规则忽略的真实目录内工具链接，`source-audit-02.log` 完整 audit exit=0，无漏洞/密钥发现，41 个依赖 license evidence 完整，notice hash 一致。

## 固定未验证与边界

`GPU-LIVE-01/02/03/04` 全部 **not_verified**：用户已确认无 GPU 环境；没有真实 F>1 消费/同卡模型推理/K 满 Pending/整卡排他验收。`EXT-01/02/03/04/05` 全部 **not_verified**：真实 Gov 占退额、旁路恢复、owner 封闭创建与完整删除、防绕过和账本联合验收尚未执行。

受控签名、provider 构造回放、测试客户端与 PG 耐久只证明本服务软件合同。没有正式供给被伪造 VERIFIED/OPEN，没有安装驱动/Volcano/HAMi/K8s，没有部署正式服务。多节点完整 NVML 遥测不在本地 socket 读取部署能力内；非本地节点明确不完整。恢复点和日志保留，最终任务资源清理见仓外回执。
