# Accelerator v1.2 接入合同（ACC-V12-01）

范围为本仓 22 个内部 gRPC RPC。下列约束是本批实施合同，不表示 Governance/Inference 已接入。以 `api/protos/accelerator/` 为字段/tag 来源。不得依赖来源设计文件的旧路径或历史验证结果。

## 身份、委托和可见范围

业务监听要求 TLS 1.3、受管 CA 验证和客户端证书；直接身份只接受证书唯一 URI SAN 的精确匹配。默认配置登记 `spiffe://ani.internal/service/ani-governance`、`spiffe://ani.internal/service/ani-inference`，部署时与真实证书颁发合同核对。同 CA 的其他证书无权限。禁止 Bearer 穿透、CN 回退、前缀匹配、明文回退和 skipVerify。

Gov 管理/租户调用携带单值 metadata：`x-ani-action`（RPC 短方法名）、`x-ani-actor-type`、`x-ani-actor-id`；租户调用再携带 `x-ani-tenant-id`（资源租户 UUID）。这些值必须与请求 `context.actor/tenant_id` 一致，并命中服务启动配置中该 actor 的精确动作与 tenant/cluster grant。平台 grant 不含 tenant，可以明确配置 cluster `*`；租户 grant 必须列出准确 cluster UUID，不接受 `*`、`0` 或伪造 tenant。配置改动须由部署配置流程完成，不开放 RPC 自授权限。

`SyncGpuUsage` 只允许精确 Gov 身份；消息中的 owner 固定登记为 `ani-inference` 或 `ani-resource-service`，其他值拒绝。Resource 实例由 Gov 同步原账投影，没有新增 Resource 直接调用 `ObserveRelease` 的授权。`ObserveRelease` 仍只允许精确 Inference 身份，并携带 `x-ani-tenant-id` 与 ref 一致。释放核对还要求实际 Pod 范围的可信 owner 断言，单靠请求自报 tenant/owner 不成立。

| RPC | 直接调用者/委托 | 关键输入来源与行为 |
|---|---|---|
| RegisterCluster | Gov / 平台 | mutation、名称、已在配置登记的 connection_ref；不接受地址或凭据 |
| ListClusters | Gov / 平台 | AdminRead；按配置 cluster 范围分页 |
| CreatePool | Gov / 平台 | mutation、已登记 cluster UUID、名称 |
| ListPools | Gov / 平台 | cluster UUID；不跨父范围 |
| AdoptSupplyGroup | Gov / 平台 | pool、固定 mode、实际 Node UID/name、型号、baseline、queue；核对已有安装，CLOSED/NOT_VERIFIED 起步 |
| ListSupplyGroups | Gov / 平台 | pool UUID |
| SetAdmission | Gov / 平台 | group、expected_version、OPEN/CLOSED、审计 reason；OPEN 复核配置与签名消费证明 |
| PublishProfile | Gov / 平台 | 固定 spec、expected_group_version、受控 verification_ref；每组首期版本 1 不可变 |
| AdminListProfiles | Gov / 平台 | cluster UUID；独立管理接口，无 tenant |
| AdminListBindings | Gov / 平台 | cluster、可选同集群 device；包括未关联实际占用 |
| ListDevices | Gov / 平台 | cluster、可选同集群 group；物理身份仅管理侧 |
| AdminGetCapacity | Gov / 平台 | profile、具体 version、include_devices |
| ListProfiles | Gov / 租户 | 已授权 cluster、可选 mode；仅已发布项 |
| GetProfile | Gov / 租户 | profile UUID、具体 version；不可见与不存在均 NotFound |
| ResolveGpuRequest | Gov / 租户 | GpuRequest；受管静态计划，不占额、不锁库存 |
| GetCapacity | Gov / 租户 | profile/version；仅汇总，devices 恒空 |
| CheckGpuFit | Gov / 租户 | GpuRequest；估计/等待/未知，不替代配额或调度 |
| SyncGpuUsage | 精确 Gov | 持久原 CREATE 与原账本事实派生投影，允许结束先到 |
| GetGpuUsage | Gov / 租户 | 完整 ref 且 tenant 与委托相同；缺投影为 USAGE_PROJECTION_NOT_READY |
| ListGpuUsages | Gov / 租户 | 可选 owner/state；tenant 谓词不可省略 |
| ListBindings | Gov / 租户 | 完整 ref；仅已验证关联，不从普通标签推租户 |
| ObserveRelease | 精确 Inference | 原 ref、明确 DELETE UUID、非空完整实际 Pod 范围；只读观察 |

