# Resume Agent Kernel

独立 Go 简历分析引擎，负责完整文本阅读与搜索、证据化画像、唯一当前投递标准的评估、工具/MCP 与预算控制。志愿排序、学校/学历准入、HC 和业务动作属于业务平台。

## 独立构建

需要 Go 1.25；无需平台源码、Django 或业务数据库。

```sh
make check build
make image KERNEL_VERSION=dev
```

镜像仅包含 Go 程序及系统 CA 等必要运行文件。不包含 Poppler、Tesseract、语言数据，不挂载 PDF 文件。

## 运行和协议

```sh
AGENT_KERNEL_TOKEN='<random-service-token>' \
AGENT_KERNEL_ADDRESS='127.0.0.1:8090' dist/agent-kernel
```

健康检查 `GET /healthz` 不承担版本校验；认证的 `GET /v2/capabilities` 返回实际 build、工具注册表指纹及嵌入指令指纹。公开候选人分析入口 `POST /v2/tasks/execute`，协议为 `resume-analysis/v5`，携带 `X-Agent-Kernel-Token`。模型密钥只通过请求头 `X-Model-API-Key` 传入，不进入协议正文或日志。

请求/结果 Schema 与合成样例在 `internal/contract/bundle/`，由 resume-contracts 仓发布工具生成并固定版本。服务同时校验请求与结果 Schema，测试验证 Go 序列化兼容性；不通过相对路径引用协议仓。

模型、指令和工具定义由本仓版本化；MCP 仅允许显式白名单及只读用途。内核没有业务数据库写入或消息发送权限。旧 `/v1/evaluate`、CaseEnvelope 与业务动作输出已经删除，不提供历史协议兼容。

发布镜像的 build 由 `KERNEL_VERSION` 决定，平台单独固定镜像及版本。不要覆盖已发布版本；协议变更先排空旧任务再升级。离线交付默认 `linux/amd64`，可通过 `PLATFORM` 显式指定其他目标。

## 企业模型路由器的 CA

Kernel 默认校验模型 HTTPS 连接，使用容器内的系统信任库。宿主机或调用方已安装企业 CA，不会自动使 Kernel 信任它。私有 CA 需要将现场 CA PEM 文件只读挂载到容器（例如 `/etc/agent-kernel/model-ca.pem`），并设置 `SSL_CERT_FILE=/etc/agent-kernel/model-ca.pem`。文件应包含所需根 CA 和中间 CA 证书，不含私钥，且容器内非 root 的 `agent` 用户必须可读。更新文件后重建容器，让 Go 进程重新加载信任库。

`/healthz` 和 `/v2/capabilities` 均不请求模型，不能代替 TLS 验收。调用方的模型 TEST 也可能使用不同证书配置或跳过校验；必须通过一次真实任务执行，确认模型路由器的证书链、域名和结果均正常。


## 独立版本与发布

内部工具集和指令使用内容指纹；任务冻结版本必须与运行实例完全相同，否则在文本分析与模型调用前返回 409。
平台策略版本只透传供审计；Kernel 不解释学校规则或业务阈值，也不限定平台策略版本号。
生产构建使用不可覆盖的版本标签，禁止以 `dev` 或复用旧标签表达新代码。

```sh
make check
make package KERNEL_VERSION=v5.0.0
make image KERNEL_VERSION=v5.0.0 IMAGE=gitlab.internal:5000/resume/kernel:v5.0.0
# 显式发布时才添加 PUSH=--push，认证由 CI/部署环境提供
```

`dist/<version>/` 是 linux/amd64 二进制、构建记录、协议 manifest 和 SHA256SUMS；
`release/<version>/` 记录镜像 digest。两者分别拒绝覆盖已有产物。
GitLab 检查与 GitHub 工作流调用同一 Makefile，不依赖外部 CI 的实现代码。

真实模型与全文/证据引用验收与黄金样本是上线条件；通过编译、协议和单元测试并不等于业务质量验收。

输入携带原 PDF SHA256、文本 SHA256、提取器版本和按页完整文本；空白页与页内换行保留，全局行号由协议统一定义。平台提取并校验文本，Kernel 校验全文校验值与证据引用；无有效文本或材料待处理状态不能作为完整分析输入。

代码回退依赖 Git 提交及版本标签，不创建源码副本；GitHub Release 上传并回下载校验后清理本地临时分发物。

公开 v5 任务为 `candidate.application_assessment`，结果结构为 `resume-application-assessment/v1`。`scope.jobs` 固定为一个当前投递标准；`scope.tag_catalog` 定义可提取的能力标签，`profile.tags` 返回标签编码、原文证据、置信度及已支持/待核实状态。未知标签和无效证据被拒绝；置信度低于 0.8 降为待核实。专业大类词表和 `taxonomy.lookup_major` 已移除。部门岗位池、入池资格、HC 计划数和业务归属均由平台负责。部署时需升级配套的 v5 平台，保留既有 HTTP 路径和完整文本输入。

分配 Agent 与筛选共用部署，提供 `GET /v2/allocation/capabilities` 和 `POST /v2/allocation/tasks/execute`（同样使用 `X-Agent-Kernel-Token`）。协议包 5.0.0 使用筛选 v5，并保留 `resume-allocation/v1` / `resume-allocation-plan/v1`。

## 分析预算与收敛

v5 新增独立的 `budget.max_context_tokens`（默认 32768）和结构化预算、逐轮诊断；累计预算仍为每任务输入加输出之和。Platform、Kernel 和 Contracts 5.0.0 必须配套升级，旧 v4 请求会被明确拒绝；评估结果及独立分配协议未改变。

Kernel 内置离线 tokenizer，模型返回 usage 时按实际值记账并校准后续估算；缺失 usage 或运输重试的未知消耗标为估算。每轮记录输入、输出、模型耗时、重试数、预留额度和进展，不记录提示词、简历正文或密钥。

运行时直接准备当前岗位、标签字典和页行目录；以 Collector 中已验证的画像和岗位结果重建进度。历史过长或剩余额度不足时，只移除完整旧轮次，保留近期交互和已验证状态；全文及全局行号始终保留，允许重新读取。

收尾阶段占用原有轮数、工具、时间和 token 预算，限制新的探索，仅允许必要证据修正、提交和结束。连续三个无进展轮次终止；输出格式修正最多两次，与工具校验失败、运输重试分开统计。`safe_trace.budget.stop_reason` 区分累计 token、下一轮额度、上下文、轮次、工具数和无进展，失败结果不能作为达标结论。

`internal/allocation` 的确定性执行器仅处理白名单资格、标签、允许需求、7 天供给和版本引用，按“优先标签命中数降序、priority 升序、供给升序、最后序号升序、需求 ID 升序”输出方案。`counted_demand_ids` 标识当前候选人在窗口内已计入供给的需求，防止批内重复累计。每成员恰好一个 assign/wait；平台需独立复算、校验版本并提交。

该模块有独立 4 个并发槽、256 项/5 分钟幂等缓存、最多 100 人/200 条需求/2 MiB、30 秒/16 次工具调用预算、60 秒快照期限。六个固定步骤有独立工具名及安全轨迹，不调用 LLM、简历工具、数据库或全局外部 MCP。没有新增容器，也不具备业务写入权限。
