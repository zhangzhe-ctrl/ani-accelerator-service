# ACC-V12-01 完整 Goal 提示词

下面代码块可整体复制。它用于启动后续实施；本文件落盘本身没有启动 Goal。目标限定为完整服务实现及 Fedora 无 GPU 软件验收，真实 GPU 与外部集成不会被伪装为完成。

```text
请在 /home/chabking/workspace/ani-accelerator-service 执行 ACC-V12-01，持续完成 Accelerator v1.2 首期完整独立实现、Fedora 远端软件验收和有明确目标时的源码推送。不要只完成第一小片，也不要停在计划、接口占位或测试模型。普通实现决策自行推进，只有真实阻塞才报告，并继续完成不依赖阻塞的工作。

一、必须先读取并遵守
- /home/chabking/workspace/ani-accelerator-service/AGENTS.md
- /home/chabking/workspace/ani-accelerator-service/docs/plans/accelerator-v1.2-implementation.md
- /home/chabking/workspace/ani-accelerator-service/docs/plans/accelerator-v1.2-acceptance.md
- /home/chabking/workspace/ani-accelerator-service/docs/execution/status.md
- 活跃 api/protos/accelerator/ 合同及 docs/design/v1.2/设计方案.md。
以用户已确认范围为先：本批完整实现 Accelerator 供给、规格、设备、绑定、容量、使用投影、释放观察，共 22 RPC；Governance/Inference 只交付明确接入合同，不读取它们当前代码来设实施前提，不修改兄弟仓库，不等它们改好才验收本服务。

二、执行地点是硬约束
本地仅允许必要阅读、编辑源码/文档、检查 Git 状态和差异、传输文件、提交与推送。
所有依赖下载、工具安装、格式化、代码生成、静态检查、构建、测试、审计、数据库、证书、服务进程、容器及 API 验证均通过 SSH 在 fedora 完成。远端失败不得退回本地运行。
R0 核实 fedora 身份，创建任务独占源码/缓存/PG/端口/临时目录和仓外证据目录，保护共享资源及已有工作；禁止探测或使用其他主机/集群。

三、无 GPU 环境的完成标准
用户已确认当前没有 GPU 环境。本批要求真实 PostgreSQL、真实 mTLS/gRPC、多进程与重启恢复、provider 解析/受控来源回放、22 RPC 和全部软件必验项通过。
GPU-LIVE-* 固定保留 not_verified；EXT-* 是后续外部接入项。它们不阻塞本批软件完成，但不得声称硬件验证、真实推理、三服务闭环或正式供给可用。
生产构建和配置不能伪造 VERIFIED/OPEN。只允许在测试装配和隔离测试库中注入证据覆盖正向分支，不能增加生产 skipVerification 开关或用测试数据播种正式供给。
不安装 GPU 驱动、Volcano、HAMi/K8s，不部署正式服务，不改节点或共享配置，不做真实 GPU 实验。

四、修改范围
仅修改本仓 cmd/ani-accelerator-service、internal/{biz,service,data,server,conf}、configs、api/protos/accelerator、远端生成的 api/gen/go、tests、scripts、必要 Makefile/Go依赖/Buf/CI配置、README/runtime文档及 docs/{plans,execution,contracts,runbooks}；可新增本服务 migrations、queries 和必要固定版本数据库生成配置。docs/scaffold/bom.cdx.json 由远端生成。
保持 docs/design/v1.2 来源快照及历史验证不变。禁止手改生成代码、跨repo replace、共享 ANI runtime、Governance配额写入、Inference工作负载写入、库存锁/TCC/Consumer前置、预留/动态扩缩/在线切卡/通用Agent/调度器和前端。
biz 不导入 Kratos/protobuf/K8s/数据库驱动，service 适配合同，data 实现外部端口，server 管运输与鉴权，cmd 显式装配。

五、按 R0～R5 连续推进
R0：重新盘点 HEAD/dirty/untracked/remote，保护现有文件，冻结输入、摘要/错误/权限合同、owner副本能力界限和 Fedora 环境。规划时 main 无提交且无remote，不假定执行时仍如此。若仍无HEAD，将完整候选清单传至远端，形成manifest并先运行 make verify，再在本地提交同一份通过的源码；不需要先推送才能验证。
R1：实现整卡/共享的单位、规格、请求、静态plan、规范摘要、受管渲染及所有拒绝规则；精确服务身份和动作鉴权；交付字段级调用/幂等/错误/重试/owner附件合同和测试向量。
R2：实现真实PG、显式迁移、无DDL运行角色、事务幂等、目录、组/Node UID/规格版本；Adopt默认CLOSED，发布与OPEN需要受控证据，Resolve冻结plan。启动不迁移或播种；未知容量与配置非法区分。
R3：实现可执行的provider reader/normalizer及来源映射、设备/实际绑定持久化、采集恢复和容量查询。回放样本记录来源/版本/单位/F，构造样本不称实采；绑定可先于使用投影，多来源去重、局部冲突、陈旧/失联未知、可信解除退休旧证据均正确。
R4：实现SyncGpuUsage接收幂等、乱序/冲突/终态规则和租户查询；ENDED不清Binding。实现直接owner调用的ObserveRelease，不依赖投影存在，不把空范围/强删/失联当释放，不负责执行关闭或退款。装配全部22 RPC，通过真实进程客户端覆盖正负向行为。
R5：执行全部软件必验项，完成恢复/运维/接入文档、状态台账、证据、清理和Git交付。不要把Python fixture、内存模型、成功注册RPC或HTTP200当业务完成。

六、验证与证据
现有 make tools、make api、必要的make generate、make verify、make supply-chain-tools、make audit 全部在 Fedora；新PG/contract/provider-replay/recovery及受影响race入口按实现落地并记录真实命令。make verify 是提交前必需门禁，不能省略或在本地执行。
本地编辑→远端生成/格式化→限定文件回传→审查→最终候选重新远端验证。避免覆盖并发编辑，不回传密钥/缓存。
每项记录验收ID、pass/fail/not_verified、SHA或manifest、host、目录、命令、退出码和脱敏日志；原始日志在仓外。失败保留证据，修复后跑受影响项，完成时跑规定门禁。
测试客户端只能证明本服务接纳/拒绝/隔离/持久化/恢复；ObserveRelease只证明指定范围的实际解除，不证明owner完整范围、未来不再创建或可退款。不得将局部通过升级为原设计跨服务验收PASS。

七、提交、审计和推送
允许本地提交和向明确目标非强制推送。每个候选先在Fedora make verify，再本地提交。源码和仓内报告冻结后，将真实commit传到Fedora干净检出，执行make audit，回传BOM；含BOM候选再远端verify后本地BOM-only提交；最终SHA远端verify/audit且BOM无差异。
未另指定分支时使用 codex/accelerator-v1.2，不自动合并main、不创建tag或推镜像。使用已明确配置或用户指定的remote；无remote时继续完成实现/验证/本地提交，在发布前询问一次准确URL和目标分支，不猜URL、不擅自创建仓库。
核对推送分支SHA及对应CI。CI不可用记not_verified，CI失败保留fail并修复本仓问题；推送/CI/远端软件验收分别报告。最终发布回执放仓外或最终回复，避免反复修改仓内日志导致BOM漂移。

八、恢复和停止条件
不reset/clean覆盖用户工作，不清共享PG或其他任务目录。只清理本任务明确拥有的资源；迁移实验保留数据恢复点，验证真实恢复，不用清库重建冒充。
缺GPU/Inference未改造不是本批阻塞。远端访问缺失、候选被并发改写、必须修改范围外服务或破坏共享资源、基础门禁无法通过等才是具体阻塞。继续完成独立工作，再报告事实、日志、剩余项和最小所需输入；不以重复询问代替推进，不将fail改成pass。

九、完成时交付
更新 docs/execution/status.md 和验收记录，报告：实现的22 RPC及主要行为、迁移/生成合同、Fedora软件门禁、关键负向/恢复结果、代码SHA/推送/CI、证据位置与清理结果。
明确声明GPU-LIVE-*及EXT-*仍not_verified，非测试供给仍CLOSED/NOT_VERIFIED。本批软件完成要求所有必验项通过；有发布目标时完成推送，缺目标时准确报告发布待办。不要自动启动外部接入、GPU部署或下一批。
```