租户 Binding 响应删除物理 device UUID、Pod namespace/name/UID、原始 evidence_ref、baseline 证据摘要和原始水位；保留计量、状态、容器名与完整度。租户投影查询隐藏 `source_fact_ref`；这是查询投影，不可把脱敏查询结果当成原同步报文重发。管理读取与租户公开 BFF DTO 是不同合同。页面业务列表、模型 readiness、创建完成状态由 Inference 提供。

分页 size=0 默认 50，最大 200；游标由持久 cursor key 做 HMAC-SHA256，绑定调用主体、精确动作、tenant/cluster 范围、过滤条件和排序。它不是跨页数据库快照，改变条件/身份必须从首页重新查询。重启/多副本须使用同一受管 cursor key。

ListProfiles/AdminListProfiles 的响应 scopes 与本页 items 同序一一对应，包含 profile_id、profile_version、spec_digest、cluster_id、pool_id。归属来自同一只读事务中的 Profile→Group→Pool 关系；未知/冲突不返回有效目录。scopes 是目录元数据，不加入 GpuProfile 或完整 plan，也不改变租户委托授权。

## 规格、计划与计量

owner 软件合同上限为 16 个固定副本；不声称真实 Inference 已通过该能力验证。每 Pod 一个指定 GPU 容器；WHOLE_EXCLUSIVE 的 `devices_per_replica` 为正uint32整数，且不超过 Profile 的正整数max_devices_per_replica；不另写死设备数量上限。SHARED_FIXED 的上限和请求数均为1。同一整卡Profile按同一规则接受其上限内的申请，不按Profile身份分流。实际 workload 中不得添加额外 GPU init/sidecar、GPU 容器、额外滚动副本或在线扩缩；owner 必须在持久接受和实际渲染处执行相同约束，属于 EXT-04。

共享每设备 `m>0` MiB、`F>0` MiB/block，要求 `m%F=0`，`q=m/F`。provider int32 边界对 q 和插件乘回后的 m 均校验。F=1/256/1024、m=6144 对应 q=6144/24/6；F=10 拒绝。单容器 limits 写 q，实例总 MiB 为 replicas*m；不把总量写每个 Pod。整卡number=N、cores=100、memory-percentage=100，不同时写绝对 memory；整卡总量按checked replicas*N计量，不重复计共享MiB。F不是超配倍率，memory/core scale均为100%。

CheckGpuFit 对每副本多于一个设备明确返回SHAPE_UNSUPPORTED，不把单设备KnownFreeUnits当作多卡副本估计。多卡计划/实际Pod字段的软件合同不宣称多卡组合容量、真实runtime绑定、模型并行、硬件隔离或释放已经验证；现有provider对未知多UUID运行时仍保守拒绝，不伪造Verified。

同集群只有一个共享 MiB/cores/isolation 向量；整卡和共享 Node UID 集合不重叠。受管渲染固定 scheduler `volcano`、recipe `volcano-hami-v1`、已核对 Queue、`accelerator.ani.io/{supply-group,model-key,baseline-id}` 及 `volcano.sh/vgpu-mode=hami-core`。调用者不能覆盖、删除或扩大选择器。绑定身份为 cluster+Pod UID+container+物理设备，平台资源 UUID、Node index、PodResources 伪 ID 和显存块均不等于物理 GPU UUID。

ResolvedGpuPlan 一旦持久接受即复用原快照，不按当前 F、目录或容量重算。CLOSED 只拒绝新解析，不撤销旧计划或已在途分配。配置未知/漂移/未验证是错误；当前满容量或读模型未知是参考状态。合法形态、有配额但无空闲可由外部 owner 受理等待，不以预检 0 代替配额准入。

