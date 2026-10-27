# GOV-ACC-V12-01 无 GPU 软件验收矩阵

> 合同更新（2026-10-27）：本文中对外 `ObserveRelease` 的要求已由[接口下线说明](../releases/2026-10-27-observe-release-retirement.md)取代（superseded）；其余历史计划与结果保留，不代表本次消费方或环境验收。

状态：**获准执行，尚无本批通过结论**。配套：[实施计划](governance-accelerator-v1.2-plan.md)。下面“必须证明”是验收要求，不是结果声明。原 84 项保留，追加第 9 节数据层门禁。每个 ID 单独登记 `pass/fail/not_verified`、实际命令、退出码、两仓版本、证据路径及来源层 A/B/C。

所有生成、格式化、依赖、构建、测试、审计、PG、进程及证书操作仅在 Fedora；本地不运行这些检查。没有真实 GPU、没有真实 Inference/其他 owner。本批必验 BASE～DOC 不因该环境限制跳过；SHIP 按发布授权和可用条件记录；LIVE/OWNER固定not_verified。

## 1. 输入、构建与环境

| ID | 必须证明 |
|---|---|
| BASE-01 | 冻结两仓SHA/完整source manifest、AGENTS/合同/向量摘要；工作区变化不被覆盖；本地待提交源码与Fedora受测候选逐文件一致 |
| BASE-02 | Fedora真实hostname、工具/镜像digest、任务目录/锁/端口/PG角色/缓存/证书隔离；本地没有执行运行类工作 |
| BASE-03 | GOWORK=off固定模块消费、无兄弟replace/internal导入；独立缓存能取得对应API且校验和一致 |
| BASE-04 | 固定Buf/Ent/Go/gofmt等工具；定向生成再运行无diff；正式与quota_lab构建均通过；没有借生成改无关领域 |

## 2. 配额目录、权限与 BFF

| ID | 必须证明 |
|---|---|
| CAT-01 | 两个正式GPU code/unit/CONCURRENT正确；显式数据迁移重复执行不改既有政策/余额；旧gpu.count保持实验口径 |
| CAT-02 | 旧enum/tag不变；LEGACY/LAB/NOT_ENABLED/ENFORCED准确；未知code不默认LAB；能力按受控owner/action派生而非前端字段 |
| CAT-03 | 正式无owner adapter时新建在Resolve/占额前拒绝、目录NOT_ENABLED；下游短时故障不能把强制计量改为旁路 |
| CAT-04 | 共享只扣MiB、整卡只扣张数；1～16副本与单GPU容器/设备边界；共享+整卡双计、非法粒度、零/负数和溢出拒绝 |
| AUTH-01 | 真实Governance→Accelerator mTLS；无证书、错CA、同CA错URI、错误server身份和缺必要配置失败；无明文/insecure回退 |
| AUTH-02 | JWT和可信Principal→持久resource UUID→下游context/metadata一致；请求伪造tenant/actor/grant不生效；tenant0不是全租户 |
| AUTH-03 | 20个委托RPC的下游actor/action/tenant/cluster必要grant缺失拒绝；平台身份与租户读取分离；Gov本地角色不代替下游授权。Sync另按精确Gov服务身份验证，用户grant撤销/新建关闭不阻止原投影收敛 |
| AUTH-04 | 跨租户读取原CREATE/usage/binding/delete与FK引用负向；缺少与不可见统一404；后台扫描/重试保持原tenant |
| AUTH-05 | 新模块、Ent/schema CHECK、API目录、套餐及Casbin/角色同步一致；dry-run不写；登记后未授权仍拒绝；策略刷新有实际调用证据 |
| AUTH-06 | owner→Gov按现有DNS SAN注册映射；错owner/错CA/无证书及同时命中不同owner拒绝，同owner多SAN按实际合同验证；与Accelerator URI合同分别验证；测试身份不进入正式map |
| AUTH-07 | 新GPU BFF按本批JWT范围处理；AK/SK不能因沿用Network自动取得GPU权限；既有Network/Key定向回归通过 |
| API-01 | 12个管理BFF逐个真实调用及响应内容断言；管理写幂等/版本竞争/错误映射；不是只检查HTTP200 |
| API-02 | 全部租户BFF逐个真实调用；GetProfile具体版本、usage/binding由本库可信原ref定位；任意owner过滤不扩大支持范围 |
| API-03 | Preview调用Resolve/Fit/账户且operation/charge/余额不变；满容量但配额足、容量未知、配置非法分开；无owner时提示NOT_ENABLED |
| API-04 | 使用B层审核观察fixture经生产SaveObservation形成非空绑定，含可检出的敏感字段；租户DTO去掉plan/runtime/source_fact/物理UUID/Pod证据，管理侧按授权可见；不能用空列表证明脱敏 |
| API-05 | page_size边界、opaque token、稳定排序；换tenant/actor/filter/action/授权范围的游标拒绝；不伪造total |
| API-06 | 参数、认证、权限、NotFound、冲突、前置、超时与不可用状态/reason符合合同；错误与日志无DSN/密钥/跨租户内容 |
| API-07 | 自身quota-accounts仅Principal租户，不接受任意tenant；降额显示over_limit、available不负；既有管理员账户路径权限不变 |

