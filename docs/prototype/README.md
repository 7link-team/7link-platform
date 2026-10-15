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
3. `/p/launch/work-items/W-104?tab=review`：直接接受会被质量门阻止；开始独立验证，逐项填写模拟实际结果和证据、结束批次形成固定报告；必需检查通过后由 Human 接受，再确认正式交付。
4. `/deliveries` 查看同步出现的固定版本包。设置切换 external 后查看共享工作台，提交回执或有理由退回；切回 owner 查活动与返工。
5. `/p/launch/flow` 编辑新草案、拖动并连接节点、按配置/输入/输出/运行记录/错误策略完善节点，修改契约、发布版本。协作及 Git CI/CD 节点按业务显示参数；模拟预览不实际构建或部署。添加未连接节点会被拒绝，既有发布版本保留。其他项目草案独立。
6. `/p/launch/artifacts/W-103` 比较图片；`/p/film/artifacts/W-201` 查看媒体时间点；`/p/novel/artifacts/W-301` 查看文稿；添加版本批注并处理。
7. `/machines/n1?tab=terminal` 体验模拟终端；files / preview 切换文件与预览释放。节点安装、排空与状态为模拟。
8. `/models` 配置修改会使旧批准失效；审批当前版本后模拟下发，失联节点保持待应用。
9. `/reports` 手动生成 / 打开或设置模拟定时报告，同主体同周期只有一份；校准后本人确认上报。`/p/launch/work-items/W-104?tab=analysis` 自动分析任务投入及 Agent 运行证据，Human 按需校准并确认；`/worklogs` 只保留历史证据访问，不再独立创建。owner 的 `/performance` 按周期查看租户/项目汇总与 Human/Agent 工作、考核报告。`/goals`、`/testplans`、`/tests`、`/defects`、`/schedules`、`/health` 保留相应资源与状态入口。
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

执行会话右侧根据任务交付资源展示预览标签，不提供手动增删。切换保留各标签输入、文件选择、API 结果、终端草稿和阅读位置；支持模拟断线与重连。任务之间分别保存交互状态；真实服务、终端及租约回收仍需节点实现。

静态截图：[桌面工作台](screenshots/overview-desktop.png)、[手机工作台](screenshots/overview-mobile.png)、[工作流](screenshots/workflow-desktop.png)、[任务多标签预览](screenshots/execution-desktop.png)、[日历](screenshots/calendar-desktop.png)、[手机日历](screenshots/calendar-mobile.png)、[六套主题](screenshots/themes.png)、[暗色](screenshots/overview-dark.png)。

当前全量深审未完成，不能将之前有限检查视作全部需求验收通过。进度与已复现缺陷见 [REVIEW](REVIEW.md)，逐项事实与缺口见 [COVERAGE](COVERAGE.md)。


## 本次管理流程调整

- Logo 右侧：已加入租户、创建、邀请码加入、收到邀请的接受 / 拒绝；`/members` 管理发出的本地项目邀请和撤销，不发送真实邮件。
- `/members` 只有 Human 组织树与人类成员。`/agents` 是独立 Agent 名册，分 Human 创建 / Agent 创建列表，展示准确创建者、父子关系与配置。动态身份和子 Agent 不随任务结束或父 Agent 退役自动删除。
- 任务详情右栏「配置工作小团队」引用两份名册，支持 Agent 池及 Human；当前任务、执行会话和日历同步。组织正式归属与历史成果作者不随编组修改。
- 「发起工作」进入会话草稿，Human 确认后建立工作和确认计划；新工作默认未分派执行者。
- `/reports` 同租户、主体、周期类型和周期起始日期共享一份逻辑报告，已有报告保留原时区及周期边界，重复生成不覆盖人工内容或已提交版本；历史修订可查看。
- `/worklogs` 分离本人预计、建议、校准和确认实际投入；Agent 墙钟、工具、Token 与费用为只读来源事实，缺失明确显示未采集。
- Agent 配置含岗位职责、System prompt、任务模板、Runtime、模型版本、Skills / MCP 引用；审批绑定具体配置版本。
- `/templates` 复用可视化工作流编辑器，AI 模拟起草、手动修改、图校验、发布与历史只读；专业工作包能力仍在 PRD 保留。
- 日期输入使用共享主题日历、键盘操作与自定义验证，保留直接输入 ISO 日期的能力。


## 可复现浏览器回归

先启动本地服务，使用已安装的 `agent-browser` 浏览器环境运行：

```sh
mkdir -p .audit-artifacts
python3 tests/run-browser.py --url http://127.0.0.1:4173 --width 1440 --output .audit-artifacts/prototype-desktop.json
python3 tests/run-browser.py --url http://127.0.0.1:4173 --width 375 --output .audit-artifacts/prototype-mobile.json
```

脚本为每轮创建独立浏览器会话，每套只重置原型自己的主状态键，预载 QA 逐项录入辅助，再运行 `browser-*.js`。可追加具体脚本文件名做定向复现。辅助脚本本身不计作测试。退出码同时检查断言返回值、路由错误 / 标题 / 溢出、运行期间源码是否变化；完整结果保存到指定 JSON。验收构建产物时先构建并以独立端口服务固定 `dist`，再将 `--url` 指向该服务，运行期间不得替换产物。

浏览器套件通过仅证明列明的交互断言，不自动证明覆盖全部 PRD、所有主题状态或真实外部集成。

测试源码、fixture、修复及评审文档进入 Git；截图、日志、浏览器结果和临时构建保存在本机 `.audit-artifacts/`，该目录已被 Git 忽略。需要迁移或分享证据时，可显式归档选定文件：

```sh
python3 archive-evidence.py --output .audit-artifacts/review-evidence.zip .audit-artifacts/prototype-desktop.json .audit-artifacts/prototype-mobile.json
```

归档包含原始文件、来源路径、大小及 SHA-256 索引，保留失败证据；同名归档不会被覆盖。以后复用以 `tests/` 中的正式脚本和 fixture 为准；不能把旧归档通过结果当成当前源码的验收。


## 列表设计对齐（2026-10-15）

共享 DataList 参照 [Mall 列表资料](../references/mall-design/README.md)统一筛选区、主操作区、列显隐、三态排序、每页本地序号、固定表头/两侧列与分页。顶部采用紧凑排版，桌面筛选与操作尽量同行，统计置于分页栏。左右和上下只在该方向还有隐藏内容时显示轻阴影，滚到边界或内容不溢出时自动消失；阴影不拦截点击。桌面表格占据剩余空间，手机仅表格内容横向滚动；复用现有主题及权限回调。列显隐随当前视图保存，不允许显示业务声明为不可见的列。文件图标模式同步提供默认顺序，避免切换视图后排序文案失真。

有意保留现有即时筛选和授权后本地 JSON 导出；没有后端导出任务、批量业务动作或导入能力的列表不显示虚假入口。这是原型列表体验调整，不是将来源规范的全部业务能力直接启用。

预览：[桌面列表](screenshots/list-design-desktop.png) · [手机列表](screenshots/list-design-mobile.png)。

验证：678 单元测试 / 8298 断言及构建通过；1440px、375px 各 5 套列表浏览器回归通过（task-list / workflow-list / audit-list / material-files / list-design），期间源码哈希不变。完整产品审计仍按 REVIEW 的独立状态处理。
