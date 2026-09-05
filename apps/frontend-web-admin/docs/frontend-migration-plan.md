# 前端与业务迁移方案 v2（执行中）

> 编写日期：2026-09-04。事实均来自当日对四个代码库的实测，不引用记忆或旧文档结论。
>
> **本文同时是前端迁移线的 durable task state。** `.claude/task-state.md` 由后端线（@Claude）持有，
> 两条线并行，共用一个状态文件会互相覆盖；因此前端线的状态记录在本文第零节，
> 语义与 CLAUDE.md §7 的 task-state 格式对齐。

---

## 零、当前状态（task state · 前端线）

| 项              | 值                                                         |
| --------------- | ---------------------------------------------------------- |
| Current Phase   | TESTING（联调已通过，13 个 audit 全绿）                    |
| Current Step    | 等入库的 OpenAPI 文件后重生成类型（§10.4）；余下等 §9 定名 |
| Last Checkpoint | 2026-09-05 · 13/13 audit 连过三轮；OpenAPI 漂移闸口接入 tsc |

**进度**

- [x] Phase A 前端骨架与规约（`packages/ui` 160 文件迁入）
- [x] Phase B.1 / B.2 契约映射表冻结（144 调用点 100% 解析）
- [x] Phase B.3 API 客户端按新契约改写（18 条 authn + principals 停用/恢复）
- [x] Phase B.4 客户端测试按新契约修订（含 audit 导出 schema/文件名对齐真实后端）
- [x] Phase C 代码迁移：24 个页面 + layout + 11 个 components + 13 个 audit 脚本
- [x] Phase C 验收：13 个 browser audit 脚本 —— 对 `82f2966` 后端 13/13，连过三轮
      （账号仍是手工建的；脚本自举等 §9.3b，见下）
- [x] OpenAPI 漂移闸口（§十）：51 个响应类型接入 `tsc`，查出并修掉三处真实漂移
- [ ] Phase D IAM 收口
- [ ] Phase E 商城迁移（`mall` 分支）
- [ ] A.1 前端规约写入 `CLAUDE.md` —— 本仓无前端章节、无前端 skill，待确认

**Verification（2026-09-04 实测，清缓存后复跑）**

| 命令                                 | 结果                                                                                                                                                          |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `bun run typecheck`                  | PASS（2 packages，`tsc -b`）                                                                                                                                  |
| `bun run lint`                       | PASS（2 packages，eslint 0 error）                                                                                                                            |
| `bun run test`                       | **119 pass / 0 fail**（ui 85 + iam 34，12 文件）                                                                                                              |
| `bun run build`                      | PASS（vite 8，24 页面全部按路由代码分割）                                                                                                                     |
| `git add -n apps/frontend-web-admin` | 240 文件，无 `node_modules` / `dist` / `.turbo` / `tsbuildinfo`                                                                                               |
| `bun run dev`（8091）                | 起得来；`index.html` / `main.tsx` / `router.tsx` / `layout.tsx` 与 **23 个页面模块全部 HTTP 200**（vite 逐个转换通过）；`/api` 代理已接通（无后端时返回 502） |

> 已知遗留警告（迁移源自带，未改）：`vite.config.ts:14` 用了 `__dirname`，
> 未来 Vite 默认 `configLoader: 'native'` 后需改成 `import.meta.dirname`。当前不影响运行。

**契约自检（机器验证，非人工核对）**

改写后重新提取客户端全部 142 个 `(method, path)` 调用点，与冻结表的 target 集合求差：
**140 条机器命中，2 条（跨行模板字符串，正则解析不了）人工核对为恒等映射。
即客户端 100% 的调用点都能落到后端真实存在的 operation 上。**

**Confirmed Constraints**

- 不修改或提交 `.claude/skills/**`。
- 提交前逐文件暂存，禁止 `git add .` / `git add -A`；不提交构建产物。
- 仓库根 `.gitignore` 是白名单式（`*` 后 `!/apps/**` 反选）——
  在 `apps/**` 下装依赖前必须先建嵌套 `.gitignore` 并用 `git check-ignore -v` 验证生效。
- 未经明确要求不提交、不推送；破坏性操作先确认。

**Known Issues / Blockers**

1. **品牌字样未清理（待裁决）** —— 迁入的 24 个文件里仍有 `Chaosplus` / `chaosplus`。
   **逐文件逐行清单见第九节**，定名后照单执行即可。分三类，处理方式不同：
   - **纯 UI 文案**（登录页/侧边栏/页脚的 "Chaosplus IAM"、"Chaosplus · © 2026" 等，7 个页面）——
     改名安全，但属于品牌决策，等 @gankcode 定新名。
   - **有行为影响**：`localStorage` 键 `chaosplus.tenant`、下载文件名兜底 `chaosplus-audit.ndjson`
     和 `chaosplus-recovery-codes.txt`、service-account 默认 scope `chaosplus-api`、
     audit 脚本的环境变量 `CHAOSPLUS_BROWSER_PASSWORD`。改了会变运行时行为，**未擅自改**。
   - **已修正**：测试里的 `chaosplus.audit-export.v1` / `chaosplus-audit-*.ndjson` 断言 ——
     7link 后端实际发的是 `audit-export.v1` 和 `audit-*.ndjson`（见
     `internal/iam/audit/audit.go:372`、`http.go:88`），旧断言编码了错误的后端认知，已改。
2. **后端 Phase 10.1 / 10.2 —— 归 @Claude（gankcode 2026-09-04 裁决）。**
   @Claude 已确认菜单初始化不是独立项，而是 10.2 建租户事务的第 3 步，会一起解决。
   未落地前，页面代码全部就位但登录与侧边栏无法联调。

- 商城 9 个重叠模块归属未裁决（Q6），不裁决则 Phase E 返工。
- 过期克隆 `aspeed-framework/7link-platform` 是否删除待授权（Q7）。

