# GPU 绝对显存实施与真实验证索引（2026-10-18）

状态：共享、多规格、异常恢复、官方共享与官方整卡真实链及最终全局只读观察均通过。
固定方案 SHA256：`34a111e7896a0f8f13b62f6b60cca8520ae7c0c3fef7f5843419385e9468b32a`。

原始请求、回执、日志、私密配置与操作工具均在仓库外任务目录。下文 `R` 为 Fedora
`/home/chabking/gpu-catalog-absolute-memory-20261018`，本地镜像目录为
`/home/chabking/.codex/tasks/gpu-catalog-absolute-memory-20261018`。重任务均经 `ssh fedora`。
集群为 `kubernetes-admin@ani-lab` / `https://172.16.101.10:6443`，仅节点 `.10/.11/.12`；
GPU 节点 `ani-03` UID `af992ed9-cbe1-4f64-8fac-7433808bfab2`，唯一 T4
`GPU-a86cb9fe-53ca-e474-5375-b662a54e8878`。共享与整卡按独立 ACC 数据库和 scope 顺序验证。
执行采用 F1/MiB；逐卡安全预算为注册 blocks×实际 F 与 NVML 量化容量的较小值，多规格共用该物理账。

## 运行身份与正式协议

下列为实际业务验证所用运行版本；后续 CI、BOM 或本索引提交不等同于重新构建的二进制。

| 服务 | 运行源码 SHA | OCI manifest | 实际 executable SHA256 |
| --- | --- | --- | --- |
| ACC | `066f4abeb7a39ddaf40f493cc1c0fed352c8828b` | `e5d4738d19f6549be219cef038a6618e3a701179470ed43efd4a046efcc66d31` | `e20c3ce73e77b0f2e46ecfcddb9ffef7b8995ad1b51fe57702bafa24ac3b3424` |
| Resource | `4f341a0ae98d46760fb1314c001cc6d9515686d9` | `0583093a9c199b6995eda96731a9c913c742776e2dc4ed1fb629f3a37b50a1d9` | `ad9abd310c1477012ae18f64407be4029c99d335d85697dc0a39c1ae2b91d05c` |
| Governance | `8c64919d1b99171036d907ced7ca7f0e6539d111` | `8d1449632de0f6344cb5b63380fcdb359e719a5992de55fa34baad9a1caaf50d` | `1d737da12225b1349eea26a4779644ba300c807333cd796b880e10c1b63346b1` |

来源：`R/acc/deploy/{release-runtime,image-pin}.json`、`R/res/{build/receipt,deploy/runtime-receipt}.json`、
`R/gov/build/receipt.json`。表中 manifest 均为 `sha256:` digest；Resource 的 `6398defa…` 是 Docker image ID。

正式依赖均精确下载核对 Origin.Hash/checksum，使用 `GOWORK=off`，无 replace 或复制协议产物：

| 模块 | 正式版本 / Origin commit |
| --- | --- |
| `github.com/zhangzhe-ctrl/ani-accelerator-service` | `v0.0.0-20261017194326-24820240a6b0` / `24820240a6b02bc82f922e98448b32a1451b81f8` |
| `github.com/zhangzhe-ctrl/ani-governance/api/quota` | `v0.0.0-20261017184327-1c605c793b88` / `1c605c793b88e460a84e64a88cbfa91c0a801afa` |
| `github.com/zhangzhe-ctrl/ani-resource-service` | `v0.0.0-20261017195047-4f341a0ae98d` / `4f341a0ae98d46760fb1314c001cc6d9515686d9` |
| `github.com/zhangzhe-ctrl/ani-inference-service/api` | `v0.0.0-20261017190858-cf3b944ea479` / `cf3b944ea479bb92e93102d6bf74e909210e6291` |

