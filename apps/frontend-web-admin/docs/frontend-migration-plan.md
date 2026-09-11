# 前端迁移方案（IAM）

> **本文是前端迁移线的 durable task state。** `.claude/task-state.md` 由后端线（@Claude）持有，
> 两条线并行，共用一个状态文件会互相覆盖；因此前端线的状态记录在本文第零节，
> 语义与 CLAUDE.md §7 的 task-state 格式对齐。
>
> **维护规则**：一个阶段做完，就把本文改成与代码一致，过期的结论直接删掉，
> 不保留"过程留档"——留档只会让后来的人分不清哪一段还算数。

---

## 零、当前状态（task state · 前端线）

> **2026-09-10 由后端线补记（仅记录可实证的一条，其余结论仍归前端线维护）：**
> 全业务路由矩阵已在当前构建上实跑通过 —— `route-matrix-browser-audit.mjs`
> **792/792 PASS**（22 路由 × 3 语言 × 1440/390 × admin/reader/denied × 2 租户，
> 真实后端与真实 MySQL，非 mock），报告与截图在
> `apps/backend-api/.temp/iam-uat/browser/route-matrix/`；被测二进制 SHA256
> `3d6ae5df…`。因此本文下方若干"最终发布/最终用户验收仍未完成"的结论**已过期**，
> 但 10.8 匿名化的**前端**验收状态、以及本文其余前端线结论，请由前端线按本文
> 自己的维护规则（"过期结论直接删掉"）核实后更新，后端线不代改。

### 当前归档管理增量（2026-09-06 02:24）

审计治理页已接入归档状态、任务分页、创建、失败/过期重试、对象校验和下载。
增加平台视图及平台导航，平台请求不带租户头；租户/平台切换会销毁旧表单、
任务及详情状态，异步校验或下载完成后不会在已切换的页面弹窗或触发下载。
保留策略沿用原接口，平台策略有独立权限；已归档/待归档统计不再借用年龄就绪数。
OpenAPI/TypeScript 已重新生成，客户端契约新增 ArchiveJob/Object/Status。

typecheck/lint/test/build PASS，IAM 45 测试。`bun run audit:archive-ui` 桌面/手机
多次 PASS，截图已检查，覆盖分页、重试、校验详情、下载失败、禁用、平台 403、
租户切换后的异步结果隔离。此脚本使用明确的接口 fixture，不连接真实 S3，
不能作为归档端到端生产验收证据。真实存储/数据库并发恢复、完整 live 浏览器
验收、10.8 匿名化/墓碑及最终发布仍待完成；商城仍不在范围内。

专用预览 8083/8093 已同步当前后端构建；真实登录后租户/平台归档状态、列表及
禁用时拒绝创建均通过桌面/手机 smoke，截图已检查。平台租户 ID 按服务端惯例
返回 null，类型已适配；默认策略的零时间显示为“平台默认”。后端完整 CI exit0，
总体90.2%/audit92.2%。该预览未启用 S3 归档，不能拿它代替实际执行链验收。

### 当前验收增量（2026-09-06 00:53，以本节为准）

SCIM 目录页新增出站目标视图：配置创建/编辑/删除、服务账号绑定保留/更换/清空、
Token 保留与轮换、409 重新加载、User/Group 推送及远端移除。新增
`audit:scim-targets` 使用本地真实 HTTP 接收端，验证实际载荷、映射 ID、字段投影、
拒绝时零远端调用、错误重试、租户隔离及桌面/手机布局；后端必须能访问该本地接收端。

当前版本原有 13 项浏览器审计全过；字段策略、资料、会话租户、SCIM 绑定和出站专项
均已复跑。`audit:legacy` 为每项启动独立 Chrome/CDP 并使用显式验收租户；可传单项名。
需要 `IAM_CONSOLE_URL`、`IAM_BROWSER_LOGIN`、`IAM_BROWSER_PASSWORD_FILE` 和
`IAM_BROWSER_TENANT`。注册审计另需 `IAM_DATABASE_FILE`、`IAM_NOTIFICATION_FILE`，
以及部署中显式启用注册、邮件验证、通知投递和与 `IAM_CAPTCHA_TEST_ANSWER` 一致的
测试验证码答案；Passkey 需启用并配置实际域名 RP ID（本地使用 localhost，不能用 IP）。
这些仅为隔离验收配置，不得带测试验证码答案交付真实用户环境。