**Next Action**

等 @Claude 的后端 10.1 / 10.2 落地（他确认菜单初始化并入 10.2 的建租户事务），
然后：空库 → bootstrap 平台管理员 → 建租户 → 起 `bun dev`（端口 8091，`/api` 代理到 127.0.0.1:18080）
→ 依次跑 `apps/iam/scripts/*-browser-audit.mjs` 13 个脚本 → 修实际渲染/交互缺陷 → 进 Phase D。

在此之前可并行处理的：品牌字样清理（清单见第九节，等 @gankcode 裁决新名）。
第九节 9.3 已按 @Claude 的澄清更正：`admin@chaosplus.local` 是脚本自己的 fixture，
后端按设计不会有 bootstrap 账号常量，所以**改名不阻塞**；仍然阻塞的是让脚本自己
把管理员建出来（依赖尚未实现的 `7link-api iam bootstrap-platform-admin`）。见 9.3.1。

---

## 一、事实基线（已实测）

### 1.1 四个代码库的位置与角色

| 代码库 | 路径                                              | 角色             | 实测状态                                                                                                |
| ------ | ------------------------------------------------- | ---------------- | ------------------------------------------------------------------------------------------------------- |
| 旧后端 | `D:\Projects\Chaosplus\chaosplus-api\apps\server` | IAM 域的迁移源   | Go，`internal/modules/{audit,authn,federation,governance,iam,identity,oauth,organization,provisioning}` |
| 旧前端 | `D:\Projects\Chaosplus\chaosplus-api\apps\admin`  | **本次迁移源**   | Bun + Turborepo；`apps/iam`（业务）+ `packages/ui`（组件库）                                            |
| 目标   | `D:\Projects\SevenLink\mos\7link-platform`        | **当前工作项目** | 分支 `IAM`，`apps/backend-api` 已完成 IAM 后端迁移；`apps/frontend-web-admin` **是空目录**              |
| 商城   | `D:\Projects\SevenLink\mos\aspeed-framework\apps` | 最终迁移目标     | `7link-mall-api`（Go，46 模块 ~10.1 万行生产代码）+ `7link-mall-web`（~1.9 万行 TS）                    |

### 1.2 旧前端 IAM 应用的规模

`apps/admin/apps/iam`（package name `sys`）：

- **24 个页面**，`src/app/<domain>/page.tsx` 结构，合计约 1.24 万行
- 最大的 5 个：`entities`(1405) `security`(1032) `roles`(1004) `service-accounts`(923) `scim-directories`(829)
- **`src/lib/iam-api.ts` 1711 行**，是唯一的 API 客户端层，配套 1742 行测试
- 11 个业务组件（`app-sidebar` / `auth-provider` / `directory-members-dialog` 等，约 813 行）
- 技术栈：React 19 + react-router 8 + Vite 8 + Tailwind 4 + `@workspace/ui`
- 14 个 `scripts/*-browser-audit.mjs` 浏览器审计脚本（每个业务域一个）

### 1.3 后端契约现状 —— 最关键的一条

7link `backend-api` 的 IAM 后端迁移**已经完成**（见 `apps/backend-api/docs/iam-domain-migration-plan.md` v10：9 模块 / 63 表 / 208 条 HTTP operation，三引擎门禁全绿）。但 v9 版本**主动把 RPC 风格路由改成了 RESTful 目标契约**。

实测比对（旧前端实际调用的路由 vs 新后端非测试代码注册的路由）：

- `/iam/*` 与 `/oauth/*`：**全部命中**，契约兼容
- `/authn/*`：**18 条全部落空**，无一命中

| 旧前端调用                                  | 新后端对应                                    |
| ------------------------------------------- | --------------------------------------------- |
| `POST /authn/login`                         | `POST /authn/sessions`                        |
| `POST /authn/login/mfa`                     | `POST /authn/session-verifications`           |
| `POST /authn/logout`                        | `DELETE /authn/sessions/current`              |
| `POST /authn/logout-all`                    | `DELETE /authn/sessions`                      |
| `GET /authn/session`                        | `GET /authn/me`                               |
| `POST /authn/register`                      | `POST /authn/registrations`                   |
| `POST /authn/mfa/totp/enroll`               | `POST /authn/mfa/totp-enrollments`            |
| `POST /authn/mfa/totp/confirm`              | `POST /authn/mfa/totp`（推测，需逐条核）      |
| `POST /authn/mfa/recovery-codes/regenerate` | `POST /authn/mfa/recovery-codes`              |
| `POST /authn/passkey/login/options`         | `POST /authn/passkey-session-challenges`      |
| `POST /authn/passkey/login/verify`          | `POST /authn/passkey-session-verifications`   |
| `POST /authn/passkeys/registration/options` | `POST /authn/passkey-registration-challenges` |
| `POST /authn/passkeys/registration/verify`  | `POST /authn/passkeys`                        |
| `POST /authn/email/verification/start`      | `POST /authn/email-verification-requests`     |
| `POST /authn/email/verification/complete`   | `POST /authn/email-verifications`             |
| `POST /authn/password/recovery/start`       | `POST /authn/password-recovery-requests`      |
| `POST /authn/password/recovery/complete`    | `POST /authn/password-recoveries`             |
| `POST /authn/password/change`               | `PUT /authn/password`（推测，需逐条核）       |

新后端另有旧前端**没有**的能力：`/authn/captchas`、`/authn/step-up-challenges`、`/authn/step-up-verifications`。

> **结论：`iam-api.ts` 不能整体平移**，authn 部分必须按新契约重写；method 和请求/响应体也可能同步变了，上表右列必须以运行中的 OpenAPI 为准逐条冻结，不能照抄本表推测项。