## 3. 摘要、受理和投递

| ID | 必须证明 |
|---|---|
| CAN-01 | Governance按acc-c14n-v1匹配冻结baseline/spec/plan/projection向量的规范字节和摘要；protojson直接hash等错误实现有负向样例 |
| CAN-02 | Unicode/转义/null/整数上界/无浮点/受管key排序与重复拒绝；F=1/256/1024、6144MiB单位一致；F=10拒绝 |
| CAN-03 | 原输入hash与冻结plan摘要分开；改变业务payload/GPU语义必识别；观察时间/重试时间/新UUID不改变原请求身份 |
| CREATE-01 | 独立测试owner下真实Resolve→Governance单PG事务保存operation+完整charges+一次扣额→事务外Dispatch；失败事务不发RPC |
| CREATE-02 | 同key同内容重放读取原plan，不访问已停机/已CLOSED/已变F的Accelerator；临时撤下adapter/关闭新建不阻止有权限的历史重放；当前权限先校验；新请求仍受新政策约束 |
| CREATE-03 | 同key异内容冲突；双进程至少12个同key竞争只有一个原快照和一次扣额；并发分别Resolve后不覆盖获胜plan |
| CREATE-04 | 多合法配额维度原子扣额，任一不足全部回滚；数据库时间到期边界、换套餐/降额竞争不超扣；未授权重放不泄漏 |
| CREATE-05 | 原charge全集合和GPU子集按code/id校验，顺序变化无影响；漏项/多项/重复/错单位拒绝；不构造整卡共享双计假合法向量 |
| CREATE-06 | 结构化actor保持原类型/ID；附件完整plan/ref/hash/业务摘要/计量版本一致；不能让用户自报plan或charge成为权威 |
| CREATE-07 | owner ACK错误operation/resource/accepted=false/非法报文不能记ACKED；永久错误暂停、传输不确定保留占额并同ID/报文重试 |
| CREATE-08 | 容量0/UNKNOWN但合法plan+有额度+测试owner可用仍持久受理等待；没有二次reserved→allocated扣额 |

## 4. 删除、释放与投影

