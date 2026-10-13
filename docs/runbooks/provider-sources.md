# volcano-hami-factor-v1 来源合同

这是生产 reader/normalizer 的支持边界。当前回放是按固定上游格式**构造**的样本，未采集 GPU、未安装 Kubernetes/Volcano/HAMi。软件 pass 不认证硬件组合；`GPU-LIVE-*` 全部 not_verified。

## 支持组合与来源

| 来源 | 固定合同/读取字段 | 身份、单位及信任 |
|---|---|---|
| Kubernetes core/v1 | HTTPS GET Nodes、Pods、指定 ConfigMaps；Ready lastHeartbeatTime、Node UID、annotations、容器与资源、imageID | 受管 CA/server_name、client cert 或 token 文件；只读 RBAC，无写入；分页截断拒绝；失败不会当空成功 |
| Volcano v1.12.0 | `volcano.sh/node-vgpu-register` 的 UUID/count/memory/model/health/mode；`volcano.sh/node-vgpu-handshake` | memory 是 BLOCKS，绑定受控 F；heartbeat 为实际上游时间，非本服务接收时间 |
| Volcano device plugin v1.12.0 | 实际 Ready 插件 imageID；显式 `--gpu-memory-factor`、`--device-split-count`；ConfigMap摘要 | 按镜像 SHA256、F、K、组 Node UID 核对。未显式暴露有效参数拒绝，不能仅信请求 baseline |
| Volcano scheduler v1.12.0 | scheduler imageID/config 摘要、Queue Open；`volcano.sh/vgpu-ids-new`、`volcano.sh/vgpu-time` | 分配字符串为 UUID/NVIDIA/blocks/cores，按 GPU 容器顺序；时间 Unix 秒。调度分配是历史事实，不续写为新 runtime 观测 |
| Volcano scheduler v1.15.2 + independent device plugin v1.12.0 | 上述默认注册/分配格式；额外读取三个默认 vgpu Allocatable 资源键 | [固定源码比较](volcano-v1.15.2-compatibility.md)。Allocatable 必须为正整数；scheduler 和插件版本/digest 分别冻结，硬件证据仍必需；其他组合拒绝 |
| Kubernetes PodResources v1（Go 模块 v0.35.3） | List 的 namespace/Pod name/container/resource/device_ids | 仅显式配置的 Unix socket；设备伪 ID 只证明分配存在，不能当物理 UUID；由同 Pod UID 的 CRI 事实桥接 |
| containerd 2.1.5 / CRI v1（Go 模块 v0.35.3） | Version、ListContainers、verbose ContainerStatus Info[info].runtimeSpec.process.env；reserved Pod UID/container labels | `NVIDIA_VISIBLE_DEVICES=GPU-...`、`CUDA_DEVICE_MEMORY_LIMIT_0=<MiB>m`、`CUDA_DEVICE_SM_LIMIT`。这是已编码后的 MiB，不能再乘 F；仅 Unix socket，其他 runtime 版本未知 |
| NVML（Go binding v0.13.0-1） | GetUUID/GetName/GetMemoryInfo.Total | 实际本节点物理 bytes；平台 device UUID 由 cluster+Node UID+vendor UUID 派生 UUIDv8，不使用 index |
| 硬件消费证明 | `gpu-hardware-consumption-v1` Ed25519 签名 JSON | 独立受控实验 manifest 摘要、cluster/group/baseline/spec、完整 Node UID/物理 UUID、物理 bytes/安全 MiB、有效期。RPC 不能自报 VERIFIED |
| owner 归属证明 | `acc-owner-pod-v1` Ed25519 | 签名绑定实际 Pod UID/container、原资源 ref、冻结 plan digest；普通标签不获得租户归属 |