### 1.4 两个 `packages/ui` 实际是同一套组件库

`chaosplus-api/apps/admin/packages/ui` 与 `aspeed-framework/apps/7link-mall-web/packages/ui`：

- 都是 145 个源文件、42 个组件，**文件名 100% 重合**
- 内容差异经归一化验证后，**只是格式**：引号风格（双 vs 单）、分号、`prettier-plugin-tailwindcss` 的 class 排序
- 依赖同为 `@base-ui/react` + shadcn 体系 + tiptap + codemirror + leaflet
- 含 `auto-form` / `auto-table` 两套表单表格生成器，是 IAM 与商城页面共同的骨架

> **结论：7link 只需要建立一份 `packages/ui`**，同时服务本次 IAM 后台和后续商城迁移。这是本方案最大的复用点。
> 旧前端 `apps/iam` 的 `vite.config.ts` 注释显示它本身就是从 `apps/web` 根迁出来的 —— 即从商城前端抽离 IAM 页面这条路径**已经被走通过一次**，本次是同一手法的第二次执行。

### 1.5 商城业务与 IAM 的能力重叠

`7link-mall-api` 的 46 个模块里，这些与已迁入的 IAM 域**语义重叠**：

`auth` `user` `role` `permission` `tenant` `dept` `operator` `operatortenant` `menu` `fieldperm`

其中 **`fieldperm`（字段权限）正是 7link IAM 的 Phase 10.5 空缺**（后端计划记录：字段权限"没有 schema、catalog、管理 API、repository projection、masking 或 response serialization filter"）。商城侧已有实现，可作为该缺口的设计输入。

### 1.6 商城前端规模

`7link-mall-web/apps/web`：约 1.9 万行，按路由分组：

| 分组  | 页面数 | 说明                                                                           |
| ----- | ------ | ------------------------------------------------------------------------------ |
| `mch` | 86     | 商户端，`[scope]/[scopeId]` 动态作用域                                         |
| `sys` | 29     | 平台系统管理（含 users / menus / dicts / audit-log / settings —— 与 IAM 重叠） |
| `ops` | 9      | 运营端（tenants / plans）                                                      |

---

## 二、关键风险与陷阱

1. **`apps/frontend-web-admin` 是空目录**（2026-09-04 18:51 创建）。前端脚手架、包管理器、构建工具、目录规约**全部未定**，`CLAUDE.md` 26KB 里没有任何前端章节 —— 这是需要先拍板的空白，不是可以照抄的既定事实。

2. **`aspeed-framework/7link-platform/` 是一个过期克隆**。它与工作目录同源（`codeup.aliyun.com/gankcode/gankcloud/7link-platform.git`），但停在 `main` 分支的 initial commit（`15c7b68`，只有 health 模块）。**不要在那里工作**，容易误改。

3. **`IAM` 分支当前工作区不干净**：`apps/backend-api` 下有未提交的修改与文件重命名（README、config、moduleconfig、docs 重组等）。开新分支前必须先收口。

4. **IAM 后端"迁移完成"≠"可用"**。Phase 10 有两条 Critical 未做：
   - 10.1 空库无法建立第一个平台管理员
   - 10.2 新建租户不写第一个 member/Administrator，导致所有 tenant 路由被 membership guard 挡死

   **前端登录页做完也无法真正登录**。这两条是前端联调的硬前置。

5. **菜单权限初始化缺失**（Phase 10 记录：新租户未写 `DefaultMenus()`，`EffectiveMenus()` 结果为空）。旧前端 `app-sidebar` 依赖 `/iam/me/menus` 动态渲染菜单 —— 直接迁过来会得到一个空侧边栏。

6. **Docker 环境地址不一致**：仓库 `.claude/decisions.md` 记录的是 `root@10.0.0.100`（Debian，Docker 26.1.5），本机已有可用密钥 `~/.ssh/iam-docker-10.0.0.100`，实测免密可登录。今天给到的 `root@10.0.0.1` 是**另一台主机**（同样在线、SSH 开放，但拒绝该密钥）。需要确认哪台是本次基准。

---

## 三、目标形态

```
7link-platform/
├── apps/
│   ├── backend-api/              # 已存在，Go
│   └── frontend-web-admin/       # 本次建立，Bun + Turborepo
│       ├── apps/
│       │   ├── iam/              # 阶段 C 迁入
│       │   └── mall/             # 阶段 E 迁入（mall 分支）
│       ├── packages/
│       │   └── ui/               # 单一共享组件库
│       ├── package.json
│       └── turbo.json
```

复用旧 `apps/admin` 的 workspace 布局（`apps/*` + `packages/*`、bun 1.3、turbo 2.9），因为 IAM 与商城两侧前端都已按这个形状组织，改形状只会增加迁移摩擦。

---

## 四、阶段拆分

依赖关系：**A → B → C → D**，其中 D 完成后才开 `mall` 分支进入 E。B 与 C 之间可部分并行（C 的页面搬运不依赖 B 的 authn 重写，但联调依赖）。

### Phase A —— 前端骨架与规约（无业务代码）—— **已完成（2026-09-04）**

- [x] A.2 `apps/frontend-web-admin` workspace 骨架：`package.json`（bun 1.3.12 / workspaces
      `apps/*` + `packages/*`）、`turbo.json`（5 条 task）、`tsconfig.json`、`.prettierrc`、
      `.prettierignore`、`.gitignore`
- [x] A.3 迁入 `packages/ui`：160 个文件，包名与 `exports` 保持 `@workspace/ui` 不变，**未做任何
      功能或格式改动**（保持与迁移源逐字一致，避免给后续 24 个页面的迁移制造 diff 噪声）
- [x] A.4 校验命令：`build` / `lint` / `format` / `typecheck` / `test` 五条经 turbo 统一编排
- [ ] A.1 前端规约写入 `CLAUDE.md` —— **未做，待确认**（见 §五 遗留项）

