# Governance 源码核对与改动地图

日期：2026-09-23。基于用户上传 `ani-governance.zip` 解压内容进行静态核对。ZIP SHA256见本目录input-sha256.json；未将历史文档的HEAD当成本次包的Git提交。本轮没有业务代码修改，没有重新执行包内历史验收，没有读取真实Inference仓库。

## 1. 实际定位

路径均相对上传包的ani-governance根。行号是本次解压文件的位置，不是未来修改后行号。没有把新建议冒充已有方法。源码包可能含本地配置，本交付不重打包源码、env、私钥或凭据。

| 引用 | 路径 | 位置 | 源码事实及限度 |
|---|---|---|---|
| G1 | `app/admin/service/internal/data/quota_ledger_repo.go` | 114–335;375–567;602–710 | 已有Occupy、Release、CancelUnsent。真实PG/进程行为本次未运行。 |
| G2 | `app/admin/service/internal/service/quota_adapter.go` | 18–43;60–100 | 显式owner/action注册；一owner一个adapter；command目前Actor为拼接字符串。 |
| G3 | `app/admin/service/internal/service/quota_dispatch_worker.go` | 155–211 | 删除原charge回补只取第一笔；失败只记日志仍继续构造命令。正式增量应取全向量、失败不发送。 |
| G4 | `app/admin/service/cmd/server/wiring_quota_default.go` | 1–32 | 正式构建返回空registry和nil owner map，不能说真实Inference已接入。 |
| G5 | `app/admin/service/cmd/server/wiring_quota_lab.go` | 1–56 | 实验注册与owner身份独立，保留build tag隔离。 |
| G6 | `app/admin/service/internal/server/quota_internal_server.go` | 45–66;83–163;173起 | 内部TLS与owner映射、累计释放接线已经存在。运行验证未执行。 |
| G7 | `api/protos/quota/service/v1/quota_release.proto` | 1–59 | 真实释放服务/字段/tag；int64累计值，现有协议不重新声明。 |
| G8 | `app/admin/service/internal/data/quota_admin_repo.go` | 178–184 | 非legacy代码一律LAB_ONLY；正式GPU执行能力需要定向增量。 |
| G9 | `api/protos/quota/service/v1/quota_catalog.proto` | 1–51 | 当前Enforcement只有UNSPECIFIED、LEGACY_CONFIG_ONLY、LAB_ONLY。账户量是string字段。 |
| G10 | `app/admin/service/internal/service/quota_admin_service.go` | 1–65 | 现有管理查询requirePlatformUser；新租户自助接口不能开放任意tenant路径。 |
| G11 | `api/protos/identity/service/v1/module.proto` | 完整枚举 | 没有GPU/Inference模块；正式权限模块编号必须明确登记。 |
| G12 | `docs/evidence/quota-gpu-local-01/README.md` | 历史报告 | 包内历史结果，不是本轮复跑；真实GPU仍不据此宣称通过。 |
| G13 | `app/admin/service/internal/data/ent/schema/quota_operation.go` | 1–175 | operation_id等为string，tenant为uint32映射；dispatch_state既有有限集合。 |
| G14 | `app/admin/service/internal/data/ent/schema/quota_charge.go` | 完整字段 | 原始/累计量与账户关系，复用通用账本。 |
| G15 | `app/admin/service/schema.sql` | 3310起，及quota定义/账户段 | 现有DB operation_id为varchar，tenant_id为bigint；增量表FK不能想当然写uuid列。 |
| G16 | `AGENTS.md` | 全文 | 核对仓库工程约束；本轮仅源码读取及方案生成。 |
| G17 | `go.mod` | 前部module/go及相关依赖 | 真实module为go-wind-admin，所需Go版本1.26.7；本轮未拉依赖、未编译。 |

## 2. 相对旧计划应纠正的说法

通用配额功能不能再统称“仅有计划”：上传代码中已有账本、持久命令worker、mTLS释放及实验实现。另一方面，正式registry仍为空，存在源码也不等于真实GPU接入已完成。旧计划文件自述“未实现”与后续代码同包并存时，以实际源码解释当前实现面，历史文档保留其当时范围。

当前返回LAB_ONLY并不意味着添加一个正式quota_code就启用强制资源适配。正式目录数据、adapter注册、启用配置、mTLS owner白名单、公开路由和权限要一起接线。

## 3. 需要实施的定向改动

| 项目 | 现有位置 | 新计划要求 |
|---|---|---|
| GPU输入和冻结规格 | 尚未定位正式入口 | 新业务service解析具体版本、使用原始输入hash幂等，首次受理保存plan，不让用户自报charge |
| 注册正式Inference | G2/G4/G5 | 一个ani-inference adapter承接正式CREATE/DELETE；不将simulator改名上线 |
| 原charge列表 | G1中GetChargeForOperation、CreateDeleteOperation、FindCreateOperationByResource；G3 | 新ListChargesForOperation返回完整类型列表按code排序；不以ChargeIDs下标对应配额项 |
| 删除错误处理 | G3 | 原charge读取失败不继续Dispatch；原operation/资源/租户匹配再发 |
| 结构化actor | G2/G3 | command增ActorType/ActorID，实验Actor兼容保留 |
| Enforcement | G8/G9 | 新ENFORCED/NOT_ENABLED为增量建议；以编译注册与启用能力派生，业务不可自报 |
| GPU使用关联 | 新专用同步repo/worker | 从原持久op+charges派生目标状态；独立进度，不改变业务dispatch attempts |
| 未发撤销DELETE | G1两个现有独立入口 | 提取同事务helper，保存本DELETE幂等结果，取消与领取共享原op锁；不写假ACK |
| 当前租户额度 | G10 | 新自助入口从Principal读租户，不带任意tenant ID |
| 模块/权限 | G11及既有API同步/角色装配 | 正式模块统一登记后固定；不擅借TENANT或新增GPU配额枚举 |

## 4. 正常链之外但要记录的已有代码差异

- Occupy中的租户/到期检查在幂等查询之前；入口重试、权限读取和新占额需要区分，不能直接称现有方法已满足“先命中重放再做新受理”。
- 源码到期判断使用time.Now，旧计划写数据库时间。需要严格兑现旧时间合同则做定向修复与测试；本轮不默默改动。
- 返回ChargeIDs的顺序注释与创建时排序、重放无排序不完全一致，正式调用不要依赖位置。
- worker当前批量领取后串行RPC；本轮未执行租约/多worker时间行为测试。新增接入时复测批尾租约、领取时next_attempt_at复核、旧代次回写，不能把纯计算测试替代实际worker测试。
- 存在构造函数内net.Listen等与工程文档的生命周期措辞差异。本任务不借GPU接入重构整个服务器，按受影响范围登记即可。

## 5. 尚未获得的依据

真实Inference的API源Proto、持久接受与删除实现、工作负载控制器、镜像/模型参数、重试策略和资源清理证据来源均未随本ZIP提供。新GPU附件不能替代这些业务合同。真实集群的插件镜像digest、有效factor、Node上报和运行显存限制也未提供。

## 6. 允许说明的验证结论

本次“已核对”指源文件、结构和定向实现逻辑；实际Go/Ent生成、PG事务、mTLS调用及业务执行仍未验证。输入和选读文件摘要用于复查版本，不证明业务正确。
