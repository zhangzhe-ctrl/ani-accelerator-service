# ACC-V12-01 依赖许可证据

2026-09-23，Fedora，cyclonedx-gomod v1.12.0。We examined the 41 non-main runtime components in the Linux/amd64 module graph. 以下为工具检测到的 SPDX license evidence 分类，非本项目许可授权。

| Detected SPDX license | Components |
| --- | --- |
| Apache-2.0 | 21 |
| BSD-2-Clause | 1 |
| BSD-3-Clause | 9 |
| MIT | 10 |
| no detected evidence | 0 |

`make audit` 会将分类数、component 总数与实际重新生成 BOM 逐项核对，不能沿用旧 layout 数量。BOM 为 `docs/scaffold/bom.cdx.json`；主模块尚无项目级 LICENSE，未把上游模板 MIT notice 当成本服务的项目许可。工具没有发现 runtime component 缺少许可证据。

原 `THIRD_PARTY_NOTICES.go-kratos-layout.txt` 保持完整，SHA256 `3b44929bdd575a570c42165d4fd4eac57a5055745d2ecf3f121aa6a084cf9230`。原始 detector 输出和最终工具二进制摘要保存于 Fedora 仓外 evidence。首次漏洞审计失败及修复记录见验收记录，不将许可清单等同于安全扫描结果。
