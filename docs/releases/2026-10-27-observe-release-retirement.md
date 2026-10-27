# ObserveRelease 对外接口下线

2026-10-27，[ACC issue #7](https://github.com/zhangzhe-ctrl/ani-accelerator-service/issues/7) 的最新决定由本说明统一记录。当前接入以[消费方合同](../contracts/accelerator-consumer-contract.md)和活跃 Proto 为准；历史设计、计划与验收中的 `ObserveRelease` 描述仅适用于其原始快照。

ACC 移除 `AcceleratorUsageService/ObserveRelease` 的对外 RPC、服务入口和 owner 专用授权路径，不提供兼容处理器。旧 Inference、Resource 或其他客户端继续调用该路径时收到 gRPC `Unimplemented`，需要由各 owner 在自己的仓库迁移。ModelDev 当前流程已经移除这项依赖；部署中是否仍有旧客户端须由其 owner 核实。本说明不代表这些消费方已发布或验收。

`ResolveGpuRequest` 保留，且仍仅允许 Governance 调用。目录、规格、供给、绑定及容量查询、Gov `SyncGpuUsage` 和独立节点认证的 `NodeTelemetryService/Connect` 保留各自原有合同。ACC 内部设备退出、正向解除事实、绑定和容量归并继续运行；Usage ENDED 不清除 live Binding，普通退出和重启不等同于业务实例删除。

ACC 只完成自身接口与授权整改。Inference、Resource 各自负责工作负载清理和与 Governance 的后续通知；Governance 负责配额及账本。ACC 不实现其他 owner 的删除流程，不执行退款，也不增加替代退款闸门。历史遗留配额等整改完成后到环境中手动释放；本次不写历史数据、不操作环境、不宣称退款完成。

本次接口下线不需要数据库迁移。既有观测、绑定、解除事实和 owner 签名公钥保留。软件验证应分别记录移除方法返回 `Unimplemented`、Resolve 的 Gov 身份限制，以及未改路径的相关回归结果；历史 PASS、源码检查和软件替身验证均不能替代本次消费方或真实环境验收。
