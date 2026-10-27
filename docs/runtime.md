# Runtime

对外 `ObserveRelease` 已下线，`ResolveGpuRequest` 继续仅供 Gov 调用；目录/容量、`SyncGpuUsage` 和节点 `Connect` 保留，详见[接口下线说明](releases/2026-10-27-observe-release-retirement.md)。

生产组合根使用真实 PostgreSQL、mTLS 业务 RPC、独立节点 telemetry 双向流、初始化 worker 和 PG readiness。中心 Inspect/Collect 使用认证持久节点事实，本机采集由独立 ani-node-agent 承担。详细操作见 [运行与恢复](runbooks/accelerator-runtime.md)、[provider 来源](runbooks/provider-sources.md)、[接入合同](contracts/accelerator-consumer-contract.md)。源码接线与实际部署验收分别记录。

`cmd/ani-accelerator-service/app.go` 是唯一生产组合根；类型配置缺失即失败，不生成密钥或启动迁移。原 transport-only fixture 仅用于中间件和 HTTP/gRPC 生命周期回归，不是生产业务模式。历史 runtime 验证说明不能替代本次 PG、mTLS、provider、恢复或硬件证据；当前结果见 [执行状态](execution/status.md)。
