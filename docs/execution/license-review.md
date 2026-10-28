# ACC-V12-01 依赖许可证据

2026-10-28，Fedora，Go 1.26.10，cyclonedx-gomod v1.12.0。We examined the 45 non-main runtime components in the Linux/amd64 module graph. 以下为工具检测到的 SPDX license evidence 分类，非本项目许可授权。

| Detected SPDX license | Components |
| --- | --- |
| Apache-2.0 | 22 |
| BSD-2-Clause | 1 |
| BSD-3-Clause | 10 |
| MIT | 12 |
| no detected evidence | 0 |

本次使用官方 `scripts/generate-sbom` 重新生成并逐项核对。旧 BOM 中的 `github.com/NVIDIA/go-nvml v0.13.0-1`（Apache-2.0）已不在当前模块图中，是 component 总数 46→45、Apache-2.0 分类 23→22 的唯一差异。没有新增组件，其余 45 个组件的版本、purl 与许可分类均保持不变；没有新增未知许可或模块归属。

`make audit` 会将分类数、component 总数与实际重新生成 BOM 逐项核对，不能沿用旧 layout 数量。BOM 为 `docs/scaffold/bom.cdx.json`；主模块尚无项目级 LICENSE，未把上游模板 MIT notice 当成本服务的项目许可。工具没有发现 runtime component 缺少许可证据。

原 `THIRD_PARTY_NOTICES.go-kratos-layout.txt` 保持完整，SHA256 `3b44929bdd575a570c42165d4fd4eac57a5055745d2ecf3f121aa6a084cf9230`。原始 detector 输出和最终工具二进制摘要保存于 Fedora 仓外 evidence。首次漏洞审计失败及修复记录见验收记录，不将许可清单等同于安全扫描结果。