**实测验收证据：**

| 命令                | 结果                                                                    |
| ------------------- | ----------------------------------------------------------------------- |
| `bun install`       | 1221 packages installed                                                 |
| `bun run typecheck` | PASS（`tsc --noEmit`，1 package）                                       |
| `bun run lint`      | PASS（eslint，0 error）                                                 |
| `bun run test`      | **85 pass / 0 fail**，157 expect calls，6 文件                          |
| `bun run build`     | 无 task —— `@workspace/ui` 是源码包，无 build 脚本；首个 app 迁入后生效 |
| `git add -n`        | 167 个文件待跟踪，无 `node_modules` / `.turbo` / `dist` 混入            |

> **踩到的坑（已处理）：** 仓库根 `.gitignore` 是白名单式（`*` 全忽略后 `!/apps/**` 反选），
> 因此 `apps/**` 下的 `node_modules` **不会**被默认忽略。已在 `apps/frontend-web-admin/.gitignore`
> 补嵌套规则并用 `git check-ignore -v` 验证生效**后**才执行 `bun install`。

### Phase B —— API 客户端层重建（本阶段的核心工作量）

- [x] B.1 ~~从运行中的 backend-api 导出 OpenAPI~~ —— **不需要**。后端已把映射冻结在
      `internal/app/testdata/iam_http_mappings.txt`（HEAD 208 条 / 工作区 211 条，见 §八版本注意），
      并由 `bootstrap_iam_openapi_test.go` 的 `iamHTTPMappingCount` 守住。直接用该表，
      比起服务导出更权威且可复算。
- [x] B.2 映射表已冻结，见 §八。144 个调用点 100% 解析，0 悬空，0 推测项。
      实质变化 20 条（`/authn/*` 18 + `/iam/principals` 2），其余 124 条原样可用。
- [x] B.3 已重写：18 条 authn 路径 + `disablePrincipal`/`restorePrincipal` 改为 suspension
      子资源的 `PUT`/`DELETE`。函数名与响应类型保持不变，调用方零改动。
- [x] B.4 测试已按新契约修订（9 处断言 + stub 服务器的分发逻辑；原先靠
      `path.includes("confirm"|"enroll")` 猜路径，新契约下会误判，改成 method+path 精确匹配）。
      另修正 audit 导出的 schema/文件名断言 —— 旧断言写的是 `chaosplus.audit-export.v1`，
      7link 后端实际发 `audit-export.v1`。
- [ ] B.5 OpenAPI codegen（D16）—— **建议不做**。路由映射已由冻结表解决；类型侧当前手写
      且 `tsc` 全绿，引入 codegen 会带来生成步骤与指纹门禁的长期维护成本，收益不明确。
      留待 Phase D 复盘时定。
- 验收：**已达成** —— 映射表覆盖 100% 调用点；改写后重新提取 142 个调用点与后端 operation
  集合求差，140 条机器命中 + 2 条人工核对，零落空；`bun run test` 119 pass / 0 fail。

### Phase C —— IAM 页面迁移

按依赖与风险从低到高分批，每批独立可验收：

| 批次 | 内容                                                                                  | 页面数 |
| ---- | ------------------------------------------------------------------------------------- | ------ |
| C.1  | 骨架：`layout` / `router` / `auth-provider` / `app-sidebar` / `theme` / `page-header` | —      |
| C.2  | 认证入口：`login` `register` `recover` `verify-email` `accept-invitation`             | 5      |
| C.3  | 组织：`tenants` `departments` `positions` `groups` `principals`                       | 5      |
| C.4  | 授权：`roles` `entities` `menus`                                                      | 3      |
| C.5  | 治理：`access-requests` `access-reviews` `audit-events` `audit-governance`            | 4      |
| C.6  | 集成：`oauth-clients` `service-accounts` `scim-directories` `invitations`             | 4      |
| C.7  | 其余：`security` `dashboard`                                                          | 2      |

**实际执行（2026-09-04）：代码迁移未分批。** Phase B 证明契约漂移只有 20 条且全部收在
`lib/iam-api.ts` 一个文件里，页面本身是纯复制、不接触 HTTP 契约，分 7 批复制只增加仪式、
不降低风险。因此一次性迁入 24 个页面 + `layout.tsx` + 11 个 components + 13 个 audit 脚本，
用 `typecheck` / `lint` / `test` / `build` 四道门禁一次性验证（全绿，见 §零）。

**分批的价值保留在验收侧**：13 个 `scripts/*-browser-audit.mjs` 仍按上表分组跑 ——
那才是真正验证渲染与交互的地方，而它依赖真实后端，**当前阻塞于 Phase 10.1 / 10.2**。

- 验收：24 个页面全部可渲染、可交互，浏览器审计脚本通过 —— **未达成，阻塞中**。
  当前只能声明：代码全部迁入，类型/lint/单测/生产构建四项全绿。

### Phase D —— IAM 收口

- D.1 联调阻塞项确认：Phase 10.1 / 10.2 / 菜单初始化是否已由后端补齐（**不是前端能绕过的**）
- D.2 端到端：空库 → bootstrap 平台管理员 → 建租户 → 建角色/成员 → 登录 → 菜单渲染 → 各业务页面 CRUD
- D.3 更新 `.claude/task-state.md` 与 `decisions.md`
- D.4 `IAM` 分支合并收口
- 验收：从全新空库走通完整链路

### Phase E —— 商城迁移（`mall` 分支，D 完成后开启）

先做方案，不在本文展开细节。必须先解决的**结构性问题**：

