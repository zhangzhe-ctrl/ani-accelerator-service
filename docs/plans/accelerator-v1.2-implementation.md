# Accelerator v1.2 首期独立实施计划

> 合同更新（2026-10-27）：本文中对外 `ObserveRelease` 的要求已由[接口下线说明](../releases/2026-10-27-observe-release-retirement.md)取代（superseded）；其余历史计划与结果保留，不代表本次消费方或环境验收。

批次：`ACC-V12-01`  
日期：2026-09-23  
状态：`planned`，本文件不表示已开始实现或已通过验收。

## 1. 已确认的目标和边界

本批完整实现 Accelerator 首期：GPU 供给、规格、设备清单、实际绑定、容量视图、使用投影和释放观察，交付活跃 Proto 定义的全部 22 个 RPC。Governance、Inference 只作为有明确合同的接入方；不以其当前源码、改造进度或联调可用性作为本批开发及独立验收前置。

用户已确认：

- 完整实现 Accelerator 首期，不限于单位规则和目录的小切片。
- 本地只进行必要阅读、源码/文档编辑、文件传输以及 Git 提交和推送；生成、格式化、依赖下载、构建、测试、数据库、进程、容器及环境验证全部在 SSH 别名 `fedora` 对应远端完成。
- 当前没有可用 GPU 环境。本批完成标准为**服务完整实现及 Fedora 上的无 GPU 独立验收完成**。真实硬件验证属于后续批次，固定保留 `not_verified`。
- 本轮仅落盘实施计划和 Goal 提示词，不执行实现、提交、推送或远端环境变更。后续执行以用户实际提交 Goal 为开始指令。

完成本批必须满足：22 RPC 有真实实现并装配；Go 领域规则、真实 PostgreSQL、真实 TLS 握手及权限、并发/重启恢复、provider 证据解析回放、接入合同均完成；通过本计划全部“本批必验”项。不能用接口占位、内存 repository、Python fixture 或只测 use case 代替这一目标。

以下结论独立记录，不混为一个 PASS：

| 结论 | 本批要求 |
|---|---|
| Accelerator 代码与独立软件验收 | 必须完成并有证据 |
| GPU 硬件、实际限制、共享推理及整卡排他 | `not_verified`，无 GPU 环境，后续执行 |
| Governance/Inference 正式接入及全链退额 | 本批范围外，`not_verified`，交付合同与交接清单 |
| Git 发布与 final-SHA CI | 有明确发布目标后执行并单独记录；目标缺失不伪造推送成功 |
| 正式供给开放、生产上线 | 本批不执行 |

**无 GPU 条件下，非测试供给不得被标记为 VERIFIED，不得发布正式消费能力或 OPEN。**测试环境可以通过仅测试装配的证据端口覆盖有效基线、发布、OPEN 和 Resolve 的正向分支；这些记录只能存在于任务隔离测试库，不得提供生产 `skipVerification` 开关，不得进入正式配置或交付种子数据。

## 2. 固定输入及当前基线

仓库：`/home/chabking/workspace/ani-accelerator-service`。Go module：`github.com/zhangzhe-ctrl/ani-accelerator-service`。

实施输入：

1. 用户在本计划中的已确认范围及执行位置约束。
2. [AGENTS.md](../../AGENTS.md) 的分层、生成与提交前验证要求。
3. 活跃 [Accelerator Proto](../../api/protos/accelerator/v1/accelerator.proto) 与 [owner GPU 附件](../../api/protos/accelerator/integration/v1/owner_gpu_attachment.proto)。生成代码不是合同修改入口。
4. [v1.2 设计方案](../design/v1.2/设计方案.md)、[接口字段字典](../design/v1.2/接口字段字典.md)、[配置及接线说明](../design/v1.2/运行配置与产品接线.md)。
5. [验收矩阵](accelerator-v1.2-acceptance.md)、[Goal 提示词](accelerator-v1.2-goal.md) 和 [执行状态](../execution/status.md)。

2026-09-23 规划时实查：`main` 尚无提交，项目文件均未跟踪，未配置 Git remote；运行骨架和 SDK 已存在，GPU 业务未装配。不得编造 baseline SHA，也不得把来源包历史 PASS 当成本批验证结果。R0 重新盘点并冻结实际文件清单；未来已有提交时记录真实 HEAD，保护期间新增的用户文件。

`docs/design/v1.2/` 保留为来源快照，不通过修改来源文档、SQL 或历史验证记录宣称实现完成。字段字典中的旧路径以 [导入说明](../design/v1.2/README.md) 和活跃 Proto 为准。必要的合同修正应在活跃源文件及新接入文档中说明理由和兼容影响，不能以现有 Inference 实现反向重定义本服务。