硬件验收资格 v2 绑定 baseline/spec、Node UID、物理 GPU 和原始实验，不设 24 小时或其他时间自动失效；关键环境变化后重新验收。Publish、OPEN、Resolve、CheckFit 仍核对资格和当前受管配置，不把资格永久有效解释为跳过漂移检查。旧 v1 时限证明需要显式迁移，保持原签名与到期语义。资格或准入配置不通过时，运行占用、健康水位和释放事实仍独立采集；容量保守不完整，不能作为新分配依据。ObserveRelease 继续核对真实在线节点、CRI 正向退出和 PodResources 无分配，不依赖新增准入是否开放，也不把来源失联视为释放。文件格式和迁移步骤见 [provider 读取合同](../runbooks/provider-sources.md)。

## 规范摘要 acc-c14n-v1

摘要不是鉴权令牌。实现位于 `internal/biz/canonical.go`：UTF-8，无 Unicode 归一化；对象键按 UTF-8 字典顺序；所有整数转换成无前导零十进制 JSON 字符串，范围 0..2^63-1（领域数量不允许负数）；布尔值保持 JSON 布尔；字符串使用 JSON 转义但不进行 HTML 转义；null 保持 null；无空白、无末尾换行。拒绝浮点、溢出、重复受管 KeyValue 键。数组保持语义顺序，以下集合例外。

SHA256 输入为 ASCII `acc-c14n-v1` + LF + 上述规范 JSON。字段名采用本仓领域快照的 snake_case；所有领域结构字段均存在，包括空串、0、false、null。使用 Protobuf JSON 的语言须先转换到这一规范模型，不直接 hash protobuf wire bytes 或语言默认序列化。

- baseline：ProviderBaseline 全部字段，`baseline_digest` 清为空串、`verification=null`、`factor_evidence_ref` 清为空串；观察时间和证据引用不改变静态配置摘要。
- spec：对象 `{spec: ProfileSpec, baseline_id: string}`。
- plan：完整 ResolvedPlan，`resolution_digest` 清为空串、`profile.published=false`；runtime 的三组 KeyValue 按 key 排序，重复/空键值拒绝。其他静态字段、container_name、replicas、F、q、总量和渲染全部参与摘要。
- projection：对象字段 `ref,revision,state,plan_digest,end_reason,source_fact_ref,source_operation_created_at`，plan_digest 为已校验完整内容的 resolution_digest；不包含 request_id 或 payload_digest。原 CREATE 时间两个 revision 固定一致，采用规范 UTC RFC3339Nano。

例如普通规范对象 `{"z":9223372036854775807,"a":"<&中文>","b":true}` 规范化为 `{"a":"<&中文>","b":true,"z":"9223372036854775807"}`。完整 baseline、plan、projection 及摘要见 [固定测试向量](accelerator-vectors.json)，由 Fedora Go 实现生成，回归测试逐字校验。来源设计示例中的旧哈希不得直接作为新规范向量。

## 幂等、投影终态与重试

管理 mutation 的键为 actor+RPC+idempotency_key；去重结果与业务写同一 PG 事务提交。request_id 不参与语义摘要。同键同报文返回同一个对象；异报文 `IDEMPOTENCY_CONFLICT`，不能以新 key 无条件重试一个含未知副作用的调用。expected_version 竞争失败重新读取再决定。

Sync 支持且仅支持 revision 1=DECLARED/end_reason=UNSPECIFIED，2=ENDED/明确原因。事实引用分别为 `quota-operation:<原create UUID>:accepted`、`:canceled-unsent`、`:gpu-fully-released`。两种 revision 带相同 ref、完整不可变 plan 和原 CREATE 时间；同 revision 同内容并发重放幂等，异内容冲突。2 先到可建终态，晚到的合法 1 不回退，同 resource 换 create_operation 不能复活。ENDED 不删除实际 Binding、不发退款、不写 Kubernetes。

Sync 网络失败/Unavailable 可退避重试原报文；ACK 只代表投影持久接受。Conflict/InvalidArgument/FailedPrecondition 必须修复输入或配置，不能用覆盖写、改摘要或改 create_operation 自动恢复。真实 Gov 仍需从持久操作/完整 charge 重建同步进度，不能只扫新时间戳；GPU 同步旁路不应阻塞已经接受的 owner 创建。

