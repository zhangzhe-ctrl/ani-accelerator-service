# volcano-hami-factor-v1 来源合同

这是生产 reader/normalizer 的支持边界。历史软件回放使用按固定上游格式**构造**的样本，不能认证硬件组合。2026-10-17 的单节点 T4 实测独立记录于[执行状态](../execution/status.md)：真实来源为 Kubernetes v1.35.8、Volcano v1.15.2、device plugin v1.12.0、containerd v2.3.4 和 NVIDIA driver 580.178.04。其他硬件或部署组合不能沿用该结果。

## 支持组合与来源

| 来源 | 固定合同/读取字段 | 身份、单位及信任 |
|---|---|---|
| Kubernetes core/v1、coordination.k8s.io/v1 Lease | HTTPS GET Nodes、Pods、指定 ConfigMaps；Ready lastHeartbeatTime、Node UID、annotations、容器与资源、imageID；旧 Node status 再 GET 同名 kube-node-lease Lease | Ready 必须 True。status 心跳新鲜则直接采用；过旧时核对 Lease name/namespace、ownerReference 的 Node UID/name、holderIdentity 和 renewTime，时限取 freshness 与 leaseDurationSeconds 的较小值。受管 CA/server_name、client cert 或 token 文件；只读 RBAC，无写入；缺权限/失联/过期不当在线；分页截断拒绝 |
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

本实现不提供通用节点 Agent 或跨主机明文 gRPC。NVML 只用于 Connection 的 `local_node_uid`，必须是当前进程真实所在 GPU 节点；每个 NodeSource 的 Unix socket 必须由部署者绑定到该确切 Node UID 的真实来源。非本地 NVML 节点会保留已知库存/绑定并报告不完整，不能假定拥有全群硬件遥测。完整多节点部署拓扑、签名者治理及真实驱动/HAMi lib 组合仍需 GPU-LIVE 的部署验证。

已验收组的准入还核对本节点 NVML UUID、型号和物理 bytes 与签署资格一致；当前来源不能认证远端物理 bytes，因此不为远端节点宣称该资格核对通过。受管 scheduler/plugin 的实际镜像、ConfigMap 摘要、F/K 与节点注册继续实时核对。宿主驱动版本及 HAMi 库文件原地更新目前没有自动指纹监控，必须由受管变更流程触发 baseline 更新和重新验收；不能把声明的 `hami_core_revision` 当成实时采集结果。

## 受管连接配置

`provider_config_file` 是部署者维护的 JSON，不由 RegisterCluster 写入。顶层为 `connections` 数组，每项字段如下。空数组允许以无供给模式启动，登记只能引用其中已有 ref。

```json
{
  "runtime_events_directory": "/var/lib/accelerator/runtime-exits/node-a",
  "runtime_journal": {
    "directory": "/host/journal/ACTUAL-MACHINE-ID",
    "machine_id": "ACTUAL-MACHINE-ID",
    "node_uid": "ACTUAL-NODE-UID",
    "journalctl_path": "/usr/bin/journalctl"
  },
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
    "local_node_uid": "ACTUAL-NODE-UID",
    "nodes": [{
      "uid": "ACTUAL-NODE-UID",
      "name": "actual-node-name",
      "podresources_socket": "/var/lib/kubelet/pod-resources/kubelet.sock",
      "cri_socket": "/run/containerd/containerd.sock"
    }]
  }]
}
```

名称、selector 和路径必须按实际安装核对，不能把示例视为已探测值。RBAC 只需 get/list core Nodes/Pods、get 指定 ConfigMaps、get scheduling.volcano.sh Queues，以及 get kube-node-lease namespace 中受管节点同名的 coordination.k8s.io Leases；不需要 create/update/patch/delete。Unix socket 的访问有节点级读取权限，应由宿主部署权限控制，不能通过公共 RPC 提交 socket/URL/文件路径。HTTPS 禁止重定向、未验证证书和连接 ref 中的凭据地址。

Adopt 检查既有安装并生成组 UUID，默认 CLOSED。部署者另行把返回 group/model/baseline 标签正确放入节点；本服务不 patch 节点。Publish 和 OPEN 再校验标签与有效消费证明。节点同名重建不能继承旧 Node UID 资格。

证明文件名为 SHA256(verification_ref UTF-8).json，只能从配置证明目录读取。内容结构见 `internal/data/provider/attestation.go`。v2 使用 `schema_version=2`、`kind=gpu-hardware-qualification-v2`，签名载荷为 `acc-hardware-qualification-v2` + LF + acc-c14n-v1 规范 JSON payload；保留真实 `verified_at`，不包含 `expires_at`，不因经过 24 小时或更长时间自动失效。节点/物理设备身份、显存或受管 GPU 镜像与配置不再匹配时，拒绝新解析并要求按变化后的 baseline 重新验收。

