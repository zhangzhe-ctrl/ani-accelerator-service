# ACC-V12-01 验收矩阵

状态：`planned`。执行位置：`fedora`。当前无 GPU 环境。

本文件配合 [实施计划](accelerator-v1.2-implementation.md) 使用。除明确列为发布条件项、GPU 后续项或外部后续项外，以下均为本批必须通过的验收。不执行本地验证，不把本文件的预期结果当作已运行结果。

## 1. 证据规则

- 结果只使用 `pass`、`fail`、`not_verified`。未执行、依赖不可用和证据不足不能填 pass。
- 每项保留 ID、测试层次、预期/实际、源码 SHA 或未提交文件 manifest、远端主机/目录、脱敏命令、退出码、时间、日志引用和限制。
- 必验项使用真实服务实现。PG 项使用 PostgreSQL；TLS 项使用真实握手；RPC 项实际启动服务并通过 gRPC 调用。领域单测不能覆盖掉真实依赖验收。
- provider 回放注明样本来源、版本、单位、F 和时间。构造样本标为构造样本，不称硬件采集；一次 HTTP 200 或 Pod Running 不证明模型可用。
- 用测试装配注入有效/无效基线证据，以覆盖发布/OPEN/Resolve 分支；只存在于隔离测试库。生产构建不得包含通用绕过开关或通过 RPC 自报 VERIFIED 的通道。
- 原 Python 48 项模型测试保留为回归参考，不代替 Go、PG、mTLS、provider 或 GPU 验收。

## 2. 22 RPC 的进程级覆盖

每个 RPC 至少覆盖一个正向场景、一个适用的权限/业务负向场景，并映射到后文 ID。通过请求及响应内容断言行为；仅确认方法注册或状态码不足以通过。

| 服务/族 | RPC | 关联验收 |
|---|---|---|
| Admin：目录，4 个 | RegisterCluster、ListClusters、CreatePool、ListPools | DB-01～06、AUTH-01～06、CAT-01、CAT-06 |
| Admin：供给，5 个 | AdoptSupplyGroup、ListSupplyGroups、SetAdmission、PublishProfile、AdminListProfiles | CAT-02～05、UNIT-*、AUTH-* |
| Admin：观测，3 个 | AdminListBindings、ListDevices、AdminGetCapacity | OBS-*、CAP-*、AUTH-03/05 |
| Catalog，5 个 | ListProfiles、GetProfile、ResolveGpuRequest、GetCapacity、CheckGpuFit | UNIT-*、CAT-*、CAP-*、AUTH-04/05 |
| Usage：投影/查询，4 个 | SyncGpuUsage、GetGpuUsage、ListGpuUsages、ListBindings | SYNC-RX-*、DB-*、AUTH-* |
| Usage：释放，1 个 | ObserveRelease | REL-*、OBS-*、AUTH-02/04 |

总数必须是 Admin 12 + Catalog 5 + Usage 5 = 22。实际字段约束以活跃 Proto 为准。没有实现的方法保留显式错误，不能填成功空响应；本批完成时不能还有未实现业务方法。

## 3. 基线、单位与计划

| ID | 必须证明 |
|---|---|
| BASE-01 | 记录本地源文件清单/HEAD/dirty 状态，Fedora 收到相同候选；现有用户文件未覆盖，未用旧历史日志替代新验证 |
| BASE-02 | Fedora 任务目录、PG、缓存、端口和证据隔离；所有生成/格式化/测试/进程均在远端 |
| UNIT-01 | m=6144、F=1/256/1024 时 q=6144/24/6，输出单设备计量始终为 6144 MiB |
| UNIT-02 | m=6144、F=10 时发布/解析拒绝，错误稳定；本服务不发生目录副作用，不声称已验证 Gov 未扣额 |
| UNIT-03 | 多卡容量逐卡 floor 后求和，不跨卡拼余量 |
| UNIT-04 | BLOCKS/MIB/BYTES 恰好归一化一次，来源单位及 F 可追溯 |
| UNIT-05 | F=0、未知、组内不一致或基线不完整时拒绝新供给/Resolve，不转为普通容量不足 |
| UNIT-06 | 历史 plan/观测样本使用原 factor；F 漂移不得重解释旧数据 |
| UNIT-07 | int64 乘加/provider 字段范围、负数、零值枚举、重复键、非法 container_name、devices_per_replica!=1、超合同副本上限均明确拒绝；生成片段仅表达指定 GPU 容器需求，与 container_name 一致 |
| UNIT-08 | 规范摘要不受字段/map输入顺序、观察时间和状态扰动；实际规格/计量/受管渲染字段改变必然被识别；plan 不含未知凭据或动态 Pod 身份 |

## 4. PostgreSQL、权限与目录

