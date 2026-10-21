# volcano-hami-factor-v1 来源合同

这是生产 reader/normalizer 的支持边界。历史软件回放使用按固定上游格式**构造**的样本，不能认证硬件组合。2026-10-17 的单节点 T4 实测独立记录于[执行状态](../execution/status.md)：真实来源为 Kubernetes v1.35.8、Volcano v1.15.2、device plugin v1.12.0、containerd v2.3.4 和 NVIDIA driver 580.178.04。其他硬件或部署组合不能沿用该结果。

## 支持组合与来源

| 来源 | 固定合同/读取字段 | 身份、单位及信任 |
|---|---|---|
| Kubernetes core/v1、coordination.k8s.io/v1 Lease | HTTPS GET Nodes、Pods、指定 ConfigMaps；Ready lastHeartbeatTime、Node UID、annotations、容器与资源、imageID；旧 Node status 再 GET 同名 kube-node-lease Lease | Ready 必须 True。status 心跳新鲜则直接采用；过旧时核对 Lease name/namespace、ownerReference 的 Node UID/name、holderIdentity 和 renewTime，时限取 freshness 与 leaseDurationSeconds 的较小值。受管 CA/server_name、client cert 或 token 文件；只读 RBAC，无写入；缺权限/失联/过期不当在线；分页截断拒绝 |
| Kubernetes `node.k8s.io/v1` RuntimeClass | HTTPS GET the managed baseline's exact `runtimeclasses/{name}`; metadata name/UID/resourceVersion, handler, scheduling and overhead | Read-only `get` permission is required. Only optional `kubernetes.io/hostname` scheduling is supported; it must match each original group Node UID's actual label. Nonempty overhead/tolerations, missing facts and unknown/conflicting selectors fail closed. No global RuntimeClass writes or owner fallback. |
| Volcano v1.12.0 | `volcano.sh/node-vgpu-register` 的 UUID/count/memory/model/health/mode；`volcano.sh/node-vgpu-handshake` | memory 是 BLOCKS，绑定受控 F；heartbeat 为实际上游时间，非本服务接收时间 |
| Volcano device plugin v1.12.0 | 实际 Ready 插件 imageID；显式 `--gpu-memory-factor`、`--device-split-count`；ConfigMap摘要 | 按镜像 SHA256、F、K、组 Node UID 核对。未显式暴露有效参数拒绝，不能仅信请求 baseline |
| Volcano scheduler v1.12.0 | scheduler imageID/config 摘要、Queue Open；`volcano.sh/vgpu-ids-new`、`volcano.sh/vgpu-time` | 分配字符串为 UUID/NVIDIA/blocks/cores，按 GPU 容器顺序；时间 Unix 秒。调度分配是历史事实，不续写为新 runtime 观测 |
| Volcano scheduler v1.15.2 + independent device plugin v1.12.0 | 上述默认注册/分配格式；额外读取三个默认 vgpu Allocatable 资源键 | [固定源码比较及真实 Queue 缺陷](volcano-v1.15.2-compatibility.md)。Allocatable 必须为正整数；原版 Whole 百分比计划被 Queue 拒绝，派生修复镜像须独立冻结 digest、baseline 和硬件证明。不能以版本相同沿用原证明；其他组合拒绝 |
| Kubernetes PodResources v1（Go 模块 v0.35.3） | List 的 namespace/Pod name/container/resource/device_ids | 仅显式配置的 Unix socket；设备伪 ID 只证明分配存在，不能当物理 UUID；由同 Pod UID 的 CRI 事实桥接 |
| containerd / CRI v1（Go 模块 v0.35.3） | Version 必须报告 containerd、非空 runtime 版本和 API v1；ListContainers、verbose ContainerStatus，返回容器 ID 必须一致；GPU 容器继续核对 reserved Pod UID/container metadata 与 Info[info].runtimeSpec.process.env | 按实际能力和字段校验，不锁定 containerd 小版本。`NVIDIA_VISIBLE_DEVICES=GPU-...`、`CUDA_DEVICE_MEMORY_LIMIT_0=<MiB>m`、`CUDA_DEVICE_SM_LIMIT` 仍须完整有效，MiB 不能再乘 F；其他 runtime/API、RPC 失败或状态身份不明保留未知。无 GPU 容器和 EXITED 状态不要求 GPU env；仅 Unix socket |
| CRI v1 容器事件 | GetContainerEvents 的 sandbox metadata、container ID/name/state、FinishedAt | 连续订阅并持久保存全部已见 attempt；仅正向 EXITED 补充解除事实，当前 Running/Unknown 优先阻断 |
| containerd 原始 system journal | 固定 journalctl 查询、trusted machine/boot/unit/uid 元数据与完整创建、退出链 | 只读实际节点 journal；用明确 TaskExit 时间恢复错过的事件，不从缺失推断，不改写运行分配或配额 |
| NVML（Go binding v0.13.0-1） | GetUUID/GetName/GetMemoryInfo.Total | 实际本节点物理 bytes；平台 device UUID 由 cluster+Node UID+vendor UUID 派生 UUIDv8，不使用 index |
| 硬件验收资格 | `gpu-hardware-qualification-v2` Ed25519 签名 JSON | 独立受控实验 manifest 摘要、cluster/group/baseline/spec、完整 Node UID/物理 UUID、物理 bytes/安全 MiB；绑定 baseline，无时间自动失效。旧 `gpu-hardware-consumption-v1` 保留原到期语义，须显式迁移。RPC 不能自报 VERIFIED |
| owner 归属证明 | `acc-owner-pod-v1` Ed25519 | 签名绑定实际 Pod UID/container、原资源 ref、冻结 plan digest；普通标签不获得租户归属 |