- E.0 **能力重叠裁决**：商城的 `auth/user/role/permission/tenant/dept/operator/menu/fieldperm` 9 个模块，逐个决定「废弃改用 IAM」/「保留为商城私有」/「合并进 IAM」。**这个裁决不做完，后面全部返工。**
- E.1 商城 API（46 模块 / 10.1 万行）按 7link 的 `pkg/module` 规约迁移，参照后端 IAM 迁移计划 v10 的做法（冻结 source manifest + operation 集合门禁）
- E.2 商城前端 124 个页面（mch 86 / sys 29 / ops 9）迁入 `apps/frontend-web-admin/apps/mall`；`sys` 分组与已迁 IAM 页面重叠，需先去重
- E.3 `fieldperm` 可反向补齐 IAM 的 Phase 10.5 空缺

---

## 五、决策点（2026-09-04 已裁决）

> 编号用 `Q` 前缀，避免与 `.claude/decisions.md` 的 `D1–D16` 混淆。
> 用户 2026-09-04 裁决：Q5 确认为 `10.0.0.100`（`10.0.0.1` 是笔误），其余"按推荐的最优方案"执行，
> 要求标准、通用、优雅、易维护。已落为 `.claude/decisions.md` 的 **D15**（workspace + UI 库）
> 与 **D16**（API 客户端按新契约重建）。

| #   | 决策                                               | 裁决结果                                                  | 状态                                         |
| --- | -------------------------------------------------- | --------------------------------------------------------- | -------------------------------------------- |
| Q1  | Phase 10.1/10.2 谁来做、什么时候做                 | 后端优先补齐，与 Phase A/B 并行                           | **仍需指派执行人**                           |
| Q2  | `frontend-web-admin` 是 workspace 还是单应用       | workspace（bun + turborepo）                              | 已定 → D15，Phase A 已实施                   |
| Q3  | API 客户端手写还是 OpenAPI codegen                 | codegen 生成类型 + 手写薄封装                             | 已定 → D16，未实施                           |
| Q4  | UI 库基线取哪一份                                  | 取 chaosplus 版                                           | 已定 → D15，Phase A 已实施                   |
| Q5  | Docker 基准主机                                    | **`10.0.0.100`**（与仓库 `.claude/decisions.md` D6 一致） | 已定                                         |
| Q6  | 商城 9 个重叠模块的归属（E.0）                     | 未定                                                      | Phase E 启动前必须先定                       |
| Q7  | `aspeed-framework/7link-platform` 过期克隆如何处置 | 未定                                                      | 建议删除或标记；删除属破坏性操作，需明确授权 |

### 仍然阻塞的两项

- **Q1** —— 没有指派执行人。Phase C 的页面可以迁，但登录联调走不通。
- **`.claude/task-state.md` 的归属** —— 该文件当前记录的是后端 IAM 审计任务（Current Phase
  ACCEPTANCE，条目全部勾完）。`CLAUDE.md` §7 规定 task-state 是单一当前任务状态文件且不应增加状态
  文件，因此前端迁移任务无法在不覆盖后端记录的前提下写入。**需要确认是转交给前端任务，
  还是后端任务另行归档。** 在确认前未做任何修改。

---

## 六、明确不在本次范围内

- 后端 IAM 的 Phase 10 产品化缺口（10.1–10.11）—— 属于后端计划，本文只标注其对前端的阻塞关系
- 商城业务的具体迁移细节 —— Phase E 启动时单独出方案
- 旧 `chaosplus-api` 的任何改动 —— 只读引用，不修改
- 数据迁移 —— 后端计划已确认目标库为全新空库，不迁存量数据

---

## 七、自审

1. **事实与推测已分离**：§1.3 映射表右列标注了 2 处推测项，要求以运行中 OpenAPI 复核；UI 库"实质相同"的结论经过引号/分号/class 排序三轮归一化验证。
2. **未验证项**：新后端各 operation 的请求/响应体结构未逐条比对（只比对了路径集合），这是 Phase B.2 的工作量主体，当前无法预估精确规模。
3. **未展开项**：商城 46 个后端模块与 124 个前端页面只做了规模统计，未做依赖分析 —— 属 Phase E 范围。
4. **本方案本身未经评审**，D1–D7 未决之前不应开始 Phase A。

## 八、Phase B 冻结映射表（2026-09-04 完成）

### B.1 推导方式（可复算，非推测）

后端已经把「旧 operation → 新 operation」冻结在仓库里，不需要起服务导 OpenAPI：

- `apps/backend-api/internal/app/testdata/iam_http_mappings.txt` —— 格式 `source => target|METHOD|path`。
- `apps/backend-api/internal/app/bootstrap_iam_openapi_test.go` —— `const iamHTTPMappingCount`，该表被测试守住，改了会红。

> **版本注意：这张表是活的。** `HEAD` 上是 **208** 条（`iamHTTPMappingCount = 208`）；
> 当前工作区是 **211** 条，多出的 3 条 `oauth-consent-page|request|decision` 是 @Claude 当日
> 新增的 OAuth 同意页，尚未提交。引用条数时必须注明版本。本节结论基于工作区（211）推导，
> 但对 144 个调用点的映射结果与版本无关 —— 那 3 条是新增接口，旧前端不可能调用过。

本节的表 = 三段 join，每段都来自仓库事实：

1. 旧前端调用点：`chaosplus-api/apps/admin/apps/iam/src/lib/iam-api.ts` 第 905 行以后，提取 `(method, path)`，共 **144 处**。
2. 旧后端注册：`chaosplus-api/apps/server/internal/modules/**/*.go` 的 `OperationID / Method / Path`，按 `(method, path)` 反查 source operation。
3. 冻结表：source operation → target operation + 新 method + 新 path。

路径参数名（`{id}` vs `{role_id}`）不参与比较——前端是插值，参数改名对前端无影响。

### B.2 结论

