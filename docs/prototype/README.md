# Web / App 交互原型

以 [PRD_FINAL](../PRD_FINAL.md) 为产品基线，使用独立 React / Vite 原型审阅交互。复用原 Aurora 主题与顶部五区布局；正文、路由和业务视图重新设计。生产 Web 与 Tauri 工程未因原型而改写。

## 启动

```sh
cd docs/prototype
bun install --frozen-lockfile
bun run dev --port 4173
```

- 原型：http://127.0.0.1:4173/
- 评审台：http://127.0.0.1:4173/review.html （同一页面切换 Web、平板、手机宽度）
- 构建：`bun run build`，生成 `dist/`；可用 `bun run preview --port 4174` 验证构建产物。
- 状态测试：`bun test`。
- 数据保存在当前浏览器 localStorage；设置页可重置演示。不会发出邮件、调用模型或执行 Shell。

## 推荐验收顺序

1. `/overview` 查看多项目汇总，进入 `/p/launch` 和 `/work?view=board`。
2. `/chat?channel_id=launch` 发消息、回复线程、转为工作。工作草稿补充责任与验收要求，确认计划和 SOP 后启动。
3. `/p/launch/work-items/W-104?tab=review`：直接接受会被质量门阻止；模拟独立复核后由 Human 接受，再确认正式交付。
4. `/deliveries` 查看同步出现的固定版本包。设置切换 external 后查看共享工作台，提交回执或有理由退回；切回 owner 查活动与返工。
5. `/p/launch/flow` 编辑新草案、拖动并连接节点、修改契约、发布版本。添加未连接节点会被拒绝，既有发布版本保留。其他项目草案独立。
6. `/p/launch/artifacts/W-103` 比较图片；`/p/film/artifacts/W-201` 查看媒体时间点；`/p/novel/artifacts/W-301` 查看文稿；添加版本批注并处理。
7. `/machines/n1?tab=terminal` 体验模拟终端；files / preview 切换文件与预览释放。节点安装、排空与状态为模拟。
8. `/models` 配置修改会使旧批准失效；审批当前版本后模拟下发，失联节点保持待应用。
9. `/goals`、`/reports`、`/worklogs`、`/testplans`、`/tests`、`/defects`、`/schedules`、`/health` 体验资源创建、编辑与状态动作。定时报告只能生成草稿。
10. `/settings` 切换主题、暗色、角色与离线；`/work?state=empty|loading|error|denied` 分别演示四类状态（将 `|` 分隔内容替换成一个值）。

## 范围与交付边界

这是可运行的交互设计稿，**不是只剩接 API 即可上线的成品**。Mock 支持共享状态、版本引用、操作校验、活动记录和刷新恢复；各模块入口见 [覆盖表](COVERAGE.md)，实际检查见 [评审记录](REVIEW.md)。

尚须工程实现与验收：服务端权限与真实身份、并发幂等、组织级可见范围、原件/快照存储、模型与 runtime 协议、自动计划与调度、永久归档与搜索、Centrifugo 推送、聊天桥接、FRP 与远程文件/PTY、分布式节点与无中断升级、Tauri 原生及真机能力。图片/视频/软件预览是示例内容；历史版本批注可分离，但历史媒体不是独立二进制快照。专业包是结构与示例交互，不包含完整专业编辑器。

外部角色只演示共享视角，不是安全隔离：所有演示数据都在本地，不能输入生产密钥或真实业务材料。手机视图复用 Web 页面，不代表 iOS / Android 已打包或真机验收。

## 维护入口

- `src/ui.jsx` / `src/styles.css`：共享组件、主题与响应式。
- `src/store.js` / `src/data.js`：模拟命令、关联与样例数据。
- `src/main.jsx`：外壳、规范路由、搜索、角色与页面状态。
- `src/pages-work.jsx`：工作与项目；`pages-flow.jsx`：工作流节点编辑；`pages-calendar.jsx`：日历；`task-previews.jsx`：任务预览标签；`pages-collab.jsx`：会话与成果；`pages-admin.jsx`：治理与资源。
- [设计契约](DESIGN.md)、[借鉴资料](../references/mall-design/README.md)。

## 导航、租户与主题

左侧菜单切换当前区域内容，不改变 URL 或 history；下方不重复项目列表。左上角 Logo 右侧管理租户，租户内部通过一个项目实例选择器切换范围。创建租户得到空白演示空间，加入使用原型邀请码 `DEMO-JOIN`；切换时本地项目、消息和业务记录分别保存。正式邀请和租户安全仍需服务端。

顶部调色盘可切六套原主题，并提供深色模式。主题定义在 `src/themes.css`，下拉、弹窗、校验和滚动条统一复用 `src/ui.jsx` / `src/styles.css`。

日历入口：`/work?view=calendar` 或 `/p/launch/work-items?view=calendar`。月 / 周切换、Human / Agent 筛选、跨日任务、项目截止与未排期共用工作记录；点击任务调整周期，需填写理由。移动端使用当前周期议程。日期重叠提示不代表精确工时超载。

租户视角的工作台提供全部项目管理；实例视角的工作台就是当前实例概览。协作区没有重复概览入口。工作流通过「编辑新草案 → 添加节点」打开节点库，右侧切换参数 / 输入输出 / 调试，并支持复制、删除、移除连接、配置检查与发布。

执行会话右侧提供任务预览多标签：新增 Web / API / 文件 / 终端 / 文稿，可重复打开同类，切换保留组件状态；模拟断线 / 重连、关闭标签可操作。任务之间分别保存标签配置；真实服务、终端及租约回收仍需节点实现。

静态截图：[桌面工作台](screenshots/overview-desktop.png)、[手机工作台](screenshots/overview-mobile.png)、[工作流](screenshots/workflow-desktop.png)、[任务多标签预览](screenshots/execution-desktop.png)、[日历](screenshots/calendar-desktop.png)、[手机日历](screenshots/calendar-mobile.png)、[六套主题](screenshots/themes.png)、[暗色](screenshots/overview-dark.png)。
