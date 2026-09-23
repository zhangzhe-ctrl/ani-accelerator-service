# GPU 资源管理服务详细计划 v1.2

日期：2026-09-23。性质：设计与实施计划，不是已修改的业务源码或部署结果。

先阅读 `设计方案.md`；浏览器打开 `完整计划.html`，或阅读合并后的 `完整计划.md`。字段、tag 和消息约束见 `api/` 与 `接口字段字典.md`。`references/Governance源码核对.md` 区分上传代码事实、尚缺的正式适配和本次建议；`references/上游核对.md` 说明 gpu-memory-factor 的源码依据及适用版本。

本版采用共享申请 MiB 配额；静态整卡/共享供给；Governance 一次占额、向 Inference 投递，并异步同步 GPU 使用关联；Inference 不执行 RegisterConsumer；实际清理完成后向 Governance 报告累计可退额度。容量读模型不是库存交易系统。

容量保障只保留扩展边界，不包含预留接口。本文统一使用“首期”，不再使用 CORE 范围标签。

## 目录

- `设计方案.md`：完整业务、接口、数据、控制和验证计划。
- `api/accelerator/v1/accelerator.proto`：新 GPU 服务字段级合同草案。
- `api/integration/v1/owner_gpu_attachment.proto`：嵌入真实 Inference 创建/删除接口的附件合同；不是通用工作负载服务。
- `接口字段字典.md`：按本版 Proto 编译描述文件生成的字段字典。
- `运行配置与产品接线.md`：新配置键、BFF字段和实际业务接线边界。
- `db/`：GPU 自有数据库与 Governance 同步进度表的 SQL 提案、目录增量；不直接执行于业务库。
- `examples/`：factor、容量、同步、退额纯计算实现及 JSON/YAML 示例。
- `tests/`：可运行的设计模型回归测试，不替代真实 GPU/PG。
- `references/`：源码核对、来源索引、输入和源码摘要。
- `validation/`：本次实际执行的检查和未验证范围。

## 本地复现

```sh
python3 -m unittest discover -s tests -v
python3 examples/design_reference.py
```

Proto 检查命令以 `validation/验证说明.md` 为准。生成与测试只修改本设计包及隔离输出目录。业务仓库中真正的接口模块路径、Inference 接受合同、模型/镜像/卷字段和现有执行控制，需要在接入阶段核对；本次仅获得 Governance 源码。
