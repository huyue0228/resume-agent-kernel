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

健康检查 `GET /healthz`；公开候选人分析入口 `POST /v2/tasks/execute`，协议为 `resume-analysis/v1`，携带 `X-Agent-Kernel-Token`。模型密钥只通过请求头 `X-Model-API-Key` 传入，不进入协议正文或日志。

请求/结果 Schema 与合成样例在 `internal/contract/bundle/`，由 resume-contracts 仓发布工具生成并固定版本。服务同时校验请求与结果 Schema，测试验证 Go 序列化兼容性；不通过相对路径引用协议仓。

模型、指令和工具定义由本仓版本化；MCP 仅允许显式白名单及只读用途。内核没有业务数据库写入或消息发送权限。旧 `/v1/evaluate` 仅留作兼容基线，新平台不使用；不得据此设计新接入。

发布镜像的 build 由 `KERNEL_VERSION` 决定，平台单独固定镜像及版本。不要覆盖已发布版本；协议变更先排空旧任务再升级。离线交付默认 `linux/amd64`，可通过 `PLATFORM` 显式指定其他目标。

## GitHub 发布

私有仓：`https://github.com/huyue0228/resume-agent-kernel`，负责人 `@huyue0228`。普通 PR/push 只验证；手动执行 Release 工作流只演练构建。
维护者从 main 已合并提交推送 `vX.Y.Z` 或预发布标签后，自动执行 race/vet/构建，发布 linux/amd64 二进制、SHA256SUMS、镜像 digest 和 `ghcr.io/huyue0228/resume-agent-kernel:<tag>` 镜像。二进制 build 与完整标签一致，平台需设置相同的 `AGENT_KERNEL_VERSION`。不自动部署、不覆盖已发布版本。
通过 CI 不代表真实模型/OCR 验收完成；enforced 上线仍需平台黄金样本与人工放行。
