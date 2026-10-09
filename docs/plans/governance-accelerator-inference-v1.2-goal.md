# GOV-ACC-INF-V12-WIRING-01 完整 Goal 提示词

以下代码块可整体复制启动后续实施。本文件落盘没有启动 Goal 或业务修改。提示词包含为正式跨仓模块消费所需的任务分支提交、推送；不包含合并 main、生产部署或真实 GPU 验收。

```text
请启动并持续完成 GOV-ACC-INF-V12-WIRING-01，修改以下三个项目，交付 v1.2 的创建参数接入和退额双向接线：
- /home/chabking/workspace/ani-governance
- /home/chabking/workspace/ani-accelerator-service
- /home/chabking/workspace/ani-inference-service

必须先完整读取：
- /home/chabking/workspace/ani-accelerator-service/docs/plans/governance-accelerator-inference-v1.2-plan.md
- 三仓当前适用 AGENTS.md；Inference 若无仓内 AGENTS，核对上层适用指导，不编造已有规则或命令。
- Acc 活跃 api/protos/accelerator/v1/accelerator.proto、api/protos/accelerator/integration/v1/owner_gpu_attachment.proto 和 docs/contracts/accelerator-consumer-contract.md。
- Gov 当前 main 的 docs/contracts/gpu-owner-integration-guide.md、gpu_acceptance.go、gpu_contract.go、quota_adapter.go、quota_ledger_repo.go、quota_internal_server.go、wiring_ent.go。
- Inference 当前 inference.proto、创建/删除受理、gpu.Plan、PG快照、operation/runner和KServe参数投影、buf/sqlc配置。

一、用户确定的目标与责任
完整主链是：用户经Gov提交业务和GPU选择→Gov调用Acc解析冻结计划→Gov一次占额并持久命令→可靠投递Inference→Inference正确接受、保存和交付plan/原charges→推理负责人执行模型与完整清理→Inference回调Gov→Gov幂等退额及旁路Sync。
本Goal只负责：Gov正式创建/删除入口和adapter；Inference可信命令接收、同事务保存、ACK、参数取值/交付；Inference退额客户端和Gov退额身份/listener装配；公开契约发布；定向软件验证和负责人交接文档。
真实工作负载调度、模型加载/推理、物理GPU限制、完整清理判定、ObserveRelease业务调用、何时产生退额通知、业务outbox触发/调度由推理负责人完成。这些保持not_verified，不是本Goal软件接线完成的前置。不能自动扩大为整个推理生命周期重写或真实集群验收。
退额不能只留文档或空接口：要有实际mTLS/gRPC客户端、受管配置、composition root装配和真实Gov receiver/PG的受控调用测试。测试明确注入完成通知，不把它冒充真实删除。

二、基线与工作树
规划证据为Acc 1d32dd9a9173b8869fa0ef2ae64e20b88f2ca0a3、Inference 01033dbbca64164e7a1ae52516a4b731e5858e66、Gov远端main 25a171cc676e8dc5f2a614da08f64c0dc5daff06。它们是已核对输入；R0逐仓在线核实remote/main、HEAD、分支、dirty和untracked，记录执行base SHA及相关差异。
特别注意Gov旧工作区是refactor/quota-contract-convergence，不能当main，不能覆盖其_agent/或其他工作。优先复用本任务适用worktree；否则从核实main创建任务独占worktree，分支用codex/前缀。不要reset/clean/stash/强推、覆盖并发修改或直接在main实施。
计划/Goal未提交时将其携入任务worktree，保证不丢任务依据。无关main新增提交核对相关路径并记录即可；合同或主体责任冲突才阻塞依赖步骤，继续独立工作，不重复全仓审计。

三、必须复用的能力和禁止改写
Gov main已实现AcceptGpuCreate的授权/重放/Resolve/冻结/Occupy，AcceptGpuDelete、Ent事务账本、严格ACK、dispatch、usage sync和累计ReportQuotaRelease。复用它们，补真实Inference调用。不要照搬旧计划把当前Ent账本改成pgx/sqlc，不重建另一套配额/dispatch。
Inference已有gpu.Plan/校验/PG/KServe投影可复用；新受管GPU路径改为接受Gov plan，后台不再自行Resolve，不二次Reserve/Confirm。不要把新refund reporter塞进stop/restart/update的旧reservation退额步骤。
Acc继续只负责供给/规格/绑定/容量/投影/只读释放观察：Resolve/Sync调用者保留Gov，ObserveRelease保留Inference。ENDED不清活Binding，不加RegisterConsumer/MarkClosing/Finalize/TCC、库存锁或quota写入。

四、允许修改的路径
Gov：直接相关api/protos（推理BFF、退额源、必要identity登记）、拟新增api/quota公开子模块、受管buf生成配置、api/gen/go定向产物、app/admin/service/internal/{service,data,server}中的接线及对应测试、app/admin/service/cmd/server/wiring_ent.go、必要权限/模块数据脚本、go.mod/sum、contracts/交接文档和定向生成验证入口。
Inference：api/inference/v1及生成输出、buf和本任务需要的确定生成入口、internal/service、internal/biz/{inference,gpu}、internal/data/{postgres,governance,kubernetes}的持久化/退额/必要参数投影、cmd、queries/migrations/sqlc输入、go.mod/sum、对应测试和交接文档。
Acc：docs/{plans,contracts}和必要contract/auth用例；发现真实互操作缺陷才最小修复对应活跃API或接收/鉴权代码，不为三仓都有代码diff而改runtime。
新路径须明确标为新增。不得改第四仓、共享ANI runtime、来源设计快照、历史验收结论、无关依赖/框架或全平台认证；不手改protobuf/sqlc/Ent生成代码，不复制权威Proto/生成包，不跨仓internal导入或本地replace。

五、固定接口与持久化要求
1. Gov用户入口接收原推理业务+resource.gpu规格选择，用户不能提交可信plan/charges/tenant/owner或目的URL。创建调用已有AcceptGpuCreate，删除调用已有AcceptGpuDelete；拟新增公开路径在R1按当前仓接口规则登记，CRUD保持data包装及可信Principal。模块/action/角色/套餐关系来自权威登记，不能拿ACCELERATOR读权限放行推理创建，不能猜全局模块编号。
2. 沿用真实Inference CreateInferenceService，在业务消息增加公共GpuOwnerCreateAttachment；删除使用专用请求携带GpuOwnerDeleteAttachment，保留既有字段tag；响应增加明确DurableOwnerAck。公共BFF不暴露内部附件。
3. 原业务摘要只覆盖不含附件/即时派生ID的规范业务正文，避免自包含循环。计划摘要、业务摘要、资源引用和计量版本各自核验；使用同一规范和固定向量。
4. 受管实例采用Gov resource_id和原create_operation_id；删除采用Gov对应DELETE ID。后续generation/local operation不能改原退额引用。一次PG事务保存命令、业务正文、plan、原GPU子集及存在的非GPU全量charges、执行意图和ACK后返回。同ID同内容重放原ACK，异内容拒绝。adapter严格核对IDs和accepted并映射既有三字段ACK。
5. resource.gpu、plan.request、外层副本/LWS拓扑/容器一致。devices_per_replica=1；Deployment容器kserve-container；LWS容器main且GPU Pods=groups×(leader+workers)，不超过现合同16。读取原mode/model_key/shared_memory_mib/core_limit_percent、F/q/percentage、totals、scheduler/queue/selectors/annotations/limits/runtime-class及digest，重试复用原快照。
6. 通过真实参数转换及KServe渲染函数断言参数交付；queue字段核对受管annotation/既有PodGroup合同，不把保存字段当接线完成。不替负责人修改engine command/args或推导模型flags。缺plan/附件或不一致拒绝，不降级CPU。
7. 完成可信DELETE命令接收、原创建/计划/charge保存和durable ACK；允许实例尚不存在时保存DELETE接收/关闭意图，不能要求调用者先知道本地generation。已有明确关闭意图的迟到CREATE不得安排新执行。实际执行fencing/在途写/清理由负责人补齐，不把ACK或意图当完成。
8. 为受管GPU路径隔离旧reservation语义：stop/start/restart及普通异常不触发GPU refund，也不重占原charge；首次交付拒绝未经治理的GPU规格/数量/拓扑变更。历史direct记录只盘点、标记和保留恢复/清理路径，禁止补造Gov charges或自动退额，不做本Goal之外的批量历史迁移。

六、公开退额契约与正式依赖顺序
Gov根module=go-wind-admin，其当前退额生成包不能直接作为Inference的正式远程依赖。建立仅含退额协议生成代码的公开API子模块，拟定module github.com/zhangzhe-ctrl/ani-governance/api/quota，目录api/quota，生成输出api/quota/gen/go/quota/service/v1。
唯一Proto源仍为Gov api/protos/quota/service/v1/quota_release.proto；wire package quota.service.v1、RPC、enum和tag保持。Gov根module不改名。受管生成迁移后Gov和Inference引用同一发布包，旧Release生成包退出最终依赖图，避免重复descriptor。它不是共享runtime。
先生成/验证并发布Gov quota API候选→Gov receiver与Inference锁实际发布版本→完成并发布Inference新接口模块→Gov adapter锁该Inference版本→最终三仓验证。依赖为Gov runtime→Inference API→Gov quota API，不能两个根runtime循环。若Acc接口确有增量，先发布Acc并统一锁版本；文档-only沿用现有Acc版本v0.0.0-20260924030150-1d32dd9a9173。
最终GOWORK=off、无本地replace，以实际provider提交推导pseudo-version并核对sum，不能先复制生成包让编译过再宣称正式消费。

七、退额双向接线
Inference biz声明明确的退额端口，data实现ReportQuotaRelease adapter，cmd装配受管地址/CA/cert/key/server name/timeout；TLS1.3、无明文或skipVerify。端口接收负责人已经持久保存的完成通知，不自己决定何时退额，不创建公开HTTP退额入口。
通知使用稳定release_event_id、原CREATE operation_id、原charge_id/quota_code和累计released_total。GPU必须完整原GPU子集全量，不用provider块数代替MiB；reason仅RESOURCE_RELEASED或ABORTED_CLEANED，resource_refs仅审计。客户端对照保存的原上下文校验、检查完整响应并传播错误。重试同event/同内容，不临时换ID；触发/持久outbox由负责人做。
Gov装配固定ani-inference owner和已登记精确客户端DNS SAN映射，启用真实退额listener。它与Acc对客户端唯一URI SAN的规则不同。缺生产地址/证书可完成独占测试CA的软件接线，但不得猜SAN、接受任意同CA证书或靠环境任意owner授权。
Gov继续使用已有事务退额，必须有持久DELETE关联。测试先建立该关联，再显式注入完成通知；不得用超时、NotFound、ACK、退出或Binding缺失自动退额。

八、实施顺序与验收
R0：冻结三仓基线和隔离环境、版本/身份/权限/生成路线，保护工作树。
R1：公共契约、接口和真实生成，优先完成退额API发布，避免后期才发现不可消费；常规实现决策自行处理。
R2：先打通一副本共享最小创建接线：真实Gov受理/占额→真实InferenceRPC/PG/ACK→真实参数投影。Gov/Inference/Acc/账本/权限/持久化业务代码不能用替身；只在外部provider证据、模型/K8s接口使用明确测试替身或暂停外部执行，且标注software-only。
R3：接通DELETE接收和refund客户端→真实Gov mTLS/PG，用显式注入通知验证一次退额及重放零增量；不写业务完成触发器。
R4：覆盖计划C01-C06、Q01-Q04、A01、D01、H01：共享/整卡/固定副本/LWS参数、并发/ACK丢失、权限与跨租户/伪造附件、重启恢复、DELETE先到、错owner/CA/原CREATE/漏项/部分/超量/异事件内容/无DELETE、旧生命周期误触发防护、Sync旁路和正式模块消费。
R5：更新最终真实接线及负责人文档、最终门禁、三仓版本对/分支发布、资源清理和结果索引。不停在接口占位、复制fixture owner、纯JSON检查或只有文档。

九、地点、工具、生成与验证
本地仅阅读、编辑、Git和传输。所有依赖下载/工具/格式化/生成/构建/测试/审计/PG/证书/进程/容器/API驱动在已有授权Fedora完成，不回退本地。核对Fedora身份和现有重任务锁，使用任务独占目录/PG/角色/端口/缓存/TMPDIR/证书及仓外日志，GOWORK=off，建议GOMAXPROCS=2、GOFLAGS=-p=2。
Acc使用make api和规定make verify；Gov使用当前受管make api/模板和verify-ci。只改文档不自动跑全量。
Inference当前没有Makefile/scripts/.github，不能编造make api；核对buf.gen.yaml/buf.model.gen.yaml及锁定插件，建立本任务最小确定生成入口，只生成本仓文件，Acc依赖Proto从固定上游输入读取，引用其已发布Go包。SQL用现有sqlc.yaml；迁移显式执行。
Inference真实PG用任务INFERENCE_PG_DSN和现有lifecycleDatabase，迁移现有schema及新增增量后再跑；缺DSN导致skip不能记pass。先跑受影响service/biz/gpu/inference/data/postgres/退额adapter/cmd包的行为测试；API/数据库/装配跨模块影响要求一次最终生成可重现、go vet/go build和必要相关race及三仓规定交付门禁。不为了扩大测试启动未授权集群或GPU实验，无新变更/失败不重复全量。
每项记录验收ID、candidate SHA/manifest、host、工作目录、发布module/version/sum、命令、退出码和脱敏日志。复用已有真实PG/mTLS合同设施，用实际Inference receiver替换测试owner；fixture只替外部系统。保留失败，修复后验证相关项，不复制旧PASS。

十、提交、推送、边界与阻塞
本Goal允许在任务worktree实施，提交仅本任务文件，并向三仓已核实对应remote的codex任务分支非强制推送，以交付公开契约和正式consumer版本；不创建tag/PR、不合并main、不改remote/仓库可见性、不推镜像、不部署生产或操作共享业务库。provider先发布、consumer再锁版本，最终核对远端分支SHA和发布模块；CI按实际结果报告，不把未运行写成pass。
缺GPU/模型/生产部署资料不阻塞本批software wiring；缺权威模块编号/注册、正式模块发布权限、远端访问或必须改范围外服务时停止对应依赖工作，继续独立项，给出一次具体输入需求。未知不编造，不用测试身份启用生产，不把阻塞记成完成。
保护用户dirty/untracked/主分支；不reset/clean/stash/强推或复制整树覆盖。远端生成回传仅白名单差异，防止覆盖并发编辑；只清本任务资源。停止本Goal后不自动做下一批生命周期、部署或GPU验收。

十一、完成交付
给出三仓base/final SHA、provider→version/sum→consumer关系、任务分支、必验结果、CI、显式迁移/生成命令、脱敏证据和清理位置。所有范围内实现和必验项完成才标记Goal achieved。
更新一套权威三方合同及负责人交接：创建/删除/ACK完整消息、可信字段来源、PG保存位置、参数映射、整卡和共享示例、退额实际配置/可调用方法/稳定事件与累计单位/错误和重试。
明确推理负责人后续事项：真实模型/调度/硬件、可信Pod签名与完整范围、封闭创建/核清在途写、完整清理和ObserveRelease、关闭事实/可靠outbox、退额触发、真实环境恢复与验收。报告这些not_verified，不以软件退额测试宣布业务已释放或生产可用。
使用pass/fail/not_verified区分源码、软件接线、公开依赖、CI、部署和真实owner/GPU；最终回复首先说明创建和退额接线实际跑到哪一步及未完成责任，不用代码行数或测试数代替行为。
```
