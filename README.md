# Resume Agent Kernel

独立 Go 简历分析引擎，负责完整文本阅读与搜索、证据化画像、合规岗位池内的匹配、工具/MCP 与预算控制。志愿排序、学校/学历准入、HC 和业务动作属于业务平台。

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

健康检查 `GET /healthz` 不承担版本校验；认证的 `GET /v2/capabilities` 返回实际 build、工具注册表指纹及嵌入指令指纹。公开候选人分析入口 `POST /v2/tasks/execute`，协议为 `resume-analysis/v3`，携带 `X-Agent-Kernel-Token`。模型密钥只通过请求头 `X-Model-API-Key` 传入，不进入协议正文或日志。

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
make package KERNEL_VERSION=v2.0.0
make image KERNEL_VERSION=v2.0.0 IMAGE=gitlab.internal:5000/resume/kernel:v2.0.0
# 显式发布时才添加 PUSH=--push，认证由 CI/部署环境提供
```

`dist/<version>/` 是 linux/amd64 二进制、构建记录、协议 manifest 和 SHA256SUMS；
`release/<version>/` 记录镜像 digest。两者分别拒绝覆盖已有产物。
GitLab 检查与 GitHub 工作流调用同一 Makefile，不依赖外部 CI 的实现代码。

真实模型与全文/证据引用验收与黄金样本是上线条件；通过编译、协议和单元测试并不等于业务质量验收。

输入携带原 PDF SHA256、文本 SHA256、提取器版本和按页完整文本；空白页与页内换行保留，全局行号由协议统一定义。平台提取并校验文本，Kernel 校验全文校验值与证据引用；无有效文本或材料待处理状态不能作为完整分析输入。

代码回退依赖 Git 提交及版本标签，不创建源码副本；GitHub Release 上传并回下载校验后清理本地临时分发物。

公开 v3 任务为 `candidate.application_assessment`，结果结构为 `resume-application-assessment/v1`。`scope.jobs` 固定为一个当前投递标准；`scope.tag_catalog` 定义可提取的能力标签，`profile.tags` 返回标签编码、原文证据、置信度及已支持/待核实状态。未知标签和无效证据被拒绝；置信度低于 0.8 降为待核实。部门岗位池、入池资格、人工复核、HC 和分配均由平台负责。部署时需升级配套的 v3 平台，保留既有 HTTP 路径和完整文本输入。