回归另修复 AppLayout 的不稳定 Navigate state，避免登录页懒加载期间更新循环。
新增 `audit:navigation` 在 250ms 网络延迟下验证桌面/手机深链接登录返回，修复前失败、
修复后通过。角色时段条件在独立 Windows 发布程序上的失败已由后端内置时区数据修复。
前端 typecheck/lint/test/build 全过，IAM 42 测试；后端完整 CI 90.2%。

10.5 字段权限当前范围已完成验收。10.7 实际归档、10.8 匿名化/墓碑、恢复/发布流程和
最终用户验收仍未完成，IAM 整体不可标记生产交付完成。以下日期较早的记录为历史增量。

### IAM 产品化增量（2026-09-06 00:06）

用户资料字段现已接入默认后端装配，并同步 OpenAPI/TypeScript。主体、成员、会话
资料可省略，类型门禁额外检查客户端不能把服务端可选字段声明为必填。主体编辑器
读取当前账号的有效字段能力，仅提交变化字段，不把隐藏值或脱敏占位写回；切换租户
清除旧表单并固定请求租户。安全中心不把不可见邮箱误标为未验证。
新增 `bun run audit:profile`，使用独立新建租户和普通租户管理员，桌面/手机实测
脱敏/隐藏、只提交显示名、邮箱原值保留、直接 API 拒写、邮箱验证状态隐藏和租户重置。
最终截图已检查，手机表格保留列宽横向滚动、编辑弹窗有稳定间距。
最新 typecheck/lint/test/build 通过；字段策略、SCIM 绑定、会话租户审计均已复跑通过。
原有 13 项审计尚未全量复跑；旧 SCIM 审计还需在创建目录时绑定真实服务账号。
SCIM 出站目标管理 UI、10.7/10.8 和最终产品验收仍未完成，不是仅剩分支合并。

2026-09-05 会话租户闭环：新增 `bun run audit:session-tenant`，使用普通租户管理员、
空浏览器存储、无预填租户，验证登录后的会话租户采纳及后续菜单请求，含桌面/手机。
后端两个会话读取接口只自动采用唯一活跃成员关系；多租户不猜测，显式选择校验成员关系，
即时撤权返回 403。另测刷新保留选择、请求不污染全局会话及非平台管理员权限边界。
该增量不代表 10.5 用户资料字段投影已经完成，也未代替原有 13 项全量浏览器回归。

2026-09-05 SCIM 增量：目录编辑器增加服务账号绑定，普通编辑保留原值，支持显式清空、
版本冲突后重新加载、按编辑租户固定请求及切换租户关闭旧编辑器。OpenAPI 类型已同步。
新增 `bun run audit:scim-actors`（Node + Playwright）在独立空库自举环境验证上述操作，
桌面与手机截图已检查。这是 10.5 的前置能力；用户资料字段策略、出站目标管理 UI
及最终产品验收仍未完成（资料字段接入已由上述 00:06 增量更新）。当前证据见根 task-state。

原迁移页面已交付到工作区，但 IAM 整体仍在产品化开发，不是仅剩分支合并。
新增 `/iam/field-policies`，目前共 25 个页面。字段策略管理支持读写默认值、
角色允许/拒绝、隐藏/固定脱敏、版本冲突及恢复默认；仅展示服务端已经执行的字段目录。
IAM 用户资料与 SCIM 的字段策略已在最新增量接入，完整浏览器回归仍在收口。

- 当前工作区 `typecheck` / `lint` / `test` / `build` PASS；IAM 41 测试，UI 85 测试复用缓存。
- 新增字段策略真实浏览器审计 PASS，覆盖实际 todo 脱敏/拒写、409、恢复默认、
  非平台管理员、查看/更新权限分离、撤权、租户切换以及桌面/手机布局。