上游源码参考：[Volcano v1.12.0](https://github.com/volcano-sh/volcano/tree/v1.12.0)、[device plugin v1.12.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/tree/v1.12.0)、[containerd 2.1.5 ContainerStatus](https://github.com/containerd/containerd/blob/v2.1.5/internal/cri/server/container_status.go)、[Kubelet PodResources v0.35.3](https://github.com/kubernetes/kubelet/tree/v0.35.3/pkg/apis/podresources)、[CRI v0.35.3](https://github.com/kubernetes/cri-api/tree/v0.35.3/pkg/apis/runtime/v1)。实际下载文件与 SHA 保存在本次 Fedora 仓外 evidence，上游 tag 名不能替代部署镜像 digest。

插件 v1.12.0 在 [PodAllocationSuccess/Failed](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/util/util.go#L295-L312) 都会把 `volcano.sh/vgpu-time` 改写为 `18446744073709551615`。只有这个确切 marker 且 `volcano.sh/bind-phase=success` 时，reader 采用 scheduler 写入的 `volcano.sh/bind-time` 严格 Unix 秒；缺失、失败或未知时间仍拒绝，不用当前时间续写历史。scheduler Claim 的 revision 由分配事实摘要构成，无关 Pod status/resourceVersion 变更不会改写同一历史证据。

`node_test.go` 通过 Unix gRPC 驱动生产 `readNode`，覆盖 containerd 2.1.5/2.3.4、runtime/API 不匹配、Version/List/Status 失败、状态 ID 不匹配，以及 GPU 身份和 env 缺失拒绝。这是协议边界回归，不能替代相应实际部署版本的 GPU 消费验收。

本机 NVML、PodResources、CRI/events 与有限 journal 由独立 `ani-node-agent` DaemonSet 采集。中心的 Inspect/Collect 只读取认证后持久保存的逐节点事实；K8s、owner、预算、Claim/Binding 与释放判断仍由中心执行。节点服务使用 TLS 1.3、精确服务 SAN 和 Pod 绑定 SA token，唯一节点 RPC 是 `accelerator.node.v1.NodeTelemetryService/Connect`。未接管节点可被发现，但不产生可售容量。

新准入核对每个认证节点的 NVML UUID、型号和物理 bytes，并继续核对受管 scheduler/plugin 的实际镜像、ConfigMap 摘要、F/K 与节点注册。宿主驱动版本及 HAMi 库文件原地更新没有自动指纹监控，必须由受管变更流程触发 baseline 更新和重新验收；不能把声明的 `hami_core_revision` 当成实时采集结果。

## 受管连接配置

`provider_config_file` 是部署者维护的 JSON，不由 RegisterCluster 写入。顶层为 `connections` 数组，每项字段如下。空数组允许以无供给模式启动，登记只能引用其中已有 ref。

这也是 ACC 的唯一基础设施管理范围：管理、目录、容量和解析根据数据库中的 `cluster.connection_ref` 回查该配置；不新增独立集群授权清单，不按用户或租户重复配置连接范围。用户权限、套餐和配额由 Gov 管理，ACC 仍校验调用服务、完整 RPC 方法、可信上下文、租户数据和实体关联。已登记集群的连接不在当前配置时，相关访问拒绝；注册接口不能提交任意 API 地址、凭据或 socket 路径。配置读取于启动装配，变更通过受管部署流程生效。

```json
{
  "connections": [{
    "ref": "managed:cluster-a",
    "api_server": "https://api.cluster.example:6443",
    "server_name": "api.cluster.example",
    "ca_file": "/run/accelerator/kubernetes/ca.crt",
    "cert_file": "/run/accelerator/kubernetes/client.crt",
    "key_file": "/run/accelerator/kubernetes/client.key",
    "token_file": "",
    "plugin_namespace": "kube-system",
    "plugin_selector": "app=volcano-device-plugin",
    "plugin_container": "device-plugin",
    "plugin_config_map": "device-plugin-config",
    "scheduler_namespace": "volcano-system",
    "scheduler_selector": "app=volcano-scheduler",
    "scheduler_container": "volcano-scheduler",
    "scheduler_config_map": "volcano-scheduler-configmap",
    "node_agent_identity": {
      "namespace": "ani-node-system",
      "service_account": "ani-node-agent",
      "daemon_set": "ani-node-agent",
      "audience": "ani-node-agent"
    },
    "installation_baselines": []
  }]
}
```

名称、selector 和路径必须按实际安装核对，不能把示例视为已探测值。中心保留原读取权限，并增加固定用途的 TokenReview、Agent Pod/SA/DaemonSet 读取和 Node patch；中心代码只允许写供给组/型号/基线三键，patch 包含 UID/resourceVersion test。Agent 不持有业务数据库或 owner 私钥。HTTPS 禁止重定向和未验证证书；token_file 每次请求重新读取。

`installation_baselines` 每项为 `mode` 与现有 `ProviderBaseline` 的 `baseline`；运维登记真实安装声明，摘要复用 BaselineDigest。空配置显示未知，不猜预算或版本。中心启动明确拒绝旧 `local_node_uid`、`nodes`、`runtime_events_directory` 和 `runtime_journal` 采集配置；socket、journal 与持久状态目录移至 Agent 部署参数。

旧 Adopt RPC 仍同步验证并登记 CLOSED 组。新的初始化 operation 编排三键标签、ACTIVE Profile 和自身版本链 OPEN；已有 CLOSED 组与无 Profile 的新组不会自动开放。节点同名重建不能继承旧 UID。

证明文件名为 SHA256(verification_ref UTF-8).json，只能从配置证明目录读取。内容结构见 `internal/data/provider/attestation.go`。v2 使用 `schema_version=2`、`kind=gpu-hardware-qualification-v2`，签名载荷为 `acc-hardware-qualification-v2` + LF + acc-c14n-v1 规范 JSON payload；保留真实 `verified_at`，不包含 `expires_at`，不因经过 24 小时或更长时间自动失效。节点/物理设备身份、显存或受管 GPU 镜像与配置不再匹配时，拒绝新解析并要求按变化后的 baseline 重新验收。

v1 仍使用 `acc-hardware-attestation-v1` 签名域及其原有效期，不会被静默解释为长期资格。迁移时核对当前受管配置与原实验范围，用新文件和部署 ConfigMap 显式签发 v2；原始签名、实验、baseline 摘要与回执保留，不能改写旧 `expires_at` 或把政策迁移称为新硬件实验。没有关键环境变更时不重训模型或重复整套满载实验。签名者必须在服务外核验原始实验；重复 JSON 键、未知字段、错作用域/错预算/错签名、未来验收时间及给 v2 指定非空到期时间均拒绝。签名者私钥不在服务配置中。

## 持久观测与容量

同一 cluster+Pod UID+container+device 的 scheduler/allocation/runtime 合并一次。runtime 与 allocation 必须一致才是完整 ACTIVE；冲突保守保留最大计量，只使可定位的设备 UNKNOWN。无法定位物理设备/容器的破损来源使该节点未知，不能分摊到猜测的父卡。

供给组未关联 usage 的真实占用以及未签名的工作负载仍减少该组容量；完全没有可信 F 的未接管节点不产生可售容量。已观察但未放置的 Pod 请求按实际 group selector 单独计入 unplaced_requested_units，不分配到设备。全组缺观测时 complete=false，known_free_units=0 是未知下界而非“确定耗尽”。

安全预算不得超过物理 bytes 与逐卡量化预算；份数受 K、MiB、可加核心预算约束。逐卡 floor 后求和，不能跨卡拼余量。配置/F 漂移先拒绝新解析和新解释，历史样本保留原 factor。来源失败保留最后可信时间，落库失败水位；接收时间不伪造观察时间。

验收资格与运行源健康分开判断。资格缺失/损坏/旧 v1 过期，或调度镜像、配置偏离准入 baseline，不停止 Kubernetes、PodResources、CRI 的事实采集和正向释放证明。此时库存/可售容量保持保守不完整，不凭健康水位宣称可以新增分配；单位 F 或注册格式无法核对时不重新解释分配块数。来源水位表达实际连接、节点与读取健康，不把准入配置不匹配伪装为来源失联。真实来源失联、容器未退出或仍有设备分配时仍不能确认释放。

调度分配按原始 assigned 时间记录历史事实。owner 签名晚于首次采集到达时，同 Pod/container/device/node/assigned 时间的已有 scheduler 记录保留原归属及版本；不把后来到达的归属回填为同一历史版本的新报文。当前 runtime/allocation 事实继续独立严格验签并关联 usage；两个有效归属相异仍产生 ownership conflict，同时间的真实分配字段变化仍拒绝为 source revision conflict。

解除证明只退休被覆盖 Pod/container/device 在该时间之前的旧事实。节点在线、PodResources 无 GPU 分配、同 UID 所有已知 CRI 容器具有正向 EXITED/finished_at 是必要条件；CRI 单次空列表或 API 强删不够。新于解除证明的分配重新计占用。ObserveRelease 必要时在三秒内部预算内刷新一次完整归并，并在与 ingest 相同的节点锁内验证 current/consumed/证明 revision；冲突或证据不足返回 UNKNOWN。它不删除 workload、不结束 usage、不发配额释放。

Agent 连续读取 CRI `GetContainerEvents`，保存 CREATED/RUNNING/EXITED/UNKNOWN、独立原 event_type 与未解释尝试。记录先 append/fsync；每节点仅一条 Report 在途，中心事务提交后 ACK，Agent 持久 ACK 后发下一条。网络断线而日志连续不制造来源缺口；真实缺口不被重连、ACK 或空快照清除。普通同内容快照和心跳不推进领域 revision；新占用、UNKNOWN、缺口及有效性变化仍使旧判断失效。

中心只向认证节点下发有界的已知 Pod/container RecoverExitRequest，并持久保存其范围以支持跨 epoch 重放。Agent 用固定本机 journal 配置验证 machine/boot/root/containerd 来源和 sandbox→create→exit 正向链，最多十六次尝试、四 MiB 输出。结果经中心校验和持久归并后补充历史退出；当前 Running/UNKNOWN 不会被旧退出覆盖，局部补证不清除全局 SourceGap。

journal 路径与超时是手动安装参数，RPC 不接收任意命令或路径。中心等待继续服从整体采集/释放期限，超时保留 UNKNOWN。证据来源时间与传输 receipt 分离，重放不刷新采样时间。

完整有效的 `RELEASE_CONFIRMED` 是已持久化的历史事实，绑定列表及其聚合不能因 120 秒采样窗口过去或当前来源失联而将其降为 UNKNOWN；返回原始 observed_at、received_at 和 release 引用，不刷新证明时间。ACTIVE 仍要求来源健康及实时新鲜度；不完整、未知或时间无效的历史记录仍保守返回不完整。该规则不改变新分配重新计占用的合并逻辑，也不替代 ObserveRelease 对当前解除条件的核验。

## 构造样本与验收入口

`parse_test.go` 固定上游字符串，F=1024，blocks=6、runtime=6144m；`replay_test.go` 构造两张 24576 MiB 设备、K=4、6 GiB/25% 请求，通过真实 TLS HTTP server 与真实 Unix gRPC server 驱动生产读取器；NVML 库存仅在测试装配中注入。Node/scheduler/PodResources/CRI 原始格式和签名 payload 均在测试源码中可复现，源码 manifest 给出样本模板摘要。每次回放的随机身份、证书与当前时间是隔离输入，不称实际采集。

`make test-provider` 在真实 PG 中验证三源合并、未投影占用、pending、单卡矛盾、失联/陈旧/F 漂移未知、适配器重建、API 强删保留、正向解除恢复；`trust_test.go` 验证签名、预算、Node UID、过期和重复键边界。最终命令/候选摘要/原始日志见执行验收记录。
