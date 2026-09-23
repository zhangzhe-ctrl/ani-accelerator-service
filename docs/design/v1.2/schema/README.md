# Accelerator 数据结构提案

[001_accelerator.proposed.sql](001_accelerator.proposed.sql) 是 v1.2 的 Accelerator 自有数据结构提案。它保留供给、规格、使用投影、设备、绑定与释放观察等设计，不包含 Governance 配额账本。

本次仅迁入原文件，没有执行 SQL，没有接入数据库或启动迁移。后续应结合实际持久化方案生成正式迁移、审查升级/恢复步骤并验证约束和并发行为；不得把本目录加入自动执行迁移路径。