| ID | 必须证明 |
|---|---|
| DELETE-01 | QUEUED且attempt_count=0：取消、全集合退款、DELETE幂等结果同事务；返回CANCELED_BEFORE_DISPATCH且无假owner ACK |
| DELETE-02 | 领取与取消双进程竞争无“已退额又首次创建”；只有一个合法分支，锁序/重放稳定 |
| DELETE-03 | 任何尝试/UNKNOWN/ACKED均不本地退款；持久原DELETE operation并传原plan/完整charges，QUEUED_FOR_OWNER不是完成 |
| DELETE-04 | 删除worker原集合读取失败绝不调用owner；恢复后完整发送；含两种合法独立计量维度的操作证明不是只取First |
| DELETE-05 | 相同DELETE key同请求稳定结果，异内容冲突；不同key仍同原CREATE；原CREATE已CANCELED_UNSENT时新key仍返回本地取消且不重退/不调owner；测试owner DELETE先到留关闭墓碑，迟到CREATE不产生新试验分配 |
| DELETE-06 | 跨tenant/owner/资源/原CREATE、空charge、错plan拒绝；本地取消崩溃前后没有半条acceptance/孤立dispatch |
| RELEASE-01 | 真实mTLS调用既有ReportQuotaRelease；按原charge归属和证书owner校验；请求不能另行声明可信tenant |
| RELEASE-02 | 含GPU item的通知要求完整GPU子集/全量累计及本库DELETE关联；少项、重复code/id、错原CREATE、无DELETE/错归属、超量、部分GPU释放拒绝；正确释放早于DELETE ACK可接收；纯非GPU累计通知不结束GPU |
| RELEASE-03 | 同事件同内容幂等、异内容冲突；新事件重复累计值delta=0；乱序通知不负退、不超退；向量失败全部回滚 |
| RELEASE-04 | owner通知提交前崩溃、提交后响应丢失、Gov不可用时重试：原事件/报文稳定，最终余额只减少一次 |
| RELEASE-05 | 主动发送可信owner但无DELETE意图的全量报文，验证生产校验拒绝且余额不变；DELETE ACK/NotFound/超时等本身不触发退款。测试owner只在全部合同清理后通知，不能把fixture自律当生产鉴权证据 |
| RELEASE-06 | tenant到期/降额或新建入口关闭后可信原操作退款仍可执行；同步失败不阻塞退款；测试owner持久未曾执行/已关闭分支与空Pod假释放分开 |
| SYNC-01 | 原CREATE提交后、同步行创建前杀进程，恢复从原canonical+charges补齐；同步表不是Occupy/Release关键链依赖 |
| SYNC-02 | 稳定分页完整扫描可发现旧operation后来释放；轮次结束重新扫描；不只扫描新增时间戳；跨租户不漏/串记录 |
| SYNC-03 | 部分GPU退款/非GPU退款/DELETE ACK/空集合不产生ENDED；安全本地取消或可信全量GPU释放才revision2，后者不等待无关非GPU charge归零；同revision重建payload逐字一致 |
| SYNC-04 | DECLARED/ENDED含相同ref/plan/原CREATE时间；2先到、晚1、重复2不复活；同resource换create_operation及同revision异内容拒绝 |
| SYNC-05 | 双worker租约接管、RPC超时、旧generation和晚revision1 ACK不能覆盖新目标/确认未发revision2；不改业务attempt_count |
| SYNC-06 | 同步失败/冲突保存原证据并按分类重试或阻断该条；没有新占额/退款/owner调用；创建投递可独立继续 |
| SYNC-07 | B层叠加测试装配以审核观察fixture经SaveObservation先写非空binding，随后真实Gov同步可关联；ENDED仍有live binding时不清占用；记录fixture与overlay来源，不称真实采集；投影ACK仅持久接收 |

## 5. 数据库、进程、provider 与恢复