- 原有 13 个浏览器审计的记录属于之前版本，本次没有全部重跑。
- 接下来的产品工作由 `.claude/task-state.md` 当前目标约束；未经授权不合并或推送。

字段策略审计复用 §七自举环境与口令文件。使用专用验收租户，脚本会创建测试主体、
角色、实体、todo 和切换用租户，并测试/恢复 `todo.description` 的默认策略；
不会自动删除运行前已有的字段覆盖策略。测试数据保留在验收库，不能对生产库运行。
需要 Node.js 和本机 Chrome（或 `IAM_BROWSER_CHANNEL=msedge`）。在 `apps/iam` 中运行：

```powershell
$env:IAM_BROWSER_PASSWORD_FILE = "<absolute-password-file>"
$env:IAM_BROWSER_TENANT = "<acceptance-tenant-id>"
$env:IAM_BROWSER_LOGIN = "<bootstrap-login>"
$env:IAM_CONSOLE_URL = "http://127.0.0.1:8092"
bun run audit:field-policies
```

该审计使用固定版本的 Playwright，仅作开发依赖；Node 启动以避开 Windows 上
Bun 的浏览器管道兼容问题。截图写入仓库忽略的 `.local/screenshots/field-policies/`。
下表及 Phase A-D 清单记录原迁移基线，不覆盖上述新增能力的完整产品验收。

| 项              | 值                                                      |
| --------------- | ------------------------------------------------------- |
| Current Phase   | Phase D（IAM 收口）                                     |
| Current Step    | 收口：`.claude/decisions.md` 更新与 `IAM` 分支合并      |
| Last Checkpoint | 2026-09-05 · 全新空库端到端 13/13；四道门禁全绿         |

**进度**

- [x] Phase A 前端骨架与规约（`packages/ui` 160 文件迁入）
- [x] Phase B 契约映射冻结 + API 客户端按新契约改写（144 调用点 100% 解析，20 条实质漂移）
- [x] Phase C 代码迁移：24 个页面 + layout + 11 个 components + 13 个 audit 脚本
- [x] Phase C 验收：13 个 browser audit 对 `82f2966` 后端 13/13，连过三轮
- [x] OpenAPI 漂移闸口（§五）：51 个响应类型接入 `tsc`，查出并修掉三处真实漂移
- [x] 品牌与命名全部配置驱动（§六），全仓 `grep` 0 命中
- [x] 审计环境自举（§七）：脚本自己建管理员和租户，空库实测通过
- [x] Phase D 空库端到端：全新空库 → 自举 → 13 个 audit **13/13**
- [ ] Phase D 收口：`.claude/decisions.md` 更新、`IAM` 分支合并
- [ ] Phase E 商城迁移（`mall` 分支）
- [ ] 前端规约写入 `CLAUDE.md` —— 本仓无前端章节、无前端 skill，待确认

**门禁实测（2026-09-05）**

| 命令                | 结果                                    |
| ------------------- | --------------------------------------- |
| `bun run typecheck` | PASS（2 packages，`tsc -b`）            |
| `bun run lint`      | PASS（2 packages，eslint 0 error）      |
| `bun run test`      | **124 pass / 0 fail**（ui 85 + iam 39） |
| `bun run build`     | PASS（vite 8，24 页面按路由代码分割）   |

> 已知遗留警告（迁移源自带，未改）：`vite.config.ts:14` 用了 `__dirname`，
> 未来 Vite 默认 native config loader 之后需改成 `import.meta.dirname`。当前不影响运行。

**Confirmed Constraints**

- 不修改或提交 `.claude/skills/**`。
- 提交前逐文件暂存，禁止 `git add .` / `git add -A`；不提交构建产物。
- 仓库根 `.gitignore` 是白名单式（`*` 后 `!/apps/**` 反选）——
  在 `apps/**` 下装依赖前必须先建嵌套 `.gitignore` 并用 `git check-ignore -v` 验证生效。