## 3. 执行位置与资源边界

| 位置 | 允许的工作 |
|---|---|
| 本地仓库 | 阅读、编辑、检查 Git 状态/差异、文件转运、提交、推送；接收远端生成和格式化后的文件 |
| `fedora` 任务独占目录 | 所有工具安装、依赖下载、格式化、Proto/配置/数据库代码生成、静态检查、构建、测试、PG、服务进程、测试证书、容器及审计 |
| 其他主机和集群 | 本批无授权目标，不探测或使用，不从其他任务继承授权 |

本地不运行 Go/Python/Node 测试或生成器、`gofmt`、`make`、数据库、容器、服务、API 验证或集群命令；不能因 Fedora 不可达而回退本地执行。必要的源码差异阅读不等同于运行验证。

R0 在 Fedora 冻结：实际主机身份、任务目录、可用工具链、任务专用缓存与临时目录、隔离 PG 数据库/角色/端口、证据目录、磁盘和现有资源占用。路径和端口依据现场选择，不写死共享目录，不复用其他批次数据库。测试 CA/密钥仅在远端生成并保存于任务目录，不写入 Git、不打印私钥；合同示例不含凭据。

不安装 GPU 驱动、Volcano、HAMi 或 Kubernetes 集群，不改节点标签/全局 ConfigMap，不执行真实 GPU 工作负载。provider 的解析和客户端行为使用可追溯输入回放、受控测试 API 和故障注入验证，不称为真实 provider 接入通过。

生成回传流程：本地编辑源文件 → 传输到 Fedora 候选目录 → 固定工具生成/格式化 → 回传限定路径的结果 → 阅读差异 → 将最终候选树重新传至 Fedora 验证。禁止把整份远端目录覆盖回本地，禁止覆盖并发编辑或回传缓存/密钥。

## 4. 允许和排除的修改

允许本仓以下已有目录及明确列出的新增目录，均按实际切片逐步增加：

- `cmd/ani-accelerator-service/`：显式装配、进程生命周期。
- `internal/biz/`、`internal/service/`、`internal/data/`、`internal/server/`、`internal/conf/`、`configs/`。
- `api/protos/accelerator/`；`api/gen/go/` 仅接收远端固定生成流程的产物。
- `go.mod`、`go.sum`、`Makefile`、`buf*.yaml`、`.github/workflows/`、`scripts/`、`tests/`、`.gitignore`：仅与本批直接相关的依赖和验证入口。
- 新增 `migrations/`、`queries/` 及必要的数据库生成配置：只属于 Accelerator，不复制 Governance 表。
- `README.md`、`docs/runtime*.md`、`docs/plans/`、`docs/execution/`；新增 `docs/contracts/`、`docs/runbooks/`。
- `docs/scaffold/bom.cdx.json`：由远端审计流程生成；其他来源记录保持历史语义。

禁止修改兄弟仓库（包括 Governance、Inference、IAM、installer、layout），禁止新增共享 ANI runtime、替代 Inference 业务服务、配额账本/退额 worker、库存锁定交易、RegisterConsumer 前置、预留服务、在线切卡、动态扩缩、多 GPU 容器、通用 Agent 平台或调度器。BOSS/Console/BFF 改造、生产发布、现有集群操作、仓库创建/改名/迁移均在本批范围外。

`internal/biz` 使用领域类型及所需端口，不导入 Kratos、protobuf、Kubernetes client 或存储驱动。`internal/service` 做传输适配，`internal/data` 实现 PG/provider 端口，`internal/server` 做传输与鉴权，composition root 位于 `cmd/`。

## 5. 实施阶段

### R0：冻结输入、基线和远端验证场地

- 重新阅读本计划，盘点 dirty/untracked/HEAD/remote，不清理或重置用户工作。
- 冻结首期静态整卡组与共享组、每集群一个共享向量、每 Pod 一个 GPU 容器、每容器一设备。副本上限属于受控 owner 能力合同；给出明确支持值和负向校验，不从未完成的 Inference 代码推定能力。
- 完成 Fedora 环境清单。确定 PG 版本、驱动/查询生成及迁移工具并固定版本；数据定义/查询保持单一来源，不照搬设计 SQL 成为启动迁移。
- 冻结规范摘要算法及跨语言测试向量：排序、重复 key 拒绝、整数范围、编码、排除动态字段；冻结稳定错误 reason、调用身份/动作表及分页规则。
- 对未提交源码：将显式文件清单传入 Fedora 独占 Git 工作目录，远端形成逐文件摘要并执行现有 `make verify`；该门禁不要求已有 HEAD。验证后保持候选内容不变，在本地建立首次基线提交。无需先推送才能验证。
- 产物：输入/环境记录、基线 manifest 或真实 SHA、远端基线门禁日志、后续阶段执行清单。

