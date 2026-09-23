# 示例边界与单位

所有身份、型号类别、节点名、额度均为说明数据，不是用户实际环境。JSON中的摘要仅对应本例规范化算法，不能替代正式多语言canonical序列化合同。

- `gpu-request.json`：用户只选规格和张数，没有F、原始vgpu字段或应扣额度。
- `resolved-plan-f1024.json`：示例F=1024，6GiB转成6块；两副本计额12288MiB。
- `owner-*-attachment.json`：只是嵌入真实Inference接受接口的GPU附件，不伪造模型/镜像接口。
- `usage-declared.json` / `usage-ended.json`：Gov旁路同步。结束先到合法，旧登记不能复活。
- `quota-release-existing-contract.json`：使用上传Gov原有字段；不是新建退额服务。回调原创建operation，而非delete操作。
- `plan-quota-bff.json`：使用已有套餐JSON风格；与QuotaRelease的裸protobuf JSON不同，不统一改写。
- 两份`*.fragment.yaml`：只有GPU受管字段，不是完整Kubernetes对象，没有假镜像，没有强制restartPolicy。
- `node-config.example.json`：对照当前插件节点配置形状的例子，必须检查实际版本；不要把它误当成可直接套用的Helm values。

`gpu-memory-factor`是插件进程的有效配置。示例可以在其已验证的启动参数中显式设置`--gpu-memory-factor=1024`，但本包不会改安装清单。不得只向用户Pod添加该参数，也不假定设置任意名为GPU_MEMORY_FACTOR的环境变量就一定生效。

整卡fragment不同时写显存绝对值和百分比；设备独占由整卡组、每卡splitcount=1、cores=100、请求边界共同验证。整卡可用显存可能存在小于F的量化尾差，不能宣传为每一物理byte都可用。

## 真实初装检查顺序

1. 记录插件和调度器镜像digest、HAMi构建标识与节点型号；获取已加载配置和日志中有效F。不要将包含凭据的完整环境输出纳入证据包。
2. 使用授权只读Kubernetes客户端读取Node的`volcano.sh/vgpu-memory`、节点注册设备注解和Pod实际分配注解；保存原始单位及来源。注解键以实际tag源码登记，不猜别名。
3. 读取单卡真实bytes，比较逐卡floor后的块数和Node合计。出现0或不一致先定位ListAndWatch/配置，不将它标成“无卡可用”。
4. 运行6GiB示例，核对Pod块数、分配Usedmem、运行时CUDA限制、业务请求和Gov charge的12288MiB。至少两个共享实例同卡完成真实模型推理。
5. 在全部可选设备名额满时额外请求应等待；整卡并发请求必须排他。不要将另一张GPU正常承载新增实例判为超额。
6. 完整删除后核对owner关闭、设备解除、原charge退额和关联同步。未知不退额，投影延迟不伪造设备空闲。

纯计算脚本和测试仅检查设计模型。没有执行上述真实环境步骤。