主要 reason：`MEMORY_UNIT_UNVERIFIED`、`MEMORY_GRANULARITY_MISMATCH`、`PROVIDER_INTEGER_OVERFLOW`、`INTEGER_OVERFLOW`、`SHAPE_UNSUPPORTED`、`BASELINE_DIGEST_MISMATCH`、`PLAN_DIGEST_MISMATCH`、`CONFIG_DRIFT`、`ADMISSION_CLOSED`、`PROFILE_NOT_ACTIVE`、`CONSUMPTION_NOT_VERIFIED`、`IDEMPOTENCY_CONFLICT`、`VERSION_CONFLICT`、`USAGE_PROJECTION_CONFLICT`、`USAGE_PROJECTION_NOT_READY`、`PAGE_SCOPE_MISMATCH`、`SOURCE_UNKNOWN`。gRPC ErrorInfo reason 稳定；错误不携带 DSN、私钥、token 或 provider 原始响应。完整度不足的容量响应通过 complete/issues 表达，不伪装为全部耗尽。

## Scheduler 配置与供给基线

Scheduler 基线统一使用公开纯函数 `pkg/schedulerconfig.ConfigMapDigest(volcanoVersion, data, binaryData)` 产生 `scheduler_config_digest`；provider 的 `Inspect` 使用同一函数消费。版本明确传 `v1.12.0` 或 `v1.15.2`，固定 YAML 键为 `volcano-scheduler.conf`。只比较本次 YAML 的编辑方使用 `ProtectedDigest(volcanoVersion, document)`；这不授予写 ACC 基线的权限，也不证明 scheduler 已加载。

两个已审核版本允许调整 nodeorder 的八个 `*.weight`、binpack 的评分权重/资源列表、deviceshare 的 `SchedulePolicy`/`ScheduleWeight` 及对应 `enableNodeOrder` 评分开关。纯 nodeorder/binpack 条目可增删，不要求保留亲和评分插件或使用正权重。policy 原生为 string，`binpack`/`spread` 和 default 分支按对应版本源码处理。错误 YAML、重复 key、错误结构及已审核字段的错误类型拒绝。

模式开关、设备配置来源、其他 hook、未知字段、actions 顺序和受保护 tiers 结构均参与摘要；其他 data/binaryData 键也保留保护。device-plugin ConfigMap 仍使用完整内容摘要。算法没有旧整份 scheduler 摘要回退：旧基线须经清点后明确重建/重新验证；这会改变 BaselineDigest 及后续新 plan 的承诺，不能重写已受理 plan、usage 或 Inference 原 charges，也不能沿用旧 evidence 伪造 Verified。

## owner 身份附件与释放观察

创建/删除消息附件使用 `api/protos/accelerator/integration/v1/owner_gpu_attachment.proto`，嵌入真实 Inference 业务请求，不新建虚构 owner 业务服务。Gov 传可信 ref/actor、完整冻结 plan、原规范请求摘要、全部 GPU charge、完整业务 payload digest、`gpu-metering-v1`。charge 按 quota_code/charge_id 匹配，不依赖数组顺序；共享代码为 `gpu.shared_memory_mib`，整卡为 `gpu.physical.count`，单位为原始整数。DurableOwnerAck 只证明命令持久接受，必须核对 operation_id/resource_id/accepted。

为避免仅凭可伪造 Pod 标签推租户，owner 须在实际 Pod UID 已知后给 GPU 容器对应 Pod 加签名断言：标签采用原设计四个 `accelerator.ani.io/{resource-tenant-id,owner-service,resource-id,create-operation-id}`，注解为 `accelerator.ani.io/plan-digest` 与 `accelerator.ani.io/owner-signature`。签名是 Ed25519，载荷为 ASCII `acc-owner-pod-v1` + LF + acc-c14n-v1 **规范 JSON 本身**（不加 canonical 哈希前缀），对象 `{pod:完整PodRef,ref:完整GpuUsageRef,plan_digest:string}`；标准 Base64 编码签名。服务配置只存 owner 公钥。未签名/不匹配的实际占用仍计容量，保持未关联；签名断言与持久投影匹配后才能出现在租户 Binding 列表。签名不含凭据，额外容器仍不被合同允许。

Resource 实例固定 `replicas=1` 和主容器 `workspace`，卡数按上述模式规则进入devices_per_replica，通过 Notebook 完整投影原 plan。`resource_owner_public_key_file` 为可选受管绝对路径，存 Resource 的独立 Base64 Ed25519 公钥；它不能使用 Inference 公钥验证 Resource owner。未配置时 Resource 实际占用仍计容量、保持未关联。查询 ref/owner filter 和 Gov 同步支持该固定 owner，使用终态不等于物理分配已清空。

