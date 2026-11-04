# Kubernetes / Volcano 当前分配来源

ACC 保存 Kubernetes 与 Volcano 的当前设备、分配和配置投影。它不读取
CRI、PodResources、containerd journal 或 node-agent 事件，也不证明历史
容器进程已经退出。Governance 配额、owner 删除和 ACC 当前分配分别更新。

## 来源与单位

| 来源 | 使用的数据 | 用途 |
|---|---|---|
| Kubernetes PodList | 完整 Pod UID 集合、phase、容器资源、Volcano 分配注解、list resourceVersion | 当前绑定成员、分配数量及未放置需求 |
| Kubernetes NodeList | Node UID/name、当前组标签、Ready、心跳与绑定该 UID 的 Lease | 当前节点范围和容量健康 |
| `volcano.sh/node-vgpu-register` | 物理 GPU UUID、型号、注册块数、K、Healthy | 当前设备清单和预算来源 |
| `volcano.sh/node-vgpu-handshake` | 上游注册观察时间 | 设备事实的新鲜度 |
| `volcano.sh/vgpu-ids-new` | 按 GPU 容器顺序排列的 UUID、blocks、core percent | 当前分配，不能用设备索引代替 UUID |
| 已管理的 plugin/scheduler Pod 和 ConfigMap | imageID、显式 F/K、挂载与配置摘要 | 确认 baseline、单位与配置一致性 |
| RuntimeClass / Volcano Queue | 精确 RuntimeClass 身份、受支持 selector、Queue Open | 初始化和供给组配置检查 |
| 可选 Prometheus NVML total | Node UID + canonical GPU UUID 的物理显存 bytes | 设备参数展示；缺失为 0，不参与容量或释放 |

显存预算固定为 `注册 blocks × 已验证 F`，支持 F 为 1 或 10。分配注解中
的 blocks 使用同一个 F 归一为 MiB。K、核心预算和 Whole 单卡互斥限制继续
生效；不从 NVML bytes、监控使用率或空闲显存计算调度预算。

支持的软件组合由已冻结的 `ProviderBaseline` 校验。现有镜像、启动参数、
ConfigMap 摘要、RuntimeClass 和 Queue 检查保留；v1.15.2 / v1.16.0 scheduler
继续要求三个默认 vgpu Allocatable 资源为正整数。历史硬件验证结果只覆盖
其记录的组合，不能替代当前配置检查。

## 当前绑定更新

完整成功的 PodList 才能替换当前绑定集合，resourceVersion 原样保存为
SnapshotRef；不把它解释为数字或释放凭证。

- Pod UID 缺席、phase 为 Failed/Succeeded，或合法当前 assignment 不再包含
  原绑定 key：该绑定不再计占。公开状态仍使用 `RELEASE_CONFIRMED`。
- Pending、重启中、非终态 Terminating Pod 仍有合法 assignment：继续计占。
- 合法空 assignment：结束该 Pod 原逐卡占用；仍有未放置请求时更新
  `UnplacedRequestedUnits`，不与已分配 Pending Pod 重复扣减。
- annotation、容器映射或单位无法解释：只保留该 Pod 原当前绑定为 Unknown；
  不复活已结束历史，不制造零显存占用。
- PodList 失败、缺少 resourceVersion 或分页未完成：保留原成员和数量，
  当前观察不完整。其他 Node/设备读取仍可独立更新。
- Node、设备或配置错误不阻止完整 PodList 中已经缺席或终态的 UID 结束。

绑定身份保持 `cluster + Pod UID + container name + physical GPU`。每轮重新
验证当前 owner 签名；没有有效签名的真实分配仍计占，但不获得 tenant/usage
关联。Usage ENDED 不会消除当前仍存在的 Pod 分配。

## 当前设备清单

完整 NodeList 中缺席的 Node UID、或完整合法注册清单中缺席的 GPU，退出
默认设备列表与容量读取。不健康卡仍在当前清单。Node/注册读取失败时保留
原当前设备身份并标 Unknown，不能把失败结果当空清单。

Node Ready、Lease、注册健康、单位/配置一致性和当前事实新鲜度决定容量
能否报告已知可用。Node 失联不触发历史证明或日志补证；正常当前来源恢复后
由下一轮集合替换收敛。

## 可选精确物理显存

受管 connection 可包含：

```json
{
  "prometheus_inventory": {
    "url": "https://existing-prometheus.example",
    "ca_file": "/run/prometheus/ca.pem",
    "bearer_token_file": "/run/prometheus/token"
  }
}
```

URL 与凭据独立于 Kubernetes 连接；CA 和 token 文件可省略。ACC 每轮每个
已注册集群最多查询一次，子超时 2 秒：

```promql
nvidia_smi_memory_total_bytes{ani_cluster_id="<clusterID>",ani_gpu_source="nvml"}
```

仅给本轮已由合法 NodeRegister 创建的设备记录补值。关联使用 `ani_node_uid`
及 `uuid`；UUID 去 `GPU-` 前缀并转小写只用于指标匹配，不改变 PhysicalID。
重复 scrape 按同一 Node UID/UUID 取 max；非法数值忽略。指标不能创建设备或
恢复已退出当前清单的设备。

未配置、查询失败或样本缺失时本轮 bytes 写 0，不沿用上轮值；因来源失败而
仅保留旧身份的设备也清零。0 表示“未知”，不改变设备观察完整性、预算、
绑定、初始化可选状态或 DiscoveryRevision。设备列表和容量请求不现查 Prom。
初始化未注册时不查 Prom，注册后只从已提交设备记录读取可选展示值。

## 配置与身份边界

保留受管 Kubernetes CA/server_name、client certificate 或 token 文件、节点
ConfigMap 覆盖、InstallationBaselines。token 文件在请求时重新读取。
初始化现有的条件标签写入继续使用独立 guard 和 Node UID/resourceVersion 检查。

移除 `node_agent_identity`、`local_node_uid`、`nodes` socket 配置、
`runtime_events_directory`、`runtime_journal`。配置仍严格解码，旧字段不能
静默生效。ACC 不再接受 node-agent mTLS/TokenReview 或 telemetry RPC；
Governance 服务认证、owner Ed25519、公有/租户隔离保持。

初始化结构版本只包含既有 connection/baseline/节点标签以及注册 GPU 的
UUID、ModelKey、MemoryBlocks、SplitCount、Healthy；排序稳定，物理 bytes、
采样时间和 agent 状态不进入哈希。旧 operation 的冻结 revision 不被改写，
继续遵守既有 VALIDATE、CONFLICT 和 Retry 行为。

完成新 ACC 与 schema/消费者切换后可退役 node-agent。GPU driver、device
plugin、scheduler、RuntimeClass、现有 exporter 和 Prometheus 继续保留。
