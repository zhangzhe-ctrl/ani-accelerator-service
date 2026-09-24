# replicas 注释纠正

基于 `c2fb7410cc2a6f583d8d253224b65861416b5ecc`，将 Proto 原来的
`1..256` 注释改为首期实际 `1..16` 上限，并通过 Fedora 固定的 `make api`
生成对应 Go 注释。没有手改生成代码。

- `generation-result.json`：只变动两个文件的一行注释；排除 source info 的
  Buf descriptor 前后字节一致，SHA-256 为
  `ae84be65db483ba4010180a4dda5b1754060f4e80078ba2f9bd03f8dc0a53ce0`。
- `api-first.log`、`api-second.log`：Buf v1.60.0 和仓库固定插件实际生成；
  两次生成文件摘要完全一致，见 `generated-*.sha256`。
- `comment-only.diff`、`changed-source.sha256`：字段、tag、运行时源码未变。
- `verify.log`、`verify.exit`、`execution-result.json`：完整 `make verify`
  执行通过，SSH 退出 0，覆盖 sqlc、生成无漂移、测试/race、租户 mutation、
  populated PG 恢复、漏洞与敏感信息扫描。退出回执的记录方式详见 JSON。

执行仅在 Fedora 任务 `acc/` 副本、`acc.lock` 和 `cache/acc-*` 下进行，
`GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2`。受限测试 DSN 使用任务 `acc-dsn`，
正式边界 DSN 使用 `joint-a/acc.dsn`，任务 PG 容器为
`gov-acc-v12-01-acc`。没有修改 acc-publish，也没有停止或重建 A/B 长驻进程。
本次生成前后的 API 描述符一致只证明接口/行为没有因注释改变，不增加真实 owner
或硬件结论。populated dump 保留在 Fedora 同名证据目录，未复制到仓库。
