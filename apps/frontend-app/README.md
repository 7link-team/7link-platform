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

## 初始化范围

当前仅接通开发与打包链路。没有实现 runner 集成或原生功能。
打包环境没有 Vite API 代理；正式接入后端时须通过 Web 的 `VITE_API_URL` 设置绝对 API 地址，并配置后端允许的来源、Cookie 与登录回跳策略。当前不宣称原生登录流程已验收。
响应式和原生 WebView 兼容性在共享 Web 中统一维护，本次未进行逐页适配。
iOS / Android 平台工程尚未初始化。
应用标识 `com.example.frontendapp` 为开发配置，发行前在 `src-tauri/tauri.conf.json` 配置正式标识与显示名称。
