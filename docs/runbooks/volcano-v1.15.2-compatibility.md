# Volcano v1.15.2 来源合同比较

2026-10-13 在 `ssh fedora` 上核对固定上游源码，范围是 Acc reader/normalizer 与 Resource 冻结 recipe 的软件合同。结论：现有单容器、单设备、`hami-core`、默认资源名的合同可以限定增加 **scheduler v1.15.2 + 独立 device plugin v1.12.0** 组合；需要修正分配时间单位，并识别 v1.15.2 的 Allocatable 门禁。这是源码兼容性判断，尚不证明该组合在 GPU 节点、驱动、HAMi-core、containerd 或实际部署镜像上运行通过。正式供给 OPEN 仍需已有硬件消费证据。

本文件是一次固定源码比较。当前实现、测试与真实验收进度以本仓[执行状态](../execution/status.md)为准；本文不建立第二份执行账本。

## 固定来源

| 仓库与版本 | Tag object | 解引用 commit |
|---|---|---|
| `volcano-sh/volcano` v1.15.2 | `f0917e48403c4ffa2d8a022f05f1f5097fbe9c10` | `1462fb7b4835970708717456e3aed85e697ec2eb` |
| `volcano-sh/volcano` v1.12.0 | `d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400` | `d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400` |
| `Project-HAMi/volcano-vgpu-device-plugin` v1.12.0 | `afefcc2ff7944e9e68275e83217cf5eb7fb4a585` | `afefcc2ff7944e9e68275e83217cf5eb7fb4a585` |

上述身份来自各仓库 `git fetch --depth=1 origin refs/tags/...` 后的 `git rev-parse <tag>`、`git rev-parse '<tag>^{commit}'`。对独立插件仓库执行 `git ls-remote --tags origin refs/tags/v1.15.2 'refs/tags/v1.15.2^{}'` 返回空；这只说明该次查询没有这个 tag，不能把 scheduler 的 v1.15.2 当成插件版本。产品 baseline 仍须分别冻结两种镜像 digest、插件版本、HAMi-core revision 与配置摘要。

Fedora 私有任务目录：`/home/chabking/workspace/ani-resource-service-runs/instance-mvp-v15-20261013/upstream-compat/`。其中 `source-manifest.json` 记录仓库 URL、tag/commit、17 个读取文件的 SHA256 与行数，manifest SHA256 为 `0b536c57fcfd17b0fbf182ee247edefb6fde3efd77266fedfa14e756a0ffbe1e`；`snapshots/` 保存固定版本原文，`volcano-contract.diff` 保存相关 scheduler 文件差异。没有编译上游、准备其 Go 依赖、修改平台或采集 GPU。

## 字段与行为差异

