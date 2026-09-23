# GPU v1.2 设计来源与导入说明

本目录保存 `/home/chabking/下载/ani-gpu-design-v1.2/` 的相关设计资料与可运行的纯计算模型。导入日期：2026-09-23。源目录保留，未删除或修改；迁入原文件保持字节一致。原包 50 条 `SHA256SUMS` 校验全部通过，39 个文件复制至本目录，12 个源文件未复制至设计目录，具体映射、摘要和原因见 [import-manifest.json](import-manifest.json)。

这里是可追溯的来源快照。活跃 Proto、生成代码、业务实现与本轮验证以仓库对应目录和报告为准；不通过修改来源记录来表示业务已经实现。

## 阅读入口与实际代码位置

| 内容 | 位置 |
|---|---|
| 原设计正文 | [设计方案.md](设计方案.md) |
| 原配置与 BFF 接线计划 | [运行配置与产品接线.md](运行配置与产品接线.md) |
| 原字段字典 | [接口字段字典.md](接口字段字典.md) |
| 原合并文档 | [完整计划.md](完整计划.md)、[完整计划.html](完整计划.html) |
| 原包说明 | [来源README.md](来源README.md) |
| 活跃 Accelerator Proto | [api/protos/accelerator/v1/](../../../api/protos/accelerator/v1/) |
| 活跃 Inference GPU 消息附件 | [api/protos/accelerator/integration/v1/](../../../api/protos/accelerator/integration/v1/) |
| 生成 Go API | [api/gen/go/](../../../api/gen/go/) |
| Accelerator SQL 提案 | [schema/](schema/README.md) |
| Governance 增量提案 | [integration/governance/](integration/governance/README.md) |
| 纯计算模型及示例 | [examples/](examples/README.md)、`tests/test_design.py` |
| 原来源核对 | `references/` |
| 原包历史验证记录 | [source-validation/](source-validation/README.md) |

原文中的 `api/`、`db/`、`validation/`、`tools/` 路径保留原包语境，不能直接当成本仓命令或自动迁移路径。上表给出本仓位置。活跃合同的 Go 包路径及生成方式由本仓维护，原字段字典仍是 v1.2 来源版本。

## 本版边界

- 首期交付 Inference owner；支持静态整卡与固定共享规格，同节点同型号，供给组匹配已核验规格。
- Governance 一次占额后直接投递 Inference；使用关联通过 `SyncGpuUsage` 异步同步，不是创建前置。没有 `RegisterConsumer`、`MarkClosing`、`Finalize` 或预留服务。
- 使用关联 `ENDED` 不清除真实 Binding，也不触发退额；`ObserveRelease` 不等待使用关联先落库。
- Inference 保留自身控制器和正常重启。明确 DELETE 且整实例清理完成后，才回调 Governance 全量累计释放原 GPU charge；退出、失败、重启和部分清理均不退额。
- Governance 精确服务身份写入使用投影；Inference 以直接 owner 身份查询对应释放范围；租户范围、身份及动作校验仍须在实现时落地。

## 可复现的设计模型

仓库根目录使用：

```bash
make design-test
```

也可以在本目录执行：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -v
PYTHONDONTWRITEBYTECODE=1 python3 examples/design_reference.py
```

`examples/` 与 `tests/` 保持相邻，原测试无需改 import。48 个用例为 Python 纯计算模型，不使用数据库或集群，不能替代 PostgreSQL 事务、设备限制、mTLS 或真实推理验收。`calculation-output.json` 是来源输出；复跑结果以实际命令输出为准。

## 未迁入的生成工具与二进制证据

原 `tools/` 未迁入服务工具链。`build_contracts.py` 会按列表顺序重写 Proto 字段 tag，并覆盖新补的 `go_package`；其他生成器还依赖原 `/mnt/data` 输入或 `/opt/pyvenv` 工具路径，会重写文档、示例和验证记录。活跃合同应使用本仓 `make api`，不要从来源包运行设计生成器覆盖它。

原 PNG 截图与 protobuf descriptor 未迁入。本目录 `source-validation/` 的通过记录均是原包历史结果，不是本轮运行证据。`source-SHA256SUMS` 仅用于核对原包布局，迁移路径已变化，不能在本目录直接把它当成本仓校验清单；使用 `import-manifest.json` 中的目标路径与 SHA256 核对复制文件。