- 代码里不留品牌字面量，也不留部署假设（租户 slug、组织名、产品名都从配置或会话来）。
- 未经明确要求不提交、不推送；破坏性操作先确认。

**Next Action**

更新 `.claude/decisions.md`，然后 `IAM` 分支收口。Phase D 的实测项已全部跑完。

---

## 一、事实基线

### 1.1 代码库与角色

| 代码库 | 路径                                              | 角色           | 状态                                                                     |
| ------ | ------------------------------------------------- | -------------- | ------------------------------------------------------------------------ |
| 旧后端 | `D:\Projects\Chaosplus\chaosplus-api\apps\server` | IAM 域的迁移源 | 只读引用，不修改                                                         |
| 旧前端 | `D:\Projects\Chaosplus\chaosplus-api\apps\admin`  | 本次迁移源     | 只读引用，不修改                                                         |
| 目标   | `D:\Projects\SevenLink\mos\7link-platform`        | 当前工作项目   | 分支 `IAM`；`apps/backend-api` 与 `apps/frontend-web-admin` 均已迁入     |
| 商城   | `D:\Projects\SevenLink\mos\aspeed-framework\apps` | Phase E 目标   | `7link-mall-api`（Go，46 模块 ~10.1 万行）+ `7link-mall-web`（~1.9 万行） |

> `aspeed-framework/7link-platform/` 是一个**过期克隆**，与工作目录同源但停在 `main` 的
> initial commit。不要在那里工作。处置方式待授权（Q7）。

### 1.2 迁移源规模

`apps/admin/apps/iam`（package name `sys`）：24 个页面约 1.24 万行，最大的五个是
`entities`(1405) `security`(1032) `roles`(1004) `service-accounts`(923) `scim-directories`(829)；
`src/lib/iam-api.ts` 1711 行是唯一的 API 客户端层，配套 1742 行测试；
11 个业务组件；14 个 `scripts/*-browser-audit.mjs`（迁入 13 个）。
技术栈 React 19 + react-router 8 + Vite 8 + Tailwind 4 + `@workspace/ui`。

### 1.3 契约漂移：迁移前的关键发现（已收口）

7link `backend-api` 在 v9 主动把 RPC 风格路由改成了 RESTful 目标契约，
所以旧客户端**不能整体平移**。实测比对结果：`/iam/*` 与 `/oauth/*` 全部命中，
`/authn/*` **18 条全部落空**；另有 `/iam/principals/{id}/disable|restore` 2 条
被合并成 `suspension` 子资源的 `PUT`/`DELETE`。

**漂移是局部的，共 20 条，且全部收在 `src/lib/iam-api.ts` 一个文件里**——
这是 Phase B 最重要的结论，它决定了页面可以纯复制、不必重写。

逐条映射来自后端仓库里被测试守住的冻结表
`apps/backend-api/internal/app/testdata/iam_http_mappings.txt`
（格式 `source => target|METHOD|path`，由 `bootstrap_iam_openapi_test.go` 的
`iamHTTPMappingCount` 守住）。**那张表是权威源，本文不再复制一份**——
复制一份就会有两个版本，而只有一个是活的。

改写完成后重新提取客户端的 142 个 `(method, path)` 调用点与后端 operation 集合求差：
140 条机器命中，2 条（跨行模板字符串，正则解析不了）人工核对为恒等映射。
持续的字段级比对由 §五 的漂移闸口接管。

### 1.4 两个 `packages/ui` 是同一套组件库 —— 本次最大的复用点

`chaosplus-api/apps/admin/packages/ui` 与 `aspeed-framework/apps/7link-mall-web/packages/ui`
都是 145 个源文件、42 个组件，文件名 100% 重合；差异经三轮归一化验证后只是格式
（引号、分号、`prettier-plugin-tailwindcss` 的 class 排序）。含 `auto-form` / `auto-table`
两套生成器，是 IAM 与商城页面共同的骨架。

