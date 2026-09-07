# Resume Agent Kernel

独立 Go 简历分析引擎，负责文档/OCR、证据化画像、合规岗位池内的匹配、工具/MCP 与预算控制。志愿排序、学校/学历准入、HC 和业务动作属于业务平台。

## 独立构建

需要 Go 1.25；无需平台源码、Django 或业务数据库。

```sh
make check build
make image KERNEL_VERSION=dev
```

镜像包含 Poppler 和 Tesseract。原生二进制处理 PDF 时也需要这些程序在 PATH 中。

## 运行和协议

```sh
AGENT_KERNEL_TOKEN='<random-service-token>' \
AGENT_KERNEL_DOCUMENT_ROOT='/read-only/resumes' \
AGENT_KERNEL_DOCUMENT_SIGNING_KEY='<shared-document-signing-key>' \
AGENT_KERNEL_ADDRESS='127.0.0.1:8090' dist/agent-kernel
```

健康检查 `GET /healthz` 不承担版本校验；认证的 `GET /v2/capabilities` 返回实际 build、工具注册表指纹及嵌入指令指纹。公开候选人分析入口 `POST /v2/tasks/execute`，协议为 `resume-analysis/v1`，携带 `X-Agent-Kernel-Token`。模型密钥只通过请求头 `X-Model-API-Key` 传入，不进入协议正文或日志。

请求/结果 Schema 与合成样例在 `internal/contract/bundle/`，由 resume-contracts 仓发布工具生成并固定版本。服务同时校验请求与结果 Schema，测试验证 Go 序列化兼容性；不通过相对路径引用协议仓。

模型、指令和工具定义由本仓版本化；MCP 仅允许显式白名单及只读用途。内核没有业务数据库写入或消息发送权限。旧 `/v1/evaluate`、CaseEnvelope 与业务动作输出已经删除，不提供历史协议兼容。

发布镜像的 build 由 `KERNEL_VERSION` 决定，平台单独固定镜像及版本。不要覆盖已发布版本；协议变更先排空旧任务再升级。离线交付默认 `linux/amd64`，可通过 `PLATFORM` 显式指定其他目标。


## 独立版本与发布

内部工具集和指令使用内容指纹；任务冻结版本必须与运行实例完全相同，否则在文档与模型调用前返回 409。
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

真实模型/OCR 验收与黄金样本是上线条件；通过编译、协议和单元测试并不等于业务质量验收。
