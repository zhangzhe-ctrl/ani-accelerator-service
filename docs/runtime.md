# Runtime

当前生产装配已升级为 Accelerator v1.2：真实 PostgreSQL、22 个 mTLS gRPC 业务方法、只读 provider 采集及 PG readiness。详细操作见 [运行与恢复](runbooks/accelerator-runtime.md)、[provider 来源](runbooks/provider-sources.md)、[接入合同](contracts/accelerator-consumer-contract.md)。

`cmd/ani-accelerator-service/app.go` 是唯一生产组合根；类型配置缺失即失败，不生成密钥或启动迁移。原 transport-only fixture 仅用于中间件和 HTTP/gRPC 生命周期回归，不是生产业务模式。历史 runtime 验证说明不能替代本次 PG、mTLS、provider、恢复或硬件证据；当前结果见 [执行状态](execution/status.md)。