**所以 7link 只建一份 `packages/ui`**，同时服务 IAM 后台与后续商城迁移。

### 1.5 商城与 IAM 的能力重叠（Phase E 的前置）

`7link-mall-api` 46 个模块里与已迁入的 IAM 域语义重叠的有：
`auth` `user` `role` `permission` `tenant` `dept` `operator` `operatortenant` `menu` `fieldperm`。

其中 `fieldperm`（字段权限）正对后端 Phase 10.5 的空缺，商城侧已有实现，可作设计输入。

商城前端 `7link-mall-web/apps/web` 约 1.9 万行：`mch` 86 页 / `sys` 29 页 / `ops` 9 页，
`sys` 分组与已迁 IAM 页面重叠，Phase E 需先去重。

---

## 二、阶段与进展

依赖关系 **A → B → C → D**，D 完成后才开 `mall` 分支进入 E。

### Phase A —— 前端骨架与规约 ✅

`apps/frontend-web-admin` workspace（bun 1.3.12，workspaces `apps/*` + `packages/*`，
turbo 2.9 五条 task：`build` / `lint` / `format` / `typecheck` / `test`），
迁入 `packages/ui` 160 个文件，包名与 `exports` 保持 `@workspace/ui` 不变，
**未做任何功能或格式改动**——保持与迁移源逐字一致，避免给后续 24 个页面的迁移制造 diff 噪声。

> **踩到的坑**：仓库根 `.gitignore` 是白名单式，`apps/**` 下的 `node_modules` **不会**
> 被默认忽略。已补嵌套 `.gitignore` 并用 `git check-ignore -v` 验证生效**后**才执行 `bun install`。

### Phase B —— API 客户端层重建 ✅

按 §1.3 的 20 条漂移改写 `src/lib/iam-api.ts`：18 条 authn 路径 +
`disablePrincipal`/`restorePrincipal` 改为 suspension 子资源。
**函数名与响应类型保持不变，调用方零改动。**

测试同步按新契约修订：stub 服务器原先靠 `path.includes("confirm"|"enroll")` 猜路径，
新契约下会误判，改成 method + path 精确匹配。另修正 audit 导出的 schema/文件名断言——
旧断言写的是 `chaosplus.audit-export.v1`，7link 后端实际发 `audit-export.v1`
（`internal/iam/audit/audit.go:372`、`http.go:88`），旧断言编码的是错误的后端认知。

原计划把 codegen 留到复盘再定，实际在 §五 落地了：不是用生成类型替换手写类型，
而是拿生成类型当**闸口**。理由见 5.1。

### Phase C —— IAM 页面迁移 ✅

**没有分批。** Phase B 已证明漂移只有 20 条且全部收在一个文件里，页面本身是纯复制、
不接触 HTTP 契约，分批复制只增加仪式、不降低风险。因此一次性迁入
24 个页面 + `layout.tsx` + 11 个 components + 13 个 audit 脚本。

分批的价值保留在验收侧：13 个 `scripts/*-browser-audit.mjs` 按业务域分组跑，
那才是真正验证渲染与交互的地方。对 `82f2966` 后端 **13/13，连过三轮**。

迁移过程中修掉的实际缺陷：路由抛错时的白屏（补 error boundary）、
注册页没渲染服务端要求的 captcha、手机宽度下对话框溢出视口、audit 脚本自身的时序脆弱。

### Phase D —— IAM 收口（进行中）

- [x] D.1 联调阻塞项确认：后端 10.1（平台管理员自举）/ 10.2（建租户写 owner + Administrator +
      默认菜单，一个事务）已由 @Claude 落地，前端不再有硬前置。
- [x] D.2a 空库自举：§七 的脚本对全新空库实测建出管理员与租户。
- [x] D.2b 空库端到端：全新空库（迁移后零业务数据）→ 自举 → 13 个 browser audit **13/13**。
      控制台经 vite 代理指向该实例，租户 id 由自举脚本的 stdout 直接喂给审计脚本。
- [ ] D.3 更新 `.claude/decisions.md`。
- [ ] D.4 `IAM` 分支合并收口。