缺少发布 remote 仅影响发布步骤，缺少 GPU 已明确属于后续范围；二者不阻塞本批可独立实现部分。

### R1：领域规则、合同及身份权限

- 实现 ProviderBaseline、Profile、GpuRequest、ResolvedGpuPlan、单位类型及不变量；同时支持 WHOLE_EXCLUSIVE、SHARED_FIXED。
- 精确 MiB/block 换算、逐卡量化、安全 int64 乘加及 provider 字段边界；禁止静默取整、双重归一化或把 factor 当超配比例。
- 生成不可变运行片段及计划摘要；受管资源/标签/注解白名单；验证 shape、replicas、container、组/规格版本及基线一致性。历史 plan 不按新 factor 重算。
- 落实精确服务证书身份、委托上下文及允许动作。AdminRead 无伪 tenant；Gov 可委托租户查询及写投影；ObserveRelease 仅允许对应受支持 owner 直接调用。不能仅凭“证书由同一 CA 签发”或请求自报 owner 授权。
- 在 `docs/contracts/accelerator-consumer-contract.md` 声明 22 RPC 的调用者、字段来源、幂等、错误及重试；说明 Gov→Inference 附件与既有 ReportQuotaRelease 的责任，不在本仓声明同名替代退额服务。
- 覆盖原 UNIT-01～06 的服务内规则及新增摘要/溢出/权限验收；Gov 是否真的未扣额仍是后续合同验收。

### R2：真实持久化和供给目录纵向实现

- 接入真实 PostgreSQL、连接生命周期及依赖 readiness。迁移显式执行，运行角色无 DDL 权限，启动不迁移、不播种。
- 实现 RegisterCluster/CreatePool/List，管理写幂等记录与业务写同事务；actor+method+key 去重，同键异报文冲突。
- 实现供给组、Node UID 唯一归属、不可变规格版本及并发版本检查；Adopt 默认为 CLOSED，只核对既有事实，不写 provider。
- 实现 PublishProfile/SetAdmission 的证据门槛、Node 标签/成员/型号/F/基线一致性校验；测试注入只通过测试装配，非测试请求不得自报 VERIFIED。
- 实现全部目录查询和 Resolve/CheckGpuFit。容量不足和未知作为结构化参考；配置未知/漂移、形态非法、组关闭拒绝新的 Resolve。CLOSED 不宣称取消既有分配。
- 正式迁移覆盖已经实现的 slice；平台设施与租户投影各按自身所有权建模，租户数据具备明确 tenant 谓词和租户保持约束，无跨服务外键。
- 真实 PG 测试包含幂等重放、冲突、并发、唯一约束、分页、服务重启和授权隔离；不得以 SQLite/内存替代。

### R3：provider 适配、设备/绑定与容量读模型

- 实现有明确支持版本的 provider reader/normalizer，不只定义空接口。为 Node、调度分配、插件/Kubelet/运行时等实际使用来源列出读取方式、权限、标识、单位、F、时间、解除证据和失败语义；仅依赖已核实存在的接口。
- 使用 v1.2 已核对的上游版本及官方源码作为适配依据，执行时在 Fedora 固定引用和必要样本来源。没有证据的字段不能猜测；不能把伪设备 ID、节点内 index 或显存块直接当物理 GPU UUID。
- 样本注明来自官方源码构造还是实际采集；无 GPU 环境不得伪造“本轮真实采集”。fixture loader 不进入生产装配，不以回放数据形成正式验证证书。
- 实现设备清单、绑定持久化、来源水位和恢复。实际键为 cluster+Pod UID+container+physical device；绑定先到、未纳管占用和缺失使用投影均须保留。
- 同一设备内归并多来源并恰好归一化一次；冲突局部化。失联/过期/单次缺项保留未知占用，不计算为可用；可信解除证据只退休其覆盖范围内的旧记录。
- 实现 GetCapacity/AdminGetCapacity/CheckGpuFit，分别表达 known free、complete、原因；计算份数服从安全预算、splitcount 和已验证计算预算，不用 max(0,...) 掩盖不变量错误。
- 后台采集按应用 Start/Stop 生命周期运行，故障可恢复且不写业务配额/工作负载；查询不推进资源状态。远端用真实服务进程与受控 provider 测试源验证恢复。

