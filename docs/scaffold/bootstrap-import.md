# 服务创建与 v1.2 资料迁入记录

日期：2026-09-23。交付位置：`/home/chabking/workspace/ani-accelerator-service`。
本次创建本地独立 Git 仓库，迁入可复用合同与设计；尚未实现 GPU 业务。

## 脚手架来源

使用 `ani-kratos-layout` 的真实 `scripts/new-service` 入口，内部调用固定的官方
`kratos new`。模板基线：`fd18422211c741dd5242d2992c3d307d522aa28c`。
原模板工作区有 README 和 LAYOUT-1 计划的未提交改动，因此从该已提交基线创建
临时干净副本执行生成，原工作区改动保留。完整工具身份见 [provenance.md](provenance.md)。

生成命令（在基线副本中执行，固定工具路径由环境变量传入）：

```bash
GOWORK=off scripts/new-service github.com/zhangzhe-ctrl/ani-accelerator-service \
  --parent /home/chabking/workspace
```

新模块没有回指模板的 import、replace、submodule 或运行时依赖。

## 迁入内容与最小适配

- 两份 v1.2 Proto 进入 `api/protos/accelerator/`，补齐本模块 go_package；
  字段类型、编号、枚举、服务和方法未改。集成附件放入与 package 一致的目录。
  逐文件对比见 [design-contract-import.json](design-contract-import.json)。
- 固定 Buf 1.60.0、protoc-gen-go v1.36.11、protoc-gen-go-grpc v1.6.2，
  生成 Go 消息与 gRPC 接口。保留源合同的四项命名规则例外，不改变内部配置检查。
- `make api`、`make design-test` 和生成一致性检查已接入；业务 RPC 暂不注册。
- 39 个设计原文件按原字节迁入，来源 50 项 checksum 全部通过。
  [导入清单](../design/v1.2/import-manifest.json)记录位置、SHA256与排除原因。
- SQL 按本域与 Governance 分放设计目录，不进入可执行迁移；原包生成器、截图和
  descriptor 未迁入。原历史验证单列，原下载目录未删改。

## 本轮实际验证

| 检查 | 结果 |
|---|---|
| 官方脚手架生成及自带结构检查 | pass |
| `make api`：Buf lint/build、固定 Go/gRPC 生成 | pass |
| v1.2 Proto 声明/tag 保持一致 | pass |
| `make verify`：生成不变、mod tidy -diff、Go tests/vet/build/mod verify | pass |
| 模板真实回环监听与运行中间件测试 | pass，属于 Go tests 的本地运行骨架范围 |
| 48 项 v1.2 Python 设计模型测试 | pass |
| 39 个复制文件摘要复核 | pass |

命令完整输出：[bootstrap-verify.txt](bootstrap-verify.txt)。`make verify` 本轮退出码0。
记录是在代码验证完成后新增的说明文件，不改变被验证的代码、合同或生成配置。

GPU provider、PostgreSQL、真实模型推理、Governance/Inference 接线、mTLS业务权限、
运行时GPU限制及删除退额链均为 `not_verified`。本地没有执行 SQL、应用集群资源，
没有远程发布或推送。本轮也没有执行需首次源码提交的供应链审计/生成 SBOM；
后续按根 README 的发布前流程处理，不能将这里的 pass 当作 CI 或业务验收通过。