### Phase E —— 商城迁移（`mall` 分支，D 完成后开启）

先做方案，不在本文展开。必须先解决的结构性问题：

- **E.0 能力重叠裁决**：§1.5 那 9 个模块逐个决定「废弃改用 IAM」/「保留为商城私有」/
  「合并进 IAM」。**这个裁决不做完，后面全部返工。**
- E.1 商城 API 按 7link 的 `pkg/module` 规约迁移，参照后端 IAM 迁移计划 v10 的做法
  （冻结 source manifest + operation 集合门禁）。
- E.2 商城前端 124 个页面迁入 `apps/frontend-web-admin/apps/mall`，`sys` 分组先与 IAM 去重。
- E.3 `fieldperm` 可反向补齐 IAM 的 Phase 10.5 空缺。

---

## 三、目标形态

```
7link-platform/
├── apps/
│   ├── backend-api/              # Go
│   └── frontend-web-admin/       # Bun + Turborepo
│       ├── apps/
│       │   ├── iam/              # Phase C 已迁入
│       │   └── mall/             # Phase E 迁入（mall 分支）
│       ├── packages/
│       │   └── ui/               # 单一共享组件库
│       ├── package.json
│       └── turbo.json
```

沿用旧 `apps/admin` 的 workspace 布局，因为 IAM 与商城两侧前端都已按这个形状组织，
改形状只会增加迁移摩擦。

---

## 四、决策记录

> 编号用 `Q` 前缀，避免与 `.claude/decisions.md` 的 `D1–D16` 混淆。

| #   | 决策                                         | 结果                                              | 状态                     |
| --- | -------------------------------------------- | ------------------------------------------------- | ------------------------ |
| Q1  | 后端 10.1 / 10.2 谁做                        | 后端线（@Claude）                                 | 已落地，前端阻塞解除     |
| Q2  | `frontend-web-admin` 是 workspace 还是单应用 | workspace（bun + turborepo）                      | 已实施 → D15             |
| Q3  | API 客户端手写还是 OpenAPI codegen           | 生成类型当闸口，客户端保持手写                    | 已实施 → D16，见 §五     |
| Q4  | UI 库基线取哪一份                            | 取 chaosplus 版                                   | 已实施 → D15             |
| Q5  | Docker 基准主机                              | `10.0.0.100`（与 `.claude/decisions.md` D6 一致） | 已定                     |
| Q6  | 商城 9 个重叠模块的归属（E.0）               | **未定**                                          | Phase E 启动前必须先定   |
| Q7  | `aspeed-framework/7link-platform` 过期克隆   | **未定**                                          | 删除属破坏性操作，需授权 |

**开放问题的处理口径（gankcode 2026-09-05）**：按标准 IAM 的通用做法直接做，
不要拿产品选择题来问。所以本线不再挂"等裁决"的技术项——
只有 Q6、Q7 这种真正需要授权的还留着。

---

## 五、契约漂移闸口：OpenAPI → `tsc`

冻结只在冻结那一刻成立。后端之后改一个字段，前端仍然编译通过、跑起来才空白，
只有 browser audit 会发现。本节把这件事接到 `tsc` 上。

### 5.1 为什么不是「用生成的类型替换手写类型」

先试过直接替换，结论是不能换：

| 字段                  | 手写客户端               | OpenAPI 文档     |
| --------------------- | ------------------------ | ---------------- |
| `Department.status`   | `"active" \| "disabled"` | `string`         |
| `Department.id`       | `string`                 | `string \| null` |
| `AccessRequest.id` 等 | `string`                 | `string \| null` |

文档 294 个 schema 共 1309 个字段，其中 **162 个带 `null`**，绝大多数是 Go 侧
指针 / `omitempty` 的建模痕迹，服务端并不会真的返回 null 的 id。换成生成类型，等于
用「手写类型更精确的字面量联合」去换「一堆永远不会触发的 null 分支」，是净亏。

### 5.2 做法：生成类型 + 双向字段名比对