### R4：使用投影、释放观察与完整 RPC 装配

- SyncGpuUsage 只接受 Gov 精确身份；校验支持 owner、可信引用和冻结计划；revision 1=DECLARED、2=ENDED。
- 真实 PG 事务保证同报文重放、异报文拒绝、2 先到可建终态、旧 1 不回退、同 resource 换 create_operation 不复活。ENDED 不删除 Binding，不触发配额或 K8s 写入。
- 实现 Get/ListGpuUsage、租户 ListBindings 和管理查询；未核验归属不暴露给租户，分页游标绑定主体和筛选条件。
- ObserveRelease 不依赖投影存在；检查对应 owner、资源引用、Pod 范围及关闭操作引用。空 Pod 列表、读取失败、失联、强删对象均不能证明释放。
- ALLOCATION_RELEASED 仅表示已核对的指定实际使用范围解除；owner 的范围完整性、未来不再创建和可退款条件仍由外部合同负责。
- 装配全部 22 RPC、稳定错误、日志脱敏和必要指标；未实现方法不得静默返回成功。以真实 gRPC+mTLS 客户端逐项覆盖所有 RPC。
- 交付测试调用器和合同样例。它只能证明本服务接纳/拒绝/持久化/隔离/观察/恢复，不能证明真实 Governance 占退额或 Inference 完整删除。

### R5：Fedora 综合验收、交接和源码交付

- 执行 [验收矩阵](accelerator-v1.2-acceptance.md) 全部必验项，按实际命令/退出码/日志/候选 SHA 或 manifest 记录；新增有必要的 PG、contract、provider replay、recovery 验证入口并写入 Makefile/说明。
- `make api`/必要生成与格式化、`make verify`、`make audit` 均在 Fedora。`make verify` 是 AGENTS 要求的必需全仓门禁；此外只跑与本批相关的集成和 race，不运行兄弟仓库全量测试，不靠删断言变绿。
- 运行真实 PG 多进程竞争、提交后中断/重启恢复、真实 TLS 负向测试、provider 来源失败/冲突/解除回放、22 RPC 覆盖；`make design-test` 仅是原 Python 模型回归。
- 完成接入合同、运维手册、显式迁移/恢复流程、配置说明、GPU 后续验证清单及全部遗留项。正式配置默认关闭供给，测试证书/数据/回放路径不进入生产样例。
- 清理本任务拥有的进程、临时数据库/角色和测试资源；保留脱敏证据及恢复点，不清理共享资源。
- 根据第 8 节完成源码提交、SBOM、final-SHA 验证和有明确目标时的推送。更新执行状态，并分别报告软件验收、发布、CI、真实 GPU 和跨服务接入状态。

## 6. 与外部服务的交接范围

| 合同 | 本仓交付 | 外部后续负责 |
|---|---|---|
| Gov→Accelerator | 目录/Resolve/管理/查询及 SyncGpuUsage 服务实现，精确身份/动作校验、样例和错误 | 入口认证、正式模块授权、账户/一次占额、可靠投递与同步重建 |
| Gov→Inference GPU 附件 | 冻结 plan、稳定引用、完整 charge、digest、创建/删除附件 SDK 与测试向量 | 在真实业务请求中携带和持久接受，正确映射业务模型/镜像等字段 |
| Inference→Accelerator | ObserveRelease、对应范围及证据语义，直接 owner 验证 | 提供完整实际 Pod 范围、关闭执行、查清在途副作用 |
| Inference→Gov | 接入文档引用既有 ReportQuotaRelease 和原 charge 全量累计释放规则 | 明确 DELETE 且整实例清理完成后，可靠报告释放事实并幂等退额 |

不为等待外部代码改造而停止 R1～R5，不修改它们的数据库或适配器。若外部实现和合同不一致，输出字段级接入事项，不降低本服务合同要求。

## 7. 真实 GPU 后续验证

本批所有 `GPU-LIVE-*` 均为 `not_verified`，原因固定为用户已确认无 GPU 环境。后续取得独立授权后，使用测试客户端和独立验证工作负载即可验证，不要求先完成 Inference 改造：

1. 固定硬件、节点 UID、组件 digest、实际有效 factor、splitcount、安全预算及受管标签；由既有运维安装流程负责配置。
2. F>1 时，冻结 plan → Pod 请求 → provider 分配 → 实际运行限制单位闭环。
3. 同父卡至少两个真实模型实例加载并推理，满 K 后新增 Pending。
4. 静态整卡竞争真正排他、量化尾差透明；可信解除后容量恢复，失联不假释放。

