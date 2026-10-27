# Frontend App

Tauri 2 桌面应用，启动时选择「本地运行」或「连接服务器」。显示名称与安装目录由 `src-tauri/tauri.conf.json` 的 `productName` 配置，当前按分支名使用 `chaosplus`。

## 开发

安装 [Tauri 平台依赖](https://v2.tauri.app/start/prerequisites/)，确保 Cargo、Go（含本机 CGO 编译器）和 Bun 在 PATH 中。
在本目录执行：

```sh
bun install --frozen-lockfile
bun run dev
```

启动页是本工程 `launcher/` 中的静态页面，无需另起 Vite 服务。

## 本地运行

点击「启动本地服务」使用安装包内的生产 Web、原生 Go 服务和 SQLite，无需服务器地址或另装开发工具、数据库。首次运行由用户创建管理员账号与密码，不预置默认账号；创建后进入共享 Web 登录流程。

数据、密钥和 `local/service.log` 保存在当前用户系统应用数据目录的 `local/` 下，不写入安装目录。绿色版也使用此目录；解压时必须保留 EXE 旁整个 `local/` 目录。卸载程序不删除用户数据。

Web 与 `/api` 共用仅监听 `127.0.0.1` 的端口，首次分配后持久化，端口占用明确报错。退出桌面应用会停止其内置服务。首次设置通过原生进程管道完成，不提供 HTTP 初始化接口。外部身份提供方等功能仍需配置。

## 服务器选择

安装包不包含固定服务器或 API 地址。打开后输入部署的 HTTP(S) 网页入口
（域名、可选端口，不含 `/api`、账号密码或查询参数），保存后连接。
可保存多个不同域名并选择连接；启动时总是显示列表，不自动连接或故障转移。
地址保存在本机 WebView 存储中，移除地址不会退出该服务器的登录。

WebView 直接加载所选入口的网页，页面和 `/api` 请求共用服务器 Nginx。
页面、路由、主题、国际化及响应式布局继续由共享 `frontend-web` 维护；
远程页面随服务器部署更新；本地模式使用安装包内的生产 Web。服务器应使用现有 nginx 同源部署，
不设置固定的前端 `VITE_API_URL`。

无论远程页面是否可用，使用原生菜单「连接 → 服务器」或 `Ctrl+Shift+S`
返回本地服务器列表，即可切换地址。网页存储按来源隔离；Cookie 遵循浏览器的
域名、路径及 Secure 等规则，同一主机的不同端口不隔离 Cookie。
切换地址不等于退出登录。同一服务的不同域名仍需服务端配置各自的 Cookie、
登录回跳与允许的域名。

## 构建

```sh
bun run build --debug --no-bundle
```

构建前自动生成生产 Web 并编译当前平台 Go 服务，再由 Tauri 打包。CI 通过 `DESKTOP_WEB_DIST` 复用生产 Web。选择页嵌入原生程序，正式打包运行 `bun run build`；运行 `bun run test` 检查地址规则和本地设置交互。Web 的构建仍仅构建 Web。

## 桌面发行包

标签发布工作流在 Windows x64 runner 上冻结安装本工程的 Bun 依赖，
使用 Cargo 锁文件构建，并通过临时 Tauri 配置注入标签版本（例如 `v0.1.0-rc.1`
对应应用版本 `0.1.0-rc.1`），不修改源码中的开发版本。
不需要配置构建时服务器或 API 地址。

GitHub Release 提供两种文件，名称中的仓库名和标签来自发布元数据：

- `<repository>-desktop_<tag>_windows_amd64-setup.exe`：NSIS 安装器提供
  “仅为我安装 / 为所有用户安装”选择。Tauri 的 `installMode=both` 模式在两种选择下
  都会要求管理员权限；选择控制安装范围，不表示当前用户模式无需提权。
- `<repository>-desktop_<tag>_windows_amd64_portable.zip`：免安装包，解压后直接运行
  `frontend-app.exe`。不注册安装项，也不通过安装器部署 WebView2；运行机器必须已有
  Microsoft Edge WebView2 Runtime。ZIP 由同一次构建的实际 Cargo 二进制生成。

ZIP 同时包含 `local/local-service.exe` 和 `local/web/`，不能只复制桌面 EXE。

两种文件都列入 `<repository>-desktop_<tag>_checksums.txt`。便携版只表示免安装，
不承诺配置或系统 WebView 数据全部保存在 EXE 旁。Windows 安装器和 EXE 当前没有
配置代码签名，不能将下载校验和视为发行者签名。

macOS 和 Linux 使用原生 Runner 独立构建两种架构：

| 平台 | 文件 |
| --- | --- |
| macOS Intel | `<repository>-desktop_<tag>_macos_amd64.dmg` |
| macOS Apple Silicon | `<repository>-desktop_<tag>_macos_arm64.dmg` |
| Linux amd64 | `<repository>-desktop_<tag>_linux_amd64.AppImage` |
| Linux arm64 | `<repository>-desktop_<tag>_linux_arm64.AppImage` |

macOS 会验证挂载后的应用版本、架构、ad-hoc 签名和启动。当前没有 Apple
Developer ID 签名或公证，Gatekeeper 可能要求用户明确允许打开。
Linux 两种架构均使用 Ubuntu 22.04 构建基线，需要图形会话；下载后先给
AppImage 添加执行权限（`chmod +x`）。工作流验证解包和原生启动，不据此
宣称各平台的完整 UI、登录及远程 Web 流程已验收。

完整新版本的桌面校验清单覆盖六个桌面文件。为已有 Windows 版本追加 Unix
包时，原有二进制及校验清单保持不变，新增四个包使用
`<repository>-desktop_<tag>_unix_checksums.txt`。

## 初始化范围

本工程提供本地服务、服务器选择与原生返回菜单，未实现 runner 原生集成或自动检查更新/自动升级。
登录沿用共享 Web 的现有流程；不宣称所有外部身份提供方与系统浏览器回跳已验收。
响应式和原生 WebView 兼容性在共享 Web 中统一维护，本次未进行逐页适配。
iOS / Android 平台工程尚未初始化。
应用标识 `com.example.frontendapp` 暂时保留，以兼容已有服务器列表与用户数据；显示名称由 `productName` 配置。