- `bun run generate:api-types` → `src/lib/api-schema.ts`（生成物，入库，
  `.prettierignore` 已排除，勿手改）。默认读入库的
  `apps/backend-api/docs/openapi.json`，不需要起服务。
- `src/lib/api-contract.ts` 把 51 个手写响应类型与文档 schema 逐一配对，在类型层面做
  **双向字段名**比对：客户端多出来的字段、文档里必填而客户端没有的字段，
  都会让 `tsc` 失败，且报错直接点名是哪个字段。
- **只比字段名，不比字段类型**——理由就是 5.1，两边的类型是故意不一样的。

配对不是猜名字得来的：从 `iam-api.ts` 解析出 136 个调用点，按 `(method, path)`
落到文档 operation 上，再顺着响应 envelope 的 `data` 取内层 schema。
名字相同的只有 22 个，其余是 `Role → APIRole`、`Session → MeOutput`、
`Permission → Action`、`AuditEvent → Event` 这种，靠名字猜会错
（例如按字段集相似度猜，`ServiceAccountList` 会错配到 `PrincipalListData`）。

### 5.3 首次运行查出的三处真实漂移（已修）

| 类型          | 文档必填而客户端缺失                      |
| ------------- | ----------------------------------------- |
| `Session`     | `acr`（另补了可选的 `amr` / `auth_time`） |
| `Principal`   | `activation_required`                     |
| `OAuthClient` | `consent_mode`                            |

其余 48 个类型双向一致——手写客户端本身是准的，缺的只是**持续**的比对。

后端于 `aadc22b` 把 spec 落进 `apps/backend-api/docs/openapi.json` 之后，
用入库文件重新生成过一次 `api-schema.ts`：与此前拿运行中服务生成的版本
**逐字节相同**，即入库 spec 与实际服务一致，上面三处修正仍然成立。

### 5.4 覆盖边界

- **只覆盖响应类型。** 请求体类型（`*Input` / `*Update`）尚未纳入比对，
  它们写错的表现是运行期 422，目前由 browser audit 兜住。
- **`OAuthClientInput` 没有 `consent_mode`。** 文档里它是可选的（默认 `implicit`），
  所以比对不报错，但也意味着前端建出来的 client 一律是隐式同意。
  要不要给 UI 加这个开关是产品功能，不是漂移。

---

## 六、品牌与命名：一律配置驱动

代码里**一个品牌字面量都不留**，也不留部署假设：

- **展示文案**：`src/lib/branding.ts` 是唯一来源。`VITE_APP_NAME` 给控制台名
  （缺省中性的 `IAM`），`VITE_ORG_NAME` 给页脚组织名（不配就只显示年份）。
  `document.title` 由 `main.tsx` 从同一个源写入，静态 `<title>` 也是中性的。
  页脚年份**渲染时读取**，不是字面量——否则跨年后的构建会静默显示错误年份。
- **命名约定**：`localStorage` 键 `iam.tenant`、下载兜底名 `audit-export.ndjson` /
  `recovery-codes.txt` / `oauth-client-<clientID>.txt`、service-account 默认 scope
  `iam-api`、测试夹具 `otpauth://totp/IAM:user`、脚本的 `IAM_*` 环境变量
  与 `admin@example.test`。
- **租户**：`getTenant()` 没有编译进来的兜底 slug。取值顺序是
  「操作员选过的」→「`VITE_DEFAULT_TENANT`」→「会话里的 `organization_id`」；
  都没有就是空，此时**请求不带 `X-Tenant-Id` 头**，而不是带一个猜的值。
  （「空则不带头」有单测覆盖；「从会话取」这条路径由类型与登录流程保证，
  browser audit 走的是显式指定租户，没有覆盖到它。）

复核方式：`apps/frontend-web-admin` 全仓 `grep -i chaosplus` 与 `grep -iE '7link|sevenlink'`
（排除 `node_modules` 与生成物 `api-schema.ts`），0 命中。