这些结果决定特定 baseline 是否可被验证和开放。软件测试中的受控有效证据不具备此资格。Governance 账本与真实 GPU 一致性另属跨服务联合验收。

## 8. Git、证据和回退

### 8.1 提交和发布

- 当前无 HEAD、无 remote。R0 首先保护现有文件；不要为了获得干净工作区执行 reset/clean/stash 或重新 clone。
- 每次提交前，本地拟提交候选内容必须在 Fedora 通过 `make verify`；远端生成回传引入变化后重新验证。验证清单包含未跟踪文件，不能仅依赖空的 `git diff`。
- 用户允许本地提交和推送。发布时使用已明确配置/用户指定的仓库 URL 和目标分支；未指定时使用 `codex/accelerator-v1.2` 工作分支交付，不默认向 main 合并。若届时仍无 remote，询问一次准确 URL 和目标分支，仅发布等待，不中断实现。
- 不从 Go module 地址推定新建 GitHub 仓库，不擅自创建 remote/repository。用户明确既有仓库 URL 后，可配置该 remote 并按指定目标推送；不强推、不改仓库可见性、不推镜像或 tag。既有 remote 若与预期不一致，停止发布并保留候选。
- 精确记录本地 final SHA、远端分支 SHA 和对应 CI。推送成功不代表 CI PASS；CI 不可用或未运行明确 `not_verified`。

### 8.2 首次提交与 SBOM 的顺序

`make verify` 不要求已有 HEAD，`make audit` 中的 SBOM 生成要求已有 HEAD 且非 BOM 文件干净，因此分开执行：

1. Fedora 验证无提交的完整候选树，本地提交通过的源码/文档。
2. 将该真实 commit 传到 Fedora 干净检出，远端运行 `make audit` 生成 BOM；回传 BOM 并审查。
3. 包含 BOM 的候选树在 Fedora 再通过提交前 `make verify`，本地仅提交 BOM。
4. Fedora 对最终 SHA 运行 `make verify` 与 `make audit`，BOM 必须无差异；按明确目标推送并核对 SHA/CI。

BOM 从自己的来源摘要中排除，BOM-only 提交不会形成自引用。对源码/仓内报告再修改时，应刷新审计，不沿用旧 BOM 结论。原始运行日志和最终提交后的回执放在 Fedora 仓外证据目录，避免日志写入候选树导致生成检查/SBOM 持续变化；仓内报告记录稳定证据引用及受测源码身份，最终发布回执可在最终回复给出。

### 8.3 证据要求

每项记录至少包括：验收 ID、层次（领域/真实PG/真实mTLS/回放/硬件/跨服务）、`pass|fail|not_verified`、源码 SHA 或 manifest、远端主机/任务目录、脱敏命令、退出码、日志位置、时间及限制。使用状态台账区分 `planned/running/completed/blocked`；不能以“计划已写”把运行验收标 PASS。

### 8.4 失败和恢复

先修复本仓可修复问题并重跑受影响门禁。远端暂时不可达时继续本地可做的源码/文档工作，不在本地运行验证。PG 实验保留迁移前恢复点及任务内上一已验证版本，验证停止进程、恢复数据、重启和状态一致性；破坏性实验只作用于明确一次性测试库，不将重建空库称为恢复。

只有需要范围外代码/资源、缺少必要访问、会覆盖用户并发工作、无法安全恢复的共享资源操作或不可修复的基础门禁时，才记录具体阻塞。缺 GPU 和外部服务未改造是已知排除项，不是本批阻塞。出现阻塞仍完成所有不依赖它的工作，不用无限重试掩盖错误，也不删除失败证据。

## 9. 收口清单

- R0～R5 所有本批必验项通过，22 RPC 已实现并经真实进程调用；没有成功占位接口。
- Fedora 的 verify、真实 PG、mTLS、恢复、回放及 audit 证据完整，最终候选与受测版本一致。
- 活跃 API/SDK、数据库迁移、provider 支持范围、接入合同、运维/恢复说明可供下一团队直接使用。
- 正式供给保持 CLOSED/NOT_VERIFIED，GPU 硬件和跨服务联合验收仍显式 `not_verified`。
- Git 提交/推送/CI 分别报告；remote 缺失时保留已验证提交并列出精确待办，不声称发布完成。
- 本批不自动启动 GPU 实装、Inference/Governance 改造、前端、生产部署或下一批。
