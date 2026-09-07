# Resume Agent Kernel

- 独立 Go 仓库；构建、测试不能依赖 Django、业务数据库或兄弟仓库。
- 公开接口为 `POST /v2/tasks/execute`，协议 `resume-analysis/v1`。
- 平台负责志愿、学校/学历准入和岗位池；内核只分析输入的单候选人及合规岗位。
- 不接入数据库、业务写入、消息发送、Shell 或任意 HTTP 工具。
- `internal/contract/bundle` 是 resume-contracts 1.1.0 的版本化副本；通过协议仓发布工具同步，禁止手工修改生成物。
- 使用 `make check build` 验证；Docker 镜像由本仓独立构建，`KERNEL_VERSION` 与平台版本独立。
- 模型、文档和 MCP 密钥不得进入代码、日志、样例或测试输出。
- 保留未提交修改；不自动 stage、commit、push 或创建远端仓库。