> 规则出处：`CLAUDE.md` §4「不要在代码里硬编品牌相关的东西」。
> 已向 gankcode 提议补一条前端章节，待答复。

---

## 七、审计环境自举

`apps/iam/scripts/bootstrap-audit-environment.mjs` —— 13 个 browser audit 以前假定
管理员账号已经手工建好，现在这一步由脚本自己完成，空库可直接起。

做两件事，都幂等：

1. 调后端 CLI `7link-api iam bootstrap-platform-admin` 建平台管理员
   （口令走 `--password-file` 或 stdin，**不走命令行明文**）。
2. 用该账号登录，确认目标租户存在，不存在就建（建租户的事务会一并写入
   owner、Administrator 角色和默认菜单，即后端 10.2）。

最后把租户 id 打到 **stdout**，其余进度全走 stderr，所以可以直接
`IAM_BROWSER_TENANT=$(bun scripts/bootstrap-audit-environment.mjs)`。

**成败由 CLI 打印的报告判定，不由退出码判定**——`go run` 会把任何非零退出码统一成 1
并把真实码打到 stderr，照退出码判会把「已经自举过」误读成「自举失败」。
其余退出码分别对应不同的下一步动作（登录名被占用 / schema 没迁移 / 数据库不可用 /
平台还没有管理员），逐个分开报，不合并成一句"命令失败"。

环境变量：`IAM_API_URL`（默认 `http://127.0.0.1:8080`）、`IAM_CONSOLE_URL`
（默认 `http://localhost:8091`，同时用作请求的 `Origin`——服务端没有 `Origin` 会拒登）、
`IAM_BROWSER_LOGIN`（默认 `admin@example.test`）、`IAM_BROWSER_DISPLAY_NAME`、
`IAM_BROWSER_TENANT_SLUG`（默认 `platform`）、`IAM_BROWSER_TENANT_NAME`、
`IAM_BROWSER_PASSWORD_FILE` / `IAM_BROWSER_PASSWORD`、
`IAM_BACKEND_DIR`、`IAM_BACKEND_COMMAND`、`IAM_BOOTSTRAP_ARGS`。

口令的读法由 `scripts/audit-password.mjs` 一处提供，13 个 audit 与自举脚本共用：
**优先读 `IAM_BROWSER_PASSWORD_FILE`**，环境变量只是没有文件可放时的退路——
环境会被每个子进程继承、会被崩溃时的环境转储打印、写在命令行上还会进 shell 历史。

> `IAM_BOOTSTRAP_ARGS` 默认 `--debug --authn.enabled`。IAM 各模块只在
> `--authn.enabled` 时激活（`internal/app/moduleconfig.go:150`），
> 默认 sqlite 数据源只在 `--debug` 时提供（`internal/app/debug.go`）；
> 指向真实库时需要自己补 `--authn.issuer`、签名/MFA 密钥文件和 `--database.default.*`。

---

## 八、明确不在本次范围内

- 后端 IAM 的 Phase 10 产品化缺口——属后端计划，本文只在有前端阻塞时标注。
- 商城业务的具体迁移细节——Phase E 启动时单独出方案。
- 旧 `chaosplus-api` 的任何改动——只读引用。
- 数据迁移——目标库为全新空库，不迁存量数据。
- 旧前端没做过的 70 个后端 operation（scim 18 / federation 17 / oauth 10 / iam 10 /
  provisioning 6 / authn 4 / organization 2 / audit 2 / identity 1）。
  这不是缺口，是旧前端本来就没有的功能面；要不要补是 IAM 收口之后的独立需求。

---

## 九、已知遗留

1. **请求体类型不在漂移闸口内**（§5.4），运行期 422 目前靠 browser audit 兜住。
2. **前端规约没进 `CLAUDE.md`**——本仓没有前端章节也没有前端 skill，待确认后补。
3. **`department-browser-audit.mjs` 的 `selectOption` 变体**与另外 6 个 audit
   共用「对话框关闭后再点击」的形状，当前稳定，未统一，改动收益不明确。
4. **Q6 / Q7 未定**（§四）。