v1 仍使用 `acc-hardware-attestation-v1` 签名域及其原有效期，不会被静默解释为长期资格。迁移时核对当前受管配置与原实验范围，用新文件和部署 ConfigMap 显式签发 v2；原始签名、实验、baseline 摘要与回执保留，不能改写旧 `expires_at` 或把政策迁移称为新硬件实验。没有关键环境变更时不重训模型或重复整套满载实验。签名者必须在服务外核验原始实验；重复 JSON 键、未知字段、错作用域/错预算/错签名、未来验收时间及给 v2 指定非空到期时间均拒绝。签名者私钥不在服务配置中。

## 持久观测与容量

同一 cluster+Pod UID+container+device 的 scheduler/allocation/runtime 合并一次。runtime 与 allocation 必须一致才是完整 ACTIVE；冲突保守保留最大计量，只使可定位的设备 UNKNOWN。无法定位物理设备/容器的破损来源使该节点未知，不能分摊到猜测的父卡。

供给组未关联 usage 的真实占用以及未签名的工作负载仍减少该组容量；完全没有可信 F 的未接管节点不产生可售容量。已观察但未放置的 Pod 请求按实际 group selector 单独计入 unplaced_requested_units，不分配到设备。全组缺观测时 complete=false，known_free_units=0 是未知下界而非“确定耗尽”。

安全预算不得超过物理 bytes 与逐卡量化预算；份数受 K、MiB、可加核心预算约束。逐卡 floor 后求和，不能跨卡拼余量。配置/F 漂移先拒绝新解析和新解释，历史样本保留原 factor。来源失败保留最后可信时间，落库失败水位；接收时间不伪造观察时间。

验收资格与运行源健康分开判断。资格缺失/损坏/旧 v1 过期，或调度镜像、配置偏离准入 baseline，不停止 Kubernetes、PodResources、CRI 的事实采集和正向释放证明。此时库存/可售容量保持保守不完整，不凭健康水位宣称可以新增分配；单位 F 或注册格式无法核对时不重新解释分配块数。来源水位表达实际连接、节点与读取健康，不把准入配置不匹配伪装为来源失联。真实来源失联、容器未退出或仍有设备分配时仍不能确认释放。

解除证明只退休被覆盖 Pod/container/device 在该时间之前的旧事实。节点在线、PodResources 无 GPU 分配、同 UID 所有已知 CRI 容器具有正向 EXITED/finished_at 是必要条件；CRI 单次空列表或 API 强删不够。新于解除证明的分配重新计占用。release 查询不写数据库、不删除 workload、不结束 usage、不发配额释放。

轮询可能错过 kubelet 清理前的短暂 EXITED 状态。生产部署应启用 CRI `GetContainerEvents` 连续采集，并把 `runtime_events_directory` 挂载到该服务独占的受管持久目录，权限 0700；多个服务不要共写目录。同节点/Unix socket 的连接别名共用一个订阅。记录保存完整容器身份及 Created/Started/Exited 状态，不保存 OCI 环境变量；状态变化原子写入并同步落盘，重启后读回。任何已知 attempt 缺少正向退出记录，或当前仍有 Running/Unknown attempt，继续保留未知占用。

已经错过事件且 CRI 已清理的容器，可由 `runtime_journal` 只读查询该确切节点的原始 containerd journal。镜像须包含标准 `journalctl`，只读挂载实际 machine journal 目录，并配置真实 machine ID 和 Node UID。恢复严格核对 root/containerd.service 来源、同一 boot 下的 Pod UID/namespace/name → sandbox → container name/完整 ID → 明确退出时间的完整链；缺失、冲突、未识别格式或超时均不能释放。不导入仓外取证 JSON 冒充实时 CRI，不用 Remove/NotFound 推断退出，也不生成 ACTIVE runtime 分配。恢复事实仅补充释放证明，仍须当前节点在线、CRI/PodResources 健康且 GPU 分配已移除。历史只有 scheduler/allocation Claim 的未知绑定也适用相同正向条件。

完整有效的 `RELEASE_CONFIRMED` 是已持久化的历史事实，绑定列表及其聚合不能因 120 秒采样窗口过去或当前来源失联而将其降为 UNKNOWN；返回原始 observed_at、received_at 和 release 引用，不刷新证明时间。ACTIVE 仍要求来源健康及实时新鲜度；不完整、未知或时间无效的历史记录仍保守返回不完整。该规则不改变新分配重新计占用的合并逻辑，也不替代 ObserveRelease 对当前解除条件的核验。

## 构造样本与验收入口

`parse_test.go` 固定上游字符串，F=1024，blocks=6、runtime=6144m；`replay_test.go` 构造两张 24576 MiB 设备、K=4、6 GiB/25% 请求，通过真实 TLS HTTP server 与真实 Unix gRPC server 驱动生产读取器；NVML 库存仅在测试装配中注入。Node/scheduler/PodResources/CRI 原始格式和签名 payload 均在测试源码中可复现，源码 manifest 给出样本模板摘要。每次回放的随机身份、证书与当前时间是隔离输入，不称实际采集。

`make test-provider` 在真实 PG 中验证三源合并、未投影占用、pending、单卡矛盾、失联/陈旧/F 漂移未知、适配器重建、API 强删保留、正向解除恢复；`trust_test.go` 验证签名、预算、Node UID、过期和重复键边界。最终命令/候选摘要/原始日志见执行验收记录。