| ID | 必须证明 |
|---|---|
| DB-01 | 旧fixture显式Atlas升级及版本数据脚本；运行角色拒绝schema/TEMP DDL；启动不迁移、不播种 |
| DB-02 | 新表tenant非空/复合FK/唯一键/revision CHECK真实PG生效；跨tenant引用失败；旧quota/Network/套餐数据保持 |
| DB-03 | 占额、取消/删除、退款、同步CAS分别有提交前杀进程和提交后丢响应案例；无半写、丢意图或双重扣退 |
| DB-04 | 填充过的数据pg_dump→另库restore，业务表逐行/规范摘要比较，含新表/原charges/receipts；恢复worker后正确收敛 |
| DB-05 | 注入失败迁移完整回滚，原数据保持；明确软件回退版本兼容/不兼容边界；不自动DROP带业务的新表 |
| PROC-01 | 真实独立Gov/Accelerator/测试owner进程，各自独立数据库/凭据；不能以内存fake替代本组 |
| PROC-02 | worker批量领取尾部接近租约过期、due-time变化、两个worker和迟到回写；必要修复有实际竞争证据；无RPC持有PG事务锁 |
| PROC-03 | PG/下游临时中断后恢复，readiness与错误准确；SIGTERM关闭监听/worker，任务进程可重启不丢持久事实 |
| PROC-04 | 受影响路径race和并发场景通过；死锁/超时保留现场并修复；不靠放宽断言/减少并发把失败变绿 |
| PROVIDER-01 | 固定生产reader的TLS HTTP/Unix gRPC回放真实执行；证据注明构造来源、版本和样本摘要，不标硬件实采 |
| PROVIDER-02 | binding先到、三源去重、局部冲突、stale/失联/来源缺失保持UNKNOWN；只在可信作用域正向解除后恢复容量 |
| PROVIDER-03 | ObserveRelease错身份/空范围/不完整历史范围/未知在途副作用不能成为业务退款证明；owner独立判断完整关闭 |
| BOUND-01 | 正式Governance没有测试owner/故障端口/实验路由；生产OpenAPI和镜像无测试业务接口；缺少owner仍NOT_ENABLED |
| BOUND-02 | Accelerator生产二进制使用合法受控只读provider输入且无消费证明，Publish实际到达证明门槛并失败且不落profile/不变VERIFIED；OPEN/Resolve按缺profile的真实前置错误拒绝，不冒充消费证明复核。Adopt仍CLOSED/NOT_VERIFIED，无测试消费私钥/skipVerification |
| BOUND-03 | 正向Resolve仅隔离测试装配，经真实管理RPC和测试外部证据端口；独立PG/CA，与生产边界层不混用；原版helper和新增观察叠加源分别记录完整manifest；不称纯生产硬件验收 |
| BOUND-04 | quota_lab保持编译隔离；既有lab通用账本回归、Network/鉴权受影响测试通过；未修改兄弟仓库生产实现 |

## 6. 文档和交付

| ID | 必须证明 |
|---|---|
| DOC-01 | 实施完成后按最终Proto/配置写通用owner指南，包含版本、单owner现状、新owner准入、创建/关闭/释放/恢复、字段/错误/向量及责任表 |
| DOC-02 | 无虚构生产RPC、无“改名字即可接入”宣称；DNS/URI身份分别说明；示例摘要/合法失败向量在Fedora校验 |
| DOC-03 | 接口登记/OpenAPI/运行手册一致；计划项、实现、fixture通过、真实owner/GPU未验分开；不得把旧报告复制为本批pass |
| SHIP-01 | 提交前精确候选Fedora verify-gpu和audit通过，生成无漂移；最终SHA有同源码门禁回执；工作区/分支/改动范围清楚 |
| SHIP-02 | 条件：有发布授权及明确remote/branch才非强推；核对远端SHA。无目标在发布前问一次，无授权不发布 |
| SHIP-03 | 条件：查最终SHA的真实CI结论和覆盖范围；失败修复，缺CI/权限记not_verified；不拿远端本地门禁冒充CI |
| SHIP-04 | 只清任务进程/容器/角色/证书/缓存；备份和脱敏日志保留；恢复入口可定位，无共享环境破坏 |

## 7. 本批固定不验证