| ID | 必须证明 |
|---|---|
| DB-01 | 正式迁移由显式命令在隔离 PostgreSQL 执行；服务角色无 DDL 权限，服务启动不迁移/播种 |
| DB-02 | 目录/规格/投影/绑定必要记录真实持久化，ACK 后杀进程重启不丢，配置和读模型可恢复 |
| DB-03 | 管理幂等记录与业务写原子提交；同 actor+方法+key 并发只产生一个结果，同键异报文冲突 |
| DB-04 | Node UID 唯一归属、资源引用/版本约束和租户保持约束生效；失败事务不留下半条业务结果 |
| DB-05 | 真实 PG 并发写、重复同步和版本竞争稳定收敛；不只在串行内存模型中通过 |
| DB-06 | 测试库迁移前恢复点、迁移失败处理、进程重启及数据恢复步骤可复现；不能清空重建冒充数据恢复 |
| AUTH-01 | 无证书、不受信 CA、错误服务身份及配置缺失均不能调用受限业务方法，不明文降级/skipVerify |
| AUTH-02 | SyncGpuUsage 仅精确 Gov 身份；ObserveRelease 仅对应支持 owner；同一 CA 的其他证书不自动获得权限 |
| AUTH-03 | AdminRead/AdminMutation 验证 Gov 委托和允许动作；平台读取不依赖 tenant=0 或虚构 UUID |
| AUTH-04 | tenant A 不能读取/引用 tenant B 的 usage/binding；伪造请求 actor/tenant/owner 不能绕过直接调用者权限；无可信归属时不从标签猜租户 |
| AUTH-05 | 租户响应无设备 UUID、节点名、他租户数据或原始敏感证据；管理员与租户视图分开；分页游标不能跨主体/筛选复用 |
| AUTH-06 | 未授权查询/写入无副作用；密钥/token 不入日志、数据库或错误；受管 connection_ref 不是任意客户端地址/凭据入口 |
| CAT-01 | RegisterCluster/CreatePool/ListClusters/ListPools 返回持久数据及正确父子范围；登记不创建 provider 设备 |
| CAT-02 | Adopt 核对既有配置且默认 CLOSED；不写 Node、ConfigMap，不重启插件；Node 重建不继承旧 UID 资格 |
| CAT-03 | 发布/OPEN 校验组成员、受管标签、型号、F、splitcount、版本、baseline digest 和受控消费证据；管理员自报通过无效 |
| CAT-04 | 测试注入的有效证据可走完 Adopt→标签核对→发布→OPEN→Resolve；正式构建/配置没有测试绕过入口 |
| CAT-05 | 规格及已解析计划不可变；CLOSED 仅拒绝新 Resolve；不能改写既有 plan 或宣称停止在途调度 |
| CAT-06 | 授权范围内查询、稳定分页、版本不存在/不可见和非法 shape 的错误正确；满容量/未知与配置非法区分 |

GpuRequest 不含完整 workload/Pod 输入。本批校验请求和生成片段所能表达的约束；owner 后续注入额外 GPU init/sidecar/container 或副本的实际防绕过属于 EXT-04，本批在合同中明确禁止条件和样例。

## 5. provider、绑定与容量

| ID | 必须证明 |
|---|---|
| OBS-01 | 有可执行的 provider reader/normalizer 及明确来源映射，不止接口或内存假数据；固定支持版本、来源字段、权限、身份、单位与证据等级 |
| OBS-02 | 来源回放覆盖 Node/调度/分配/运行时的实际使用路径；每个样本有来源类型与摘要；构造样本不标实际采集 |
| OBS-03 | 物理设备身份与平台 UUID、节点内 index、PodResources 伪 ID、显存块区分；不同节点相同 index 不合并 |
| OBS-04 | 实际绑定键为 cluster+Pod UID+container+physical device；不同 Pod UID 不合并，同使用多来源只计一次 |
| OBS-05 | 来源超时、读取失败、缺项、陈旧或失联时保留可信时间和 UNKNOWN；接收时间不能冒充观测时间，无证据不能标 RELEASE_CONFIRMED |
| OBS-06 | 采集重连/进程重启从持久水位和事实恢复；旧证据退出有覆盖范围及正向依据，查询不触发配额/工作负载写入 |
| CAP-01 | 同一实际使用来自三来源只占一份，未关联/未纳管使用仍计容量 |
| CAP-02 | 单设备证据矛盾只让该设备未知，其他设备正常；汇总 complete=false，未知的 0 不等于全池耗尽 |
| CAP-03 | 可信解除退休相应旧调度/分配事实，容量恢复；不能保留旧 true 永久占用，也不能任一 false 清全部 |
| CAP-04 | usage 缺失或 ENDED 不清实际 Binding；共享/整卡集合不重叠，份数服从安全预算及 K；预算冲突不以截零掩盖 |

本节 pass 的含义是生产适配代码在声明来源和受控回放下满足合同，不是对真实 GPU/Kubelet/插件运行行为的认证。

## 6. 使用投影、释放观察与运行恢复