Resource 由自身持久删除/未知结果规则决定计算已清理后可靠发送原 CREATE 的完整 GPU 子集；专属 PVC 失败继续重试，整体实例删除仍未完成。Gov 对合法 owner 原账声明做租户、原 charge、全向量、累计幂等和事务校验，不查询 Notebook/Pod，也不以额外 DELETE 记录二次批准退额。本合同的 Inference workload 清理职责保持；实例当前结果由 Resource 的 `docs/execution/status.md` 维护。

ObserveRelease 不以投影存在为前置。必须传原 ref、明确 DELETE operation UUID、所有当前与替换历史的实际 Pod UID/container 范围；空范围拒绝。只读核对返回 STILL_PRESENT、UNKNOWN 或 ALLOCATION_RELEASED。后者仅证明指定实际范围有受控解除事实，不证明 owner 完整范围、已停止未来创建、没有未知在途副作用或可退款。API 强删/NotFound、节点失联、单次缺项、来源冲突均不能单独证明释放。

Inference 负责封闭实际创建执行点、查清所有在途写、终止生成、删除完整工作负载并清理 GPU 分配，再可靠调用既有 `quota.service.v1.QuotaReleaseService/ReportQuotaRelease`。必须用原 CREATE operation 和完整原 GPU charge 向量，全量累计 released_total；明确 DELETE 且整实例完成后报告，普通退出、失败、重启、部分 Pod 清理均不退额。Accelerator 不实现同名替代服务，也不是该业务 charge 的退款 owner。

GOV-ACC-V12-01 在 Governance 实现公共 GPU 受理、权限/BFF、通用账本投递、释放校验与独立使用同步。后续 `GOV-ACC-INF-V12-WIRING-01` 的三方接入导航如下；软件验证、接口发布和环境验收结论由[本批唯一结果索引](../plans/governance-accelerator-inference-v1.2-acceptance.md)维护，不从历史 PASS 推导。

- Governance 公开退额 provider 为 Go 模块 `github.com/zhangzhe-ctrl/ani-governance/api/quota`，本批发布版本 `v0.0.0-20261009133356-1ab461604c74`，包 `gen/go/quota/service/v1`。其 [发布源](https://github.com/zhangzhe-ctrl/ani-governance/tree/1ab461604c74/api/quota) 定义 `QuotaReleaseService/ReportQuotaRelease`；正式消费使用 `GOWORK=off` 和锁定 module/sum，不使用兄弟目录 replace 或另建退额服务。
- Governance 的正式业务受理、当前权限、adapter、固定 owner 退款 DNS 与运维入口见兄弟仓的 [GPU owner 接入指南](../../../ani-governance/docs/contracts/gpu-owner-integration-guide.md)。Inference 的可信 mTLS intake、原始 PG context、原子命令/ACK、真实退额客户端和部署字段见兄弟仓的 [managed GPU owner handoff](../../../ani-inference-service/docs/contracts/managed-gpu-owner-handoff.md)。ACK 只证明持久接受；受控注入退额不能证明实际清理完成。
- **C02 的 KServe LWS 参数交接仍阻塞**：锁定 KServe v0.16.0 的 LLMI 不支持必需的 LWS 顶层 Volcano Queue 和 leader/worker GPU metadata 投影。事后 patch 不能保证首次调度且可能被 reconcile 覆盖；原始主源链接与缺失同步入口见 Inference handoff。不得以 unsupported `spec.annotations`、render 成功或软件受理替代实际 LWS 参数交接。固定副本 Deployment 的软件投影与 CPU LWS 按其各自支持范围判断，不能扩大为 C02 完成。

Inference 仍负责实际 workload/model readiness、创建封闭与所有在途写、可信 Pod 身份和完整替换历史、GPU 清理与 ObserveRelease、持久关闭事实、可靠通知触发/重试及真实环境恢复。Accelerator 继续只拥有供给、计划、只读观察和使用投影，不代写这些业务事实。独立持久化测试 owner 仅验证软件合同，不进入生产装配，不能替代正式 owner 业务闭环。