| ID | 结果边界 |
|---|---|
| OWNER-01 | 真实Inference或其他owner业务API、正式adapter、实际持久接受与执行：not_verified |
| OWNER-02 | 真实owner完整渲染防绕过、创建封闭、历史/替换Pod追踪、删除与可靠通知：not_verified |
| OWNER-03 | Accelerator多owner身份/公钥/查询隔离扩展及第二服务正式接入：not_verified，另批实现 |
| LIVE-01 | F>1的plan→Pod→实际分配→运行显存限制单位：not_verified |
| LIVE-02 | 同父卡两个独立模型真实加载及推理：not_verified |
| LIVE-03 | 真实K满后新增Pending且不越安全预算：not_verified |
| LIVE-04 | 整卡排他、量化尾差、正常解除后真实容量恢复：not_verified |
| LIVE-05 | 实际Gov账本、真实owner与GPU运行限制/全量释放一致：not_verified |
| DEPLOY-01 | 共享集群/生产发布、用户流量切换、真实供给OPEN：not_verified |

上表不能被测试owner、签名fixture、HTTP存活、CPU推理、sleep容器、生成YAML或provider回放升级为pass。

## 8. 结果记录格式与完成门槛

每个场景记录至少包含：`id/status/layer/governance_sha/accelerator_sha/source_manifest/command/exit_code/started_at/finished_at/evidence/assertions/limitations`。受控故障预期非零退出和断言失败要区分；修复前失败记录保留。

软件必验全部pass才可写“Governance适配及无GPU软件合同完成”。任一必验fail或not_verified都应列为实际缺口；真实blocker只影响关联ID，不阻止独立工作。SHIP条件项、OWNER/LIVE/DEPLOY结论单列，不用一个总绿状态掩盖它们。

## 9. 数据层整改追加门禁

| ID | 必须证明 |
|---|---|
| RLS-01 | 两仓目标业务表 relrowsecurity=false、relforcerowsecurity=false、pg_policy 无残留；显式迁移不由服务启动执行 |
| RLS-02 | 真实运行角色非 superuser、非 owner、无 BYPASSRLS/DDL/TEMP；禁止 tenant GUC、SET ROLE/row_security 代替隔离 |
| TENANT-01 | 租户列/业务身份/必需引用非 NULL；复合 FK/唯一键、JSON tenant/ref 与关系一致，NULL 绕过反例拒绝 |
| TENANT-02 | 已知他租户 UUID、相同业务键、外键、幂等、游标、更新/删除/冲突及恢复均有真实受限角色负向 |
| TENANT-03 | 全局候选扫描独立命名、限权、有界、返回 tenant；领取/读取/锁/ACK/退款/同步/CAS 保留 tenant；普通业务无通配入口 |
| TENANT-04 | 远端隔离副本删除关键查询 tenant 条件后，相应验收确实失败；变异不进入交付代码 |
| SQLC-01 | Acc 全部生产数据库查询和 Gov 本批 GPU + 完整通用配额/投递/删除/释放均由固定 sqlc 生成 pgx查询执行，无同账本 Ent 旧路径 |
| SQLC-02 | 原子操作只使用同一 pgx.Tx，相关 tenant/plan/account/operation/charge/receipt 查询锁在同事务；网络调用在事务外 |
| SQLC-03 | Acc migrations、Gov Ent/Atlas 权威结果供 sqlc，无第二份手写 schema；记录 schema/query/config/generated 摘要及固定工具版本/校验和 |
| SQLC-04 | verify 与 CI 执行 sqlc compile、generate 无差异、生产 Go 手写 SQL 扫描；只豁免可验证可重生的 sqlc目录 |
| SQLC-05 | 原非GPU累计释放/通用quota/quota_lab/套餐与受影响Network鉴权回归共用新实现，旧fixture升级/失败回滚/填充dump恢复保持余额幂等receipt语义 |

以上全部属于软件必验，不可用旧 PG/CI 记录或 sqlc 配置存在替代实际验证。