| 项目 | v1.12.0 → v1.15.2 | Acc / Resource 的限定处理 |
|---|---|---|
| Node 注册 | `volcano.sh/node-vgpu-register` 保持六字段 `UUID,count,memory,model,health,mode:`。内存仍为插件编码的 blocks。 | 保持物理 NVIDIA UUID、去重、`NVIDIA-` model、正整数、`hami-core` 的严格解析；不加入 MIG/MPS。来源：[1.12 decoder](https://github.com/volcano-sh/volcano/blob/d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400/pkg/scheduler/api/devices/nvidia/vgpu/utils.go#L56-L101)、[1.15 decoder](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/utils.go#L56-L104)、[插件 encoder](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/util/util.go#L163-L169)。 |
| Node 握手 | 1.12 scheduler 读取并回写 `Requesting_` / `Deleted_`；1.15 移除这段逻辑。 | 握手 freshness 仍可作为**已限定插件**的健康合同：插件 v1.12.0 继续写 `Reported ` 加 Go 时间字符串，成功每 30 秒注册。保持源时间与失败保守语义，不能用接收时间替代。来源：[旧 scheduler](https://github.com/volcano-sh/volcano/blob/d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L97-L135)、[新 scheduler](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L94-L139)、[插件时间](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/plugin/register.go#L37-L54)、[注册周期](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/plugin/server.go#L767-L782)。 |
| Node 可调度门禁 | 1.15 要求 `status.allocatable` 存在，默认 `volcano.sh/vgpu-number`、`volcano.sh/vgpu-cores`、`volcano.sh/vgpu-memory` 均存在且 `Value()!=0`；1.12 没有此门禁。 | Acc Inspect 应在 1.15 组合检查三个值为正整数，避免注册信息可读却被 scheduler 拒绝。上游没有要求总量等于注册设备之和，不新增这种相等合同。来源：[1.15 gate](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L94-L129)。 |
| Pod 分配 | `volcano.sh/vgpu-ids-new` 与 `volcano.sh/devices-to-allocate` 保持 `UUID,NVIDIA,blocks,cores:`，多个 GPU 容器以 `;` 分隔，多个设备以 `:` 分隔。 | 保持现在的有界、去重、严格 UUID 与整数解析。来源：[旧 encoder](https://github.com/volcano-sh/volcano/blob/d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400/pkg/scheduler/api/devices/nvidia/vgpu/utils.go#L104-L156)、[新 encoder/decoder](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/utils.go#L106-L159)。 |
| 分配时间 | **两版 `volcano.sh/vgpu-time` 都是十进制 Unix 秒，均非纳秒。** | 本次起始源码 `ParseAssignedTime` 使用 `time.Unix(0,n)`，会把当代时间变成 1970 年。这是旧合同错误，需改为严格秒读取 `time.Unix(n,0)`，样本与文档一同修正；不添加秒/纳秒猜测。来源：[1.12 producer](https://github.com/volcano-sh/volcano/blob/d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L248-L255)、[1.15 producer](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L344-L351)。 |
| 容器映射 | scheduler 两版只为 `limits` 有 number 或 memory 的 GPU 容器输出条目，顺序沿用 Pod spec；普通 sidecar 不占一个空位。1.15 将提取逻辑移到通用 helper。 | 现有 Acc 对 GPU 容器过滤再匹配条目的方式不变。Resource 保持一个业务容器；不借此扩展 sidecar 或多 GPU 容器能力。来源：[1.12 请求提取](https://github.com/volcano-sh/volcano/blob/d24c04ca1a2813c506a1d9d3cd38dca9bf7ce400/pkg/scheduler/api/devices/nvidia/vgpu/utils.go#L173-L239)、[1.15 请求提取](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/util.go#L205-L275)。 |
| 分配回滚 | 1.15 的 Release 对非 success bind 清理分配注解；success bind 保留。Allocate 也避免已有分配的重复分配。 | 调度注解只证明当次调度分配。缺失/清理注解不能替代 CRI/PodResources 正向退出证明，不能自动释放真实容量或配额。来源：[Release](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L280-L310)、[Allocate](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L323-L369)。 |

独立插件在 `GetNextDeviceRequest` 中按分配条目索引取 `spec.containers[idx]`，并在 Allocate 拒绝多 `ContainerRequests`。这对混排 sidecar 的解释存在额外前提，不能由 Acc 的 reader 过滤逻辑证明插件运行正确。当前 Resource 一容器、一设备 shape 避开该范围；未来放开容器结构须独立核验。[插件索引逻辑](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/util/util.go#L233-L249)、[Allocate 入口](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/plugin/server.go#L339-L342)。

## 显存单位、recipe 与 Queue

2026-10-17 真实联调修正：原版 `1462fb7` 的 `GetIgnoredDevices()` 返回空列表，通用 `NewResource` 因此把 `volcano.sh/vgpu-memory-percentage` 当作可加容量。节点只宣告 number/memory/cores，正式 Whole 百分比计划在默认 Queue 被 proportion 拒绝为 overused，Pod 一直 Pending。按绝对 `memory=60` 块成功运行的实验不能证明该正式计划可调度。原始失败及后续修复环境的独立结果见 [GPU 联调回执](../evidence/gpu-live-20261017/receipt.json)。

本次受控[最小补丁](../evidence/gpu-live-20261017/volcano-percentage-only.patch)仅让固定 scheduler 从通用 scalar 计量中排除 percentage 参数，保留 memory/cores/number 的计量和设备过滤器中的百分比分配。没有修改 ACC 的 Whole recipe、伪造 Node allocatable 或放宽 Queue 预算。派生 scheduler 必须冻结新的实际镜像 digest，并为新 baseline、新 plan 重新采集、签署硬件证明；不能沿用原版镜像的证明。该结果不能推广为未修补 v1.15.2 的 Whole 支持声明。首轮临时试验曾恢复原版；用户随后明确要求保留修复版，后续部署与双模式验收以[保留版回执](../evidence/gpu-live-20261017/retained-receipt.json)为准，不再回退已知有问题的原版。[原版忽略列表](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L139-L141)、[通用资源转换](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/resource_info.go#L94-L110)。

后续 ACC 资格政策迁移没有改变 GPU baseline 或该调度器：当前无时间自动失效的 v2 资格、采集/释放解耦部署和必要真实复核见 [baseline 资格回执](../evidence/gpu-live-20261017/baseline-qualification-receipt.json)。原保留版回执继续作为原始硬件实验的历史证据。

插件 v1.12.0 注册的逐卡内存是 `floor(bytes / 2^20) / F` 的整数结果；runtime 注入的 `CUDA_DEVICE_MEMORY_LIMIT_i` 是 `used_blocks * F` 加 `m`。因此节点注册与 scheduler 分配读数乘一次可信 F 得 MiB，而 CRI 的 runtime 环境已是 MiB，不得再次乘 F。[注册转换](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/plugin/register.go#L78-L89)、[runtime 注入](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/plugin/server.go#L414-L419)。

scheduler 的设备过滤器对每个请求容器、每张选中的卡分别使用 `vgpu-memory`；内存百分比存在时覆盖块数，100% cores 要求此前没有使用者。资源单值不能乘 Pod replicas 或设备数量，业务总计另行累加。现有 recipe 的 Shared 使用精确 `MiB/F`，Whole 使用 100% memory、100 cores，与设备分配逻辑一致，但仍须通过上述通用资源与 Queue 的真实入口。[1.15 逐卡分配](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/utils.go#L393-L468)、[当前 Render](../../internal/biz/plan.go)。

resourceCountName、resourceMemoryName、resourceCoreName、resourceMemoryPercentageName 均可被上游配置。当前 ANI recipe 与 reader 使用默认名称，兼容声明只覆盖这些默认名称和 `volcano.sh/vgpu-mode=hami-core`。ConfigMap 固定摘要不能替代实际配置字段相符的核验；继续要求有效 F/K 与已登记 baseline 一致，不引入任意资源名映射。[1.15 配置字段](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/config/vgpu.go#L81-L113)、[插件默认配置](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/volcano-vgpu-device-plugin.yml#L23-L36)。

插件先载入 `volcano-vgpu-device-config`，显式 CLI factor/split/core-scaling 覆盖对应值，再读取 `/config/config.json` 的逐节点覆盖；逐节点文件可再覆盖 split 与 scaling/mode。只有显式 factor 参数可核对时，现有 Inspect 的 F 证据路径可以保留；节点注册 count 与实际 baseline 的 K 校验也应保留。硬件实验配置需固定完整有效配置，不能仅凭存在 CLI 参数便宣称 scaling 或 core enforcement 已被验证。[配置加载顺序](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/util/util.go#L569-L615)、[逐节点覆盖](https://github.com/Project-HAMi/volcano-vgpu-device-plugin/blob/afefcc2ff7944e9e68275e83217cf5eb7fb4a585/pkg/util/util.go#L619-L652)。

`schedulerName=volcano`、Pod 注解 `scheduling.volcano.sh/queue-name` 仍是这一接线入口；PodGroup controller 从注解写入 `spec.queue`。VGPU 需要 scheduler 配置启用 `deviceshare.VGPUEnable`，版本名本身不足以证明已启用。1.15 新增 vGPU memory/cores 的 Queue 资源计量，Queue 实验容量必须覆盖实际 recipe，不能只检查 Queue 为 Open 就称可调度。[Queue key](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/staging/src/volcano.sh/apis/pkg/apis/scheduling/v1beta1/labels.go#L46-L48)、[PodGroup 继承](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/controllers/podgroup/pg_controller_handler.go#L398-L402)、[VGPU 配置](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/docs/user-guide/how_to_use_volcano_vgpu.md#L60-L78)、[Queue 计量](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/device_info.go#L150-L177)。

1.15 新增的 PodGroup spread / GPUExclusiveRules 不属于当前冻结 recipe；本文不启用这些特性，也不以它们替代 Whole 模式的已有独占合同。[spread 入口](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/pkg/scheduler/api/devices/nvidia/vgpu/type.go#L52-L57)、[ExclusiveRules 文档](https://github.com/volcano-sh/volcano/blob/1462fb7b4835970708717456e3aed85e697ec2eb/docs/user-guide/how_to_use_volcano_vgpu.md#L161-L179)。

## 最小实现与定向验证建议

1. `ValidateBaseline` 只增加明确组合 `(scheduler v1.15.2, device plugin v1.12.0)`，保留既有 `(v1.12.0,v1.12.0)`；其余版本、adapter、recipe、memory encoding、镜像/配置摘要、F/K/scaling 和硬件证据检查继续闭合。不要使用 `>=`、前缀或 any-version allowlist。
2. `ParseAssignedTime` 读取严格十进制 Unix 秒；固定一个当代秒样本验证准确 UTC，拒绝无效、非正、溢出及超出业务时间范围的值。修正调用 fixture 的 producer 与 `provider-sources.md` 的单位描述，保留来源时间参与陈旧判断。
3. v1.15.2 Inspect 为所选组节点读取三个默认 Allocatable 资源键并检查正整数；缺失、零或破损值时拒绝供给就绪。只新增这项来源门禁，不扩大为新供给状态机或资源总量等式。
4. 在既有 provider replay 中复用真实 reader，覆盖 1.15.2 非零 Allocatable + 插件 Reported + 原有 Node/Pod/CRI 编码的成功路径，以及缺失/零键拒绝；Render 覆盖 Shared 每设备块数与 Whole 100% recipe 未漂移、未知版本拒绝。测试仅在 Fedora 定向执行。

本文记录固定源码比较；随后实现、定向测试与真实验收结果在本仓 [执行状态](../execution/status.md) 维护。物理 UUID、实际显存/核心限制、共享与整卡互斥、PodResources/CRI 对齐、Queue 可调度性和删除后的硬件容量恢复，均以相应实际镜像与节点的联调证据为准，不能由源码比较推断通过。
