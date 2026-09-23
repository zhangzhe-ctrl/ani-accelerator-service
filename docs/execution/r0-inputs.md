# ACC-V12-01 执行输入与隔离范围

2026-09-23 已授权执行 R0～R5。规划文件中的“本轮只落盘”是历史阶段说明，由本次用户执行指令替代。

初始仓库为 `main`、无 HEAD、97 个候选源文件、无 remote。原始候选已暂存冻结，传输到 Fedora 后逐文件校验，通过 `make tools && make verify`，再建立基线提交 `b898fd8`；业务新增文件不在该基线提交中。工作分支为 `codex/accelerator-v1.2`。

验证主机 `fedora`：Fedora Linux 44，用户 `chabking`/UID 1000，Go 1.26.7，Docker 29.7.2。任务根目录 `/home/chabking/acc-v12-01-20260923-bdtawbnM`，源码 `src`，独占缓存 `cache/{go-build,gomod}`、临时目录 `tmp`、证书 `certs`、数据库 `pg`，仓外证据 `evidence`。仅本任务的 PG 容器 `acc-v12-01-bdtawbnm-pg`；数据库端口由 Docker 绑定到 loopback 随机端口，实际端口写入仓外环境记录。未访问其他主机或集群。

PG 固定为 PostgreSQL 17.11，镜像 `postgres@sha256:f4c66b820c6f974249089d3d16d86a3698eae11e8746eb6644b2271031e91232`。使用 pgx v5 的固定依赖、手写参数化 SQL 和显式 SQL 迁移；不引入另一份生成查询定义。数据库启动、显式迁移、权限、备份和恢复均在 Fedora；服务启动不执行 DDL。

上游适配范围固定为 Volcano / volcano-vgpu-device-plugin v1.12.0 的 HAMi-core 路径；官方源文件与摘要保存于 `evidence/upstream`。它们是来源证据，不是实际设备采样或本次硬件验证。

owner 合同上限为 16 个固定副本，每 Pod 一个指定 GPU 容器、每容器一设备。该值是本批软件合同与测试边界；不声称已经验证 Inference 实际控制器支持。额外 GPU init/sidecar、滚动额外副本和动态扩缩不开放。

正式 mTLS 身份精确匹配受管 URI SAN，Gov 和 owner 分别授权。Gov 委托通过服务配置中的 actor/动作/tenant/cluster 范围和传输委托字段核对；平台调用无 tenant。游标以持久私钥 HMAC 绑定主体、过滤条件、动作和排序，不跨主体复用。规范摘要版本为 `acc-c14n-v1`，字段级定义和测试向量随合同交付。

基线证据：`evidence/r0-initial.manifest`、`r0-index.manifest`、`r0-index-match.log`、`r0-baseline.log`、`r0-baseline.exit`。基线包含历史 48 个 Python 模型回归通过；它们不证明本次业务实现。原始基线日志中的 Fedora `BASHRCSOURCED` shell 启动警告未使门禁失败；后续任务环境 unset `BASH_ENV`，不改主机共享 shell 配置。

全部生成、格式化、依赖、测试、审计、证书、数据库和服务运行只在 Fedora。本地只阅读、编辑、传输和 Git 操作。GPU-LIVE-*、EXT-* 保持 `not_verified`；正式供给缺少硬件消费证明时保持 CLOSED/NOT_VERIFIED。