| 项                                               | 数                       |
| ------------------------------------------------ | ------------------------ |
| 旧 `iam-api.ts` 调用点                           | 144                      |
| 其中 **契约有实质变化**（method 或路径结构变了） | **20**                   |
| 其中原样可用                                     | 124                      |
| 涉及的 source operation（去重）                  | 141                      |
| 后端 operation 中旧前端没用到的                  | 67（HEAD）/ 70（工作区） |

### B.3 需要改的 20 条（这就是 Phase B 的全部改动面）

| 旧 method | 旧 path                                | 新 method | 新 path                                  |
| --------- | -------------------------------------- | --------- | ---------------------------------------- |
| POST      | `/authn/register`                      | POST      | `/authn/registrations`                   |
| GET       | `/authn/session`                       | GET       | `/authn/sessions/current`                |
| POST      | `/authn/login`                         | POST      | `/authn/sessions`                        |
| POST      | `/authn/login/mfa`                     | POST      | `/authn/session-verifications`           |
| POST      | `/authn/logout`                        | DELETE    | `/authn/sessions/current`                |
| POST      | `/authn/logout-all`                    | DELETE    | `/authn/sessions`                        |
| POST      | `/authn/password/change`               | PATCH     | `/authn/password`                        |
| POST      | `/authn/password/recovery/start`       | POST      | `/authn/password-recovery-requests`      |
| POST      | `/authn/password/recovery/complete`    | POST      | `/authn/password-recoveries`             |
| POST      | `/authn/email/verification/start`      | POST      | `/authn/email-verification-requests`     |
| POST      | `/authn/email/verification/complete`   | POST      | `/authn/email-verifications`             |
| POST      | `/authn/mfa/totp/enroll`               | POST      | `/authn/mfa/totp-enrollments`            |
| POST      | `/authn/mfa/totp/confirm`              | POST      | `/authn/mfa/totp`                        |
| POST      | `/authn/mfa/recovery-codes/regenerate` | PUT       | `/authn/mfa/recovery-codes`              |
| POST      | `/authn/passkeys/registration/options` | POST      | `/authn/passkey-registration-challenges` |
| POST      | `/authn/passkeys/registration/verify`  | POST      | `/authn/passkeys`                        |
| POST      | `/authn/passkey/login/options`         | POST      | `/authn/passkey-session-challenges`      |
| POST      | `/authn/passkey/login/verify`          | POST      | `/authn/passkey-session-verifications`   |
| POST      | `/iam/principals/{id}/disable`         | PUT       | `/iam/principals/{id}/suspension`        |
| POST      | `/iam/principals/{id}/restore`         | DELETE    | `/iam/principals/{id}/suspension`        |

`/authn/*` 18 条 + `/iam/principals/*` 2 条。除此之外 **没有第三处漂移**——
这一条是本阶段最重要的结论：契约漂移是**局部**的，不是全面重写。

### B.4 语义变化（不只是改路径，调用方也要跟着改）

| 变化                                                                          | 影响                                                                |
| ----------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| `POST /authn/logout` → `DELETE /authn/sessions/current`                       | 动词变了，body 要去掉                                               |
| `POST /authn/logout-all` → `DELETE /authn/sessions`                           | 同上                                                                |
| `POST /authn/password/change` → `PATCH /authn/password`                       | 是 PATCH，不是 PUT                                                  |
| `POST /authn/mfa/recovery-codes/regenerate` → `PUT /authn/mfa/recovery-codes` | 幂等语义，重试策略可以放宽                                          |
| `POST /iam/principals/{id}/disable` → `PUT .../suspension`                    | disable/restore 合成一个子资源的 PUT/DELETE                         |
| `POST /iam/principals/{id}/restore` → `DELETE .../suspension`                 | 同上，两个函数要重写成一对                                          |
| `GET /authn/session` → `GET /authn/sessions/current`                          | **不是 `/authn/me`**；`authn-me` 是另一个独立 operation，两者都还在 |

### B.5 未被旧前端使用的 operation

按当前工作区（211 条）计 70 个，按 `HEAD`（208 条）计 67 个 —— 差的 3 个是 `oauth-consent-*`。

按模块：scim 18 / federation 17 / oauth 10（含新增的 3 条同意页）/ iam 10 / provisioning 6 /
authn 4 / organization 2 / audit 2 / identity 1。

这些不是缺口，是旧前端本来就没做的功能面。Phase C 只迁旧前端已有的 24 个页面，**不补这 70 条**；
要不要补属于 IAM 收口后的独立需求，记在 Phase D。

---

## 九、品牌字样清理清单

> ### ⚠️ 2026-09-05 方案已更正 —— 本节原方案是错的
>
> **原方案：「等 @gankcode 定名，然后把 `chaosplus` 全局替换成新名字」。**
> gankcode 指出（msg `fb11a840`）：那还是硬编码，只是换了个字符串；他的规则是
> **代码里不许硬编品牌相关的东西**。他是对的，我已在 msg `76a8ff5b` 认下并撤回原方案。
>
> **更正后的方案：代码里一个品牌字面量都不留，产品名从配置来。**
>
> - 好处：他哪天叫它什么，改一个环境变量即可，不动代码、不用等任何人拍板。
> - **「等定名」这个阻塞点已撤销** —— 本节不再依赖任何人的决定。
>
> **同时更正一处我自己的分类错误**：原来 9.1/9.2/9.3 里只有 9.1 是真的"品牌"，
> 其余是**命名约定和契约对齐**问题，被我贴错了"品牌"标签（见下方 ⑵）。
>
> 规则出处：`CLAUDE.md` 里**没有**这一条（搜 硬编码/品牌/hardcode 均无命中）；
> `.claude/skills/deep-review/references/failure-patterns.md:153` 的失败模式清单里有
> 「硬编码文案残留」。已向 gankcode 提议把它补进 CLAUDE.md 并补前端章节，待他答复。
>
> **旁证（他这条规则后端也没守住，归 @Claude）**：
> `apps/backend-api/internal/iam/federation/state.go:70` 和 `:95` 把
> `chaosplus:federation:state` 硬编进了 **AES-GCM 的附加认证数据（AAD）** ——
> 那是加密参数不是文案，上生产后再改会导致所有已封装的 federation state 解不开。
> 已在 msg `76a8ff5b` 点给 @Claude。
> （`tools/iam-preflight` 里指向旧仓库路径的几处是迁移工具按设计引用旧项目，合理，不动。）
>
> 规模实测（2026-09-05）：前端 `chaosplus` 85 处，后端 Go 11 处（其中真正要紧的是上面 2 处）。

