# Runner

独立 Go 工程，与 backend-api、frontend-web、frontend-app 共用仓库。
当前仅初始化工程，不提供任务执行、模型调用、后台服务或客户端连接。

## 开发

需要 Go 1.26.2 或更新版本。

```sh
go run ./cmd/runner
go build -o bin/runner ./cmd/runner
go test ./...
```

Windows 构建可指定 `-o bin/runner.exe`。

## 后续职责边界

- CLI：命令行入口与节点管理。
- Daemon：常驻进程生命周期、接收任务与报告状态。
- Agent：执行上下文与任务编排。
- Model：模型提供方适配。
- Executor：实际工具与进程执行。

这些职责尚未实现；按后续需求引入对应包，不预设接口。
Web / Tauri 是客户端；任务执行生命周期由 runner 独立管理。

## 技术方向

Go 承载 CLI、daemon 与任务生命周期。需要 JS/TS Agent SDK 时再引入独立 Node.js 适配进程。MCP 与 Skills 不绑定 Node.js，工具的运行时依赖按适配器声明。

保活、任务状态恢复、排空升级、版本回滚与 worker 接管属于后续设计与实现，不是当前工程已有能力。长任务不中断升级需要独立 worker 生命周期、持久化状态及兼容协议，不能仅靠替换 daemon 二进制实现。