ACC 运行提交 066 在实现提交 248 后仅增加 BOM 快照。Inference 仅发布独立 API 子模块，业务运行时未适配、未部署，Gov 未启用其 GPU 派发。
关联 PR：[ACC #1](https://github.com/zhangzhe-ctrl/ani-accelerator-service/pull/1)、
[Resource #8](https://github.com/zhangzhe-ctrl/ani-resource-service/pull/8)、
[Gov #23](https://github.com/zhangzhe-ctrl/ani-governance/pull/23)、
[Inference API Draft #2](https://github.com/zhangzhe-ctrl/ani-inference-service/pull/2)，均保留未合并。

## 真实业务结果

共同链路为正常 Gov 鉴权/选择 Preset → 原账占额与持久投递 → Res ACK/冻结执行 → 实际 Pod/CUDA →
ACC 生产 claims/binding → 正常删除与创建封闭 → 正向 RuntimeExited/AllocationAbsent/NodeOnline →
Res 薄释放 outbox/匹配 ACK → Gov 原账全退。DECLARED/ENDED Usage ACK 分别核对。

| 用例 | 实例 ID | 实际结果与原始入口（均相对 R） |
| --- | --- | --- |
| 首条 4CPU/8GiB host/8192MiB GPU | `479028cd-710c-424b-8639-39b66b855f4c` | PASS：CUDA、物理释放、8192 全退、容量恢复；`live/first-8192/` |
| 半卡 + 两四分卡 | `f58e6965-aa9d-46c3-8f82-31957b918dd2`、`e7ee1712-8b3f-4969-840a-06a57ec29602`、`03375939-64d9-402b-b182-fefde13ce3d9` | PASS：同卡共存 15360MiB/100cores，三者 CUDA 后逐个释放，全账零；`live-scenarios/shared-scenario-receipt.json` |
| 双半卡 + 额外 GPU Pending | `1d2ae87a-32f4-4a38-a808-ddabbc9627a5`、`becc7344-f754-457a-9125-651dc2ab4e77`、`66f231c9-b53e-455d-b874-c1a761aa27c6` | PASS：GPU-only Pending；CPU 余量 12.58、RAM 余量 27355589198B；释放后额外实例接替 CUDA，最终全清；`live-scenarios/evidence/full-pending-headroom.json` |
| 8 份 / 1MiB 步长 | `b3543972-acd3-4d89-b09e-24deabbc4d74` / `3a251aea-e255-4c38-b5e5-891816e34fd7` | PASS：实际 1920m/12cores、8193m/50cores；总量精确、+1B 分配被拒，CUDA 误差 0；各自全退；`live-scenarios/evidence/{step-eighth,step-8193}/` |
| 目录变化、Stop/Start、Res 重启 | `d23907fc-8a98-4556-a7bb-ff0199a1bbc6` | PASS：DISABLED/DELETED 拒新解析，既有 CREATE 重放不变；原 8193 保留、无完整释放 outbox，历史 Pod/PVC 持久且 CUDA 恢复；`live-scenarios/evidence/lifecycle/` |
| 丢失释放通知与重复上报 | 同上 | PASS：Gov 停止时先物理清理、原额仍持有；恢复后全退；同事件 ALREADY_APPLIED、冲突 reason 拒绝；换事件仅零增量审计、不二次退款；`failure-release/runs/lifecycle-d23907/` |
| 未知外部写与 ACC 离线删除 | `3599850e-2dc8-4e98-8c70-865b240f76cc` | PASS：真实 Notebook 201 后断响应，观察恢复唯一对象/原 mutation；ACC RPC 离线时 Pod 已消失仍 ComputeCleaned=false、1920 held/outbox0；同 ACC 恢复正向证明后全退；`live/unknown-write-eighth/`、`source-outage/result.json` |
| 官方 v1.16.0 共享完整闭环 | `5e3300f2-3ec2-4ff8-b69d-5c8fe48c45a5` | PASS：4CPU/8GiB host、8192MiB/50cores，CUDA/binding/物理证明、8192 全退、Usage ACK1/2、free1、GPU 对象0；`live/official-shared-8192/receipt.json` |
| 官方目录停用/恢复 | primary `6d0b7ce6-efec-42a0-9266-e2f46a3c7659` | PASS：rev1 ACTIVE→rev2 DISABLED，tenant selectable=false/GPU_PROFILE_DISABLED；rev3 ACTIVE 后 selectable=true；不可变 spec/version 不变；`live/official-shared-8192/primary-disable-restore.json` |
| 官方 v1.16.0 整卡 A/B/C | A `bca0aad9-f076-45f1-9bd8-4846cf0c2f8f`、B `4caf26ab-5683-4b9b-99f1-7ca742ce7538`、C `28e43532-0ca0-4dd5-a205-533dab5f02b0` | PASS：15360MiB/100cores/K1。A CUDA；B GPU-only Pending/重放后删除退 count1，A 不变；C Pending→A 正向物理释放并退 count1→C 同卡 CUDA/binding→C 删除退 count1。A/C RELEASE_CONFIRMED complete、最终 free1/commit0/全账零；`live/official-whole-suite/{run-result,handoff-summary}.json` |

共享阶段的零账/零对象是各记录时间点的事实，不代表之后实验窗口没有实例。官方共享 scope：
cluster `b4676598-7fcc-4bc1-9f2a-d6300aac5681`、group `1d445a4d-bdd5-44b9-8090-b61315f76bb1`、
Profile `6d0b7ce6-efec-42a0-9266-e2f46a3c7659`、Preset `159b0843-7fb1-474a-82f9-31537598329e`；
CREATE `b93e019e-176f-482b-81bb-404ef53917d9`、DELETE `a7d5a9e9-add2-4b69-b3ed-da91982172a1`、
Pod `a9043b9b-1f83-4f01-8515-1d9b93efb41b`，原 charge `19100983-b646-4612-9097-057bc2ec011f` 8192/8192。
整卡 scope：cluster `d385c7fd-669a-4364-8aa9-1eadee0fc47c`、group `75618d91-b9f8-4dbf-a682-cc56bf5c2bf9`、
baseline `86b0ce46-714d-4355-8961-6b814000941f`、Profile `d344da00-4ac0-494e-81e4-54575eea70e7`、
Preset `9f60b4da-42a3-45dc-b4bc-9d4f3af6703e`。B/C Pending 时 CPU 余量13.08、RAM余量28429331022B，
均超过请求0.5CPU/1GiB；见 `live/official-whole-suite/whole-{b,c}-pending-headroom.json`。
A/C 的完整 binding 共用 physical device `355e6009-3b77-8077-9c59-ade2480478f0`；
最终 capacity complete、free1/commit0，quota occupied/outstanding/other-GPU 均0，A/B/C 原 physical.count 各1已全退。
A→C 可直接接替，不要求观察到中间 free1；最终 C 释放明确核对 free1。
三者释放 outbox 与 Gov 原账 receipt 唯一匹配，Usage ENDED revision2/ACK2；B 从未分配，以封闭创建后的完整 owner fence 确认。
`handoff-summary.json` 固定全部 create/delete/event/receipt/charge/Pod IDs；`final-owned-object-absence.json` 核对15个所属 Notebook/STS/Pod/PVC/PV 均不存在。

## 必需门禁与验证边界

`R/acc-official-results/make-verify.log` exit=0，SHA256
`c89bcda781d933fc4b1085e91f2d0e1d3620a200aa55d4fb92a4879b00cc595b`。
包括固定工具生成/sqlc 一致性、真实 PG、22 RPC/mTLS、race、租户谓词变异、填充库恢复/迁移回滚、
build/vet/module verify、漏洞与泄漏扫描。最终候选 manifest 见 `R/acc-resource-release-gate-receipt.json`。
随后 ACC `066f4ab` Actions27、Resource `3faeeaef471706d211d6c5193f07f40938307d82` Actions131、
Gov `8c4852790a51fd2a09721082303bde985c050169` Actions94（run39069490810）均通过；
后两者运行提交之后仅 CI/test/BOM/provenance/合同文档变更，运行二进制身份仍以上表为准。

| 所需边界 | 已通过的精确测试/证据 |
| --- | --- |
| 单位/配置漂移/来源缺失仍保占用、free0 | `TestProviderConfigurationIndependentCollection/{factor,registration,scheduler_config,scheduler_image,inventory_missing,podresources,cri}`；生产读器+真实 PG，外部协议替身 |
| 冲突/未知不变空闲，缺失轮询和 ENDED 不清活分配 | `TestCapacityLocalUnknownAndBudgetInvariant`；`TestPostgresBindingsBeforeProjectionAndScopedRelease/{ani-inference,ani-resource-service}`；`TestSchedulerHistoryDoesNotHideConflictingOrInvalidOwner/two_verified_owners_conflict` |
| 伪造签名/换 tenant、owner key、Pod UID 拒绝 | `TestOwnerTrustBoundaries`；`TestSchedulerHistoryDoesNotHideConflictingOrInvalidOwner/invalid_late_signature_is_unassociated` |
| 跨 tenant/owner 与独立 Resource 方法白名单 | `TestAll22RPCsMTLSAndRecovery`：真实 mTLS 进程拒 foreign owner/伪造 tenant、Resource 非 ObserveRelease/Health Check/Watch；PG 隔离与并发恢复另在同日志 |
| 无测试证明仍创建/OPEN/Resolve | `TestFormalProductionWithoutTestProof`，`R/acc-official-results/evidence/formal-a.json`；普通生产 executable/PG，K8s/CRI/PodResources/NVML 外部源为明确替身，hardware NOT_RUN |

DELETE 先到/迟到 CREATE、未知写和 tenant/owner 原账负测由 Resource/Gov 的既有生产行为门禁覆盖；
入口分别 `R/res/evidence/`、`R/evidence/gov-verify-final/` 和 `R/gov-ci-39068256841/`。
这些门禁与上表 GPU 实测分别报告，安全负测未伪装成真实集群恶意工作负载注入。

## 首失败、恢复与后续入口

- 正常 Gov 目录 403：正常管理 API 补精确 platform-admin 路由权限后恢复；未直 SQL 授权。
- 满载额外 CREATE 曾遇 Gov 默认 3s 依赖 deadline；ACC 同范围 Resolve 实测 0.137s、complete/free0，原 key 经正常 Gov 重试成功；未改容量准入或超时映射。
- Volcano 的 GPU 不足写在 PodGroup 事件：driver 增加严格 StatefulSet UID 归属检查和 UID 事件读取；保留首 Pod-only 观察失败。
- 公共 API lowerCamel 回包：场景复用既有 field reader，按保存 mutation 重放；原失败均在 `R/live-scenarios/failure-*.json` 及 attempt 日志。
- 官方镜像首次存在 canonical tag 缺失与 CRI stale import-index：只修该 artifact 的规范别名/缓存，三节点实际版本和 PodImageID 已核对；`R/official-image-identity/summary.json`。未放宽业务 digest 校验。
- 整卡首探针把 NVML 注册预算当作 CUDA 可见总量：申请 15360MiB（16106127360B）。首个 native 句柄尝试仍被 HAMi 拦截 `dlsym`，dladdr 检查拒绝其 libvgpu 地址；该尝试的数值不作独立 native 事实。最新任务探针以 libc.so.6 resolver/libcuda.so.1 native 函数双 dladdr 检查后，A 的实际 `native_cuda_total = visible_total = expected = 15636037632B`，严格满足 `visible_total == min(requested_bytes, native_CUDA_ceiling_bytes)`，无容差。分配仍走 HAMi/libvgpu，env=15360m/core100，kernel 误差0，申请16106127361B返回CUDA2。`R/live/official-whole-suite/whole-a/` 的 actual-cuda、native-cuda-diagnostic、cuda-first-failure-diagnostic JSON 及原探针副本均保留；不声称全部 NVML 字节可由 CUDA 分配。完整 A/B/C 释放结果另以上表回执确认。

当前 driver SHA256 `d6679c35bf9055e7c53f758b419f6f8d365f63b54d9eefa038fd85a2ee93bca7`，
共享场景 SHA256 `8c8f55bd3806c4a84cc18aa4b3d22580e02d06e70fd256c72ee85c9a3f947dec`。
共享实测 CUDA 探针 SHA256 `4621609e771a4b579db2ba4f238a10bf344b1a9543e93f746f8ab1f9c0aeaadd`；
整卡首个 native 句柄尝试 SHA256 `66dd6f583b68b60417976f5c988b6e7b8a39a0d87bc28d29efaac8e0d842d768` 已失败保留；
双 dladdr 检查探针 SHA256 `1cb6a9ae978e8693eb806c520c0a67f382e0f0e655ef81b0715544b0596eb661` 已在整卡 A 实际 CUDA 通过。
恢复使用原 evidence root 的状态和稳定请求，按未完成的 `create-only/observe/delete` 接续；已退款 full 不重新 CREATE。

官方调度器固定 `volcanosh/vc-scheduler:v1.16.0` / upstream
`41f27d567fb5bdaf2cab01e1caaf48f7e266eefb` / linux-amd64 manifest
`90cadf93b0c449180ecab93d6f7be44442fc81d6d9df4e30d9c94e9e24957555`。
恢复版 artifact 保留 `v1.15.2` / `ba5120f9edf947bccf9da85b25b3a11181d8c7c55a373b521209f632e018b442`。
切换/恢复工具为 `R/supply-switch/switch.py`；已有 partial state 不盲目重跑 apply。
先正常清理/退额、关闭旧组、停止 Gov/Res，核实无 active/unknown/GPU 对象与剩余原额，再使用新独立 scope；
保留原 ledger/终态、生产 journal 和模式专用退出记录。禁止回到 F256、旧 GPU 合同或已知坏原版。

最终保留官方 v1.16.0 **WHOLE_EXCLUSIVE / F1 / K1 / OPEN**，数据库 `acc_mib_official_whole_20261018`、
CM/secret `acc-mib-official-whole`，scope 为 `R/supply-switch/official-whole/scope.json`。
共享组关闭保留；单张 T4 顺序验证两模式，不声明共享与整卡同时供给、多卡或多 GPU 节点分配。
恢复先只读原 checkpoint，以下命令在 Fedora 执行；失败时以同样参数使用 `run` 接续或 `cleanup` 正常清理：

```sh
gpu_task=/home/chabking/gpu-catalog-absolute-memory-20261018
python3 "$gpu_task/whole-scenario/run.py" status \
  --scope "$gpu_task/supply-switch/official-whole/scope.json" \
  --config "$gpu_task/supply-switch/official-whole/private/driver-config.json" \
  --evidence-root "$gpu_task/live/official-whole-suite"
```

最终只读 `R/final-observation.json` 于 `2026-10-17T21:22:34.312615+00:00` PASS：
三服务 `/proc/1/exe` 与上表精确匹配，官方 scheduler 实际 PodImageID 为90cad manifest，节点实际注册K1/F1；
capacity complete/free1、原账/account0、Res unsealed/outbox0、tenant Pod/STS/Notebook/PodGroup/PVC0、global GPU Pod0。