盘点范围：`apps/frontend-web-admin/apps/iam` 的 `src/` 与 `scripts/`。
注意：`index.html` 的 `<title>` 迁移时已写成 **7link IAM** —— 按更正后的方案，
**这一处同样属于要清掉的硬编码**，不是"命名先例"。

### ⑴ 真品牌：纯展示文案 —— 收进配置源，组件里不留字面量

**做法**：建一个单一来源（`VITE_APP_NAME` 环境变量 + 中性默认值，或一个
`src/config/branding.ts` 常量模块），下表所有位置改为引用它。**不需要任何人定名即可执行。**

| 文件                                 | 行        | 内容                                        |
| ------------------------------------ | --------- | ------------------------------------------- |
| `src/app/layout.tsx`                 | 104, 109  | `aria-label` / 侧边栏标题 `Chaosplus IAM`   |
| `src/app/login/page.tsx`             | 140 / 125 | `Chaosplus IAM` / 页脚 `Chaosplus · © 2026` |
| `src/app/register/page.tsx`          | 54 / 129  | 同上                                        |
| `src/app/recover/page.tsx`           | 59 / 97   | 同上                                        |
| `src/app/verify-email/page.tsx`      | 54 / 72   | 同上                                        |
| `src/app/accept-invitation/page.tsx` | 64 / 144  | 同上                                        |
| `src/app/security/page.tsx`          | 854       | 二维码 `title="Chaosplus TOTP 配置二维码"`  |
| `src/lib/iam-api.test.ts`            | 16        | `describe("Chaosplus IAM API client")`      |

### ⑵ 不是品牌，是命名约定 / 契约对齐（我原先归错类）

下面这些**跟"叫什么名字"无关**，是工程约定和后端契约问题，我自己就能定：

- `chaosplus.tenant`（localStorage 键）→ 存储命名空间，换中性 `iam.` 前缀。
- 下载文件名兜底 `chaosplus-audit.ndjson` → **跟后端实际发的对齐**（后端是 `audit-*.ndjson`、
  schema `audit-export.v1`）。这是契约对齐。
- `service-accounts/page.tsx:78` 的 `chaosplus-api` → **2026-09-05 复核：后端 0 命中，
  它只是表单默认值，不是后端契约**，清成中性占位即可。
- 脚本 fixture（`admin@chaosplus.local`、`CHAOSPLUS_*` 环境变量名、`.local/chaosplus-smoke.db`）
  → 脚本自己的测试数据，中性改名。

（原 9.2 明细表保留在下，逐条仍需按上面的口径处理）

### 9.2（原表）有行为影响 —— 改动前必须逐条确认

| 位置                                   | 值                                      | 为什么有风险                                                                                                 |
| -------------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `src/lib/iam-api.ts:727`               | `localStorage` 键 `chaosplus.tenant`    | 改了会丢已登录用户的租户选择；且 13 个 audit 脚本硬编码同一个键，必须同步改                                  |
| `src/lib/iam-api.ts:910`               | 下载兜底文件名 `chaosplus-audit.ndjson` | 仅兜底；后端正常会给 `audit-*.ndjson`                                                                        |
| `src/app/security/page.tsx:897`        | `chaosplus-recovery-codes.txt`          | 纯客户端生成的下载名                                                                                         |
| `src/app/oauth-clients/page.tsx:742`   | `chaosplus-oauth-${clientID}.txt`       | 同上                                                                                                         |
| `src/app/service-accounts/page.tsx:78` | 默认 scope `chaosplus-api`              | **后端 scope 校验可能认这个字面量**，改名前要跟后端对齐                                                      |
| `src/lib/iam-api.test.ts:376`          | `otpauth://totp/Chaosplus:user`         | 只是测试夹具。**真正的 TOTP issuer 由后端生成**，前端改这里不影响验证器 App 的显示名；要改 issuer 得改后端。 |

### 9.3 审计脚本的账号 fixture —— 已由 @Claude 澄清（2026-09-04）

| 位置                                                                | 值                                    | 说明                                                                                                                                                                                                                                                                    |
| ------------------------------------------------------------------- | ------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 13 个 `scripts/*-browser-audit.mjs`                                 | 登录账号 `admin@chaosplus.local`      | **我原先的判断（「必须等于后端创建的账号」）方向反了。** @Claude 澄清：10.1 按设计**永远不会有** bootstrap 账号常量 —— 后端计划 §2.5.1 规定 login/display-name/email 全是无默认值的运维参数，且禁止固定默认口令。所以这个值是**脚本自己的 fixture**，改名不受后端约束。 |
| 同上                                                                | 环境变量 `CHAOSPLUS_BROWSER_PASSWORD` | 脚本自己的输入契约，改名安全，但 13 个文件要一起改                                                                                                                                                                                                                      |
| `scripts/position-browser-audit.mjs:4`、`group-browser-audit.mjs:1` | `CHAOSPLUS_DIRECTORY_KIND`            | 同上                                                                                                                                                                                                                                                                    |
| `scripts/registration-browser-audit.mjs:11`                         | `.local/chaosplus-smoke.db`           | 冒烟库路径，仅本地                                                                                                                                                                                                                                                      |
| 若干脚本内联口令                                                    | `Chaosplus-Access-Approver-2026!` 等  | 脚本自建测试账号用的口令字面量，随脚本自洽，改不改都行                                                                                                                                                                                                                  |

