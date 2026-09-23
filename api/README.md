# GPU 合同与生成代码

本目录从 GPU 设计 v1.2 引入字段级合同。模块为
`github.com/zhangzhe-ctrl/ani-accelerator-service`。

| 路径 | 用途 |
|---|---|
| `protos/accelerator/v1/accelerator.proto` | Accelerator 自有 3 个服务、22 个 RPC 的源合同 |
| `protos/accelerator/integration/v1/owner_gpu_attachment.proto` | Governance → Inference 的 GPU 消息附件；不声明 Inference 业务服务 |
| `gen/go/accelerator/` | 由固定版本生成的 Go 消息与 gRPC 客户端/服务端接口 |

运行 `make tools && make api`。生成器为 `protoc-gen-go v1.36.11`、
`protoc-gen-go-grpc v1.6.2`，由 `buf.gen.api.yaml` 固定。
`make verify` 会重新生成并检查所有已生成文件是否有变化。

导入时只增加本仓 `go_package`、调整对应说明，并将 integration 文件放入与
`accelerator.integration.v1` 包名一致的目录；字段编号、类型、枚举和 RPC 定义保持 v1.2。
Buf 对 API 模块保留四项命名规则例外，以保留已有枚举名及共享响应消息；其他标准检查仍启用，内部配置模块不受这些例外影响。

当前只有合同与 SDK，业务 RPC 尚未在运行时注册实现。没有 HTTP 注解、公开路由、
数据库迁移或供应商适配。实现时读取设计中的权限、跨字段及幂等约束，不能用 proto3
零值或成功生成代码替代业务验证。

以本版 `SyncGpuUsage` 异步关联合同为准。Inference 不通过 RegisterConsumer
阻塞创建；Usage ENDED 不解除实际 GPU 绑定，也不决定 Governance 退额。
