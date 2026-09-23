# Accelerator 执行状态

更新：2026-09-23，批次 `ACC-V12-01`，工作分支 `codex/accelerator-v1.2`。

R0～R4 完成，R5 软件必验通过，正在完成源码/BOM/final-SHA 交付。完整结果见 [本批验收记录](acc-v12-01-acceptance.md)，实施依据为 [实施计划](../plans/accelerator-v1.2-implementation.md) 与 [验收矩阵](../plans/accelerator-v1.2-acceptance.md)。

| 项目 | 结果 | 已完成行为 |
|---|---|---|
| R0 环境与输入 | pass | 初始候选 Fedora verify 后提交 b898fd8，独占目录/PG/缓存/端口/证据 |
| R1 领域与合同 | pass | 整卡/共享计量、静态 plan、规范摘要、权限及字段级接入合同/固定向量 |
| R2 PG 与供给目录 | pass | 显式迁移、无 DDL 运行角色、事务幂等、目录/组/规格、发布与 OPEN 证据门槛 |
| R3 provider/绑定/容量 | pass | 固定来源 reader、真实 PG、三源去重、局部冲突、未知/恢复与可信解除 |
| R4 使用投影/释放/22 RPC | pass | 真实 mTLS 进程正负向、终态幂等、绑定独立、只读 owner 释放观察 |
| R5 软件门禁 | pass | verify、领域/PG/contract/provider/restore/race 全部退出 0，修复后漏洞扫描无发现 |
| SHIP-01 最终 Git/BOM/审计/清理 | not_verified | 软件已通过，正在收口最终提交与仓外回执 |
| SHIP-02 推送 | not_verified | 当前无 remote，不猜目标、不强推 |
| SHIP-03 CI | not_verified | 尚无最终 SHA CI 结果 |
| GPU-LIVE-* | not_verified | 无 GPU；未进行硬件消费、模型推理和满载/解除实测 |
| EXT-* | not_verified | 仅交付接入合同，未修改或等待 Governance/Inference |

正式构建没有测试绕过；无有效硬件证明的供给保持 CLOSED/NOT_VERIFIED。ENDED 不清活绑定，退出/重启不是实例删除，本服务不写配额或工作负载。

仓外证据根：`fedora:/home/chabking/acc-v12-01-20260923-bdtawbnM/evidence`。`software-final-01` 保存完整源码 manifest、真实命令/时间/退出码/日志。最终提交后的 verify/audit、BOM 无差异、推送/CI/清理与代码 SHA 单独写仓外 `final-receipt.txt`，避免改写 BOM 来源。原失败日志保留，当前 pass 不抹去失败历史。
