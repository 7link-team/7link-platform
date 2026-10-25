# Frontend App

Tauri 2 原生打包壳。页面、路由、主题、国际化与响应式布局统一维护在 `../frontend-web`，本工程不包含独立 React 页面。

## 开发

安装 [Tauri 平台依赖](https://v2.tauri.app/start/prerequisites/)，确保 Cargo 在 PATH 中。
先在 `../frontend-web` 执行 `bun install --frozen-lockfile`，再在本目录执行：

```sh
bun install --frozen-lockfile
bun run dev
```

Tauri 自动启动共享 Web 的 Vite 服务（127.0.0.1:1420）。开发 API 代理沿用 Web 的 `VITE_API_PROXY_TARGET`。

## 构建

```sh
bun run build --debug --no-bundle
```

构建钩子先构建 `frontend-web`，再将其应用的 `dist` 嵌入原生程序。
正式打包运行 `bun run build`；Web 的 `bun run build` 仍仅构建 Web。

## Windows 发行包

标签发布工作流在 Windows x64 runner 上冻结安装本工程和共享 Web 的 Bun 依赖，
使用 Cargo 锁文件构建，并通过临时 Tauri 配置注入标签版本（例如 `v0.1.0-rc.1`
对应应用版本 `0.1.0-rc.1`），不修改源码中的开发版本。
发布前设置仓库 Actions 变量 `VITE_API_URL` 为绝对 HTTP(S) API 地址；
`VITE_DEFAULT_TENANT` 可选。这些变量在构建时嵌入共享 Web，不能在解压后修改，
也不能用于存放凭据。打包没有 Vite 开发代理。

GitHub Release 提供两种文件，名称中的仓库名和标签来自发布元数据：

- `<repository>-desktop_<tag>_windows_amd64-setup.exe`：NSIS 安装器提供
  “仅为我安装 / 为所有用户安装”选择。Tauri 的 `installMode=both` 模式在两种选择下
  都会要求管理员权限；选择控制安装范围，不表示当前用户模式无需提权。
- `<repository>-desktop_<tag>_windows_amd64_portable.zip`：免安装包，解压后直接运行
  `frontend-app.exe`。不注册安装项，也不通过安装器部署 WebView2；运行机器必须已有
  Microsoft Edge WebView2 Runtime。ZIP 由同一次构建的实际 Cargo 二进制生成。

两种文件都列入 `<repository>-desktop_<tag>_checksums.txt`。便携版只表示免安装，
不承诺配置或系统 WebView 数据全部保存在 EXE 旁。Windows 安装器和 EXE 当前没有
配置代码签名，不能将下载校验和视为发行者签名。

## 初始化范围

当前仅接通开发与打包链路。没有实现 runner 集成或原生功能。
打包环境没有 Vite API 代理；正式接入后端时须通过 Web 的 `VITE_API_URL` 设置绝对 API 地址，并配置后端允许的来源、Cookie 与登录回跳策略。当前不宣称原生登录流程已验收。
响应式和原生 WebView 兼容性在共享 Web 中统一维护，本次未进行逐页适配。
iOS / Android 平台工程尚未初始化。
应用标识 `com.example.frontendapp` 为开发配置，发行前在 `src-tauri/tauri.conf.json` 配置正式标识与显示名称。