### 9.3.1 但「改名」和「脚本能跑」是两件事

改名不阻塞，**脚本自备账号这件事仍然阻塞**，区分清楚：

- **改名（把 fixture 提成变量、换成新域名）** —— 现在就能做，只等 @gankcode 定名。
- **让脚本自己把管理员建出来** —— 还做不了。命令 `7link-api iam bootstrap-platform-admin`
  只存在于后端计划 §2.5.1（`docs/iam-domain-migration-plan.md:307`），
  **代码里搜 `bootstrap-platform-admin` / `BootstrapPlatformAdmin` 是 0 命中**，尚未实现。
  现状是 13 个脚本直接 `login(...)`，**假定账号已存在**，没有任何自举步骤。
- **`authn.EnsureBootstrapPrincipal` 不是捷径。** 它确实存在
  （`internal/iam/authn/web.go:1136`），但只能从 Go 测试里调，浏览器脚本够不着；
  而且它只写 principal + credential，**不写 `iam_platform_administrators` 行**，
  这样建出来的账号过不了 platform admin guard。

结论：9.3 拆成 **9.3a 改名（不阻塞）** 和 **9.3b 脚本自举（等 10.1）**。

### 9.4 执行方式

定名后执行，分四次提交：9.1 纯文案 / 9.2 有行为影响 / 9.3a 脚本 fixture 改名 /
9.3b 脚本自举步骤。每次跑全量门禁。前三批不依赖后端，只有 9.3b 等 10.1。

9.3b 落地时按 §2.5.1 的约定写：密码走 `--password-file`（不用命令行明文），
并依赖命令区分 created / already bootstrapped 的退出码来做重跑幂等。
账号的 login 与 email 是两个字段，脚本统一用「邮箱当登录名」一种口径，
和现有 `#login-name` 输入保持一致。

## 十、按 OpenAPI 生成的类型与漂移闸口（2026-09-05）

§1.3 说「必须以运行中的 OpenAPI 为准逐条冻结」。冻结只在冻结那一刻成立，
后端之后改一个字段，前端仍然编译通过、跑起来才空白 —— 只有 browser audit 会发现。
本节把这件事接到 `tsc` 上。

### 10.1 为什么不是「用生成的类型替换手写类型」

先试过直接把 `iam-api.ts` 的域类型换成生成类型，结论是**不能换**：

| 字段                     | 手写客户端               | OpenAPI 文档     |
| ------------------------ | ------------------------ | ---------------- |
| `Department.status`      | `"active" \| "disabled"` | `string`         |
| `Department.id`          | `string`                 | `string \| null` |
| `AccessRequest.id` 等    | `string`                 | `string \| null` |

文档里 294 个 schema 共 1309 个字段，其中 **162 个带 `null`**，绝大多数是 Go 侧
指针/`omitempty` 的建模痕迹，服务端并不会真的返回 null 的 id。换成生成类型等于
用「手写类型更精确的字面量联合」去换「一堆永远不会触发的 null 分支」，是净亏。

### 10.2 实际做法：生成类型 + 双向字段名比对

- `bun run generate:api-types` → `src/lib/api-schema.ts`（生成物，入库，
  `.prettierignore` 已排除，勿手改）。
- `src/lib/api-contract.ts` 把 51 个手写响应类型与文档 schema 逐一配对，
  在类型层面做**双向字段名**比对：客户端多出来的字段、文档里必填而客户端没有的字段，
  都会让 `tsc` 失败，且报错信息直接点名是哪个字段。
- **只比字段名，不比字段类型** —— 理由就是 10.1，两边的类型是故意不一样的。

配对不是猜名字得来的：从 `iam-api.ts` 解析出 136 个调用点，按 `(method, path)`
落到文档 operation 上，再顺着响应 envelope 的 `data` 取内层 schema。
名字相同的只有 22 个，其余是 `Role → APIRole`、`Session → MeOutput`、
`Permission → Action`、`AuditEvent → Event` 这种，靠名字猜会错
（例如按字段集相似度猜，`ServiceAccountList` 会错配到 `PrincipalListData`）。

### 10.3 首次运行查出的三处真实漂移（已修）

| 类型          | 文档必填而客户端缺失   |
| ------------- | ---------------------- |
| `Session`     | `acr`（另补了可选的 `amr` / `auth_time`） |
| `Principal`   | `activation_required`  |
| `OAuthClient` | `consent_mode`         |

其余 48 个类型双向一致 —— 也就是说手写客户端本身是准的，缺的只是**持续**的比对。

### 10.4 遗留

- **生成的输入源是运行中的服务，不是入库文件。** 仓库里既没有 spec 文件，也没有
  离线导出命令，所以 `api-schema.ts` 目前是拿 `--spec http://localhost:8091/api/openapi.json`
  生成的，CI 复算不了。@Claude 计划加 `7link-api openapi --out apps/backend-api/docs/openapi.json`；
  该文件落地后，`bun run generate:api-types` 不带参数就会读它（已是默认路径），
  届时须重新生成一次并复核本节的三处修正。
- **只覆盖响应类型。** 请求体类型（`*Input` / `*Update`）尚未纳入比对，
  它们写错的表现是运行期 422，目前由 browser audit 兜住。
- **`OAuthClientInput` 没有 `consent_mode`。** 文档里它是可选的（默认 `implicit`），
  所以比对不报错，但也意味着前端建出来的 client 一律是隐式同意。要不要给 UI
  加这个开关是产品决定，不是漂移。