上游源码参考：[Volcano v1.12.0](https://github.com/volcano-sh/volcano/tree/v1.12.0)、[device plugin v1.12.0](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/tree/v1.12.0)、[containerd 2.1.5 ContainerStatus](https://github.com/containerd/containerd/blob/v2.1.5/internal/cri/server/container_status.go)、[Kubelet PodResources v0.35.3](https://github.com/kubernetes/kubelet/tree/v0.35.3/pkg/apis/podresources)、[CRI v0.35.3](https://github.com/kubernetes/cri-api/tree/v0.35.3/pkg/apis/runtime/v1)。实际下载文件与 SHA 保存在本次 Fedora 仓外 evidence，上游 tag 名不能替代部署镜像 digest。

本实现不提供通用节点 Agent 或跨主机明文 gRPC。NVML 只用于 Connection 的 `local_node_uid`，必须是当前进程真实所在 GPU 节点；每个 NodeSource 的 Unix socket 必须由部署者绑定到该确切 Node UID 的真实来源。非本地 NVML 节点会保留已知库存/绑定并报告不完整，不能假定拥有全群硬件遥测。完整多节点部署拓扑、签名者治理及真实驱动/HAMi lib 组合仍需 GPU-LIVE 的部署验证。

## 受管连接配置

`provider_config_file` 是部署者维护的 JSON，不由 RegisterCluster 写入。顶层为 `connections` 数组，每项字段如下。空数组允许以无供给模式启动，登记只能引用其中已有 ref。

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

名称、selector 和路径必须按实际安装核对，不能把示例视为已探测值。RBAC 只需 get/list core Nodes/Pods、get 指定 ConfigMaps、get scheduling.volcano.sh Queues；不需要 create/update/patch/delete。Unix socket 的访问有节点级读取权限，应由宿主部署权限控制，不能通过公共 RPC 提交 socket/URL/文件路径。HTTPS 禁止重定向、未验证证书和连接 ref 中的凭据地址。

Adopt 检查既有安装并生成组 UUID，默认 CLOSED。部署者另行把返回 group/model/baseline 标签正确放入节点；本服务不 patch 节点。Publish 和 OPEN 再校验标签与有效消费证明。节点同名重建不能继承旧 Node UID 资格。

证明文件名为 SHA256(verification_ref UTF-8).json，只能从配置证明目录读取。内容结构见 `internal/data/provider/attestation.go`；签名载荷为 `acc-hardware-attestation-v1` + LF + acc-c14n-v1 规范 JSON payload。签名者必须在服务外核验原始实验，签名本身不生成硬件事实。重复 JSON 键、未知字段、过期/错作用域/错预算/错签名均拒绝。签名者私钥不在服务配置中。

## 持久观测与容量

同一 cluster+Pod UID+container+device 的 scheduler/allocation/runtime 合并一次。runtime 与 allocation 必须一致才是完整 ACTIVE；冲突保守保留最大计量，只使可定位的设备 UNKNOWN。无法定位物理设备/容器的破损来源使该节点未知，不能分摊到猜测的父卡。

供给组未关联 usage 的真实占用以及未签名的工作负载仍减少该组容量；完全没有可信 F 的未接管节点不产生可售容量。已观察但未放置的 Pod 请求按实际 group selector 单独计入 unplaced_requested_units，不分配到设备。全组缺观测时 complete=false，known_free_units=0 是未知下界而非“确定耗尽”。

安全预算不得超过物理 bytes 与逐卡量化预算；份数受 K、MiB、可加核心预算约束。逐卡 floor 后求和，不能跨卡拼余量。配置/F 漂移先拒绝新解析和新解释，历史样本保留原 factor。来源失败保留最后可信时间，落库失败水位；接收时间不伪造观察时间。

解除证明只退休被覆盖 Pod/container/device 在该时间之前的旧事实。节点在线、PodResources 无 GPU 分配、同 UID 所有已知 CRI 容器具有正向 EXITED/finished_at 是必要条件；CRI 单次空列表或 API 强删不够。新于解除证明的分配重新计占用。release 查询不写数据库、不删除 workload、不结束 usage、不发配额释放。

## 构造样本与验收入口

`parse_test.go` 固定上游字符串，F=1024，blocks=6、runtime=6144m；`replay_test.go` 构造两张 24576 MiB 设备、K=4、6 GiB/25% 请求，通过真实 TLS HTTP server 与真实 Unix gRPC server 驱动生产读取器；NVML 库存仅在测试装配中注入。Node/scheduler/PodResources/CRI 原始格式和签名 payload 均在测试源码中可复现，源码 manifest 给出样本模板摘要。每次回放的随机身份、证书与当前时间是隔离输入，不称实际采集。

`make test-provider` 在真实 PG 中验证三源合并、未投影占用、pending、单卡矛盾、失联/陈旧/F 漂移未知、适配器重建、API 强删保留、正向解除恢复；`trust_test.go` 验证签名、预算、Node UID、过期和重复键边界。最终命令/候选摘要/原始日志见执行验收记录。