| ID | 必须证明 |
|---|---|
| SYNC-RX-01 | 真实 PG 中同 ref/revision/内容重放幂等，并发调用结果一致 |
| SYNC-RX-02 | 同 revision 异摘要拒绝并保留原数据；错误不得覆盖现场 |
| SYNC-RX-03 | revision 2 先到可建终态；后来的合法 1 不回退，换 create_operation 不复活 |
| SYNC-RX-04 | ref、不可变 plan、结束原因和事实引用校验完整；未知 owner/缺失依据拒绝 |
| SYNC-RX-05 | 投影接纳后重启仍在；ENDED 与实际绑定独立，不执行退款/K8s 写；绑定先到最终可核验关联 |
| REL-01 | 合法 owner 在 usage 投影缺失时仍能查询对应实际范围，不凭缺行拒绝或假定释放 |
| REL-02 | 受控正向解除证据得到 ALLOCATION_RELEASED，摘要对应明确资源/Pod范围/来源水位，不等于 owner 已关闭 |
| REL-03 | 任一相关使用仍存在返回 STILL_PRESENT；部分 Pod 清理不能得到整范围已解除 |
| REL-04 | 空列表、错误 tenant/owner、节点失联、读取失败、单次缺项、强删 API 对象和证据冲突分别被拒绝或返回 UNKNOWN，不假释放 |
| REL-05 | ObserveRelease 只读，不推进关闭、不结束投影、不通知 Gov 退款；未曾外部创建的关闭事实仍属于 owner |
| RUN-01 | 22 RPC 均经实际 gRPC+mTLS 进程调用；正负向覆盖可追溯，无成功占位接口 |
| RUN-02 | 服务启动/停止、依赖 readiness、采集重试和优雅退出行为正确；来源未知通过结构化状态表达，不用 liveness 冒充 GPU 可用 |
| RUN-03 | 真实 PG 提交前/后进程中断、响应丢失后重放、并发投影/绑定更新测试保持幂等和可恢复，不清库规避问题 |
| RUN-04 | Fedora `make verify`、受影响并发路径 race 和新增有必要的集成入口通过；没有本地替代执行或通过修改断言隐藏失败 |

## 7. 交付和发布

| ID | 必须证明 |
|---|---|
| HANDOFF-01 | 合同文档说明全部 RPC 调用身份/字段/错误/幂等、plan/hash样例、Gov→Inference附件、ObserveRelease限制及原 charge 删除完成释放责任 |
| HANDOFF-02 | 运维文档说明支持 provider 版本、配置、显式迁移、恢复、证据含义和真实 GPU 后续门禁；无需阅读未完成的 Inference 源码即可接入 |
| SHIP-01 | 最终受测源码/生成文件与本地提交一致；Fedora final-SHA verify/audit通过，BOM可重现且无差异，测试资源按任务范围清理 |
| SHIP-02 | 条件项：有明确 remote/目标分支时，本地非强制推送成功且远端SHA一致；目标缺失记录 not_verified并请求具体目标，不猜仓库 |
| SHIP-03 | 条件项：CI若可用必须查最终SHA结果，失败不能忽略；CI不存在/无可用权限则记not_verified，不能用Fedora门禁冒充CI |

本批软件完成要求全部必验项 pass。SHIP-02/03 按真实条件单独记录：已配置可用目标时需完成发布；缺失目标不能声称“交付推送完成”，但不抹掉独立软件验收结论。

## 8. 后续真实 GPU：本批固定 not_verified

| ID | 后续场景 | 本批原因 |
|---|---|---|
| GPU-LIVE-01 | F>1，plan→Pod→实际分配→运行显存限制单位一致 | 无 GPU 环境 |
| GPU-LIVE-02 | 同父卡至少两个独立模型实例真实加载和推理 | 无 GPU 环境 |
| GPU-LIVE-03 | 实际 K 满后新增 Pending，不越过安全预算 | 无 GPU 环境 |
| GPU-LIVE-04 | 整卡排他、量化尾差及正常解除后真实容量恢复 | 无 GPU 环境 |

不得用回放、注入测试证据、CPU推理、sleep容器、已生成 YAML 或 HTTP存活升级这些结果。后续获得硬件和集群授权后再执行，不在本批擅自安装或占用集群。

## 9. 外部后续项及原设计验收映射

| ID | 外部责任 | 原设计对应 |
|---|---|---|
| EXT-01 | Gov 一次占额、幂等重放复用计划、完整 charge、正式目录及 enforcement | GOV-01～06 |
| EXT-02 | Gov 同步旁路故障不阻塞创建、从原账本恢复、旧操作释放后重扫 | SYNC-01/03/04 |
| EXT-03 | 原子未发撤销、DELETE先到、owner关闭创建、完整清理及可靠全量退额 | DEL-01～05 |
| EXT-04 | 正式入口权限、owner额外GPU/副本防绕过、容量满但有额度可提交等待、BFF页面 | API-01～03 的跨服务部分 |
| EXT-05 | 实际 Gov 账本与 GPU 运行限制一致 | REAL-01 的账本部分 |

原 UNIT-01～06、CAP-01～04 的服务内规则纳入本批；SYNC-02 接收侧由 SYNC-RX-* 验收；API-01 本服务隔离由 AUTH-* 验收。原 ID 包含外部行为时，不因局部通过而把原整项标 pass。

Gov/owner 测试客户端只证明 Accelerator 对指定身份和报文的接纳、拒绝、持久化、隔离、观测与恢复。独立工作负载未来可以证明指定硬件组合能消费冻结 plan；两者均不证明外部服务正式集成完成。
