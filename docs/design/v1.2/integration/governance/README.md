# Governance 侧增量参考

这两份 SQL 的 owner 是 Governance，不是 ani-accelerator-service：

- [002_governance_gpu_increment.proposed.sql](002_governance_gpu_increment.proposed.sql)：使用关联同步进度、删除受理幂等记录的提案。
- [003_quota_catalog_increment.proposed.sql](003_quota_catalog_increment.proposed.sql)：整卡与共享显存配额目录的增量提案。

它们依赖 Governance 现有表结构、通用账本与接口合同，只保留作跨仓接入参考。本轮未修改 Governance、未执行 SQL；不得放入 Accelerator 的正式迁移目录或启动流程。正式接入应在 Governance 按其 Ent/Atlas 和 API 登记流程实施，不在 GPU 服务复制额度账本。
