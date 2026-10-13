# 动态字段规则：前端应用完整影响登记（2026-10-13）

## 范围与口径

这是设计附件和待实施验收台账，**不是已迁移完成报告**。只修改本文件；生产代码未改。配套主设计为 [dynamic-field-rules-design-2026-10-13.md](dynamic-field-rules-design-2026-10-13.md)。

全量结构扫描 `apps/frontend-web/apps/web/src` 下 **96 个非测试、非生成 TS/TSX**；以 TypeScript AST 同时提取 form、输入组件、iamApi 引用（包括传递为函数值）、api alias 调用和原始 fetch。识别 **48 个原生 form 节点 / 33 个 TSX 文件**；扩展至 **54 个有 form、输入、API调用/引用或直接transport委托的 UI TSX 文件**。每个 form 节点不等于单一业务流程：登录4节点、安全5节点、共享dialog与条件分支必须展开。

结构清单完整性已核对；运行时语义尚未验证。以下每项状态均为 **IDENTIFIED / NOT IMPLEMENTED / NOT RUNTIME-VERIFIED**，没有用 unknown/server_only 将现有前端迁移任务视为完成。后端 tag/handler/service 校验逐字段语义由后端影响附件负责，不能由本清单推断已一致。

## 所有项共同适用的接入与验收合同

- **UI-A**：共享 API 表单边界一次声明 method/URL/content-type/最终载荷构造；name 或 requestPath 自动绑定控件。state-only、复合/数组控件须显式映射，不在页面逐字段重新写 rules。
- **UI-B**：校验发送的同一最终快照，JSON 的 undefined/null/数组/非有限数语义一致；PATCH 不填默认值。method/目标切换重取正确契约，保留原权限决策。
- **UI-C**：删除重复 API required/length/range/pattern/enum/跨字段约束；仅保留明确纯交互确认、文件解码保护、导航状态。原生 form 使用 noValidate，Unicode/IME/中间输入/粘贴/撤销共用适配器。
- **UI-D**：字段及请求对象/数组规则、隐藏path/query/header值全部校验；日期时区、unsafe整数、精度与空值按后台契约。
- **UI-E**：服务端 structured issues 回填字段；无法映射的隐藏/协议错误全局显示；已有业务错误 data 和总体提示保留，字段变化清除对应错误、过期异步返回丢弃。
- **UI-F**：zh-CN/en-US/ms-MY 全部规则、业务字段错误、语言切换均验证；规则消息可重译，不缓存已翻译字符串或只回显key。
- **UI-G**：metadata故障/断网/禁用/unknown、会话及账号视图切换、后端规则变更前端不重建，均保留输入；不新增metadata/校验驱动的写重放，保留既有UI客户端401刷新后一次重试，不扩大或取消其传输政策；原401/403及账号视图关闭处理优先。
- **UI-H**：每个流程真实成功/失败与条件分支回归；现有浏览器审计可复用但不能当作新增动态校验已通过。最终每个编号都需记录运行证据，不允许只关闭首批邀请/PATCH任务。

错误路径现状：通常 catch→setError((cause as Error).message)→Alert；少数登录使用 loginError→t，账号页使用 operationError，文件页保留 conflict data。共同 transport `iam-api.ts` 中 apiError 保留 data，但没有字段 issue normalization/field binding；`lib/account-view.ts` 的直接 fetch 甚至用通用 Error 丢弃非2xx响应体。清单中的错误行是迁移位置，而非已支持字段回填的证据。

本附件使用 deep-audit 的只读证据方式；没有运行全仓构建/测试，也没有浏览器交互测试，理由是交付物为盘点文档。结构验证与路径检查命令在文末，不能替代实施验收。

## 每文件/流程处理台账

| 编号 | 文件 / form数 | 流程、转换、条件与必要动作 | 验收 |
|---|---|---|---|
| FE-001 | [app/accept-invitation/page.tsx:1](../../frontend-web/apps/web/src/app/accept-invitation/page.tsx#L1) / 1 | 接受邀请：URL token + login_name/display_name trim + password；confirmation 仅交互保留，不发送。token 必须以隐藏请求字段验证；密码字符/字节接受集合兼容，不继承登录强度。 | UI-A–H；每个下列操作分支 |
| FE-002 | [app/access-requests/page.tsx:1](../../frontend-web/apps/web/src/app/access-requests/page.tsx#L1) / 2 | 创建与审批/拒绝/撤销/撤回两类 form；roleID state→role_id，expiresAt→UTC access_expires_at，reason/note trim；四种 decision 目标分别绑定，path request.id；当前 datetime-local min 及文本长度迁移。 | UI-A–H；每个下列操作分支 |
| FE-003 | [app/access-reviews/page.tsx:1](../../frontend-web/apps/web/src/app/access-reviews/page.tsx#L1) / 2 | 创建 + action form；dueAt→UTC due_at，决策提交 item/review path + decision/note，完成/取消提交固定 status；隐藏 action 决定方法和载荷，不能按一个 form 固定一个 schema。 | UI-A–H；每个下列操作分支 |
| FE-004 | [app/account/applications.tsx:1](../../frontend-web/apps/web/src/app/account/applications.tsx#L1) / 0 | 无 form：同意授权撤销确认按钮→revokeOAuthConsent(client_id)。无输入也须校验 path；保留确认框与总体错误。 | UI-A–H；每个下列操作分支 |
| FE-005 | [app/account/closure.tsx:1](../../frontend-web/apps/web/src/app/account/closure.tsx#L1) / 0 | 无 form：关闭账号先 ReauthenticationDialog 再 submitClosureRequest(proof,version)，cancelClosureRequest(version)；sessionStorage continuation 与 proof 不显示为普通输入，不记录敏感值。 | UI-A–H；每个下列操作分支 |
| FE-006 | [app/account/identities.tsx:1](../../frontend-web/apps/web/src/app/account/identities.tsx#L1) / 1 | 选择 provider 的 form 只发起 reauth；获得 proof 后 link transaction，另有 unlink 按钮；return_url 是浏览器地址、existing link/target 来源不同；绑定两个真实提交而非只包住 begin()。 | UI-A–H；每个下列操作分支 |
| FE-007 | [app/account/memberships.tsx:1](../../frontend-web/apps/web/src/app/account/memberships.tsx#L1) / 0 | 无 form：离开租户先 reauth，再 leaveMembership(selected,proof)；租户与权限数据加载不构成写表单；continuation 恢复也必须经过同一最终请求校验。 | UI-A–H；每个下列操作分支 |
| FE-008 | [app/account/tokens.tsx:1](../../frontend-web/apps/web/src/app/account/tokens.tsx#L1) / 1 | 两阶段 preparePersonalToken→reauth→createPersonalToken：name、tenant、permissions[]、本地日期→UTC expires_at；最终 prepared.request+proof 不得重构为旧草稿。另有 revoke；过期 proof/变更 tenant 清选项需原行为回归。 | UI-A–H；每个下列操作分支 |
| FE-009 | [app/audit-events/page.tsx:1](../../frontend-web/apps/web/src/app/audit-events/page.tsx#L1) / 1 | GET 查询 form 不是纯本地过滤：event_type/principal_id/target_type/target_id trim→query，起止日期→UTC；tenant/system auditEvents 与 export 同查询。查询规则纳入；NDJSON 下载保留现有完整性校验，错误不能按 JSON 成功体解析。 | UI-A–H；每个下列操作分支 |
| FE-010 | [app/audit-governance/archives.tsx:1](../../frontend-web/apps/web/src/app/audit-governance/archives.tsx#L1) / 0 | 无form/输入但api prop调用：requestAuditArchive(scope)、retryAuditArchive(job.id,scope)、verifyAuditArchive(job.id,scope)、downloadAuditArchive(job.id,scope)；scope决定system/tenant；run统一catch→setError，全局错误必须承接path/query/header issues，下载是NDJSON。 | UI-A–H；每个下列操作分支 |
| FE-011 | [app/audit-governance/page.tsx:1](../../frontend-web/apps/web/src/app/audit-governance/page.tsx#L1) / 1 | Number(minDays/archiveDays)→min_days/archive_after_days；当前 min=1、max=36500、归档天数>=保留天数是重复/跨字段候选。scope 选择 tenant/platform 目标；另有无表单 auditSignRoot/auditAnchor。 | UI-A–H；每个下列操作分支 |
| FE-012 | [app/dashboard/page.tsx:1](../../frontend-web/apps/web/src/app/dashboard/page.tsx#L1) / 0 | 只有读统计（principals/roles/menus/sessions），无用户构造请求字段：不创建表单绑定；共享客户端错误兼容回归即可。 | UI-A–H；每个下列操作分支 |
| FE-013 | [app/departments/page.tsx:1](../../frontend-web/apps/web/src/app/departments/page.tsx#L1) / 1 | create/update 共用 form：parent root→空串、sort_order Number、status state；create 带 entity_id，PATCH 带 version。system alias 删除 entity_id 并改 endpoints，普通/运营上下文沿 organization routing；entity 选择仅新建时显示。另有版本 DELETE。 | UI-A–H；每个下列操作分支 |
| FE-014 | [app/device/page.tsx:1](../../frontend-web/apps/web/src/app/device/page.tsx#L1) / 1 | code 输入 form 走 GET deviceRequest；批准/拒绝按钮走 decideDeviceRequest(code.trim(),approve)。URL 预填也经查询；不把 lookup 的规则与 decision 写规则合并。 | UI-A–H；每个下列操作分支 |
| FE-015 | [app/entities/page.tsx:1](../../frontend-web/apps/web/src/app/entities/page.tsx#L1) / 3 | 三个 form：实体（metadata JSON.parse，拒绝null/数组/非对象）、角色绑定（role/principal/effect/optional UTC expires_at）、relationship（subject 类型决定 subject_relation、resource_id 是否输入决定 entity_id/resource_type）。trustedContextCondition 生成 version/all 嵌套条件；inspection 非 form 输入走 authorizationConstraint/explain* 请求，不能漏。关系/绑定删除保留原冲突和全局错误。 | UI-A–H；每个下列操作分支 |
| FE-016 | [app/entities/workspace.tsx:1](../../frontend-web/apps/web/src/app/entities/workspace.tsx#L1) / 0 | 只读实体摘要和权限允许的数据源；写入口委托实体页面/其他 tabs，不重复建立表单。 | UI-A–H；每个下列操作分支 |
| FE-017 | [app/field-policies/page.tsx:1](../../frontend-web/apps/web/src/app/field-policies/page.tsx#L1) / 1 | 每字段 policy editor form，嵌套 read/write default/allow_roles/deny_roles 与 mask；保存 PUT policy+version，reset DELETE version。权限目录配置不是字段校验规则，不能与本次 OPTIONS validation schema 混淆。 | UI-A–H；每个下列操作分支 |
| FE-018 | [app/files/page.tsx:1](../../frontend-web/apps/web/src/app/files/page.tsx#L1) / 0 | FileBrowser 回调 owner：创建账户/系统根、目录、空文件、上传/目录递归、rename、trash、restore、copy/move conflict decisions。fileEntityID 三态区分 account/tenant/entity，system 单独分支；revision、Idempotency-Key、observed_revision 和策略默认在最终请求验证；部分成功/冲突交互保留。 | UI-A–H；每个下列操作分支 |
| FE-019 | [app/groups/page.tsx:1](../../frontend-web/apps/web/src/app/groups/page.tsx#L1) / 1 | group create/PATCH：Number sort_order；dynamic 才含 membership_rule{version:1,match,conditions[{field,operator,values:split comma/trim/filter}]}；edit 不发送 type，带 version。static member 子表单委托 DirectoryMembersDialog（putGroupMember/deleteGroupMember），另有 role binding dialog。 | UI-A–H；每个下列操作分支 |
| FE-020 | [app/invitations/page.tsx:1](../../frontend-web/apps/web/src/app/invitations/page.tsx#L1) / 1 | create：email trim、departmentID空→undefined、roleIDs[]、expires_in_hours Number；resend/revoke 非 form。email maxlength320、hours1..720 迁移同源；数组和隐藏 path 必须验收。 | UI-A–H；每个下列操作分支 |
| FE-021 | [app/layout.tsx:1](../../frontend-web/apps/web/src/app/layout.tsx#L1) / 0 | shell 数据源/租户选择/导航读取，无独立业务写表单；会话和 workspace 切换必须使共享元数据缓存与过期响应隔离。 | UI-A–H；每个下列操作分支 |
| FE-022 | [app/login/page.tsx:1](../../frontend-web/apps/web/src/app/login/page.tsx#L1) / 4 | 四个原生 form：手机号取码、手机 code 登录、密码登录、MFA；真实调用经 AuthProvider。另有 passkey 登录、MFA短信重发、图形验证码刷新/Turnstile。保留 challenge/captcha状态、return_url、dev_tenant_id 的纯 UI/上下文选择；不能只绑定 iamApi.capabilities。 | UI-A–H；每个下列操作分支 |
| FE-023 | [app/login/workspaces.tsx:1](../../frontend-web/apps/web/src/app/login/workspaces.tsx#L1) / 1 | registerTenant 表单 trim name/slug；其余 workspace 选择是上下文/导航，不是业务 POST。tenantRegistrationPolicy 决定入口可见，不能变为 schema required。 | UI-A–H；每个下列操作分支 |
| FE-024 | [app/menus/page.tsx:1](../../frontend-web/apps/web/src/app/menus/page.tsx#L1) / 1 | menuApi alias tenant/system；translations rows过滤空label、locale trim/lowercase去重检测→translations 对象，sort_order Number、parent/status等 state；create/update/delete、树拖动排序 update 全纳入。语言重复是请求对象构造约束而非简单 Input maxlength。 | UI-A–H；每个下列操作分支 |
| FE-025 | [app/oauth-clients/page.tsx:1](../../frontend-web/apps/web/src/app/oauth-clients/page.tsx#L1) / 1 | name trim、redirectUris按行 trim/去重→redirect_uris[]、scopes按词→scopes[]、grantTypes/publicClient state；edit才含status。当前 scopes/grants 非空、authorization_code要求redirect 是跨字段服务规则候选；secret rotate/delete 按钮也纳入，secret仅一次显示保持。 | UI-A–H；每个下列操作分支 |
| FE-026 | [app/principals/closure-requests.tsx:1](../../frontend-web/apps/web/src/app/principals/closure-requests.tsx#L1) / 0 | 无 form：status select触发GET；reject/process确认按钮通过条件函数引用调用 rejectClosureRequest/processClosureRequest(principal_id,version)。不能只统计直接 CallExpression。 | UI-A–H；每个下列操作分支 |
| FE-027 | [app/principals/lifecycle-dialog.tsx:1](../../frontend-web/apps/web/src/app/principals/lifecycle-dialog.tsx#L1) / 0 | 无 form：保留期限日期强制T00:00:00Z、legal_hold/reference/version→setPrincipalRetention；anonymize confirmation/version；confirmation匹配登录名是危险操作确认，应保留，同时声明字段规则；日期含义明确保留UTC午夜。 | UI-A–H；每个下列操作分支 |
| FE-028 | [app/principals/page.tsx:1](../../frontend-web/apps/web/src/app/principals/page.tsx#L1) / 1 | create login/password/display_name/email；effectiveFields create/update 决定字段是否渲染及提交，data.has 保留缺省。edit changes 与 department-only路径分开；禁用/恢复无form。字段授权策略不等于字段约束；隐藏省略不得默认填值。 | UI-A–H；每个下列操作分支 |
| FE-029 | [app/recover/page.tsx:1](../../frontend-web/apps/web/src/app/recover/page.tsx#L1) / 1 | 同一 form 按 token 切换 startPasswordRecovery(identifier.trim(),entry) / completePasswordRecovery(token,new_password)；confirmation 纯 UI。开始与完成不同契约；URL token 为隐藏字段。 | UI-A–H；每个下列操作分支 |
| FE-030 | [app/register/page.tsx:1](../../frontend-web/apps/web/src/app/register/page.tsx#L1) / 1 | email/display_name trim，password原样；entry + captcha_type/id/answer 条件载荷。captcha refresh另有创建接口；confirmation保留纯UI。当前 minLength12 不能继续比后端字符/字节规则更严格，兼容样例必须涵盖多字节。 | UI-A–H；每个下列操作分支 |
| FE-031 | [app/roles/page.tsx:1](../../frontend-web/apps/web/src/app/roles/page.tsx#L1) / 2 | 两个 form：角色名称说明、permission condition builder；其余 permission toggle、成员增删/expiry、directory bindings、data-scope保存、删除全是按钮流。system alias endpoints与普通不同；selected_departments 才发送IDs否则[]；condition Time/zone/ACR与实体复用helper，不能只迁移两个form。 | UI-A–H；每个下列操作分支 |
| FE-032 | [app/scim-directories/page.tsx:1](../../frontend-web/apps/web/src/app/scim-directories/page.tsx#L1) / 2 | directory create/edit与credential两个form：trim name，service_account_id空→null；edit status/version；credential local datetime→UTC optional expires_at；revoke按钮。这里是JSON管理API，不是原始SCIM协议，不能整体标server_only。 | UI-A–H；每个下列操作分支 |
| FE-033 | [app/scim-directories/targets.tsx:1](../../frontend-web/apps/web/src/app/scim-directories/targets.tsx#L1) / 2 | target form + push/deprovision resource form；name/base_url trim，service_account_id空→null；edit token空则省略，status/version；create必含bearer_token。delete/retire按钮及resource_type切换资源目录全部纳入；原始令牌不出错误值。 | UI-A–H；每个下列操作分支 |
| FE-034 | [app/security/page.tsx:1](../../frontend-web/apps/web/src/app/security/page.tsx#L1) / 5 | 5处form语法实际承载手机号验证码、passkey register/rename/delete三分支、改密、MFA begin/regenerate/disable共享密码form与MFA confirm。还有revokeSession/logoutAll/startEmailVerification/startPhoneVerification。WebAuthn credential对象由浏览器产生；当前password/code/name限制和6位strip/length逻辑逐个分类迁移，不能机械改成新密码强度。 | UI-A–H；每个下列操作分支 |
| FE-035 | [app/service-accounts/page.tsx:1](../../frontend-web/apps/web/src/app/service-accounts/page.tsx#L1) / 2 | account create/edit与credential两个form：trim字符串，可选description/display_name→undefined，edit含version/status；credential scopes拆分并现有非空检查，expiresAt→UTC optional。delete携带version、revoke凭证按钮均纳入。 | UI-A–H；每个下列操作分支 |
| FE-036 | [app/system/accounts.tsx:1](../../frontend-web/apps/web/src/app/system/accounts.tsx#L1) / 1 | create/update system principal form；profile由display_name/email输入组装，create另含login/password。SystemMembershipDialog负责独立成员/角色写入，不能将其算成本form自动覆盖。 | UI-A–H；每个下列操作分支 |
| FE-037 | [app/system/layout.tsx:1](../../frontend-web/apps/web/src/app/system/layout.tsx#L1) / 0 | 只读运营/系统权限导航；页面参数选择不是业务写表单。metadata上下文和权限缓存隔离仍需要回归。 | UI-A–H；每个下列操作分支 |
| FE-038 | [app/system/operating-platforms.tsx:1](../../frontend-web/apps/web/src/app/system/operating-platforms.tsx#L1) / 1 | create slug/name/owner_principal_id；edit name/status/version不发送slug/owner；delete path+version。隐藏字段与可编辑字段映射显式保留。 | UI-A–H；每个下列操作分支 |
| FE-039 | [app/system/owner-members.tsx:1](../../frontend-web/apps/web/src/app/system/owner-members.tsx#L1) / 0 | 无原生form：owner选择读取members/departments/roles，写动作在SystemMembershipDialog且scope.kind决定平台/tenant endpoint；该入口需一并做集成验收。 | UI-A–H；每个下列操作分支 |
| FE-040 | [app/system/platform-members.tsx:1](../../frontend-web/apps/web/src/app/system/platform-members.tsx#L1) / 1 | 新增form principal_id→body.subject，另有display_name/email；状态按钮 updateMember(status)。platformID作为路径上下文，不可把DOM name principal_id直接套到/subject之外。 | UI-A–H；每个下列操作分支 |
| FE-041 | [app/tenants/page.tsx:1](../../frontend-web/apps/web/src/app/tenants/page.tsx#L1) / 1 | create slug/name+state ownerID；edit name/status/version；删除版本。includeDeleted/search/pagination传GET query，不全是纯本地过滤；搜索字段约束归共享请求边界。 | UI-A–H；每个下列操作分支 |
| FE-042 | [app/verify-email/page.tsx:1](../../frontend-web/apps/web/src/app/verify-email/page.tsx#L1) / 1 | token来自URL，password仅activation-required路径展示；completeEmailVerification(token,password\|\|undefined)。自动/重复打开、缺省密码、失效token都需表单级错误fallback。 | UI-A–H；每个下列操作分支 |
| FE-043 | [components/account-view-banner.tsx:1](../../frontend-web/apps/web/src/components/account-view-banner.tsx#L1) / 0 | 无form：退出按钮与到期timer→exitAccountView→DELETE /account-views/{id}；保持先清本地状态、撤销best-effort。既有撤销失败继续被抑制，不增加OPTIONS等待、必填校验或阻断提示；accountViewExitFailed仅保留现有外层异常路径，不将被抑制的DELETE失败转为可见错误。无可编辑字段，不要求退出执行元数据预检。 | UI-A–H；每个下列操作分支 |
| FE-044 | [components/account-view-dialog.tsx:1](../../frontend-web/apps/web/src/components/account-view-dialog.tsx#L1) / 0 | 无form：scope/mode选择→enter→issueAccountView(selection)，下游lib/account-view.ts直接fetch；GET候选iamApi.accountViewWorkspaces。organization模式隐藏scope；条件entity需要tenant。在现有transport注入共同规范化；dialog catch保留cause并回填字段/全局可重译issue。dialog打开时预取metadata，openAccountViewTab必须仍在首个await前同步window.open；不能先等待异步校验导致弹窗被阻止。不能同步完成的预检在既有开窗/握手内完成，保留原失败撤销且不重放发行请求。 | UI-A–H；每个下列操作分支 |
| FE-045 | [components/auth-provider.tsx:1](../../frontend-web/apps/web/src/components/auth-provider.tsx#L1) / 0 | login页面委托的真实入口：login/requestPhoneLoginCode/loginWithPhoneCode/verifyLoginMfa/requestMfaPhoneCode/begin+finishPasskeyLogin/logout；trim输入、captchas、return URL及credential注入发生这里。让校验和发送消费同一快照，并把issues交还原page边界。 | UI-A–H；每个下列操作分支 |
| FE-046 | [components/avatar-editor.tsx:1](../../frontend-web/apps/web/src/components/avatar-editor.tsx#L1) / 0 | 无form：style/candidate_url保存，上传MIME+2MiB+2048px本地检查→裁剪256×256 PNG data URL→setMyAvatar JSON image_data；不是multipart。将API约束归后端声明，保留合理文件解码/裁剪UI保护并清楚分类；错误当前全局Alert。 | UI-A–H；每个下列操作分支 |
| FE-047 | [components/directory-members-dialog.tsx:1](../../frontend-web/apps/web/src/components/directory-members-dialog.tsx#L1) / 1 | onPut/onDelete回调表单实际只由groups/page使用：selectedPrincipal为path，starts_at/ends_at非空→UTC→条件对象；本地ends>starts检查需同源跨字段规则；computed/static分支与原值不丢失，错误经onError回到父级。 | UI-A–H；每个下列操作分支 |
| FE-048 | [components/directory-role-bindings-dialog.tsx:1](../../frontend-web/apps/web/src/components/directory-role-bindings-dialog.tsx#L1) / 0 | 无form：角色checkbox/按钮→add/removeRoleDirectoryBinding(role.id,assigneeType,assigneeID)，主语来自父页面group；三个path字段及后端issues表单级显示，不将选择过滤当业务规则。 | UI-A–H；每个下列操作分支 |
| FE-049 | [components/file-browser.tsx:1](../../frontend-web/apps/web/src/components/file-browser.tsx#L1) / 1 | 1个editor form承载mkdir/new-empty-file/rename/restore；实际请求由files/page callbacks；还包含file input、多选删除、拖放/递归目录、粘贴/复制移动、冲突逐项decision及rename输入。name trim、空文件扩展补全属于构造；maxlength255迁移；上传、copy/move的非form边界单列绑定。 | UI-A–H；每个下列操作分支 |
| FE-050 | [components/reauthentication-dialog.tsx:1](../../frontend-web/apps/web/src/components/reauthentication-dialog.tsx#L1) / 1 | form两阶段challenge→verify；method/password/passkey/federation分支，federation使用identity transaction重定向及continuation。purpose/target/proof/credential为隐藏字段；密码原样，不能套注册强度；失败回到共享字段+全局错误。 | UI-A–H；每个下列操作分支 |
| FE-051 | [components/system-membership-dialog.tsx:1](../../frontend-web/apps/web/src/components/system-membership-dialog.tsx#L1) / 0 | 无form：search选account；save根据scope/system与已有member选择4类create/update，protected时省略status；role checkbox变更选择add/removeSystem[Owner]RoleMember。department/status虽无name需requestPath映射，异步选择切换响应隔离保留。 | UI-A–H；每个下列操作分支 |
| FE-052 | [components/user-avatar.tsx:1](../../frontend-web/apps/web/src/components/user-avatar.tsx#L1) / 0 | 头像内容GET，无用户输入/写载荷；不创建表单。blob错误回退不等于validation错误。 | UI-A–H；每个下列操作分支 |
| FE-053 | [components/user-menu.tsx:1](../../frontend-web/apps/web/src/components/user-menu.tsx#L1) / 0 | locale/theme仅本地偏好；sessionForTenant用于显示，logout经AuthProvider，退出account-view经lib/account-view；这些共享边界回归，不重复表单。 | UI-A–H；每个下列操作分支 |
| FE-054 | [components/workspace-switcher.tsx:1](../../frontend-web/apps/web/src/components/workspace-switcher.tsx#L1) / 0 | tenantFilter纯本地过滤 + workspace导航选择：排除字段校验迁移；仍要触发metadata上下文隔离，不能制造不存在的业务接口。 | UI-A–H；每个下列操作分支 |

## 48 个原生 form 节点逐一定位

回调型 onSubmit 的真实业务入口见对应 FE 条目及操作附录；同一个共享 form 可能有多种method。

| Form ID | FE | 位置 | id / onSubmit |
|---|---|---|---|
| FORM-001 | FE-001 | [app/accept-invitation/page.tsx:92](../../frontend-web/apps/web/src/app/accept-invitation/page.tsx#L92) | onSubmit={submit} |
| FORM-002 | FE-002 | [app/access-requests/page.tsx:223](../../frontend-web/apps/web/src/app/access-requests/page.tsx#L223) | id="access-request-form"; onSubmit={submitRequest} |
| FORM-003 | FE-002 | [app/access-requests/page.tsx:293](../../frontend-web/apps/web/src/app/access-requests/page.tsx#L293) | id="access-decision-form"; onSubmit={applyDecision} |
| FORM-004 | FE-003 | [app/access-reviews/page.tsx:299](../../frontend-web/apps/web/src/app/access-reviews/page.tsx#L299) | id="access-review-create"; onSubmit={createReview} |
| FORM-005 | FE-003 | [app/access-reviews/page.tsx:356](../../frontend-web/apps/web/src/app/access-reviews/page.tsx#L356) | id="access-review-action"; onSubmit={applyAction} |
| FORM-006 | FE-006 | [app/account/identities.tsx:140](../../frontend-web/apps/web/src/app/account/identities.tsx#L140) | onSubmit={(event) => { event.preventDefault() begin({ purpose: "identity_link", target: provider }) }} |
| FORM-007 | FE-008 | [app/account/tokens.tsx:299](../../frontend-web/apps/web/src/app/account/tokens.tsx#L299) | onSubmit={prepare} |
| FORM-008 | FE-009 | [app/audit-events/page.tsx:261](../../frontend-web/apps/web/src/app/audit-events/page.tsx#L261) | onSubmit={submit} |
| FORM-009 | FE-011 | [app/audit-governance/page.tsx:400](../../frontend-web/apps/web/src/app/audit-governance/page.tsx#L400) | onSubmit={(event) => void saveRetention(event)} |
| FORM-010 | FE-013 | [app/departments/page.tsx:456](../../frontend-web/apps/web/src/app/departments/page.tsx#L456) | id="department-form"; onSubmit={submit} |
| FORM-011 | FE-014 | [app/device/page.tsx:196](../../frontend-web/apps/web/src/app/device/page.tsx#L196) | onSubmit={submit} |
| FORM-012 | FE-015 | [app/entities/page.tsx:697](../../frontend-web/apps/web/src/app/entities/page.tsx#L697) | id="entity-form"; onSubmit={submit} |
| FORM-013 | FE-015 | [app/entities/page.tsx:791](../../frontend-web/apps/web/src/app/entities/page.tsx#L791) | id="entity-binding-form"; onSubmit={putBinding} |
| FORM-014 | FE-015 | [app/entities/page.tsx:913](../../frontend-web/apps/web/src/app/entities/page.tsx#L913) | id="entity-relationship-form"; onSubmit={putRelationship} |
| FORM-015 | FE-017 | [app/field-policies/page.tsx:459](../../frontend-web/apps/web/src/app/field-policies/page.tsx#L459) | onSubmit={(event) => void submit(event)} |
| FORM-016 | FE-019 | [app/groups/page.tsx:424](../../frontend-web/apps/web/src/app/groups/page.tsx#L424) | id="group-form"; onSubmit={submitGroup} |
| FORM-017 | FE-020 | [app/invitations/page.tsx:280](../../frontend-web/apps/web/src/app/invitations/page.tsx#L280) | id="invitation-form"; onSubmit={submit} |
| FORM-018 | FE-022 | [app/login/page.tsx:453](../../frontend-web/apps/web/src/app/login/page.tsx#L453) | onSubmit={onSubmit} |
| FORM-019 | FE-022 | [app/login/page.tsx:509](../../frontend-web/apps/web/src/app/login/page.tsx#L509) | onSubmit={onSubmit} |
| FORM-020 | FE-022 | [app/login/page.tsx:639](../../frontend-web/apps/web/src/app/login/page.tsx#L639) | onSubmit={onSubmit} |
| FORM-021 | FE-022 | [app/login/page.tsx:814](../../frontend-web/apps/web/src/app/login/page.tsx#L814) | onSubmit={onSubmit} |
| FORM-022 | FE-023 | [app/login/workspaces.tsx:189](../../frontend-web/apps/web/src/app/login/workspaces.tsx#L189) | onSubmit={createTenant} |
| FORM-023 | FE-024 | [app/menus/page.tsx:721](../../frontend-web/apps/web/src/app/menus/page.tsx#L721) | id="menu-form"; onSubmit={submit} |
| FORM-024 | FE-025 | [app/oauth-clients/page.tsx:592](../../frontend-web/apps/web/src/app/oauth-clients/page.tsx#L592) | id="oauth-client-form"; onSubmit={onSubmit} |
| FORM-025 | FE-028 | [app/principals/page.tsx:484](../../frontend-web/apps/web/src/app/principals/page.tsx#L484) | id="principal-form"; onSubmit={submit} |
| FORM-026 | FE-029 | [app/recover/page.tsx:85](../../frontend-web/apps/web/src/app/recover/page.tsx#L85) | onSubmit={submit} |
| FORM-027 | FE-030 | [app/register/page.tsx:153](../../frontend-web/apps/web/src/app/register/page.tsx#L153) | onSubmit={submit} |
| FORM-028 | FE-031 | [app/roles/page.tsx:1072](../../frontend-web/apps/web/src/app/roles/page.tsx#L1072) | id="role-permission-condition-form"; onSubmit={savePermissionCondition} |
| FORM-029 | FE-031 | [app/roles/page.tsx:1279](../../frontend-web/apps/web/src/app/roles/page.tsx#L1279) | id="role-form"; onSubmit={submit} |
| FORM-030 | FE-032 | [app/scim-directories/page.tsx:599](../../frontend-web/apps/web/src/app/scim-directories/page.tsx#L599) | id="scim-directory-form"; onSubmit={onSubmit} |
| FORM-031 | FE-032 | [app/scim-directories/page.tsx:721](../../frontend-web/apps/web/src/app/scim-directories/page.tsx#L721) | id="scim-credential-form"; onSubmit={onSubmit} |
| FORM-032 | FE-033 | [app/scim-directories/targets.tsx:465](../../frontend-web/apps/web/src/app/scim-directories/targets.tsx#L465) | id="scim-target-form"; onSubmit={save} |
| FORM-033 | FE-033 | [app/scim-directories/targets.tsx:582](../../frontend-web/apps/web/src/app/scim-directories/targets.tsx#L582) | id="scim-resource-form"; onSubmit={runOperation} |
| FORM-034 | FE-034 | [app/security/page.tsx:530](../../frontend-web/apps/web/src/app/security/page.tsx#L530) | onSubmit={(event) => { event.preventDefault() if (code.length === 6) onSubmit(code) }} |
| FORM-035 | FE-034 | [app/security/page.tsx:692](../../frontend-web/apps/web/src/app/security/page.tsx#L692) | id={formID}; onSubmit={ state?.type === "register" ? onRegister : state?.type === "rename" ? onRename : onDelete } |
| FORM-036 | FE-034 | [app/security/page.tsx:896](../../frontend-web/apps/web/src/app/security/page.tsx#L896) | id="password-form"; onSubmit={onSubmit} |
| FORM-037 | FE-034 | [app/security/page.tsx:1031](../../frontend-web/apps/web/src/app/security/page.tsx#L1031) | id={id}; onSubmit={onSubmit} |
| FORM-038 | FE-034 | [app/security/page.tsx:1089](../../frontend-web/apps/web/src/app/security/page.tsx#L1089) | id="mfa-confirm-form"; onSubmit={onSubmit} |
| FORM-039 | FE-035 | [app/service-accounts/page.tsx:542](../../frontend-web/apps/web/src/app/service-accounts/page.tsx#L542) | id="service-account-form"; onSubmit={onSubmit} |
| FORM-040 | FE-035 | [app/service-accounts/page.tsx:658](../../frontend-web/apps/web/src/app/service-accounts/page.tsx#L658) | id="service-credential-form"; onSubmit={onSubmit} |
| FORM-041 | FE-036 | [app/system/accounts.tsx:441](../../frontend-web/apps/web/src/app/system/accounts.tsx#L441) | onSubmit={(event) => { void save(event) }} |
| FORM-042 | FE-038 | [app/system/operating-platforms.tsx:191](../../frontend-web/apps/web/src/app/system/operating-platforms.tsx#L191) | onSubmit={(event) => { void save(event) }} |
| FORM-043 | FE-040 | [app/system/platform-members.tsx:151](../../frontend-web/apps/web/src/app/system/platform-members.tsx#L151) | onSubmit={(event) => { void save(event) }} |
| FORM-044 | FE-041 | [app/tenants/page.tsx:401](../../frontend-web/apps/web/src/app/tenants/page.tsx#L401) | id="tenant-form"; onSubmit={submit} |
| FORM-045 | FE-042 | [app/verify-email/page.tsx:100](../../frontend-web/apps/web/src/app/verify-email/page.tsx#L100) | onSubmit={submit} |
| FORM-046 | FE-047 | [components/directory-members-dialog.tsx:222](../../frontend-web/apps/web/src/components/directory-members-dialog.tsx#L222) | id={formID}; onSubmit={submit} |
| FORM-047 | FE-049 | [components/file-browser.tsx:1656](../../frontend-web/apps/web/src/components/file-browser.tsx#L1656) | id="file-editor-form"; onSubmit={submitEditor} |
| FORM-048 | FE-050 | [components/reauthentication-dialog.tsx:196](../../frontend-web/apps/web/src/components/reauthentication-dialog.tsx#L196) | onSubmit={submit} |

## 每文件输入和请求证据

每条字段列出现有显式name/id/value以及HTML限制；这用于识别重复声明，不代表值必然对应同名API字段。没有name的Select、checkbox、嵌套条件、hidden state按FE动作补requestPath。每文件列出**全部**直接iamApi/已知api调用（含GET），避免将查询、预检/准备动作漏作本地交互。菜单与角色系统别名、AuthProvider、文件callbacks等跨文件链另在后文固定。

### FE-001 — app/accept-invitation/page.tsx

接受邀请：URL token + login_name/display_name trim + password；confirmation 仅交互保留，不发送。token 必须以隐藏请求字段验证；密码字符/字节接受集合兼容，不继承登录强度。

**API 引用/调用集合**：`acceptInvitation`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 93 | `InvitationField` | id="invitation-login"; name="login_name"; maxLength={200} |
| 100 | `InvitationField` | id="invitation-display-name"; name="display_name"; maxLength={128} |
| 107 | `InvitationField` | id="invitation-password"; name="password"; type="password"; minLength={12}; maxLength={1024} |
| 116 | `InvitationField` | id="invitation-confirm-password"; name="confirm_password"; type="password"; minLength={12}; maxLength={1024} |
| 174 | `Input` | id={id}; name={name}; type={type}; required |

| 调用位置 | 实际参数表达式 |
|---|---|
| 38 | `iamApi.acceptInvitation({ token, login_name: String(data.get("login_name") ?? "").trim(), password, display_name: String(data.get("display_name") ?? "").trim(), })` |

**转换/构造证据**：`28:const data = new FormData(event.currentTarget)`；`40:login_name: String(data.get("login_name") ?? "").trim(),`；`42:display_name: String(data.get("display_name") ?? "").trim(),`.

**错误接入证据**：`31:setError(t("passwordMismatchSentence"))`；`35:setError("")`；`45:} catch (cause) {`；`46:setError(`.

### FE-002 — app/access-requests/page.tsx

创建与审批/拒绝/撤销/撤回两类 form；roleID state→role_id，expiresAt→UTC access_expires_at，reason/note trim；四种 decision 目标分别绑定，path request.id；当前 datetime-local min 及文本长度迁移。

**API 引用/调用集合**：`accessRequests`, `approveAccessRequest`, `createAccessRequest`, `myAccessRequests`, `rejectAccessRequest`, `requestableRoles`, `revokeAccessRequest`, `withdrawAccessRequest`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 230 | `SimpleSelect` | id="access-request-role"; value={roleID} |
| 245 | `Input` | id="access-request-expiry"; type="datetime-local"; value={expiresAt}; min={toLocalInputValue(new Date())}; required |
| 258 | `Textarea` | id="access-request-reason"; name="reason"; minLength={3}; maxLength={500}; required |
| 301 | `Textarea` | id="access-decision-note"; name="note"; maxLength={500} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 85 | `iamApi.myAccessRequests()` |
| 86 | `iamApi.requestableRoles()` |
| 87 | `iamApi.accessRequests()` |
| 116 | `iamApi.createAccessRequest({ role_id: roleID, reason: String(data.get("reason") ?? "").trim(), access_expires_at: new Date(expiresAt).toISOString(), })` |
| 140 | `iamApi.approveAccessRequest(decision.request.id, note)` |
| 143 | `iamApi.rejectAccessRequest(decision.request.id, note)` |
| 146 | `iamApi.revokeAccessRequest(decision.request.id, note)` |
| 149 | `iamApi.withdrawAccessRequest(decision.request.id, note)` |

**转换/构造证据**：`112:const data = new FormData(event.currentTarget)`；`118:reason: String(data.get("reason") ?? "").trim(),`；`119:access_expires_at: new Date(expiresAt).toISOString(),`；`133:const data = new FormData(event.currentTarget)`；`134:const note = String(data.get("note") ?? "").trim()`；`582:return new Date(value.getTime() - offset).toISOString().slice(0, 16)`.

**错误接入证据**：`98:void refresh().catch((cause: Error) => {`；`99:setError(cause.message)`；`114:setError("")`；`123:} catch (cause) {`；`124:setError((cause as Error).message)`；`136:setError("")`；`154:} catch (cause) {`；`155:setError((cause as Error).message)`；`171:onClick={() => void refresh().catch(showError(setError))}`；`589:function showError(setError: (message: string) => void) {`；`590:return (cause: unknown) => setError((cause as Error).message)`.

### FE-003 — app/access-reviews/page.tsx

创建 + action form；dueAt→UTC due_at，决策提交 item/review path + decision/note，完成/取消提交固定 status；隐藏 action 决定方法和载荷，不能按一个 form 固定一个 schema。

**API 引用/调用集合**：`accessReview`, `accessReviews`, `cancelAccessReview`, `completeAccessReview`, `createAccessReview`, `decideAccessReviewItem`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 306 | `Input` | id="access-review-name"; name="name"; minLength={3}; maxLength={128}; required |
| 317 | `Input` | id="access-review-due-at"; type="datetime-local"; value={dueAt}; min={toLocalInputValue(new Date())}; required |
| 364 | `Textarea` | id="access-review-note"; name="note"; maxLength={500} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 81 | `iamApi.accessReviews()` |
| 82 | `iamApi.accessReview(reviewID)` |
| 118 | `iamApi.createAccessReview({ name: String(data.get("name") ?? "").trim(), due_at: new Date(dueAt).toISOString(), })` |
| 140 | `iamApi.decideAccessReviewItem( action.review.id, action.item.id, action.decision, String(data.get("note") ?? "").trim() )` |
| 147 | `iamApi.completeAccessReview(action.review.id)` |
| 149 | `iamApi.cancelAccessReview(action.review.id)` |
| 212 | `iamApi .accessReview(review.id)` |

**转换/构造证据**：`114:const data = new FormData(event.currentTarget)`；`119:name: String(data.get("name") ?? "").trim(),`；`120:due_at: new Date(dueAt).toISOString(),`；`139:const data = new FormData(event.currentTarget)`；`144:String(data.get("note") ?? "").trim()`；`746:.toISOString()`.

**错误接入证据**：`87:} catch (cause) {`；`116:setError("")`；`125:} catch (cause) {`；`126:setError((cause as Error).message)`；`136:setError("")`；`154:} catch (cause) {`；`155:setError((cause as Error).message)`；`171:void refresh(selected?.id).catch(showError(setError))`；`211:setError("")`；`215:.catch(showError(setError))`；`757:function showError(setError: (message: string) => void) {`；`758:return (cause: unknown) => setError((cause as Error).message)`.

### FE-004 — app/account/applications.tsx

无 form：同意授权撤销确认按钮→revokeOAuthConsent(client_id)。无输入也须校验 path；保留确认框与总体错误。

**API 引用/调用集合**：`oauthConsents`, `revokeOAuthConsent`

| 调用位置 | 实际参数表达式 |
|---|---|
| 24 | `iamApi.oauthConsents()` |
| 30 | `iamApi.oauthConsents()` |
| 47 | `iamApi.revokeOAuthConsent(selected.client_id)` |

**错误接入证据**：`26:setError("")`；`35:if (active) setError(cause.message)`；`45:setError("")`；`52:} catch (cause) {`；`53:setError((cause as Error).message)`；`71:void refresh().catch((cause: Error) => setError(cause.message))`；`94:setError("")`；`123:setError("")`；`143:setError("")`.

### FE-005 — app/account/closure.tsx

无 form：关闭账号先 ReauthenticationDialog 再 submitClosureRequest(proof,version)，cancelClosureRequest(version)；sessionStorage continuation 与 proof 不显示为普通输入，不记录敏感值。

**API 引用/调用集合**：`cancelClosureRequest`, `closureRequest`, `submitClosureRequest`

| 调用位置 | 实际参数表达式 |
|---|---|
| 35 | `iamApi.closureRequest()` |
| 53 | `iamApi.submitClosureRequest(proof, version)` |
| 66 | `iamApi.cancelClosureRequest(request.version)` |

**转换/构造证据**：`21:const saved = JSON.parse(sessionStorage.getItem(pendingKey) ?? "null")`.

**错误接入证据**：`27:} catch {`；`39:setError("")`；`43:if (active) setError(cause.message)`；`64:setError("")`；`69:} catch (cause) {`；`70:setError((cause as Error).message)`.

### FE-006 — app/account/identities.tsx

选择 provider 的 form 只发起 reauth；获得 proof 后 link transaction，另有 unlink 按钮；return_url 是浏览器地址、existing link/target 来源不同；绑定两个真实提交而非只包住 begin()。

**API 引用/调用集合**：`createIdentityLinkTransaction`, `deleteIdentityLink`, `identityLinkProviders`, `identityLinks`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 148 | `select` | id="identity-provider"; required; value={provider} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 43 | `iamApi.identityLinks()` |
| 44 | `iamApi.identityLinkProviders()` |
| 72 | `iamApi.createIdentityLinkTransaction({ provider_id: pending.target, proof, return_url: location.origin + location.pathname, })` |
| 81 | `iamApi.deleteIdentityLink(pending.target, proof)` |

**转换/构造证据**：`27:const saved = JSON.parse(sessionStorage.getItem(pendingKey) ?? "null")`.

**错误接入证据**：`34:} catch {`；`50:setError("")`；`54:if (active) setError(cause.message)`.

### FE-007 — app/account/memberships.tsx

无 form：离开租户先 reauth，再 leaveMembership(selected,proof)；租户与权限数据加载不构成写表单；continuation 恢复也必须经过同一最终请求校验。

**API 引用/调用集合**：`leaveMembership`, `personalMemberships`, `tenantRegistrationPolicy`

| 调用位置 | 实际参数表达式 |
|---|---|
| 28 | `iamApi.tenantRegistrationPolicy()` |
| 54 | `iamApi.personalMemberships(offset)` |
| 73 | `iamApi.leaveMembership(selected, proof)` |

**转换/构造证据**：`40:const saved = JSON.parse(sessionStorage.getItem(pendingKey) ?? "null")`.

**错误接入证据**：`46:} catch {`；`59:setError("")`；`63:if (active) setError(cause.message)`.

### FE-008 — app/account/tokens.tsx

两阶段 preparePersonalToken→reauth→createPersonalToken：name、tenant、permissions[]、本地日期→UTC expires_at；最终 prepared.request+proof 不得重构为旧草稿。另有 revoke；过期 proof/变更 tenant 清选项需原行为回归。

**API 引用/调用集合**：`createPersonalToken`, `myTenants`, `personalTokenPermissions`, `personalTokens`, `preparePersonalToken`, `revokePersonalToken`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 301 | `Input` | id="token-name"; name="name"; required; maxLength={128} |
| 303 | `select` | id="token-tenant"; required; value={tenant} |
| 323 | `Input` | id="token-expiry"; name="expires_at"; type="datetime-local"; required |
| 342 | `Checkbox` | checked={selectedPermissions.includes(permission.code)} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 69 | `iamApi.personalTokens(offset)` |
| 91 | `iamApi.myTenants()` |
| 109 | `iamApi.personalTokenPermissions(tenant)` |
| 131 | `iamApi.createPersonalToken({ ...prepared.request, proof, })` |
| 151 | `iamApi.preparePersonalToken({ name: String(form.get("name")), tenant_id: tenant, permissions: selectedPermissions, expires_at: new Date(String(form.get("expires_at"))).toISOString(), })` |
| 174 | `iamApi.revokePersonalToken(revoking.id)` |

**转换/构造证据**：`54:const saved = JSON.parse(value)`；`147:const form = new FormData(event.currentTarget)`；`155:expires_at: new Date(String(form.get("expires_at"))).toISOString(),`.

**错误接入证据**：`61:} catch {`；`74:setError("")`；`80:setError(cause.message)`；`163:} catch (cause) {`；`178:} catch (cause) {`；`411:.catch(() => setOperationError(t("clipboardRejected")))`.

### FE-009 — app/audit-events/page.tsx

GET 查询 form 不是纯本地过滤：event_type/principal_id/target_type/target_id trim→query，起止日期→UTC；tenant/system auditEvents 与 export 同查询。查询规则纳入；NDJSON 下载保留现有完整性校验，错误不能按 JSON 成功体解析。

**API 引用/调用集合**：`auditEvent`, `auditEvents`, `auditExport`, `auditIntegrity`, `systemAuditEvent`, `systemAuditEvents`, `systemAuditExport`, `systemAuditIntegrity`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 265 | `FilterInput` | id="audit-event-type"; value={filters.eventType} |
| 274 | `SimpleSelect` | id="audit-outcome"; value={filters.outcome \|\| "all"} |
| 286 | `FilterInput` | id="audit-principal"; value={filters.principalID} |
| 292 | `FilterInput` | id="audit-target-type"; value={filters.targetType} |
| 299 | `FilterInput` | id="audit-target-id"; value={filters.targetID} |
| 305 | `FilterInput` | id="audit-from"; type="datetime-local"; value={filters.from} |
| 312 | `FilterInput` | id="audit-to"; type="datetime-local"; value={filters.to} |
| 555 | `Input` | id={id}; type={type}; value={value}; maxLength={128} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 111 | `iamApi.systemAuditEvents({ ...query, offset, limit: pageSize })` |
| 112 | `iamApi.auditEvents({ ...query, offset, limit: pageSize })` |
| 113 | `iamApi.systemAuditIntegrity()` |
| 113 | `iamApi.auditIntegrity()` |
| 169 | `iamApi.systemAuditEvent(event.id)` |
| 170 | `iamApi.auditEvent(event.id)` |
| 184 | `iamApi.systemAuditExport(query)` |
| 185 | `iamApi.auditExport(query)` |

**转换/构造证据**：`141:event_type: filters.eventType.trim() \|\| undefined,`；`148:principal_id: filters.principalID.trim() \|\| undefined,`；`149:target_type: filters.targetType.trim() \|\| undefined,`；`150:target_id: filters.targetID.trim() \|\| undefined,`；`721:return value ? new Date(value).toISOString() : ""`.

**错误接入证据**：`165:setError("")`；`172:} catch (cause) {`；`173:setError((cause as Error).message)`；`181:setError("")`；`197:} catch (cause) {`；`198:setError((cause as Error).message)`.

### FE-010 — app/audit-governance/archives.tsx

无form/输入但api prop调用：requestAuditArchive(scope)、retryAuditArchive(job.id,scope)、verifyAuditArchive(job.id,scope)、downloadAuditArchive(job.id,scope)；scope决定system/tenant；run统一catch→setError，全局错误必须承接path/query/header issues，下载是NDJSON。

**API 引用/调用集合**：`auditArchives`, `downloadAuditArchive`, `requestAuditArchive`, `retryAuditArchive`, `verifyAuditArchive`

| 调用位置 | 实际参数表达式 |
|---|---|
| 92 | `api.auditArchives(offset, pageSize, scope)` |
| 129 | `api.requestAuditArchive(scope)` |
| 139 | `api.verifyAuditArchive(job.id, scope)` |
| 148 | `api.downloadAuditArchive(job.id, scope)` |
| 318 | `api.retryAuditArchive(job.id, scope)` |

**错误接入证据**：`98:setError("")`；`105:setError(cause.message)`；`115:setError("")`；`120:} catch (cause) {`；`121:if (active.current) setError((cause as Error).message)`.

### FE-011 — app/audit-governance/page.tsx

Number(minDays/archiveDays)→min_days/archive_after_days；当前 min=1、max=36500、归档天数>=保留天数是重复/跨字段候选。scope 选择 tenant/platform 目标；另有无表单 auditSignRoot/auditAnchor。

**API 引用/调用集合**：`auditAnchor`, `auditArchiveStatus`, `auditGovernance`, `auditSignRoot`, `setAuditRetention`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 404 | `Input` | id="min-days"; type="number"; min={1}; max={36500}; required; value={minDays} |
| 416 | `Input` | id="archive-days"; type="number"; min={Number(minDays) \|\| 1}; max={36500}; required; value={archiveDays} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 136 | `iamApi.auditGovernance()` |
| 137 | `iamApi.auditArchiveStatus(scope)` |
| 209 | `iamApi.setAuditRetention( { min_days: Number(minDays), archive_after_days: Number(archiveDays), }, scope )` |
| 230 | `iamApi.auditSignRoot()` |
| 243 | `iamApi.auditAnchor()` |

**转换/构造证据**：`45:createIamApi,`；`119:const iamApi = useMemo(() => createIamApi(undefined, () => tenant), [tenant])`；`211:min_days: Number(minDays),`；`212:archive_after_days: Number(archiveDays),`；`419:min={Number(minDays) \|\| 1}`.

**错误接入证据**：`147:setError("")`；`153:} catch (cause) {`；`157:setError((cause as Error).message)`；`175:setError("")`；`180:setError(cause.message)`；`207:setError("")`；`219:} catch (cause) {`；`220:if (active.current) setError((cause as Error).message)`；`228:setError("")`；`232:} catch (cause) {`；`233:if (active.current) setError((cause as Error).message)`；`241:setError("")`；`245:} catch (cause) {`；`246:if (active.current) setError((cause as Error).message)`.

### FE-012 — app/dashboard/page.tsx

只有读统计（principals/roles/menus/sessions），无用户构造请求字段：不创建表单绑定；共享客户端错误兼容回归即可。

**API 引用/调用集合**：`menus`, `principals`, `roles`, `sessions`

| 调用位置 | 实际参数表达式 |
|---|---|
| 33 | `iamApi.principals()` |
| 36 | `iamApi.roles()` |
| 39 | `iamApi.menus()` |
| 43 | `iamApi.sessions()` |

**错误接入证据**：`47:setError("")`；`51:.catch((cause: Error) => {`；`52:if (active) setError(cause.message)`.

### FE-013 — app/departments/page.tsx

create/update 共用 form：parent root→空串、sort_order Number、status state；create 带 entity_id，PATCH 带 version。system alias 删除 entity_id 并改 endpoints，普通/运营上下文沿 organization routing；entity 选择仅新建时显示。另有版本 DELETE。

**API 引用/调用集合**：`createDepartment`, `createSystemDepartment`, `deleteDepartment`, `deleteSystemDepartment`, `departments`, `entities`, `systemDepartments`, `updateDepartment`, `updateSystemDepartment`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 465 | `SimpleSelect` | id="department-entity"; value={entityID}; disabled={editing !== null} |
| 482 | `Input` | id="department-name"; name="name"; defaultValue={editing?.name}; required; maxLength={128} |
| 492 | `SimpleSelect` | id="department-parent"; value={parentID} |
| 502 | `Input` | id="department-sort-order"; name="sort_order"; type="number"; min={0}; max={1000000}; defaultValue={editing?.sort_order ?? 0}; required |
| 514 | `SimpleSelect` | id="department-status"; value={status} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 78 | `iamApi.createSystemDepartment({ name, parent_id, sort_order, status, })` |
| 113 | `api.departments()` |
| 121 | `iamApi.entities()` |
| 228 | `api.updateDepartment(editing.id, { ...values, version: editing.version, })` |
| 233 | `api.createDepartment({ ...values, entity_id: entityID, })` |
| 251 | `api.deleteDepartment(department.id, department.version)` |

**转换/构造证据**：`219:const data = new FormData(event.currentTarget)`；`224:sort_order: Number(data.get("sort_order") \|\| 0),`.

**错误接入证据**：`127:} catch (cause) {`；`218:setError("")`；`240:} catch (cause) {`；`241:setError((cause as Error).message)`；`249:setError("")`；`258:} catch (cause) {`；`259:setError((cause as Error).message)`.

### FE-014 — app/device/page.tsx

code 输入 form 走 GET deviceRequest；批准/拒绝按钮走 decideDeviceRequest(code.trim(),approve)。URL 预填也经查询；不把 lookup 的规则与 decision 写规则合并。

**API 引用/调用集合**：`decideDeviceRequest`, `deviceRequest`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 202 | `Input` | id="user_code"; name="user_code"; value={code} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 34 | `iamApi .deviceRequest(userCode)` |
| 53 | `iamApi.deviceRequest(prefilled)` |
| 76 | `iamApi.decideDeviceRequest(code.trim(), approve)` |

**转换/构造证据**：`68:if (code.trim()) void lookup(code.trim())`；`76:await iamApi.decideDeviceRequest(code.trim(), approve)`；`217:<Button type="submit" disabled={busy \|\| !code.trim()}>`.

**错误接入证据**：`32:setError("")`；`40:setError(cause.message)`；`58:if (active) setError(cause.message)`；`74:setError("")`；`79:} catch (cause) {`；`80:setError((cause as Error).message)`.

### FE-015 — app/entities/page.tsx

三个 form：实体（metadata JSON.parse，拒绝null/数组/非对象）、角色绑定（role/principal/effect/optional UTC expires_at）、relationship（subject 类型决定 subject_relation、resource_id 是否输入决定 entity_id/resource_type）。trustedContextCondition 生成 version/all 嵌套条件；inspection 非 form 输入走 authorizationConstraint/explain* 请求，不能漏。关系/绑定删除保留原冲突和全局错误。

**API 引用/调用集合**：`authorizationConstraint`, `createEntity`, `deleteEntity`, `deleteEntityRoleBinding`, `deleteRelationship`, `entities`, `entityRoleBindings`, `explainEntityAuthorization`, `explainResourceAuthorization`, `groups`, `members`, `permissions`, `putEntityRoleBinding`, `putRelationship`, `relationships`, `resourceRelationships`, `roles`, `updateEntity`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 706 | `Input` | id="entity-name"; name="name"; defaultValue={editing?.name}; required; maxLength={200} |
| 716 | `Input` | id="entity-type"; name="type"; defaultValue={editing?.type}; pattern="[a-z][a-z0-9_-]{0,63}"; required; maxLength={64} |
| 730 | `SimpleSelect` | id="entity-parent"; value={parentID} |
| 739 | `SimpleSelect` | id="entity-status"; value={status} |
| 751 | `Textarea` | id="entity-metadata"; name="metadata"; defaultValue={JSON.stringify(editing?.metadata ?? {}, null, 2)} |
| 798 | `SimpleSelect` | id="entity-binding-role"; value={roleID} |
| 811 | `SimpleSelect` | id="entity-binding-principal"; value={principalID} |
| 824 | `SimpleSelect` | id="entity-binding-effect"; value={effect} |
| 835 | `Input` | id="entity-binding-expiry"; type="datetime-local"; value={expiresAt} |
| 922 | `SimpleSelect` | id="relationship-subject-type"; value={relationshipSubjectType} |
| 940 | `SimpleSelect` | id="relationship-subject"; value={relationshipSubjectID} |
| 953 | `SimpleSelect` | id="relationship-subject-relation"; value={relationshipSubjectRelation} |
| 968 | `Input` | id="relationship-subject-relation"; value={ relationshipSubjectType === "principal" ? t("directPrincipal") : "member" }; disabled |
| 983 | `SimpleSelect` | id="relationship-relation"; value={relationshipRelation} |
| 1000 | `SimpleSelect` | id="relationship-resource-type"; value={relationshipResourceType}; disabled={!relationshipResourceID} |
| 1013 | `Input` | id="relationship-resource-id"; value={relationshipResourceID}; maxLength={255} |
| 1027 | `Input` | id="relationship-starts-at"; type="datetime-local"; value={relationshipStartsAt} |
| 1038 | `Input` | id="relationship-ends-at"; type="datetime-local"; value={relationshipEndsAt} |
| 1051 | `Input` | id="relationship-minimum-acr"; type="number"; min="1"; max="99"; value={relationshipMinimumAcr} |
| 1067 | `Input` | id="relationship-client-id"; value={relationshipClientID}; maxLength={255} |
| 1081 | `Input` | id="relationship-network-zone"; value={relationshipNetworkZone}; maxLength={255} |
| 1095 | `Input` | id="relationship-time-start"; type="time"; value={relationshipTimeStart}; required={Boolean(relationshipTimeEnd)} |
| 1107 | `Input` | id="relationship-time-end"; type="time"; value={relationshipTimeEnd}; required={Boolean(relationshipTimeStart)} |
| 1119 | `Input` | id="relationship-timezone"; value={relationshipTimezone}; required={Boolean( relationshipTimeStart \|\| relationshipTimeEnd )}; maxLength={64} |
| 1252 | `SimpleSelect` | id="authorization-inspection-principal"; value={principalID} |
| 1270 | `SimpleSelect` | id="authorization-inspection-permission"; value={inspectionPermission} |
| 1288 | `Input` | id="authorization-inspection-resource"; value={inspectionResourceID}; maxLength={255} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 69 | `iamApi.relationships(entityID)` |
| 70 | `iamApi.resourceRelationships(entityID)` |
| 135 | `iamApi.entities()` |
| 254 | `iamApi.updateEntity(editing.id, input)` |
| 255 | `iamApi.createEntity(input)` |
| 270 | `iamApi.deleteEntity(entity.id)` |
| 296 | `iamApi.entityRoleBindings(entity.id)` |
| 298 | `iamApi.roles()` |
| 299 | `iamApi.members()` |
| 300 | `iamApi.groups()` |
| 301 | `iamApi.permissions()` |
| 351 | `iamApi.putRelationship({ entity_id: resourceID ? bindingEntity.id : undefined, subject_type: relationshipSubjectType, subject_id: relationshipSubjectID, subject_relation: relationshipSubjectType === "principal" ? undefined : relationshipSubjectType === "entity" ? relationshipSubjectRelation : "member", relation: relationshipRelation, resource_type: resourceID ? relationshipResourceType : bindingEntity.type, resource_id: resourceID \|\| bindingEntity.id, starts_at: relationshipStartsAt ? new Date(relationshipStartsAt).toISOString() : undefined, ends_at: relationshipEndsAt ? new Date(relationshipEndsAt).toISOString() : undefined, condition: trustedContextCondition( relationshipMinimumAcr, relationshipClientID, relationshipNetworkZone, relationshipTimeStart, relationshipTimeEnd, relationshipTimezone ), })` |
| 402 | `iamApi.deleteRelationship(relationship)` |
| 439 | `iamApi.putEntityRoleBinding(bindingEntity.id, roleID, principalID, { effect, expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined, })` |
| 443 | `iamApi.entityRoleBindings(bindingEntity.id)` |
| 456 | `iamApi.deleteEntityRoleBinding( bindingEntity.id, binding.role_id, binding.principal_id )` |
| 461 | `iamApi.entityRoleBindings(bindingEntity.id)` |
| 479 | `iamApi.authorizationConstraint(inspectionPermission, principalID)` |
| 481 | `iamApi.explainResourceAuthorization( bindingEntity.id, resourceType, resourceID, inspectionPermission, principalID )` |
| 488 | `iamApi.explainEntityAuthorization( bindingEntity.id, inspectionPermission, principalID )` |

**转换/构造证据**：`245:const data = new FormData(event.currentTarget)`；`350:const resourceID = relationshipResourceID.trim()`；`367:? new Date(relationshipStartsAt).toISOString()`；`370:? new Date(relationshipEndsAt).toISOString()`；`441:expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,`；`474:const resourceID = inspectionResourceID.trim()`；`1137:(Boolean(relationshipResourceID.trim()) &&`；`1469:value = JSON.parse(raw \|\| "{}")`.

**错误接入证据**：`145:void refresh().catch((cause: Error) => setLoadError(cause.message))`；`226:setError("")`；`234:setError("")`；`244:setError("")`；`259:} catch (cause) {`；`260:setError((cause as Error).message)`；`268:setError("")`；`277:} catch (cause) {`；`278:setError((cause as Error).message)`；`286:setError("")`；`337:} catch (cause) {`；`338:setError((cause as Error).message)`；`348:setError("")`；`390:} catch (cause) {`；`391:setError((cause as Error).message)`；`400:setError("")`；`405:} catch (cause) {`；`406:setError((cause as Error).message)`；`437:setError("")`；`444:} catch (cause) {`；`445:setError((cause as Error).message)`；`454:setError("")`；`462:} catch (cause) {`；`463:setError((cause as Error).message)`；`472:setError("")`；`495:} catch (cause) {`；`496:setError((cause as Error).message)`；`535:void refresh().catch((cause: Error) =>`；`1470:} catch {`.

### FE-016 — app/entities/workspace.tsx

只读实体摘要和权限允许的数据源；写入口委托实体页面/其他 tabs，不重复建立表单。

**API 引用/调用集合**：`entity`, `entityRoleBindings`, `members`, `resourceRelationships`, `roles`

| 调用位置 | 实际参数表达式 |
|---|---|
| 67 | `iamApi.entity(entityID)` |
| 69 | `iamApi.entityRoleBindings(entityID)` |
| 72 | `iamApi.resourceRelationships(entityID)` |
| 77 | `iamApi.roles()` |
| 80 | `iamApi.members()` |

**错误接入证据**：`87:if (active) setError(cause.message)`.

### FE-017 — app/field-policies/page.tsx

每字段 policy editor form，嵌套 read/write default/allow_roles/deny_roles 与 mask；保存 PUT policy+version，reset DELETE version。权限目录配置不是字段校验规则，不能与本次 OPTIONS validation schema 混淆。

**API 引用/调用集合**：`deleteFieldPolicy`, `fieldCatalog`, `fieldPolicies`, `roles`, `setFieldPolicy`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 67 | `TenantFieldPolicies` | 无静态name/约束；按父级state映射 |
| 170 | `SimpleSelect` | id="field-resource"; value={resource} |
| 182 | `Input` | id="field-search"; value={search}; type="search" |
| 315 | `FieldPolicyEditor` | 无静态name/约束；按父级state映射 |
| 470 | `SimpleSelect` | id="field-read"; value={draft.read.default}; disabled={neverRead \|\| busy \|\| conflict} |
| 491 | `SimpleSelect` | id="field-write"; value={draft.write.default}; disabled={readOnly \|\| busy \|\| conflict} |
| 512 | `SimpleSelect` | id="field-mask"; value={draft.mask}; disabled={busy \|\| conflict} |
| 535 | `Input` | id="field-role-search"; type="search"; value={roleSearch} |
| 590 | `Checkbox` | checked={draft[side][effect].includes(id)}; disabled={ busy \|\| conflict \|\| (side === "read" ? neverRead : readOnly) } |
| 630 | `Input` | id="field-role-id"; value={roleID} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 91 | `api.fieldCatalog()` |
| 92 | `api.fieldPolicies()` |
| 107 | `api.fieldCatalog()` |
| 107 | `api.fieldPolicies()` |
| 120 | `api .roles()` |
| 422 | `api.deleteFieldPolicy(field.resource, field.name, current.version)` |
| 424 | `api.setFieldPolicy( field.resource, field.name, draft, current?.version ?? 0 )` |

**转换/构造证据**：`43:createIamApi,`；`75:const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])`；`346:api: ReturnType<typeof createIamApi>`.

**错误接入证据**：`88:setError("")`；`96:} catch (cause) {`；`99:setError((cause as Error).message)`；`116:setError(cause.message)`；`125:.catch((cause: Error) => {`；`401:setError(t("maxRolesPerRule"))`；`419:setError("")`；`431:} catch (cause) {`；`434:setError(t("policyVersionConflict"))`；`435:} else setError((cause as Error).message)`.

### FE-018 — app/files/page.tsx

FileBrowser 回调 owner：创建账户/系统根、目录、空文件、上传/目录递归、rename、trash、restore、copy/move conflict decisions。fileEntityID 三态区分 account/tenant/entity，system 单独分支；revision、Idempotency-Key、observed_revision 和策略默认在最终请求验证；部分成功/冲突交互保留。

**API 引用/调用集合**：`createAccountFileRoot`, `createDirectory`, `createFileOperation`, `createSystemFileRoot`, `downloadFile`, `fileContentURL`, `previewFile`, `renameFileEntry`, `restoreFileEntry`, `trashFileEntry`, `uploadFile`

| 调用位置 | 实际参数表达式 |
|---|---|
| 308 | `iamApi.createAccountFileRoot()` |
| 324 | `iamApi.downloadFile(fileEntityID, node.id)` |
| 337 | `iamApi.downloadFile(fileEntityID, node.id)` |
| 348 | `iamApi.fileContentURL(fileEntityID, node.id)` |
| 355 | `iamApi.previewFile(fileEntityID, node.id, maxBytes, signal)` |
| 366 | `iamApi.restoreFileEntry(fileEntityID, node.id, node.revision, name)` |
| 399 | `iamApi.createFileOperation(fileEntityID, request, key, system)` |
| 526 | `iamApi.createSystemFileRoot()` |
| 539 | `iamApi.createDirectory(fileEntityID, id, name, system)` |
| 549 | `iamApi.uploadFile( fileEntityID, id, new File([], name, { type: mimeType }), system )` |
| 569 | `iamApi.createDirectory( fileEntityID, targetID, name, system )` |
| 579 | `iamApi.uploadFile( fileEntityID, targetID, file, system )` |
| 614 | `iamApi.renameFileEntry( fileEntityID, node.id, node.revision, name )` |
| 628 | `iamApi.trashFileEntry( fileEntityID, node.id, node.revision )` |
| 643 | `iamApi.trashFileEntry( fileEntityID, node.id, node.revision )` |

**错误接入证据**：`217:.catch((cause: unknown) => {`；`289:} catch (cause) {`；`369:} catch (cause) {`.

### FE-019 — app/groups/page.tsx

group create/PATCH：Number sort_order；dynamic 才含 membership_rule{version:1,match,conditions[{field,operator,values:split comma/trim/filter}]}；edit 不发送 type，带 version。static member 子表单委托 DirectoryMembersDialog（putGroupMember/deleteGroupMember），另有 role binding dialog。

**API 引用/调用集合**：`createGroup`, `deleteGroup`, `deleteGroupMember`, `groupMembers`, `groups`, `members`, `putGroupMember`, `updateGroup`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 433 | `Input` | id="group-name"; name="name"; defaultValue={editingGroup?.name}; maxLength={128}; required |
| 443 | `SimpleSelect` | id="group-type"; value={groupType}; disabled={Boolean(editingGroup)} |
| 462 | `SimpleSelect` | id="group-rule-match"; value={ruleMatch} |
| 491 | `SimpleSelect` | id={`group-rule-field-${index}`}; value={condition.field} |
| 514 | `SimpleSelect` | id={`group-rule-operator-${index}`}; value={condition.operator} |
| 533 | `Input` | value={condition.values}; required |
| 571 | `Textarea` | id="group-description"; name="description"; defaultValue={editingGroup?.description}; maxLength={1024} |
| 582 | `Input` | id="group-sort-order"; name="sort_order"; type="number"; min={0}; max={1000000}; defaultValue={editingGroup?.sort_order ?? 0}; required |
| 594 | `SimpleSelect` | id="group-status"; value={status} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 94 | `iamApi.groups()` |
| 169 | `iamApi.updateGroup(editingGroup.id, { name: input.name, description: input.description, status: input.status, sort_order: input.sort_order, ...(input.membership_rule ? { membership_rule: input.membership_rule } : {}), version: editingGroup.version, })` |
| 179 | `iamApi.createGroup(input)` |
| 194 | `iamApi.deleteGroup(group.id, group.version)` |
| 210 | `iamApi.groupMembers(group.id)` |
| 212 | `iamApi.members()` |
| 233 | `iamApi.putGroupMember(memberGroup.id, principalID, input)` |
| 234 | `iamApi.groupMembers(memberGroup.id)` |
| 249 | `iamApi.deleteGroupMember(memberGroup.id, member.principal_id)` |
| 250 | `iamApi.groupMembers(memberGroup.id)` |

**转换/构造证据**：`143:const data = new FormData(event.currentTarget)`；`149:sort_order: Number(data.get("sort_order") \|\| 0),`；`160:.map((value) => value.trim())`.

**错误接入证据**：`96:} catch (cause) {`；`142:setError("")`；`183:} catch (cause) {`；`184:setError((cause as Error).message)`；`192:setError("")`；`196:} catch (cause) {`；`197:setError((cause as Error).message)`；`205:setError("")`；`218:} catch (cause) {`；`219:setError((cause as Error).message)`；`231:setError("")`；`236:} catch (cause) {`；`237:setError((cause as Error).message)`；`247:setError("")`；`252:} catch (cause) {`；`253:setError((cause as Error).message)`.

### FE-020 — app/invitations/page.tsx

create：email trim、departmentID空→undefined、roleIDs[]、expires_in_hours Number；resend/revoke 非 form。email maxlength320、hours1..720 迁移同源；数组和隐藏 path 必须验收。

**API 引用/调用集合**：`createInvitation`, `departments`, `invitations`, `resendInvitation`, `revokeInvitation`, `roles`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 283 | `Input` | id="invitation-email"; name="email"; type="email"; required; maxLength={320} |
| 294 | `Input` | id="invitation-expiry"; name="expires_in_hours"; type="number"; min={1}; max={720}; defaultValue={72}; required |
| 308 | `SimpleSelect` | id="invitation-department"; value={departmentID \|\| "__none__"} |
| 336 | `Checkbox` | checked={roleIDs.includes(role.id)} |
| 379 | `Input` | id="issued-invitation-token"; value={issued?.token ?? ""} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 61 | `iamApi.invitations()` |
| 81 | `iamApi.departments()` |
| 82 | `iamApi.roles()` |
| 100 | `iamApi.createInvitation({ email: String(data.get("email") ?? "").trim(), department_id: departmentID \|\| undefined, role_ids: roleIDs, expires_in_hours: Number(data.get("expires_in_hours")), })` |
| 123 | `iamApi.resendInvitation(invitation.id)` |
| 140 | `iamApi.revokeInvitation(invitation.id)` |

**转换/构造证据**：`98:const data = new FormData(event.currentTarget)`；`101:email: String(data.get("email") ?? "").trim(),`；`104:expires_in_hours: Number(data.get("expires_in_hours")),`.

**错误接入证据**：`63:} catch (cause) {`；`97:setError("")`；`111:} catch (cause) {`；`112:setError((cause as Error).message)`；`121:setError("")`；`125:} catch (cause) {`；`126:setError((cause as Error).message)`；`138:setError("")`；`142:} catch (cause) {`；`143:setError((cause as Error).message)`；`391:.catch(() => setError(t("clipboardRejected")))`.

### FE-021 — app/layout.tsx

shell 数据源/租户选择/导航读取，无独立业务写表单；会话和 workspace 切换必须使共享元数据缓存与过期响应隔离。

**API 引用/调用集合**：`accountViewWorkspace`, `effectiveMenus`, `memberEntities`, `myOperatingPlatforms`, `myPermissions`, `myTenants`

| 调用位置 | 实际参数表达式 |
|---|---|
| 123 | `iamApi.myOperatingPlatforms()` |
| 217 | `iamApi.myPermissions(tenantValue)` |
| 223 | `iamApi.memberEntities("file_view")` |
| 268 | `iamApi.effectiveMenus(tenantValue)` |
| 270 | `iamApi.myPermissions(tenantValue \|\| undefined, entityID \|\| undefined)` |
| 321 | `iamApi.accountViewWorkspace()` |
| 366 | `iamApi.myTenants()` |

**错误接入证据**：当前文件无本地catch/setError，错误由父回调、共享provider或只读fallback承接；见FE台账。

### FE-022 — app/login/page.tsx

四个原生 form：手机号取码、手机 code 登录、密码登录、MFA；真实调用经 AuthProvider。另有 passkey 登录、MFA短信重发、图形验证码刷新/Turnstile。保留 challenge/captcha状态、return_url、dev_tenant_id 的纯 UI/上下文选择；不能只绑定 iamApi.capabilities。

**API 引用/调用集合**：`capabilities`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 379 | `input` | type="hidden"; name="captcha_answer"; value={answer} |
| 386 | `Input` | id="login-captcha"; name="captcha_answer"; value={answer}; required; maxLength={16} |
| 458 | `Input` | id="phone"; name="phone"; type="tel"; required; disabled={busy} |
| 469 | `CaptchaField` | 无静态name/约束；按父级state映射 |
| 517 | `Input` | id="code"; name="code"; maxLength={6}; required; disabled={busy} |
| 644 | `input` | type="hidden"; name="dev_tenant_id"; value={tenantID} |
| 645 | `input` | type="hidden"; name="dev_platform_id"; value={platformID} |
| 650 | `SimpleSelect` | value={selectedAccount} |
| 671 | `Input` | id="login-name"; name="login_name"; value={loginName}; required; maxLength={200} |
| 685 | `Input` | id="password"; name="password"; type={visible ? "text" : "password"}; value={password}; required; maxLength={1024} |
| 712 | `CaptchaField` | 无静态name/约束；按父级state映射 |
| 822 | `Input` | id="mfa-code"; name="code"; required; minLength={6}; maxLength={64} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 108 | `iamApi .capabilities()` |

**转换/构造证据**：`125:const data = new FormData(event.currentTarget)`；`163:new FormData(event.currentTarget).get("phone") ?? ""`；`164:).trim()`；`188:const code = String(new FormData(event.currentTarget).get("code") ?? "").trim()`；`229:const data = new FormData(event.currentTarget)`；`233:String(data.get("code") ?? "").trim()`.

**错误接入证据**：`91:setError("captchaUnavailable")`；`124:setError("")`；`146:} catch (cause) {`；`148:setError(t(loginError(cause)))`；`167:setError("")`；`176:} catch (cause) {`；`177:setError(t(loginError(cause)))`；`187:setError("")`；`196:} catch (cause) {`；`197:setError(t(loginError(cause, "invalidPhoneCode")))`；`209:setError("")`；`213:} catch (cause) {`；`214:setError(`；`228:setError("")`；`235:} catch (cause) {`；`236:setError(t(loginError(cause, "invalidChallenge")))`；`244:setError("")`；`247:} catch (cause) {`；`248:setError(`；`279:setError("")`；`290:setError("")`；`300:setError("")`；`307:onCaptchaUnavailable={() => setError(t("captchaUnavailable"))}`；`325:onCaptchaUnavailable={() => setError(t("captchaUnavailable"))}`；`331:setError("")`.

### FE-023 — app/login/workspaces.tsx

registerTenant 表单 trim name/slug；其余 workspace 选择是上下文/导航，不是业务 POST。tenantRegistrationPolicy 决定入口可见，不能变为 schema required。

**API 引用/调用集合**：`myOperatingPlatforms`, `myTenants`, `registerTenant`, `systemPermissions`, `tenantRegistrationPolicy`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 193 | `Input` | id="tenant-registration-name"; name="name"; required; maxLength={128} |
| 202 | `Input` | id="tenant-registration-slug"; name="slug"; required; minLength={3}; maxLength={63}; pattern="[a-z0-9][a-z0-9-]*[a-z0-9]" |

| 调用位置 | 实际参数表达式 |
|---|---|
| 42 | `iamApi.systemPermissions()` |
| 48 | `iamApi.myOperatingPlatforms()` |
| 53 | `iamApi.myTenants()` |
| 54 | `iamApi.tenantRegistrationPolicy()` |
| 105 | `iamApi.registerTenant({ name: String(form.get("name") ?? "").trim(), slug: String(form.get("slug") ?? "").trim(), })` |

**转换/构造证据**：`103:const form = new FormData(event.currentTarget)`；`106:name: String(form.get("name") ?? "").trim(),`；`107:slug: String(form.get("slug") ?? "").trim(),`.

**错误接入证据**：`66:void load().catch((cause: Error) => {`；`67:if (active) setError(cause.message)`；`102:setError("")`；`111:} catch (cause) {`；`112:setError((cause as Error).message)`；`138:void logout().catch((cause: Error) => setError(cause.message))`.

### FE-024 — app/menus/page.tsx

menuApi alias tenant/system；translations rows过滤空label、locale trim/lowercase去重检测→translations 对象，sort_order Number、parent/status等 state；create/update/delete、树拖动排序 update 全纳入。语言重复是请求对象构造约束而非简单 Input maxlength。

**API 引用/调用集合**：`createMenu`, `createSystemMenu`, `deleteMenu`, `deleteSystemMenu`, `menus`, `systemMenus`, `updateMenu`, `updateSystemMenu`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 378 | `select` | value={previewLanguage ?? locale} |
| 727 | `MenuField` | name="label"; defaultValue={editing?.label}; maxLength={128}; required |
| 742 | `select` | value={item.locale} |
| 771 | `Input` | value={item.label}; maxLength={128} |
| 827 | `MenuField` | name="route"; defaultValue={editing?.route}; maxLength={512} |
| 833 | `MenuField` | name="icon"; defaultValue={editing?.icon}; maxLength={64} |
| 839 | `MenuField` | name="permission_code"; disabled={system && !allowed("menu_bind_permission")}; defaultValue={editing?.permission_code}; maxLength={128} |
| 846 | `MenuField` | name="sort_order"; type="number"; defaultValue={String(editing?.sort_order ?? 0)}; min={-100000}; max={100000} |
| 856 | `SimpleSelect` | id="menu-parent"; value={parentID} |
| 865 | `SimpleSelect` | id="menu-status"; value={status} |
| 916 | `Input` | id={`menu-${name}`}; name={name}; type={type}; required={required}; disabled={disabled}; defaultValue={defaultValue}; maxLength={maxLength}; min={min}; max={max} |

**转换/构造证据**：`176:const data = new FormData(event.currentTarget)`；`177:const translated = translations.filter((item) => item.label.trim())`；`179:new Set(translated.map((item) => item.locale.trim().toLowerCase()))`；`190:translated.map((item) => [item.locale.trim(), item.label.trim()])`；`198:sort_order: Number(data.get("sort_order") \|\| 0),`.

**错误接入证据**：`131:} catch (cause) {`；`175:setError("")`；`182:setError(t("duplicateLanguage"))`；`208:} catch (cause) {`；`209:setError((cause as Error).message)`；`217:setError("")`；`222:} catch (cause) {`；`223:setError((cause as Error).message)`；`257:setError("")`；`262:} catch (cause) {`；`263:setError((cause as Error).message)`.

### FE-025 — app/oauth-clients/page.tsx

name trim、redirectUris按行 trim/去重→redirect_uris[]、scopes按词→scopes[]、grantTypes/publicClient state；edit才含status。当前 scopes/grants 非空、authorization_code要求redirect 是跨字段服务规则候选；secret rotate/delete 按钮也纳入，secret仅一次显示保持。

**API 引用/调用集合**：`createOAuthClient`, `deleteOAuthClient`, `oauthClients`, `rotateOAuthClientSecret`, `updateOAuthClient`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 595 | `Input` | id="oauth-client-name"; value={draft.name}; required; minLength={1}; maxLength={128} |
| 638 | `Checkbox` | checked={draft.grantTypes.includes(grant.value)}; disabled={disabled} |
| 655 | `Textarea` | id="oauth-redirect-uris"; value={draft.redirectUris}; maxLength={8192} |
| 671 | `Input` | id="oauth-scopes"; value={draft.scopes}; required; maxLength={2048} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 136 | `iamApi.oauthClients()` |
| 148 | `iamApi .oauthClients()` |
| 207 | `iamApi.updateOAuthClient(editing.id, { ...body, status: draft.status, })` |
| 213 | `iamApi.createOAuthClient(body)` |
| 239 | `iamApi.rotateOAuthClientSecret( confirmation.client.id )` |
| 247 | `iamApi.deleteOAuthClient(confirmation.client.id)` |

**转换/构造证据**：`102:.map((item) => item.trim())`；`113:.map((item) => item.trim())`；`185:name: draft.name.trim(),`.

**错误接入证据**：`138:} catch (cause) {`；`167:setError("")`；`173:setError("")`；`192:setError(t("grantAndScopeRequired"))`；`199:setError(t("redirectRequired"))`；`203:setError("")`；`225:} catch (cause) {`；`226:setError((cause as Error).message)`；`235:setError("")`；`252:} catch (cause) {`；`253:setError((cause as Error).message)`.

### FE-026 — app/principals/closure-requests.tsx

无 form：status select触发GET；reject/process确认按钮通过条件函数引用调用 rejectClosureRequest/processClosureRequest(principal_id,version)。不能只统计直接 CallExpression。

**API 引用/调用集合**：`closureRequests`, `processClosureRequest`, `rejectClosureRequest`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 83 | `select` | value={status} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 33 | `iamApi.closureRequests(offset, 50, status)` |

**错误接入证据**：`38:setError("")`；`44:setError(cause.message)`；`69:} catch (cause) {`.

### FE-027 — app/principals/lifecycle-dialog.tsx

无 form：保留期限日期强制T00:00:00Z、legal_hold/reference/version→setPrincipalRetention；anonymize confirmation/version；confirmation匹配登录名是危险操作确认，应保留，同时声明字段规则；日期含义明确保留UTC午夜。

**API 引用/调用集合**：`anonymizePrincipal`, `principalLifecycle`, `setPrincipalRetention`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 176 | `Input` | id="retain-until"; type="date"; value={retainUntil}; disabled={busy} |
| 188 | `Input` | id="retention-reference"; value={reference}; maxLength={200}; disabled={busy} |
| 197 | `Checkbox` | checked={legalHold}; disabled={busy} |
| 218 | `Input` | id="anonymize-confirmation"; value={confirmation}; disabled={busy \|\| blocked} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 64 | `iamApi.principalLifecycle(id)` |
| 87 | `iamApi.setPrincipalRetention(principal.id, { version: status.lifecycle.version, retain_until: new Date(\`${retainUntil}T00:00:00Z\`).toISOString(), legal_hold: legalHold, reference, })` |
| 108 | `iamApi.anonymizePrincipal(principal.id, { version: status.lifecycle.version, confirmation, })` |

**转换/构造证据**：`39:return parsed.toISOString().slice(0, 10)`；`89:retain_until: new Date(\`${retainUntil}T00:00:00Z\`).toISOString(),`；`124:status !== null && confirmation.trim() === status.login_name`；`206:disabled={busy \|\| !retainUntil \|\| !reference.trim()}`.

**错误接入证据**：`65:setError("")`；`76:void load(principal.id).catch((cause: Error) => setError(cause.message))`；`82:setError("")`；`95:} catch (cause) {`；`96:setError((cause as Error).message)`；`105:setError("")`；`115:} catch (cause) {`；`116:setError((cause as Error).message)`.

### FE-028 — app/principals/page.tsx

create login/password/display_name/email；effectiveFields create/update 决定字段是否渲染及提交，data.has 保留缺省。edit changes 与 department-only路径分开；禁用/恢复无form。字段授权策略不等于字段约束；隐藏省略不得默认填值。

**API 引用/调用集合**：`createPrincipal`, `departments`, `disablePrincipal`, `effectiveFields`, `memberAuthorizationSources`, `members`, `principals`, `restorePrincipal`, `updateMember`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 259 | `Input` | value={search} |
| 491 | `Field` | name="login_name"; required |
| 493 | `Field` | name="display_name"; defaultValue={ fields.display_name?.read === "allow" ? editingMembership?.display_name : undefined }; disabled={ !(editing ? fields.display_name?.update : fields.display_name?.create) }; required={!editing && Boolean(fields.display_name?.create)} |
| 513 | `SimpleSelect` | id="principal-department"; value={departmentID \|\| "__none__"} |
| 538 | `Field` | name="email"; type="email"; defaultValue={ fields.email?.read === "allow" ? editingMembership?.email : undefined }; disabled={ !(editing ? fields.email?.update : fields.email?.create) } |
| 552 | `Field` | name="password"; type="password"; minLength={12}; required |
| 888 | `Input` | id={name}; name={name}; type={type}; required={required}; minLength={minLength}; defaultValue={defaultValue}; disabled={disabled}; maxLength={type === "password" ? 1024 : 320} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 109 | `iamApi.principals(search)` |
| 110 | `iamApi.members()` |
| 111 | `iamApi.departments()` |
| 112 | `iamApi.effectiveFields("user_profile")` |
| 164 | `iamApi.updateMember(editing.id, changes)` |
| 166 | `iamApi.updateMember(editing.id, { department_id: departmentID })` |
| 168 | `iamApi.createPrincipal({ login_name: String(data.get("login_name")), password: String(data.get("password")), ...(data.has("display_name") ? { display_name: String(data.get("display_name")) } : {}), ...(data.has("email") ? { email: String(data.get("email")) } : {}), })` |
| 200 | `iamApi.memberAuthorizationSources(principal.id)` |
| 215 | `iamApi.disablePrincipal(principal.id)` |
| 216 | `iamApi.restorePrincipal(principal.id)` |

**转换/构造证据**：`56:createIamApi,`；`82:const iamApi = useMemo(() => createIamApi(undefined, () => tenant), [tenant])`；`148:const data = new FormData(event.currentTarget)`.

**错误接入证据**：`147:setError("")`；`180:} catch (cause) {`；`181:setError((cause as Error).message)`；`201:} catch (cause) {`；`212:setError("")`；`218:} catch (cause) {`；`219:setError((cause as Error).message)`.

### FE-029 — app/recover/page.tsx

同一 form 按 token 切换 startPasswordRecovery(identifier.trim(),entry) / completePasswordRecovery(token,new_password)；confirmation 纯 UI。开始与完成不同契约；URL token 为隐藏字段。

**API 引用/调用集合**：`completePasswordRecovery`, `startPasswordRecovery`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 86 | `ResetFields` | 无静态name/约束；按父级state映射 |
| 86 | `IdentifierField` | 无静态name/约束；按父级state映射 |
| 124 | `Input` | id="recovery-identifier"; name="identifier"; required; maxLength={320} |
| 143 | `Input` | id="recovery-password"; name="new_password"; type="password"; required; minLength={12}; maxLength={1024} |
| 156 | `Input` | id="recovery-confirm"; name="confirm_password"; type="password"; required; minLength={12}; maxLength={1024} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 33 | `iamApi.startPasswordRecovery( String(data.get("identifier") ?? "").trim(), entry ?? undefined )` |
| 43 | `iamApi.completePasswordRecovery({ token, new_password: password, })` |

**转换/构造证据**：`30:const data = new FormData(event.currentTarget)`；`34:String(data.get("identifier") ?? "").trim(),`.

**错误接入证据**：`29:setError("")`；`40:setError(t("passwordMismatch"))`；`49:} catch (cause) {`；`50:setError(t(recoveryError(cause, Boolean(token))))`.

### FE-030 — app/register/page.tsx

email/display_name trim，password原样；entry + captcha_type/id/answer 条件载荷。captcha refresh另有创建接口；confirmation保留纯UI。当前 minLength12 不能继续比后端字符/字节规则更严格，兼容样例必须涵盖多字节。

**API 引用/调用集合**：`capabilities`, `register`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 156 | `Input` | id="registration-email"; name="email"; type="email"; required; maxLength={320} |
| 168 | `Input` | id="registration-name"; name="display_name"; maxLength={128} |
| 175 | `PasswordField` | id="registration-password"; name="password" |
| 182 | `PasswordField` | id="registration-confirm"; name="confirm_password" |
| 202 | `Input` | id="registration-captcha"; name="captcha_answer"; value={answer}; required; maxLength={16} |
| 284 | `Input` | id={id}; name={name}; type={visible ? "text" : "password"}; required; minLength={12}; maxLength={1024} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 49 | `iamApi .capabilities()` |
| 92 | `iamApi.register({ ...(entry ? { entry } : {}), email: String(data.get("email") ?? "").trim(), password, display_name: String(data.get("display_name") ?? "").trim(), ...(delegated ? { captcha_type: "turnstile", captcha_id: "widget" } : { captcha_type: captcha!.type, captcha_id: captcha!.captcha_id }), captcha_answer: answer.trim(), })` |

**转换/构造证据**：`84:const data = new FormData(event.currentTarget)`；`94:email: String(data.get("email") ?? "").trim(),`；`96:display_name: String(data.get("display_name") ?? "").trim(),`；`100:captcha_answer: answer.trim(),`.

**错误接入证据**：`56:setError(t("captchaUnavailable"))`；`83:setError("")`；`87:setError(t("passwordMismatch"))`；`103:} catch (cause) {`；`104:setError(t(registrationError(cause)))`；`195:onUnavailable={() => setError(t("captchaUnavailable"))}`.

### FE-031 — app/roles/page.tsx

两个 form：角色名称说明、permission condition builder；其余 permission toggle、成员增删/expiry、directory bindings、data-scope保存、删除全是按钮流。system alias endpoints与普通不同；selected_departments 才发送IDs否则[]；condition Time/zone/ACR与实体复用helper，不能只迁移两个form。

**API 引用/调用集合**：`addRoleDirectoryBinding`, `addRoleMember`, `addSystemRoleMember`, `clearRolePermissionCondition`, `createRole`, `createSystemRole`, `deleteRole`, `deleteSystemRole`, `departments`, `grantPermission`, `grantSystemRolePermission`, `groups`, `members`, `permissions`, `removeRoleDirectoryBinding`, `removeRoleMember`, `removeSystemRoleMember`, `revokePermission`, `revokeSystemRolePermission`, `roleDataScope`, `roleDirectoryBindings`, `roleMemberGrants`, `roleMembers`, `rolePermissionGrants`, `roles`, `setRoleDataScope`, `setRolePermissionCondition`, `systemDepartments`, `systemMembers`, `systemRoleMembers`, `systemRolePermissionCatalog`, `systemRolePermissionGrants`, `systemRoles`, `updateRole`, `updateSystemRole`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 747 | `SimpleSelect` | id="role-data-scope"; value={scopeDraft}; disabled={busy \|\| !allowed("role_update")} |
| 780 | `Checkbox` | checked={checked}; disabled={ busy \|\| !allowed("role_update") \|\| (department.status !== "active" && !checked) } |
| 870 | `Checkbox` | checked={boundMembers.includes(member.subject)}; disabled={ busy \|\| !allowed("role_manage_member") \|\| (system && (selected.builtin \|\| member.protected)) \|\| member.status !== "active" } |
| 968 | `Checkbox` | checked={Boolean(grant)}; disabled={ busy \|\| selected.builtin \|\| loadingDetails \|\| !allowed("role_grant_permission") } |
| 1079 | `SimpleSelect` | id="permission-minimum-acr"; value={conditionDraft.minimumAcr} |
| 1110 | `Input` | id="permission-client-id"; value={conditionDraft.clientID}; maxLength={255} |
| 1127 | `Input` | id="permission-network-zone"; value={conditionDraft.networkZone}; maxLength={255} |
| 1142 | `Input` | id="permission-timezone"; value={conditionDraft.timezone}; disabled={!conditionDraft.timeStart && !conditionDraft.timeEnd}; required={Boolean( conditionDraft.timeStart \|\| conditionDraft.timeEnd )}; maxLength={64} |
| 1160 | `Input` | id="permission-time-start"; type="time"; value={conditionDraft.timeStart}; required={Boolean(conditionDraft.timeEnd)} |
| 1175 | `Input` | id="permission-time-end"; type="time"; value={conditionDraft.timeEnd}; required={Boolean(conditionDraft.timeStart)} |
| 1287 | `Input` | id="role-name"; name="name"; defaultValue={editing?.name}; required; maxLength={128} |
| 1297 | `Input` | id="role-description"; name="description"; defaultValue={editing?.description}; maxLength={4096} |
| 1376 | `Input` | type="datetime-local"; value={draft}; disabled={disabled} |
| 1481 | `Checkbox` | checked={checked}; disabled={busy \|\| (item.status !== "active" && !checked)} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 165 | `api.roles()` |
| 165 | `api.permissions()` |
| 189 | `api.rolePermissionGrants(selected.id)` |
| 190 | `api.roleMembers(selected.id)` |
| 193 | `iamApi.roleMemberGrants(selected.id)` |
| 194 | `api.members()` |
| 195 | `iamApi.roleDirectoryBindings(selected.id)` |
| 196 | `iamApi.groups()` |
| 204 | `iamApi.roleDataScope(selected.id)` |
| 205 | `api.departments()` |
| 327 | `api.updateRole(editing.id, body)` |
| 328 | `api.createRole(body)` |
| 347 | `api.grantPermission(selected.id, code)` |
| 348 | `api.revokePermission(selected.id, code)` |
| 349 | `api.rolePermissionGrants(selected.id)` |
| 380 | `iamApi.setRolePermissionCondition( selected.id, conditionPermission.code, condition )` |
| 385 | `iamApi.rolePermissionGrants(selected.id)` |
| 404 | `iamApi.clearRolePermissionCondition( selected.id, conditionPermission.code )` |
| 408 | `iamApi.rolePermissionGrants(selected.id)` |
| 426 | `api.roleMembers(selected.id)` |
| 429 | `iamApi.roleMemberGrants(selected.id)` |
| 452 | `api.addRoleMember(selected.id, subject, end)` |
| 466 | `api.addRoleMember(selected.id, subject)` |
| 467 | `api.removeRoleMember(selected.id, subject)` |
| 486 | `iamApi.addRoleDirectoryBinding( selected.id, assigneeType, assigneeID )` |
| 492 | `iamApi.removeRoleDirectoryBinding( selected.id, assigneeType, assigneeID )` |
| 497 | `iamApi.roleDirectoryBindings(selected.id)` |
| 515 | `api.deleteRole(selected.id)` |
| 533 | `iamApi.setRoleDataScope(selected.id, { scope: scopeDraft, department_ids: scopeDraft === "selected_departments" ? scopeDepartmentIDs : [], })` |

**转换/构造证据**：`320:const data = new FormData(event.currentTarget)`；`1202:{conditionDraft.clientID.trim() && (`；`1205:client: conditionDraft.clientID.trim(),`；`1209:{conditionDraft.networkZone.trim() && (`；`1212:zone: conditionDraft.networkZone.trim(),`；`1390:onChange(draft ? new Date(draft).toISOString() : null)`.

**错误接入证据**：`171:.catch((cause: Error) => setLoadError(cause.message)),`；`319:setError("")`；`335:} catch (cause) {`；`336:setError((cause as Error).message)`；`345:setError("")`；`357:} catch (cause) {`；`358:setError((cause as Error).message)`；`378:setError("")`；`392:} catch (cause) {`；`393:setError((cause as Error).message)`；`402:setError("")`；`415:} catch (cause) {`；`416:setError((cause as Error).message)`；`450:setError("")`；`454:} catch (cause) {`；`455:setError((cause as Error).message)`；`464:setError("")`；`469:} catch (cause) {`；`470:setError((cause as Error).message)`；`483:setError("")`；`503:} catch (cause) {`；`504:setError((cause as Error).message)`；`513:setError("")`；`520:} catch (cause) {`；`521:setError((cause as Error).message)`；`531:setError("")`；`542:} catch (cause) {`；`543:setError((cause as Error).message)`.

### FE-032 — app/scim-directories/page.tsx

directory create/edit与credential两个form：trim name，service_account_id空→null；edit status/version；credential local datetime→UTC optional expires_at；revoke按钮。这里是JSON管理API，不是原始SCIM协议，不能整体标server_only。

**API 引用/调用集合**：`createSCIMCredential`, `createSCIMDirectory`, `revokeSCIMCredential`, `scimCredentials`, `scimDirectories`, `serviceAccounts`, `updateSCIMDirectory`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 607 | `Input` | id="scim-directory-name"; value={draft.name}; required; minLength={1}; maxLength={128} |
| 623 | `Input` | id="scim-directory-service-account"; value={draft.serviceAccountId}; pattern="[1-9][0-9]*"; maxLength={64}; disabled={busy} |
| 728 | `Input` | id="scim-credential-name"; value={draft.name}; required; minLength={1}; maxLength={128}; disabled={disabled} |
| 744 | `Input` | id="scim-credential-expiry"; type="datetime-local"; value={draft.expiresAt}; min={localDateTime(new Date().toISOString())}; disabled={disabled} |
| 929 | `SecretField` | value={endpoint} |
| 934 | `SecretField` | value={value?.bearer_token ?? ""} |
| 965 | `Input` | value={value} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 159 | `api.scimDirectories()` |
| 204 | `api.scimDirectories()` |
| 223 | `api.updateSCIMDirectory(editing.id, { name: directoryDraft.name.trim(), status: directoryDraft.enabled ? "active" : "disabled", version: editing.version, service_account_id: directoryDraft.serviceAccountId.trim() \|\| null, })` |
| 231 | `api.createSCIMDirectory({ name: directoryDraft.name.trim(), service_account_id: directoryDraft.serviceAccountId.trim() \|\| null, })` |
| 255 | `api.scimCredentials(directory.id)` |
| 269 | `api.createSCIMCredential(credentialDirectory.id, { name: credentialDraft.name.trim(), expires_at: isoDateTime(credentialDraft.expiresAt), })` |
| 287 | `api.revokeSCIMCredential(credentialDirectory.id, revoking.id)` |
| 288 | `api.scimCredentials(credentialDirectory.id)` |
| 565 | `api.serviceAccounts(draft.serviceAccountId)` |

**转换/构造证据**：`59:createIamApi,`；`132:const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])`；`224:name: directoryDraft.name.trim(),`；`227:service_account_id: directoryDraft.serviceAccountId.trim() \|\| null,`；`232:name: directoryDraft.name.trim(),`；`233:service_account_id: directoryDraft.serviceAccountId.trim() \|\| null,`；`270:name: credentialDraft.name.trim(),`；`545:api: ReturnType<typeof createIamApi>`；`751:min={localDateTime(new Date().toISOString())}`；`1063:return new Date(date.getTime() - offset).toISOString().slice(0, 16)`；`1067:return value ? new Date(value).toISOString() : undefined`.

**错误接入证据**：`161:} catch (cause) {`；`182:setError("")`；`189:setError("")`；`202:setError("")`；`209:} catch (cause) {`；`210:setError((cause as Error).message)`；`219:setError("")`；`239:} catch (cause) {`；`242:setError((cause as Error).message)`；`253:setError("")`；`256:} catch (cause) {`；`257:setError((cause as Error).message)`；`267:setError("")`；`275:} catch (cause) {`；`276:setError((cause as Error).message)`；`285:setError("")`；`291:} catch (cause) {`；`292:setError((cause as Error).message)`；`302:} catch {`；`303:setError(clientErrorMessage("clipboard_access_denied"))`.

### FE-033 — app/scim-directories/targets.tsx

target form + push/deprovision resource form；name/base_url trim，service_account_id空→null；edit token空则省略，status/version；create必含bearer_token。delete/retire按钮及resource_type切换资源目录全部纳入；原始令牌不出错误值。

**API 引用/调用集合**：`createSCIMTarget`, `deleteSCIMTarget`, `deprovisionSCIMResource`, `groups`, `principals`, `pushSCIMResource`, `retireSCIMTarget`, `scimTargets`, `serviceAccounts`, `updateSCIMTarget`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 468 | `Input` | id="scim-target-name"; value={draft.name}; required; maxLength={128}; disabled={busy \|\| conflict} |
| 481 | `Input` | id="scim-target-url"; type="url"; value={draft.url}; required; maxLength={1024}; disabled={busy \|\| conflict} |
| 497 | `Input` | id="scim-target-token"; type="password"; value={draft.token}; required={!editing}; maxLength={4096}; disabled={busy \|\| conflict} |
| 514 | `Input` | id="scim-target-account"; value={draft.account}; pattern="[1-9][0-9]*"; maxLength={64}; disabled={busy \|\| conflict} |
| 589 | `SimpleSelect` | id="scim-resource-type"; value={resource.resource_type}; disabled={busy} |
| 613 | `Input` | id="scim-resource-id"; value={resource.resource_id}; required; pattern="[1-9][0-9]*"; maxLength={64}; disabled={busy} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 95 | `api.scimTargets()` |
| 105 | `api .scimTargets()` |
| 124 | `api.serviceAccounts(draft.account)` |
| 147 | `api.principals()` |
| 153 | `api .groups()` |
| 196 | `api.scimTargets()` |
| 220 | `api.updateSCIMTarget(editing.id, { ...body, status: draft.enabled ? "active" : "disabled", version: editing.version, ...(draft.token ? { bearer_token: draft.token } : {}), })` |
| 227 | `api.createSCIMTarget({ ...body, bearer_token: draft.token })` |
| 245 | `api.deleteSCIMTarget(deleting.id)` |
| 264 | `api.retireSCIMTarget(deleting.id)` |
| 292 | `api.deprovisionSCIMResource(operation.target.id, body)` |
| 295 | `api.pushSCIMResource(operation.target.id, body)` |

**转换/构造证据**：`43:createIamApi,`；`68:const api = useMemo(() => createIamApi(undefined, () => tenant), [tenant])`；`215:name: draft.name.trim(),`；`216:base_url: draft.url.trim(),`；`217:service_account_id: draft.account.trim() \|\| null,`；`290:const body = { ...resource, resource_id: resource.resource_id.trim() }`.

**错误接入证据**：`97:} catch (cause) {`；`114:(cause: Error) => active && setError(cause.message)`；`168:setError("")`；`194:setError("")`；`201:} catch (cause) {`；`202:setError((cause as Error).message)`；`211:setError("")`；`232:} catch (cause) {`；`235:setError((cause as Error).message)`；`243:setError("")`；`250:} catch (cause) {`；`251:setError((cause as Error).message)`；`262:setError("")`；`269:} catch (cause) {`；`270:setError((cause as Error).message)`；`276:setError("")`；`287:setError("")`；`299:} catch (cause) {`；`300:setError((cause as Error).message)`；`319:setError("")`；`322:.catch((cause: Error) => setError(cause.message))`；`429:setError("")`.

### FE-034 — app/security/page.tsx

5处form语法实际承载手机号验证码、passkey register/rename/delete三分支、改密、MFA begin/regenerate/disable共享密码form与MFA confirm。还有revokeSession/logoutAll/startEmailVerification/startPhoneVerification。WebAuthn credential对象由浏览器产生；当前password/code/name限制和6位strip/length逻辑逐个分类迁移，不能机械改成新密码强度。

**API 引用/调用集合**：`beginPasskeyRegistration`, `beginTotpEnrollment`, `changePassword`, `completePhoneVerification`, `confirmTotpEnrollment`, `deletePasskey`, `disableTotp`, `finishPasskeyRegistration`, `logoutAll`, `mfaStatus`, `passkeys`, `regenerateRecoveryCodes`, `renamePasskey`, `revokeSession`, `sessions`, `startEmailVerification`, `startPhoneVerification`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 539 | `Input` | id="phone_code"; maxLength={6}; value={code} |
| 706 | `Input` | id={`${formID}-name`}; name="name"; defaultValue={ state?.type === "rename" ? state.passkey.name : "" }; required; maxLength={200} |
| 719 | `PasswordField` | id={`${formID}-password`} |
| 897 | `PasswordField` | id="current-password" |
| 900 | `Input` | id="new-password"; name="new_password"; type="password"; required; minLength={12}; maxLength={1024} |
| 1032 | `PasswordField` | id={`${id}-password`} |
| 1033 | `CodeField` | id={`${id}-code`} |
| 1090 | `CodeField` | id="mfa-confirm-code" |
| 1162 | `Input` | id={id}; name="current_password"; type="password"; required; maxLength={1024} |
| 1188 | `Input` | id={id}; name="code"; required; minLength={6}; maxLength={64} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 88 | `iamApi.mfaStatus()` |
| 91 | `iamApi.sessions()` |
| 92 | `iamApi.passkeys()` |
| 103 | `iamApi.sessions()` |
| 106 | `iamApi.passkeys()` |
| 116 | `iamApi.mfaStatus()` |
| 140 | `iamApi.revokeSession(session.id)` |
| 150 | `iamApi.logoutAll()` |
| 157 | `iamApi.startEmailVerification()` |
| 163 | `iamApi.startPhoneVerification()` |
| 169 | `iamApi.completePhoneVerification(code)` |
| 179 | `iamApi.changePassword({ current_password: String(data.get("current_password") ?? ""), new_password: String(data.get("new_password") ?? ""), })` |
| 195 | `iamApi.beginTotpEnrollment( String(data.get("current_password") ?? "") )` |
| 209 | `iamApi.confirmTotpEnrollment( String(data.get("code") ?? "").trim() )` |
| 233 | `iamApi.regenerateRecoveryCodes(input)` |
| 237 | `iamApi.disableTotp(input)` |
| 258 | `iamApi.beginPasskeyRegistration( String(data.get("current_password") ?? "") )` |
| 262 | `iamApi.finishPasskeyRegistration({ challenge_id: options.challenge_id, name: String(data.get("name") ?? "").trim(), credential, })` |
| 281 | `iamApi.renamePasskey(id, String(data.get("name") ?? "").trim())` |
| 295 | `iamApi.deletePasskey(id, String(data.get("current_password") ?? ""))` |

**转换/构造证据**：`177:const data = new FormData(form)`；`193:const data = new FormData(form)`；`207:const data = new FormData(form)`；`210:String(data.get("code") ?? "").trim()`；`226:const data = new FormData(form)`；`229:code: String(data.get("code") ?? "").trim(),`；`256:const data = new FormData(form)`；`264:name: String(data.get("name") ?? "").trim(),`；`278:const data = new FormData(form)`；`281:await iamApi.renamePasskey(id, String(data.get("name") ?? "").trim())`；`292:const data = new FormData(form)`.

**错误接入证据**：`100:if (active) setError(cause.message)`；`127:setError("")`；`131:} catch (cause) {`；`132:setError((cause as Error).message)`.

### FE-035 — app/service-accounts/page.tsx

account create/edit与credential两个form：trim字符串，可选description/display_name→undefined，edit含version/status；credential scopes拆分并现有非空检查，expiresAt→UTC optional。delete携带version、revoke凭证按钮均纳入。

**API 引用/调用集合**：`createServiceAccount`, `createServiceAccountCredential`, `deleteServiceAccount`, `revokeServiceAccountCredential`, `serviceAccountCredentials`, `serviceAccounts`, `updateServiceAccount`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 547 | `Field` | 无静态name/约束；按父级state映射 |
| 548 | `Input` | id="service-account-login"; value={draft.loginName}; disabled={Boolean(account) \|\| busy}; required; maxLength={200} |
| 559 | `Field` | 无静态name/约束；按父级state映射 |
| 560 | `Input` | id="service-account-display"; value={draft.displayName}; disabled={busy}; required={Boolean(account)}; maxLength={128} |
| 571 | `Field` | 无静态name/约束；按父级state映射 |
| 572 | `Textarea` | id="service-account-description"; value={draft.description}; disabled={busy}; maxLength={1000} |
| 583 | `Field` | 无静态name/约束；按父级state映射 |
| 584 | `Input` | id="service-account-expiry"; type="datetime-local"; value={draft.expiresAt}; disabled={busy} |
| 663 | `Field` | 无静态name/约束；按父级state映射 |
| 664 | `Input` | id="service-credential-name"; value={draft.name}; disabled={busy}; required; maxLength={128} |
| 675 | `Field` | 无静态name/约束；按父级state映射 |
| 676 | `Input` | id="service-credential-scopes"; value={draft.scopes}; disabled={busy}; required |
| 686 | `Field` | 无静态name/约束；按父级state映射 |
| 687 | `Input` | id="service-credential-expiry"; type="datetime-local"; value={draft.expiresAt}; disabled={busy} |
| 840 | `SecretField` | value={value?.clientID ?? ""} |
| 845 | `SecretField` | value={value?.clientSecret ?? ""} |
| 873 | `Input` | value={value} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 125 | `iamApi.serviceAccounts()` |
| 174 | `iamApi.updateServiceAccount(editing.id, body)` |
| 183 | `iamApi.createServiceAccount(body)` |
| 200 | `iamApi.deleteServiceAccount(deleting.id, deleting.version)` |
| 219 | `iamApi.serviceAccountCredentials(account.id)` |
| 239 | `iamApi.createServiceAccountCredential( credentialAccount.id, { name: credentialDraft.name.trim(), scopes, expires_at: isoDateTime(credentialDraft.expiresAt), } )` |
| 264 | `iamApi.revokeServiceAccountCredential( credentialAccount.id, revokeCredential.id )` |
| 269 | `iamApi.serviceAccountCredentials(credentialAccount.id)` |

**转换/构造证据**：`168:display_name: draft.displayName.trim(),`；`169:description: draft.description.trim() \|\| undefined,`；`178:login_name: draft.loginName.trim(),`；`179:display_name: draft.displayName.trim() \|\| undefined,`；`180:description: draft.description.trim() \|\| undefined,`；`242:name: credentialDraft.name.trim(),`；`985:.map((item) => item.trim())`；`995:return new Date(date.getTime() - offset).toISOString().slice(0, 16)`；`999:return value ? new Date(value).toISOString() : undefined`.

**错误接入证据**：`127:} catch (cause) {`；`148:setError("")`；`154:setError("")`；`162:setError("")`；`188:} catch (cause) {`；`189:setError((cause as Error).message)`；`198:setError("")`；`204:} catch (cause) {`；`205:setError((cause as Error).message)`；`217:setError("")`；`220:} catch (cause) {`；`221:setError((cause as Error).message)`；`233:setError(t("credentialScopeRequired"))`；`237:setError("")`；`252:} catch (cause) {`；`253:setError((cause as Error).message)`；`262:setError("")`；`273:} catch (cause) {`；`274:setError((cause as Error).message)`.

### FE-036 — app/system/accounts.tsx

create/update system principal form；profile由display_name/email输入组装，create另含login/password。SystemMembershipDialog负责独立成员/角色写入，不能将其算成本form自动覆盖。

**API 引用/调用集合**：`createSystemPrincipal`, `systemAccountOwners`, `systemDepartments`, `systemMembers`, `systemPrincipals`, `systemRoles`, `updateSystemPrincipal`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 240 | `Input` | value={search} |
| 250 | `SimpleSelect` | id="account-owner"; value={owner \|\| "__all__"} |
| 268 | `SimpleSelect` | id="account-status"; value={status \|\| "__all__"} |
| 451 | `Input` | id="system-account-login"; name="login_name"; required |
| 457 | `Input` | id="system-account-password"; type="password"; name="password"; required |
| 469 | `Input` | id="system-account-name"; name="display_name"; defaultValue={editing?.display_name ?? ""} |
| 477 | `Input` | id="system-account-email"; name="email"; type="email"; defaultValue={editing?.email ?? ""} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 92 | `iamApi.systemAccountOwners(category)` |
| 110 | `iamApi.systemMembers()` |
| 112 | `iamApi.systemDepartments()` |
| 115 | `iamApi.systemRoles()` |
| 143 | `iamApi.systemPrincipals(search, offset, { category, owner_id: owner \|\| undefined, status: status \|\| undefined, })` |
| 179 | `iamApi.updateSystemPrincipal(editing.id, profile)` |
| 181 | `iamApi.createSystemPrincipal({ ...profile, login_name: String(form.get("login_name")), password: String(form.get("password")), })` |

**转换/构造证据**：`171:const form = new FormData(event.currentTarget)`.

**错误接入证据**：`99:if (active) setError(cause.message)`；`121:} catch (cause) {`；`122:setError((cause as Error).message)`；`141:setError("")`；`151:} catch (cause) {`；`153:setError((cause as Error).message)`；`173:setError("")`；`188:} catch (cause) {`；`189:setError((cause as Error).message)`.

### FE-037 — app/system/layout.tsx

只读运营/系统权限导航；页面参数选择不是业务写表单。metadata上下文和权限缓存隔离仍需要回归。

**API 引用/调用集合**：`myOperatingPlatforms`, `myTenants`, `operatingNavigation`, `operatingPermissions`, `systemNavigation`, `systemPermissions`

| 调用位置 | 实际参数表达式 |
|---|---|
| 71 | `iamApi.myTenants()` |
| 72 | `iamApi.myOperatingPlatforms()` |
| 73 | `iamApi.systemPermissions()` |
| 97 | `iamApi.operatingPermissions(platformID)` |
| 98 | `iamApi.systemPermissions()` |
| 100 | `iamApi.operatingNavigation(platformID)` |
| 101 | `iamApi.systemNavigation()` |

**错误接入证据**：当前文件无本地catch/setError，错误由父回调、共享provider或只读fallback承接；见FE台账。

### FE-038 — app/system/operating-platforms.tsx

create slug/name/owner_principal_id；edit name/status/version不发送slug/owner；delete path+version。隐藏字段与可编辑字段映射显式保留。

**API 引用/调用集合**：`createOperatingPlatform`, `deleteOperatingPlatform`, `operatingPlatforms`, `updateOperatingPlatform`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 200 | `Input` | id="operating-slug"; name="slug"; required; minLength={3}; maxLength={63} |
| 211 | `Input` | id="operating-name"; name="name"; defaultValue={editing?.name ?? ""}; required; maxLength={128} |
| 222 | `Input` | id="operating-owner"; name="owner_principal_id"; defaultValue={session?.subject ?? ""}; required |
| 232 | `SimpleSelect` | id="operating-status"; value={status} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 44 | `iamApi.operatingPlatforms()` |
| 68 | `iamApi.updateOperatingPlatform(editing.id, { name: String(data.get("name")), status, version: editing.version, })` |
| 74 | `iamApi.createOperatingPlatform({ slug: String(data.get("slug")), name: String(data.get("name")), owner_principal_id: String(data.get("owner_principal_id")), })` |
| 92 | `iamApi.deleteOperatingPlatform(deleting.id, deleting.version)` |

**转换/构造证据**：`65:const data = new FormData(event.currentTarget)`.

**错误接入证据**：`45:setError("")`；`46:} catch (cause) {`；`47:setError((cause as Error).message)`；`64:setError("")`；`82:} catch (cause) {`；`83:setError((cause as Error).message)`；`96:} catch (cause) {`；`97:setError((cause as Error).message)`.

### FE-039 — app/system/owner-members.tsx

无原生form：owner选择读取members/departments/roles，写动作在SystemMembershipDialog且scope.kind决定平台/tenant endpoint；该入口需一并做集成验收。

**API 引用/调用集合**：`systemAccountOwners`, `systemOwnerDepartments`, `systemOwnerMembers`, `systemOwnerRoles`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 128 | `Input` | value={search} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 61 | `iamApi.systemOwnerMembers(scope)` |
| 62 | `iamApi.systemOwnerDepartments(scope)` |
| 63 | `iamApi.systemOwnerRoles(scope)` |
| 75 | `iamApi.systemAccountOwners(kind)` |

**错误接入证据**：`58:setError("")`；`69:} catch (cause) {`；`70:if (current === generation.current) setError((cause as Error).message)`；`81:if (active) setError(cause.message)`.

### FE-040 — app/system/platform-members.tsx

新增form principal_id→body.subject，另有display_name/email；状态按钮 updateMember(status)。platformID作为路径上下文，不可把DOM name principal_id直接套到/subject之外。

**API 引用/调用集合**：`addOperatingMember`, `members`, `updateMember`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 159 | `Input` | id="platform-member-id"; name="principal_id"; required |
| 163 | `Input` | id="platform-member-name"; name="display_name" |
| 167 | `Input` | id="platform-member-email"; type="email"; name="email" |

| 调用位置 | 实际参数表达式 |
|---|---|
| 39 | `iamApi.members()` |
| 61 | `iamApi.addOperatingMember(platformID, { subject: String(data.get("principal_id")), display_name: String(data.get("display_name")), email: String(data.get("email")), })` |
| 77 | `iamApi.updateMember(item.subject, { status: item.status === "active" ? "disabled" : "active", })` |

**转换/构造证据**：`59:const data = new FormData(event.currentTarget)`.

**错误接入证据**：`40:setError("")`；`41:} catch (cause) {`；`42:setError((cause as Error).message)`；`68:} catch (cause) {`；`69:setError((cause as Error).message)`；`81:} catch (cause) {`；`82:setError((cause as Error).message)`.

### FE-041 — app/tenants/page.tsx

create slug/name+state ownerID；edit name/status/version；删除版本。includeDeleted/search/pagination传GET query，不全是纯本地过滤；搜索字段约束归共享请求边界。

**API 引用/调用集合**：`createTenant`, `deleteTenant`, `principals`, `tenantPage`, `updateTenant`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 221 | `Input` | value={search} |
| 237 | `Checkbox` | id="include-deleted-tenants"; checked={includeDeleted} |
| 409 | `Input` | id="tenant-name"; name="name"; defaultValue={editing?.name}; maxLength={128}; required |
| 419 | `Input` | id="tenant-slug"; name="slug"; defaultValue={editing?.slug}; minLength={3}; maxLength={63}; `pattern="[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?"`; disabled={Boolean(editing)}; required={!editing} |
| 433 | `SimpleSelect` | id="tenant-owner"; value={ownerID} |
| 451 | `SimpleSelect` | id="tenant-status"; value={status} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 88 | `iamApi .tenantPage({ includeDeleted, search, offset, limit: tenantPageSize })` |
| 122 | `iamApi .principals()` |
| 150 | `iamApi.updateTenant(editing.id, { name: String(data.get("name")), status, version: editing.version, })` |
| 156 | `iamApi.createTenant({ slug: String(data.get("slug")), name: String(data.get("name")), owner_principal_id: ownerID, })` |
| 178 | `iamApi.deleteTenant(deleting.id, deleting.version)` |

**转换/构造证据**：`147:const data = new FormData(event.currentTarget)`.

**错误接入证据**：`95:.catch((cause: Error) => {`；`134:.catch((cause: Error) => setError(cause.message))`；`146:setError("")`；`166:} catch (cause) {`；`167:setError((cause as Error).message)`；`176:setError("")`；`182:} catch (cause) {`；`183:setError((cause as Error).message)`；`225:setError("")`；`241:setError("")`.

### FE-042 — app/verify-email/page.tsx

token来自URL，password仅activation-required路径展示；completeEmailVerification(token,password||undefined)。自动/重复打开、缺省密码、失效token都需表单级错误fallback。

**API 引用/调用集合**：`completeEmailVerification`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 105 | `Input` | id="verification-password"; name="password"; type="password"; maxLength={1024} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 62 | `iamApi.completeEmailVerification(token, password \|\| undefined)` |

**转换/构造证据**：`59:new FormData(event.currentTarget).get("password") ?? ""`.

**错误接入证据**：`57:setError("")`；`64:} catch (cause) {`；`65:setError(`.

### FE-043 — components/account-view-banner.tsx

无form：退出按钮与到期timer→exitAccountView→DELETE /account-views/{id}；保持先清本地状态、撤销best-effort。既有撤销失败继续被抑制，不增加OPTIONS等待、必填校验或阻断提示；accountViewExitFailed仅保留现有外层异常路径，不将被抑制的DELETE失败转为可见错误。无可编辑字段，不要求退出执行元数据预检。

**API 引用/调用集合**：本文件经回调或本地交互；映射见台账与跨文件链。

**错误接入证据**：`26:setError("")`；`29:} catch {`；`30:setError(t("accountViewExitFailed"))`.

### FE-044 — components/account-view-dialog.tsx

无form：scope/mode选择→enter→issueAccountView(selection)，下游lib/account-view.ts直接fetch；GET候选iamApi.accountViewWorkspaces。organization模式隐藏scope；条件entity需要tenant。在现有transport注入共同规范化；dialog catch保留cause并回填字段/全局可重译issue。dialog打开时预取metadata，openAccountViewTab必须仍在首个await前同步window.open；不能先等待异步校验导致弹窗被阻止。不能同步完成的预检在既有开窗/握手内完成，保留原失败撤销且不重放发行请求。

**API 引用/调用集合**：`accountViewWorkspaces`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 161 | `SimpleSelect` | id="account-view-scope"; value={`${scope}:${scopeID}`}; disabled={loading} |
| 185 | `SimpleSelect` | id="account-view-mode"; value={mode} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 114 | `iamApi .accountViewWorkspaces(principalID)` |

**转换/构造证据**：`212:!scopeID.trim() \|\|`；`213:(scope === "entity" && !tenantID.trim())`.

**错误接入证据**：`55:setError("")`；`71:} catch {`；`72:setError(t("accountViewOpenFailed"))`；`100:setError("")`；`135:setError(t("accountViewOpenFailed"))`.

### FE-045 — components/auth-provider.tsx

login页面委托的真实入口：login/requestPhoneLoginCode/loginWithPhoneCode/verifyLoginMfa/requestMfaPhoneCode/begin+finishPasskeyLogin/logout；trim输入、captchas、return URL及credential注入发生这里。让校验和发送消费同一快照，并把issues交还原page边界。

**API 引用/调用集合**：`beginPasskeyLogin`, `finishPasskeyLogin`, `login`, `loginWithPhoneCode`, `logout`, `requestMfaPhoneCode`, `requestPhoneLoginCode`, `session`, `verifyLoginMfa`

| 调用位置 | 实际参数表达式 |
|---|---|
| 41 | `iamApi.session()` |
| 55 | `iamApi.session()` |
| 91 | `iamApi.login({ login_name: loginName.trim(), password, return_url: returnUrl, captcha_type: captcha.type, captcha_id: captcha.id, captcha_answer: captcha.answer.trim(), })` |
| 109 | `iamApi.requestPhoneLoginCode({ phone: phone.trim(), captcha_type: captcha.type, captcha_id: captcha.id, captcha_answer: captcha.answer.trim(), })` |
| 121 | `iamApi.loginWithPhoneCode({ phone: phone.trim(), code: code.trim(), return_url: returnUrl, })` |
| 131 | `iamApi.requestMfaPhoneCode(challengeId)` |
| 138 | `iamApi.verifyLoginMfa({ challenge_id: challengeId, code })` |
| 145 | `iamApi.beginPasskeyLogin(returnUrl)` |
| 148 | `iamApi.finishPasskeyLogin({ challenge_id: options.challenge_id, credential, })` |
| 169 | `iamApi.logout()` |

**转换/构造证据**：`92:login_name: loginName.trim(),`；`97:captcha_answer: captcha.answer.trim(),`；`110:phone: phone.trim(),`；`113:captcha_answer: captcha.answer.trim(),`；`122:phone: phone.trim(),`；`123:code: code.trim(),`.

**错误接入证据**：`46:} catch (cause) {`；`170:} catch {`.

### FE-046 — components/avatar-editor.tsx

无form：style/candidate_url保存，上传MIME+2MiB+2048px本地检查→裁剪256×256 PNG data URL→setMyAvatar JSON image_data；不是multipart。将API约束归后端声明，保留合理文件解码/裁剪UI保护并清楚分类；错误当前全局Alert。

**API 引用/调用集合**：`avatarCandidates`, `myAvatar`, `setMyAvatar`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 174 | `input` | type="radio"; name="avatar-style"; value={value}; checked={style === value} |
| 200 | `input` | type="file"; accept="image/png,image/jpeg,image/webp"; disabled={busy} |
| 234 | `input` | type="radio"; name="avatar-candidate"; checked={selected?.url === candidate.url} |
| 295 | `input` | id="avatar-zoom"; type="range"; min={1}; max={3}; step={0.05}; value={zoom}; disabled={busy} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 35 | `iamApi.myAvatar()` |
| 63 | `iamApi.setMyAvatar(input)` |
| 80 | `iamApi.avatarCandidates(style)` |

**转换/构造证据**：`303:onChange={(event) => setZoom(Number(event.target.value))}`.

**错误接入证据**：`42:setError(`；`60:setError("")`；`68:} catch (cause) {`；`69:setError((cause as Error).message)`；`77:setError("")`；`82:} catch (cause) {`；`83:setError((cause as Error).message)`；`94:setError(t("avatarInvalidImage"))`；`98:setError("")`；`118:} catch {`；`119:setError(t("avatarInvalidImage"))`；`153:} catch {`；`154:setError(t("avatarInvalidImage"))`.

### FE-047 — components/directory-members-dialog.tsx

onPut/onDelete回调表单实际只由groups/page使用：selectedPrincipal为path，starts_at/ends_at非空→UTC→条件对象；本地ends>starts检查需同源跨字段规则；computed/static分支与原值不丢失，错误经onError回到父级。

**API 引用/调用集合**：本文件经回调或本地交互；映射见台账与跨文件链。

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 230 | `SimpleSelect` | id={inputPrefix}; value={selectedPrincipal}; disabled={Boolean(editing)} |
| 244 | `Input` | id={`${inputPrefix}-start`}; name="starts_at"; type="datetime-local"; defaultValue={toDateTimeLocal(editing?.starts_at)} |
| 253 | `Input` | id={`${inputPrefix}-end`}; name="ends_at"; type="datetime-local"; defaultValue={toDateTimeLocal(editing?.ends_at)} |

**转换/构造证据**：`30:toISOString,`；`107:const data = new FormData(event.currentTarget)`；`108:const startsAt = toISOString(String(data.get("starts_at")))`；`109:const endsAt = toISOString(String(data.get("ends_at")))`.

**错误接入证据**：当前文件无本地catch/setError，错误由父回调、共享provider或只读fallback承接；见FE台账。

### FE-048 — components/directory-role-bindings-dialog.tsx

无form：角色checkbox/按钮→add/removeRoleDirectoryBinding(role.id,assigneeType,assigneeID)，主语来自父页面group；三个path字段及后端issues表单级显示，不将选择过滤当业务规则。

**API 引用/调用集合**：`addRoleDirectoryBinding`, `removeRoleDirectoryBinding`, `roleDirectoryBindings`, `roles`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 134 | `Checkbox` | id={`directory-role-${assigneeType}-${role.id}`}; checked={checked}; disabled={Boolean(busyRoleID)} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 42 | `iamApi .roles()` |
| 46 | `iamApi.roleDirectoryBindings(role.id)` |
| 78 | `iamApi.addRoleDirectoryBinding(role.id, assigneeType, assigneeID)` |
| 80 | `iamApi.removeRoleDirectoryBinding( role.id, assigneeType, assigneeID )` |
| 85 | `iamApi.roleDirectoryBindings(role.id)` |

**错误接入证据**：`62:.catch((cause: Error) => {`；`63:if (current) setError(cause.message)`；`75:setError("")`；`96:} catch (cause) {`；`97:setError((cause as Error).message)`.

### FE-049 — components/file-browser.tsx

1个editor form承载mkdir/new-empty-file/rename/restore；实际请求由files/page callbacks；还包含file input、多选删除、拖放/递归目录、粘贴/复制移动、冲突逐项decision及rename输入。name trim、空文件扩展补全属于构造；maxlength255迁移；上传、copy/move的非form边界单列绑定。

**API 引用/调用集合**：本文件经回调或本地交互；映射见台账与跨文件链。

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 817 | `Input` | value={query}; disabled={!ready} |
| 830 | `SimpleSelect` | value={kind}; disabled={!ready} |
| 874 | `input` | type="checkbox"; checked={ rows.length > 0 && visibleSelected.length === rows.length }; disabled={!rows.length \|\| busy} |
| 1005 | `input` | type="file"; multiple |
| 1066 | `SimpleSelect` | value={sort}; disabled={!ready} |
| 1171 | `input` | type="checkbox"; checked={selected.has(node.id)}; disabled={busy} |
| 1662 | `Input` | id="file-editor-name"; required; minLength={1}; maxLength={255}; value={editor && "name" in editor ? editor.name : ""} |

**转换/构造证据**：`197:return root.ownerName?.trim() \|\| translate(rootNameMessage(root.scope))`；`468:if (await onCreateDirectory?.(editor.name.trim())) setEditor(null)`；`472:const trimmed = editor.name.trim()`；`480:if (await onRename?.(editor.node, editor.name.trim())) setEditor(null)`；`484:const result = await onRestore?.(editor.node, editor.name.trim())`；`1691:busy \|\| !editor \|\| !("name" in editor) \|\| !editor.name.trim()`.

**错误接入证据**：`566:} catch (cause) {`.

### FE-050 — components/reauthentication-dialog.tsx

form两阶段challenge→verify；method/password/passkey/federation分支，federation使用identity transaction重定向及continuation。purpose/target/proof/credential为隐藏字段；密码原样，不能套注册强度；失败回到共享字段+全局错误。

**API 引用/调用集合**：`capabilities`, `createIdentityLinkTransaction`, `createReauthenticationChallenge`, `identityLinkProviders`, `identityLinks`, `reauthenticationMethods`, `verifyReauthentication`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 198 | `select` | id="reauth-method"; value={method}; disabled={busy} |
| 219 | `Input` | id="reauth-password"; name="password"; type="password"; required; disabled={busy} |
| 230 | `Input` | id="reauth-code"; name="code"; required; disabled={busy} |

| 调用位置 | 实际参数表达式 |
|---|---|
| 49 | `iamApi.capabilities()` |
| 58 | `iamApi.reauthenticationMethods()` |
| 59 | `iamApi.identityLinks()` |
| 60 | `iamApi.identityLinkProviders()` |
| 112 | `iamApi.createReauthenticationChallenge({ purpose, target, method: federation ? "federation" : (method as ReauthenticationMethod), })` |
| 122 | `iamApi.createIdentityLinkTransaction({ provider_id: link.provider_id, existing_link_id: link.id, proof: challenge.proof, return_url: location.origin + location.pathname, })` |
| 148 | `iamApi.verifyReauthentication({ proof: challenge.proof, ...credentials, })` |

**转换/构造证据**：`86:continuation = JSON.parse(raw)`；`107:const data = new FormData(event.currentTarget)`.

**错误接入证据**：`71:setError("")`；`74:if (active) setError(cause.message)`；`87:} catch {`；`98:} catch (cause) {`；`99:setError((cause as Error).message)`；`109:setError("")`；`154:} catch (cause) {`；`155:setError((cause as Error).message)`.

### FE-051 — components/system-membership-dialog.tsx

无form：search选account；save根据scope/system与已有member选择4类create/update，protected时省略status；role checkbox变更选择add/removeSystem[Owner]RoleMember。department/status虽无name需requestPath映射，异步选择切换响应隔离保留。

**API 引用/调用集合**：`addSystemOwnerRoleMember`, `addSystemRoleMember`, `assigned`, `createSystemMember`, `createSystemOwnerMember`, `departments`, `member`, `removeSystemOwnerRoleMember`, `removeSystemRoleMember`, `roles`, `systemDepartments`, `systemMember`, `systemMemberRoles`, `systemOwnerDepartments`, `systemOwnerMember`, `systemOwnerMemberRoles`, `systemOwnerRoles`, `systemPrincipals`, `systemRoles`, `updateSystemMember`, `updateSystemOwnerMember`

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 269 | `Input` | id="system-member-search"; value={search} |
| 274 | `SimpleSelect` | id="system-member-principal"; value={selected?.id ?? ""} |
| 303 | `SimpleSelect` | id="system-member-department"; value={department \|\| "__none__"}; disabled={busy \|\| !(member ? memberAllowed : createAllowed)} |
| 321 | `SimpleSelect` | id="system-member-status"; value={status}; disabled={ busy \|\| member?.protected \|\| !(member ? memberAllowed : createAllowed) } |
| 345 | `Checkbox` | checked={assigned.includes(role.id)}; disabled={ busy \|\| role.builtin \|\| member.protected \|\| !roleAllowed } |

| 调用位置 | 实际参数表达式 |
|---|---|
| 65 | `iamApi.systemOwnerMember(scope, id)` |
| 65 | `iamApi.systemMember(id)` |
| 68 | `iamApi.systemOwnerDepartments(scope)` |
| 69 | `iamApi.systemDepartments()` |
| 71 | `iamApi.systemOwnerRoles(scope)` |
| 71 | `iamApi.systemRoles()` |
| 74 | `iamApi.systemOwnerMemberRoles(scope, id)` |
| 75 | `iamApi.systemMemberRoles(id)` |
| 109 | `iamApi.systemPrincipals(search)` |
| 133 | `api.member(selected.id)` |
| 139 | `api.departments()` |
| 140 | `api.roles()` |
| 142 | `api.assigned(selected.id)` |
| 184 | `iamApi.updateSystemOwnerMember(scope, selected.id, { department_id: department, status, })` |
| 188 | `iamApi.createSystemOwnerMember(scope, { subject: selected.id, display_name: selected.display_name \|\| selected.login_name \|\| selected.id, email: selected.email, department_id: department, status, })` |
| 197 | `iamApi.updateSystemMember(selected.id, { department_id: department, ...(member.protected ? {} : { status }), })` |
| 201 | `iamApi.createSystemMember({ principal_id: selected.id, display_name: selected.display_name \|\| selected.login_name \|\| selected.id, email: selected.email, department_id: department, status, })` |
| 227 | `iamApi.addSystemOwnerRoleMember(scope, role.id, selected.id)` |
| 229 | `iamApi.removeSystemOwnerRoleMember(scope, role.id, selected.id)` |
| 230 | `iamApi.addSystemRoleMember(role.id, selected.id)` |
| 231 | `iamApi.removeSystemRoleMember(role.id, selected.id)` |

**错误接入证据**：`99:setError("")`；`114:if (active) setError(cause.message)`；`133:? api.member(selected.id).catch((cause: unknown) => {`；`142:? api.assigned(selected.id).catch((cause: unknown) => {`；`160:if (active) setError(cause.message)`；`180:setError("")`；`211:} catch (cause) {`；`212:setError((cause as Error).message)`；`223:setError("")`；`239:} catch (cause) {`；`240:setError((cause as Error).message)`.

### FE-052 — components/user-avatar.tsx

头像内容GET，无用户输入/写载荷；不创建表单。blob错误回退不等于validation错误。

**API 引用/调用集合**：`avatarContent`

| 调用位置 | 实际参数表达式 |
|---|---|
| 26 | `iamApi.avatarContent(id, tenant \|\| undefined)` |

**错误接入证据**：当前文件无本地catch/setError，错误由父回调、共享provider或只读fallback承接；见FE台账。

### FE-053 — components/user-menu.tsx

locale/theme仅本地偏好；sessionForTenant用于显示，logout经AuthProvider，退出account-view经lib/account-view；这些共享边界回归，不重复表单。

**API 引用/调用集合**：`myAvatar`, `sessionForTenant`

| 调用位置 | 实际参数表达式 |
|---|---|
| 48 | `iamApi.myAvatar()` |
| 74 | `iamApi.sessionForTenant(tenantID)` |

**转换/构造证据**：`111:displaySession?.preferred_username?.trim() \|\|`；`112:displaySession?.email?.trim() \|\|`；`147:{displaySession?.email?.trim() \|\| session.subject}`.

**错误接入证据**：`95:setError(false)`；`98:} catch {`；`99:setError(true)`.

### FE-054 — components/workspace-switcher.tsx

tenantFilter纯本地过滤 + workspace导航选择：排除字段校验迁移；仍要触发metadata上下文隔离，不能制造不存在的业务接口。

**API 引用/调用集合**：本文件经回调或本地交互；映射见台账与跨文件链。

| 输入位置 | 组件 | name / id / type / value绑定及既有声明 |
|---|---|---|
| 207 | `Input` | value={tenantFilter} |

**转换/构造证据**：`77:const term = tenantFilter.trim().toLowerCase()`.

**错误接入证据**：当前文件无本地catch/setError，错误由父回调、共享provider或只读fallback承接；见FE台账。

## 操作 → 后端路由模板全量索引

覆盖本次扫描所有非测试源文件引用的 **262 个实际 iamApi 方法/辅助成员**（不仅上面UI文件）。地址省略部署base `/api`；`apiRoute` 模板由生成契约约束，实际组织前缀重写见下节。route/body在链接源码处固定；选项/只读加载方法也列出，避免误认写流程缺失。

| 方法 / iam-api.ts位置 | route模板（可能多分支） | 请求体构造线索 |
|---|---|---|
| [acceptInvitation:2902](../../frontend-web/apps/web/src/lib/iam-api.ts#L2902) | `POST /membership-invitations/acceptances` | JSON body |
| [accessRequests:3362](../../frontend-web/apps/web/src/lib/iam-api.ts#L3362) | `GET /tenants/{tenant_id}/role-access-requests` | 无JSON体/查询/透传；精确构造见源码链接 |
| [accessReview:3409](../../frontend-web/apps/web/src/lib/iam-api.ts#L3409) | `GET /role-access-reviews/{review_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [accessReviews:3403](../../frontend-web/apps/web/src/lib/iam-api.ts#L3403) | `GET /tenants/{tenant_id}/role-access-reviews` | 无JSON体/查询/透传；精确构造见源码链接 |
| [accountViewWorkspace:1985](../../frontend-web/apps/web/src/lib/iam-api.ts#L1985) | `GET /account-views/workspace` | 无JSON体/查询/透传；精确构造见源码链接 |
| [accountViewWorkspaces:1993](../../frontend-web/apps/web/src/lib/iam-api.ts#L1993) | `GET /system/principals/{principal_id}/workspaces` | 无JSON体/查询/透传；精确构造见源码链接 |
| [addOperatingMember:1975](../../frontend-web/apps/web/src/lib/iam-api.ts#L1975) | `POST /operating-platforms/{platform_id}/members` | JSON body |
| [addRoleDirectoryBinding:3556](../../frontend-web/apps/web/src/lib/iam-api.ts#L3556) | `PUT /roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [addRoleMember:3521](../../frontend-web/apps/web/src/lib/iam-api.ts#L3521) | `PUT /roles/{role_id}/members/{principal_id}` | JSON { expires_at: expiresAt ?? null } |
| [addSystemOwnerRoleMember:1710](../../frontend-web/apps/web/src/lib/iam-api.ts#L1710) | `PUT /system/operating-platforms/{owner_id}/roles/{role_id}/members/{principal_id}`<br>`PUT /system/tenants/{owner_id}/roles/{role_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [addSystemRoleMember:1828](../../frontend-web/apps/web/src/lib/iam-api.ts#L1828) | `PUT /system/roles/{role_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [anonymizePrincipal:2807](../../frontend-web/apps/web/src/lib/iam-api.ts#L2807) | `PUT /system/principals/{principal_id}/lifecycle/anonymization` | JSON body |
| [approveAccessRequest:3375](../../frontend-web/apps/web/src/lib/iam-api.ts#L3375) | `PUT /role-access-requests/{request_id}/decision` | JSON { decision: "approved", note } |
| [auditAnchor:3982](../../frontend-web/apps/web/src/lib/iam-api.ts#L3982) | `POST /tenants/{tenant_id}/audit/anchors` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditArchiveStatus:3890](../../frontend-web/apps/web/src/lib/iam-api.ts#L3890) | `GET /system/audit/archive-status`<br>`GET /tenants/{tenant_id}/audit/archive-status` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditArchives:3898](../../frontend-web/apps/web/src/lib/iam-api.ts#L3898) | `GET /system/audit/archives`<br>`GET /tenants/{tenant_id}/audit/archives` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditEvent:3868](../../frontend-web/apps/web/src/lib/iam-api.ts#L3868) | `GET /tenants/{tenant_id}/audit/events/{event_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditEvents:3854](../../frontend-web/apps/web/src/lib/iam-api.ts#L3854) | `GET /tenants/{tenant_id}/audit/events` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditExport:3880](../../frontend-web/apps/web/src/lib/iam-api.ts#L3880) | `GET /tenants/{tenant_id}/audit/exports` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditGovernance:3964](../../frontend-web/apps/web/src/lib/iam-api.ts#L3964) | `GET /tenants/{tenant_id}/audit/governance-policy` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditIntegrity:3874](../../frontend-web/apps/web/src/lib/iam-api.ts#L3874) | `GET /tenants/{tenant_id}/audit/integrity-verifications` | 无JSON体/查询/透传；精确构造见源码链接 |
| [auditSignRoot:3988](../../frontend-web/apps/web/src/lib/iam-api.ts#L3988) | `POST /tenants/{tenant_id}/audit/root-signatures` | 无JSON体/查询/透传；精确构造见源码链接 |
| [authorizationConstraint:3142](../../frontend-web/apps/web/src/lib/iam-api.ts#L3142) | `POST /tenants/{tenant_id}/authorization-constraint-evaluations` | JSON { permission_code: permissionCode, subject, } |
| [avatarCandidates:2398](../../frontend-web/apps/web/src/lib/iam-api.ts#L2398) | `POST /principals/{principal_id}/avatar-candidates` | JSON { style } |
| [avatarContent:2390](../../frontend-web/apps/web/src/lib/iam-api.ts#L2390) | `GET /principals/{principal_id}/avatar/content` | 无JSON体/查询/透传；精确构造见源码链接 |
| [beginPasskeyLogin:2703](../../frontend-web/apps/web/src/lib/iam-api.ts#L2703) | `POST /login/passkey/options` | JSON { return_url: returnUrl } |
| [beginPasskeyRegistration:2681](../../frontend-web/apps/web/src/lib/iam-api.ts#L2681) | `POST /principals/{principal_id}/passkey-registration-challenges` | JSON { current_password: currentPassword } |
| [beginTotpEnrollment:2636](../../frontend-web/apps/web/src/lib/iam-api.ts#L2636) | `POST /principals/{principal_id}/mfa/totp-enrollments` | JSON { current_password: currentPassword } |
| [cancelAccessReview:3446](../../frontend-web/apps/web/src/lib/iam-api.ts#L3446) | `PATCH /role-access-reviews/{review_id}` | JSON { status: "cancelled" } |
| [cancelClosureRequest:2429](../../frontend-web/apps/web/src/lib/iam-api.ts#L2429) | `DELETE /principals/{principal_id}/closure-request` | JSON { version } |
| [capabilities:2202](../../frontend-web/apps/web/src/lib/iam-api.ts#L2202) | `GET /login/options` | 无JSON体/查询/透传；精确构造见源码链接 |
| [changePassword:2579](../../frontend-web/apps/web/src/lib/iam-api.ts#L2579) | `PATCH /principals/{principal_id}/password` | JSON body |
| [clearRolePermissionCondition:3503](../../frontend-web/apps/web/src/lib/iam-api.ts#L3503) | `DELETE /roles/{role_id}/permissions/{permission_code}/condition` | 无JSON体/查询/透传；精确构造见源码链接 |
| [closureRequest:2378](../../frontend-web/apps/web/src/lib/iam-api.ts#L2378) | `GET /principals/{principal_id}/closure-request` | 无JSON体/查询/透传；精确构造见源码链接 |
| [closureRequests:2438](../../frontend-web/apps/web/src/lib/iam-api.ts#L2438) | `GET /system/closure-requests` | 无JSON体/查询/透传；精确构造见源码链接 |
| [completeAccessReview:3439](../../frontend-web/apps/web/src/lib/iam-api.ts#L3439) | `PATCH /role-access-reviews/{review_id}` | JSON { status: "completed" } |
| [completeEmailVerification:2626](../../frontend-web/apps/web/src/lib/iam-api.ts#L2626) | `POST /email-verifications` | JSON password ? { token, password } : { token } |
| [completePasswordRecovery:2596](../../frontend-web/apps/web/src/lib/iam-api.ts#L2596) | `POST /password-recoveries` | JSON body |
| [completePhoneVerification:2618](../../frontend-web/apps/web/src/lib/iam-api.ts#L2618) | `POST /phone-verifications` | JSON { code } |
| [confirmTotpEnrollment:2645](../../frontend-web/apps/web/src/lib/iam-api.ts#L2645) | `POST /principals/{principal_id}/mfa/totp` | JSON { code } |
| [createAccessRequest:3368](../../frontend-web/apps/web/src/lib/iam-api.ts#L3368) | `POST /tenants/{tenant_id}/role-access-requests` | JSON body |
| [createAccessReview:3415](../../frontend-web/apps/web/src/lib/iam-api.ts#L3415) | `POST /tenants/{tenant_id}/role-access-reviews` | JSON body |
| [createAccountFileRoot:2066](../../frontend-web/apps/web/src/lib/iam-api.ts#L2066) | `POST /principals/{principal_id}/files` | multipart FormData：kind, scope |
| [createDepartment:3206](../../frontend-web/apps/web/src/lib/iam-api.ts#L3206) | `POST /tenants/{tenant_id}/departments` | JSON  operatingPlatformID() ? { ...body, entity_id: undefined } : body  |
| [createDirectory:2083](../../frontend-web/apps/web/src/lib/iam-api.ts#L2083) | `POST /system/files`<br>`POST /entities/{entity_id}/files`<br>`POST /principals/{principal_id}/files`<br>`POST /tenants/{tenant_id}/files` | multipart FormData：parent_id, kind, name |
| [createEntity:3012](../../frontend-web/apps/web/src/lib/iam-api.ts#L3012) | `POST /tenants/{tenant_id}/entities` | JSON body |
| [createFileOperation:2185](../../frontend-web/apps/web/src/lib/iam-api.ts#L2185) | `POST /system/file-operations`<br>`POST /tenants/{tenant_id}/entities/{entity_id}/file-operations`<br>`POST /principals/{principal_id}/file-operations`<br>`POST /tenants/{tenant_id}/file-operations` | JSON { ...input, file_policy: input.file_policy ?? "ask", directory_policy: input.directory_policy ?? "ask", type_policy: input.type_policy ?? "ask", }; header Idempotency-Key |
| [createGroup:3248](../../frontend-web/apps/web/src/lib/iam-api.ts#L3248) | `POST /tenants/{tenant_id}/principal-groups` | JSON body |
| [createIdentityLinkTransaction:2335](../../frontend-web/apps/web/src/lib/iam-api.ts#L2335) | `POST /principals/{principal_id}/identity-link-transactions` | JSON body |
| [createInvitation:2877](../../frontend-web/apps/web/src/lib/iam-api.ts#L2877) | `POST /tenants/{tenant_id}/membership-invitations` | JSON body |
| [createLoginCaptcha:2214](../../frontend-web/apps/web/src/lib/iam-api.ts#L2214) | `POST /login/captchas` | 无JSON体/查询/透传；精确构造见源码链接 |
| [createMenu:3669](../../frontend-web/apps/web/src/lib/iam-api.ts#L3669) | `POST /tenants/{tenant_id}/menus` | JSON body |
| [createOAuthClient:3699](../../frontend-web/apps/web/src/lib/iam-api.ts#L3699) | `POST /tenants/{tenant_id}/oauth-clients` | JSON body |
| [createOperatingPlatform:1953](../../frontend-web/apps/web/src/lib/iam-api.ts#L1953) | `POST /operating-platforms` | JSON body |
| [createPersonalToken:2271](../../frontend-web/apps/web/src/lib/iam-api.ts#L2271) | `POST /principals/{principal_id}/personal-tokens` | JSON body |
| [createPrincipal:2743](../../frontend-web/apps/web/src/lib/iam-api.ts#L2743) | `POST /tenants/{tenant_id}/principals` | JSON body |
| [createReauthenticationChallenge:2296](../../frontend-web/apps/web/src/lib/iam-api.ts#L2296) | `POST /principals/{principal_id}/reauthentication/challenges` | JSON body |
| [createRegistrationCaptcha:2212](../../frontend-web/apps/web/src/lib/iam-api.ts#L2212) | `POST /registrations/captchas` | 无JSON体/查询/透传；精确构造见源码链接 |
| [createRole:3453](../../frontend-web/apps/web/src/lib/iam-api.ts#L3453) | `POST /tenants/{tenant_id}/roles` | JSON body |
| [createSCIMCredential:3810](../../frontend-web/apps/web/src/lib/iam-api.ts#L3810) | `POST /scim-directories/{directory_id}/credentials` | JSON body |
| [createSCIMDirectory:3787](../../frontend-web/apps/web/src/lib/iam-api.ts#L3787) | `POST /tenants/{tenant_id}/scim-directories` | JSON body |
| [createSCIMTarget:3747](../../frontend-web/apps/web/src/lib/iam-api.ts#L3747) | `POST /tenants/{tenant_id}/scim-targets` | JSON body |
| [createServiceAccount:2826](../../frontend-web/apps/web/src/lib/iam-api.ts#L2826) | `POST /tenants/{tenant_id}/service-accounts` | JSON body |
| [createServiceAccountCredential:2853](../../frontend-web/apps/web/src/lib/iam-api.ts#L2853) | `POST /service-accounts/{service_account_id}/credentials` | JSON body |
| [createSystemDepartment:1842](../../frontend-web/apps/web/src/lib/iam-api.ts#L1842) | `POST /system/departments` | JSON body |
| [createSystemFileRoot:2057](../../frontend-web/apps/web/src/lib/iam-api.ts#L2057) | `POST /system/files` | multipart FormData：kind, scope |
| [createSystemMember:1871](../../frontend-web/apps/web/src/lib/iam-api.ts#L1871) | `POST /system/members` | JSON body |
| [createSystemMenu:3603](../../frontend-web/apps/web/src/lib/iam-api.ts#L3603) | `POST /system/menus` | JSON body |
| [createSystemOwnerMember:1642](../../frontend-web/apps/web/src/lib/iam-api.ts#L1642) | `POST /system/operating-platforms/{owner_id}/members`<br>`POST /system/tenants/{owner_id}/members` | JSON body |
| [createSystemPrincipal:1930](../../frontend-web/apps/web/src/lib/iam-api.ts#L1930) | `POST /system/principals` | JSON body |
| [createSystemRole:1785](../../frontend-web/apps/web/src/lib/iam-api.ts#L1785) | `POST /system/roles` | JSON body |
| [createTenant:2937](../../frontend-web/apps/web/src/lib/iam-api.ts#L2937) | `POST /tenants` | JSON body |
| [decideAccessReviewItem:3422](../../frontend-web/apps/web/src/lib/iam-api.ts#L3422) | `PUT /role-access-reviews/{review_id}/items/{item_id}/decision` | JSON { decision, note } |
| [decideDeviceRequest:2607](../../frontend-web/apps/web/src/lib/iam-api.ts#L2607) | `POST /tenants/{tenant_id}/oauth/device-requests/decisions` | JSON { user_code: userCode, approve } |
| [deleteDepartment:3228](../../frontend-web/apps/web/src/lib/iam-api.ts#L3228) | `DELETE /departments/{department_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteEntity:3024](../../frontend-web/apps/web/src/lib/iam-api.ts#L3024) | `DELETE /entities/{entity_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteEntityRoleBinding:3050](../../frontend-web/apps/web/src/lib/iam-api.ts#L3050) | `DELETE /entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteFieldPolicy:3322](../../frontend-web/apps/web/src/lib/iam-api.ts#L3322) | `DELETE /tenants/{tenant_id}/field-policies/{resource}/{field}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteGroup:3265](../../frontend-web/apps/web/src/lib/iam-api.ts#L3265) | `DELETE /principal-groups/{group_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteGroupMember:3285](../../frontend-web/apps/web/src/lib/iam-api.ts#L3285) | `DELETE /principal-groups/{group_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteIdentityLink:2349](../../frontend-web/apps/web/src/lib/iam-api.ts#L2349) | `DELETE /principals/{principal_id}/identity-links/{link_id}` | JSON { proof } |
| [deleteMenu:3689](../../frontend-web/apps/web/src/lib/iam-api.ts#L3689) | `DELETE /menus/{menu_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteOAuthClient:3729](../../frontend-web/apps/web/src/lib/iam-api.ts#L3729) | `DELETE /oauth-clients/{client_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteOperatingPlatform:1968](../../frontend-web/apps/web/src/lib/iam-api.ts#L1968) | `DELETE /operating-platforms/{platform_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deletePasskey:2723](../../frontend-web/apps/web/src/lib/iam-api.ts#L2723) | `DELETE /principals/{principal_id}/passkeys/{passkey_id}` | JSON { current_password: currentPassword } |
| [deleteRelationship:3085](../../frontend-web/apps/web/src/lib/iam-api.ts#L3085) | `DELETE /tenants/{tenant_id}/authorization-relationships` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteRole:3468](../../frontend-web/apps/web/src/lib/iam-api.ts#L3468) | `DELETE /roles/{role_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteSCIMTarget:3761](../../frontend-web/apps/web/src/lib/iam-api.ts#L3761) | `DELETE /scim-targets/{target_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteServiceAccount:2840](../../frontend-web/apps/web/src/lib/iam-api.ts#L2840) | `DELETE /service-accounts/{service_account_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteSystemDepartment:1856](../../frontend-web/apps/web/src/lib/iam-api.ts#L1856) | `DELETE /system/departments/{department_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteSystemMenu:3620](../../frontend-web/apps/web/src/lib/iam-api.ts#L3620) | `DELETE /system/menus/{menu_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteSystemRole:1794](../../frontend-web/apps/web/src/lib/iam-api.ts#L1794) | `DELETE /system/roles/{role_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deleteTenant:2946](../../frontend-web/apps/web/src/lib/iam-api.ts#L2946) | `DELETE /tenants/{tenant_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [departmentFiles:2042](../../frontend-web/apps/web/src/lib/iam-api.ts#L2042) | `GET /departments/{department_id}/files` | 无JSON体/查询/透传；精确构造见源码链接 |
| [departments:3194](../../frontend-web/apps/web/src/lib/iam-api.ts#L3194) | `GET /tenants/{tenant_id}/departments` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deployment:2204](../../frontend-web/apps/web/src/lib/iam-api.ts#L2204) | `GET /deployment` | 无JSON体/查询/透传；精确构造见源码链接 |
| [deprovisionSCIMResource:3774](../../frontend-web/apps/web/src/lib/iam-api.ts#L3774) | `POST /scim-targets/{target_id}/deprovisioning-jobs` | JSON body |
| [deviceRequest:2600](../../frontend-web/apps/web/src/lib/iam-api.ts#L2600) | `GET /tenants/{tenant_id}/oauth/device-requests` | 无JSON体/查询/透传；精确构造见源码链接 |
| [disablePrincipal:2767](../../frontend-web/apps/web/src/lib/iam-api.ts#L2767) | `PUT /tenants/{tenant_id}/principals/{principal_id}/suspension` | 无JSON体/查询/透传；精确构造见源码链接 |
| [disableTotp:2666](../../frontend-web/apps/web/src/lib/iam-api.ts#L2666) | `DELETE /principals/{principal_id}/mfa/totp` | JSON body |
| [downloadAuditArchive:3951](../../frontend-web/apps/web/src/lib/iam-api.ts#L3951) | `GET /system/audit/archives/{archive_id}/content`<br>`GET /tenants/{tenant_id}/audit/archives/{archive_id}/content` | 无JSON体/查询/透传；精确构造见源码链接 |
| [downloadFile:2127](../../frontend-web/apps/web/src/lib/iam-api.ts#L2127) | `GET /files/{file_id}/content` | 无JSON体/查询/透传；精确构造见源码链接 |
| [effectiveFields:3333](../../frontend-web/apps/web/src/lib/iam-api.ts#L3333) | `GET /tenants/{tenant_id}/effective-fields` | 无JSON体/查询/透传；精确构造见源码链接 |
| [effectiveMenus:3630](../../frontend-web/apps/web/src/lib/iam-api.ts#L3630) | `GET /tenants/{tenant_id}/principals/{principal_id}/menus` | 无JSON体/查询/透传；精确构造见源码链接 |
| [entities:2986](../../frontend-web/apps/web/src/lib/iam-api.ts#L2986) | `GET /tenants/{tenant_id}/entities` | 无JSON体/查询/透传；精确构造见源码链接 |
| [entity:3008](../../frontend-web/apps/web/src/lib/iam-api.ts#L3008) | `GET /entities/{entity_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [entityRoleBindings:3028](../../frontend-web/apps/web/src/lib/iam-api.ts#L3028) | `GET /entities/{entity_id}/role-bindings` | 无JSON体/查询/透传；精确构造见源码链接 |
| [explainEntityAuthorization:3156](../../frontend-web/apps/web/src/lib/iam-api.ts#L3156) | `POST /tenants/{tenant_id}/authorization-explanations` | JSON { entity_id: entityID, permission_code: permissionCode, subject, } |
| [explainResourceAuthorization:3173](../../frontend-web/apps/web/src/lib/iam-api.ts#L3173) | `POST /tenants/{tenant_id}/authorization-explanations` | JSON { entity_id: entityID, resource_type: resourceType, resource_id: resourceID, permission_code: permissionCode, subject, } |
| [fieldCatalog:3295](../../frontend-web/apps/web/src/lib/iam-api.ts#L3295) | `GET /tenants/{tenant_id}/field-catalog` | 无JSON体/查询/透传；精确构造见源码链接 |
| [fieldPolicies:3301](../../frontend-web/apps/web/src/lib/iam-api.ts#L3301) | `GET /tenants/{tenant_id}/field-policies` | 无JSON体/查询/透传；精确构造见源码链接 |
| [fileContentURL:1580](../../frontend-web/apps/web/src/lib/iam-api.ts#L1580) | `GET /files/{file_id}/content (query disposition=inline)` | 无JSON体/查询/透传；精确构造见源码链接 |
| [fileItems:2072](../../frontend-web/apps/web/src/lib/iam-api.ts#L2072) | `GET /files/{file_id}/items` | 无JSON体/查询/透传；精确构造见源码链接 |
| [files:2024](../../frontend-web/apps/web/src/lib/iam-api.ts#L2024) | `GET /system/files`<br>`GET /entities/{entity_id}/files`<br>`GET /principals/{principal_id}/files`<br>`GET /tenants/{tenant_id}/files` | 无JSON体/查询/透传；精确构造见源码链接 |
| [finishPasskeyLogin:2707](../../frontend-web/apps/web/src/lib/iam-api.ts#L2707) | `POST /login/passkey/verify` | JSON body |
| [finishPasskeyRegistration:2690](../../frontend-web/apps/web/src/lib/iam-api.ts#L2690) | `POST /principals/{principal_id}/passkeys` | JSON body |
| [grantPermission:3478](../../frontend-web/apps/web/src/lib/iam-api.ts#L3478) | `PUT /roles/{role_id}/permissions/{permission_code}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [grantSystemRolePermission:1806](../../frontend-web/apps/web/src/lib/iam-api.ts#L1806) | `PUT /system/roles/{role_id}/permissions/{permission_code}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [groupMembers:3272](../../frontend-web/apps/web/src/lib/iam-api.ts#L3272) | `GET /principal-groups/{group_id}/members` | 无JSON体/查询/透传；精确构造见源码链接 |
| [groups:3236](../../frontend-web/apps/web/src/lib/iam-api.ts#L3236) | `GET /tenants/{tenant_id}/principal-groups` | 无JSON体/查询/透传；精确构造见源码链接 |
| [identityLinkProviders:2329](../../frontend-web/apps/web/src/lib/iam-api.ts#L2329) | `GET /principals/{principal_id}/identity-link-providers` | 无JSON体/查询/透传；精确构造见源码链接 |
| [identityLinks:2323](../../frontend-web/apps/web/src/lib/iam-api.ts#L2323) | `GET /principals/{principal_id}/identity-links` | 无JSON体/查询/透传；精确构造见源码链接 |
| [invitations:2871](../../frontend-web/apps/web/src/lib/iam-api.ts#L2871) | `GET /tenants/{tenant_id}/membership-invitations` | 无JSON体/查询/透传；精确构造见源码链接 |
| [leaveMembership:2367](../../frontend-web/apps/web/src/lib/iam-api.ts#L2367) | `DELETE /principals/{principal_id}/memberships/{tenant_id}` | JSON { proof } |
| [login:2480](../../frontend-web/apps/web/src/lib/iam-api.ts#L2480) | `POST /login` | JSON body |
| [loginWithPhoneCode:2506](../../frontend-web/apps/web/src/lib/iam-api.ts#L2506) | `POST /login/phone` | JSON body |
| [logout:2536](../../frontend-web/apps/web/src/lib/iam-api.ts#L2536) | `DELETE /sessions/current` | 无JSON体/查询/透传；精确构造见源码链接 |
| [logoutAll:2570](../../frontend-web/apps/web/src/lib/iam-api.ts#L2570) | `DELETE /principals/{principal_id}/sessions` | 无JSON体/查询/透传；精确构造见源码链接 |
| [memberAuthorizationSources:2978](../../frontend-web/apps/web/src/lib/iam-api.ts#L2978) | `GET /tenants/{tenant_id}/members/{principal_id}/authorization-sources` | 无JSON体/查询/透传；精确构造见源码链接 |
| [memberEntities:2998](../../frontend-web/apps/web/src/lib/iam-api.ts#L2998) | `GET /tenants/{tenant_id}/principals/{principal_id}/entities` | 无JSON体/查询/透传；精确构造见源码链接 |
| [members:2953](../../frontend-web/apps/web/src/lib/iam-api.ts#L2953) | `GET /tenants/{tenant_id}/members` | 无JSON体/查询/透传；精确构造见源码链接 |
| [menus:3624](../../frontend-web/apps/web/src/lib/iam-api.ts#L3624) | `GET /tenants/{tenant_id}/menus` | 无JSON体/查询/透传；精确构造见源码链接 |
| [mfaStatus:2630](../../frontend-web/apps/web/src/lib/iam-api.ts#L2630) | `GET /principals/{principal_id}/mfa` | 无JSON体/查询/透传；精确构造见源码链接 |
| [myAccessRequests:3352](../../frontend-web/apps/web/src/lib/iam-api.ts#L3352) | `GET /tenants/{tenant_id}/principals/{principal_id}/role-access-requests` | 无JSON体/查询/透传；精确构造见源码链接 |
| [myAvatar:2384](../../frontend-web/apps/web/src/lib/iam-api.ts#L2384) | `GET /principals/{principal_id}/avatar` | 无JSON体/查询/透传；精确构造见源码链接 |
| [myOperatingPlatforms:1924](../../frontend-web/apps/web/src/lib/iam-api.ts#L1924) | `GET /principals/{principal_id}/operating-platforms` | 无JSON体/查询/透传；精确构造见源码链接 |
| [myPermissions:3640](../../frontend-web/apps/web/src/lib/iam-api.ts#L3640) | `GET /tenants/{tenant_id}/entities/{entity_id}/principals/{principal_id}/permissions`<br>`GET /tenants/{tenant_id}/principals/{principal_id}/permissions`<br>`GET /principals/{principal_id}/permissions` | 无JSON体/查询/透传；精确构造见源码链接 |
| [myTenants:2233](../../frontend-web/apps/web/src/lib/iam-api.ts#L2233) | `GET /principals/{principal_id}/tenants` | 无JSON体/查询/透传；精确构造见源码链接 |
| [oauthClients:3693](../../frontend-web/apps/web/src/lib/iam-api.ts#L3693) | `GET /tenants/{tenant_id}/oauth-clients` | 无JSON体/查询/透传；精确构造见源码链接 |
| [oauthConsents:2464](../../frontend-web/apps/web/src/lib/iam-api.ts#L2464) | `GET /principals/{principal_id}/oauth-consents` | 无JSON体/查询/透传；精确构造见源码链接 |
| [operatingNavigation:2018](../../frontend-web/apps/web/src/lib/iam-api.ts#L2018) | `GET /operating-platforms/{platform_id}/navigation` | 无JSON体/查询/透传；精确构造见源码链接 |
| [operatingPermissions:2012](../../frontend-web/apps/web/src/lib/iam-api.ts#L2012) | `GET /operating-platforms/{platform_id}/permissions` | 无JSON体/查询/透传；精确构造见源码链接 |
| [operatingPlatforms:1949](../../frontend-web/apps/web/src/lib/iam-api.ts#L1949) | `GET /operating-platforms` | 无JSON体/查询/透传；精确构造见源码链接 |
| [passkeys:2675](../../frontend-web/apps/web/src/lib/iam-api.ts#L2675) | `GET /principals/{principal_id}/passkeys` | 无JSON体/查询/透传；精确构造见源码链接 |
| [permissions:3592](../../frontend-web/apps/web/src/lib/iam-api.ts#L3592) | `GET /tenants/{tenant_id}/permission-catalog` | 无JSON体/查询/透传；精确构造见源码链接 |
| [personalMemberships:2360](../../frontend-web/apps/web/src/lib/iam-api.ts#L2360) | `GET /principals/{principal_id}/memberships` | 无JSON体/查询/透传；精确构造见源码链接 |
| [personalTokenPermissions:2239](../../frontend-web/apps/web/src/lib/iam-api.ts#L2239) | `GET /principals/{principal_id}/personal-token-permissions` | 无JSON体/查询/透传；精确构造见源码链接 |
| [personalTokens:2250](../../frontend-web/apps/web/src/lib/iam-api.ts#L2250) | `GET /principals/{principal_id}/personal-tokens` | 无JSON体/查询/透传；精确构造见源码链接 |
| [preparePersonalToken:2262](../../frontend-web/apps/web/src/lib/iam-api.ts#L2262) | `POST /principals/{principal_id}/personal-token-preparations` | JSON body |
| [previewFile:2135](../../frontend-web/apps/web/src/lib/iam-api.ts#L2135) | `GET /files/{file_id}/content` | 无JSON体/查询/透传；精确构造见源码链接 |
| [principalLifecycle:2784](../../frontend-web/apps/web/src/lib/iam-api.ts#L2784) | `GET /system/principals/{principal_id}/lifecycle` | 无JSON体/查询/透传；精确构造见源码链接 |
| [principals:2730](../../frontend-web/apps/web/src/lib/iam-api.ts#L2730) | `GET /tenants/{tenant_id}/principals` | 无JSON体/查询/透传；精确构造见源码链接 |
| [processClosureRequest:2457](../../frontend-web/apps/web/src/lib/iam-api.ts#L2457) | `PATCH /system/principals/{principal_id}/closure-request` | JSON { status: "processing", version } |
| [pushSCIMResource:3767](../../frontend-web/apps/web/src/lib/iam-api.ts#L3767) | `POST /scim-targets/{target_id}/provisioning-jobs` | JSON body |
| [putEntityRoleBinding:3034](../../frontend-web/apps/web/src/lib/iam-api.ts#L3034) | `PUT /entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | JSON body |
| [putGroupMember:3278](../../frontend-web/apps/web/src/lib/iam-api.ts#L3278) | `PUT /principal-groups/{group_id}/members/{principal_id}` | JSON body |
| [putRelationship:3078](../../frontend-web/apps/web/src/lib/iam-api.ts#L3078) | `POST /tenants/{tenant_id}/authorization-relationships` | JSON body |
| [reauthenticationMethods:2290](../../frontend-web/apps/web/src/lib/iam-api.ts#L2290) | `GET /principals/{principal_id}/reauthentication/methods` | 无JSON体/查询/透传；精确构造见源码链接 |
| [regenerateRecoveryCodes:2654](../../frontend-web/apps/web/src/lib/iam-api.ts#L2654) | `PUT /principals/{principal_id}/mfa/recovery-codes` | JSON body |
| [register:2216](../../frontend-web/apps/web/src/lib/iam-api.ts#L2216) | `POST /registrations` | JSON body |
| [registerTenant:2208](../../frontend-web/apps/web/src/lib/iam-api.ts#L2208) | `POST /tenant-registrations` | JSON body |
| [rejectAccessRequest:3382](../../frontend-web/apps/web/src/lib/iam-api.ts#L3382) | `PUT /role-access-requests/{request_id}/decision` | JSON { decision: "rejected", note } |
| [rejectClosureRequest:2450](../../frontend-web/apps/web/src/lib/iam-api.ts#L2450) | `PATCH /system/principals/{principal_id}/closure-request` | JSON { status: "rejected", version } |
| [relationships:3064](../../frontend-web/apps/web/src/lib/iam-api.ts#L3064) | `GET /tenants/{tenant_id}/authorization-relationships` | 无JSON体/查询/透传；精确构造见源码链接 |
| [removeRoleDirectoryBinding:3574](../../frontend-web/apps/web/src/lib/iam-api.ts#L3574) | `DELETE /roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [removeRoleMember:3528](../../frontend-web/apps/web/src/lib/iam-api.ts#L3528) | `DELETE /roles/{role_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [removeSystemOwnerRoleMember:1740](../../frontend-web/apps/web/src/lib/iam-api.ts#L1740) | `DELETE /system/operating-platforms/{owner_id}/roles/{role_id}/members/{principal_id}`<br>`DELETE /system/tenants/{owner_id}/roles/{role_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [removeSystemRoleMember:1834](../../frontend-web/apps/web/src/lib/iam-api.ts#L1834) | `DELETE /system/roles/{role_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [renameFileEntry:2148](../../frontend-web/apps/web/src/lib/iam-api.ts#L2148) | `PATCH /files/{file_id}` | JSON { name, revision } |
| [renamePasskey:2716](../../frontend-web/apps/web/src/lib/iam-api.ts#L2716) | `PATCH /principals/{principal_id}/passkeys/{passkey_id}` | JSON { name } |
| [requestAuditArchive:3917](../../frontend-web/apps/web/src/lib/iam-api.ts#L3917) | `POST /system/audit/archives`<br>`POST /tenants/{tenant_id}/audit/archives` | 无JSON体/查询/透传；精确构造见源码链接 |
| [requestMfaPhoneCode:2520](../../frontend-web/apps/web/src/lib/iam-api.ts#L2520) | `POST /login/mfa/requests` | JSON { challenge_id: challengeID } |
| [requestPhoneLoginCode:2496](../../frontend-web/apps/web/src/lib/iam-api.ts#L2496) | `POST /login/phone/requests` | JSON body |
| [requestableRoles:3346](../../frontend-web/apps/web/src/lib/iam-api.ts#L3346) | `GET /tenants/{tenant_id}/requestable-roles` | 无JSON体/查询/透传；精确构造见源码链接 |
| [resendInvitation:2895](../../frontend-web/apps/web/src/lib/iam-api.ts#L2895) | `POST /membership-invitations/{invitation_id}/deliveries` | JSON { expires_in_hours: expiresInHours } |
| [resourceRelationships:3071](../../frontend-web/apps/web/src/lib/iam-api.ts#L3071) | `GET /tenants/{tenant_id}/authorization-relationships` | 无JSON体/查询/透传；精确构造见源码链接 |
| [restoreFileEntry:2158](../../frontend-web/apps/web/src/lib/iam-api.ts#L2158) | `PATCH /files/{file_id}` | JSON { state: "active", revision, ...(name ? { name } : {} |
| [restorePrincipal:2775](../../frontend-web/apps/web/src/lib/iam-api.ts#L2775) | `DELETE /tenants/{tenant_id}/principals/{principal_id}/suspension` | 无JSON体/查询/透传；精确构造见源码链接 |
| [retireSCIMTarget:3781](../../frontend-web/apps/web/src/lib/iam-api.ts#L3781) | `PUT /scim-targets/{target_id}/retirement` | 无JSON体/查询/透传；精确构造见源码链接 |
| [retryAuditArchive:3925](../../frontend-web/apps/web/src/lib/iam-api.ts#L3925) | `POST /system/audit/archives/{archive_id}/attempts`<br>`POST /tenants/{tenant_id}/audit/archives/{archive_id}/attempts` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokeAccessRequest:3389](../../frontend-web/apps/web/src/lib/iam-api.ts#L3389) | `PUT /role-access-requests/{request_id}/grant-revocation` | JSON { reason } |
| [revokeInvitation:2889](../../frontend-web/apps/web/src/lib/iam-api.ts#L2889) | `DELETE /membership-invitations/{invitation_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokeOAuthConsent:2470](../../frontend-web/apps/web/src/lib/iam-api.ts#L2470) | `DELETE /principals/{principal_id}/oauth-consents/{client_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokePermission:3484](../../frontend-web/apps/web/src/lib/iam-api.ts#L3484) | `DELETE /roles/{role_id}/permissions/{permission_code}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokePersonalToken:2280](../../frontend-web/apps/web/src/lib/iam-api.ts#L2280) | `DELETE /principals/{principal_id}/personal-tokens/{token_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokeSCIMCredential:3820](../../frontend-web/apps/web/src/lib/iam-api.ts#L3820) | `DELETE /scim-directories/{directory_id}/credentials/{credential_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokeServiceAccountCredential:2863](../../frontend-web/apps/web/src/lib/iam-api.ts#L2863) | `DELETE /service-accounts/{service_account_id}/credentials/{credential_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokeSession:2564](../../frontend-web/apps/web/src/lib/iam-api.ts#L2564) | `DELETE /sessions/{session_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [revokeSystemRolePermission:1814](../../frontend-web/apps/web/src/lib/iam-api.ts#L1814) | `DELETE /system/roles/{role_id}/permissions/{permission_code}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [roleDataScope:3540](../../frontend-web/apps/web/src/lib/iam-api.ts#L3540) | `GET /roles/{role_id}/data-scope` | 无JSON体/查询/透传；精确构造见源码链接 |
| [roleDirectoryBindings:3534](../../frontend-web/apps/web/src/lib/iam-api.ts#L3534) | `GET /roles/{role_id}/directory-bindings` | 无JSON体/查询/透传；精确构造见源码链接 |
| [roleMemberGrants:3517](../../frontend-web/apps/web/src/lib/iam-api.ts#L3517) | `GET /roles/{role_id}/member-grants` | 无JSON体/查询/透传；精确构造见源码链接 |
| [roleMembers:3511](../../frontend-web/apps/web/src/lib/iam-api.ts#L3511) | `GET /roles/{role_id}/members` | 无JSON体/查询/透传；精确构造见源码链接 |
| [rolePermissionGrants:3472](../../frontend-web/apps/web/src/lib/iam-api.ts#L3472) | `GET /roles/{role_id}/permission-grants` | 无JSON体/查询/透传；精确构造见源码链接 |
| [roles:3340](../../frontend-web/apps/web/src/lib/iam-api.ts#L3340) | `GET /tenants/{tenant_id}/roles` | 无JSON体/查询/透传；精确构造见源码链接 |
| [rotateOAuthClientSecret:3723](../../frontend-web/apps/web/src/lib/iam-api.ts#L3723) | `POST /oauth-clients/{client_id}/credentials` | 无JSON体/查询/透传；精确构造见源码链接 |
| [scimCredentials:3804](../../frontend-web/apps/web/src/lib/iam-api.ts#L3804) | `GET /scim-directories/{directory_id}/credentials` | 无JSON体/查询/透传；精确构造见源码链接 |
| [scimDirectories:3735](../../frontend-web/apps/web/src/lib/iam-api.ts#L3735) | `GET /tenants/{tenant_id}/scim-directories` | 无JSON体/查询/透传；精确构造见源码链接 |
| [scimTargets:3741](../../frontend-web/apps/web/src/lib/iam-api.ts#L3741) | `GET /tenants/{tenant_id}/scim-targets` | 无JSON体/查询/透传；精确构造见源码链接 |
| [serviceAccountCredentials:2847](../../frontend-web/apps/web/src/lib/iam-api.ts#L2847) | `GET /service-accounts/{service_account_id}/credentials` | 无JSON体/查询/透传；精确构造见源码链接 |
| [serviceAccounts:2819](../../frontend-web/apps/web/src/lib/iam-api.ts#L2819) | `GET /tenants/{tenant_id}/service-accounts` | 无JSON体/查询/透传；精确构造见源码链接 |
| [session:2228](../../frontend-web/apps/web/src/lib/iam-api.ts#L2228) | `GET /me` | 无JSON体/查询/透传；精确构造见源码链接 |
| [sessionForTenant:2229](../../frontend-web/apps/web/src/lib/iam-api.ts#L2229) | `GET /me (tenant参数不改变请求)` | 无JSON体/查询/透传；精确构造见源码链接 |
| [sessions:2558](../../frontend-web/apps/web/src/lib/iam-api.ts#L2558) | `GET /principals/{principal_id}/sessions` | 无JSON体/查询/透传；精确构造见源码链接 |
| [setAuditRetention:3970](../../frontend-web/apps/web/src/lib/iam-api.ts#L3970) | `PUT /system/audit/retention-policy`<br>`PUT /tenants/{tenant_id}/audit/retention-policy` | JSON body |
| [setFieldPolicy:3308](../../frontend-web/apps/web/src/lib/iam-api.ts#L3308) | `PUT /tenants/{tenant_id}/field-policies/{resource}/{field}` | JSON { policy, version } |
| [setMyAvatar:2407](../../frontend-web/apps/web/src/lib/iam-api.ts#L2407) | `PUT /principals/{principal_id}/avatar` | JSON avatar |
| [setPrincipalRetention:2790](../../frontend-web/apps/web/src/lib/iam-api.ts#L2790) | `PUT /system/principals/{principal_id}/lifecycle/retention-policy` | JSON body |
| [setRoleDataScope:3546](../../frontend-web/apps/web/src/lib/iam-api.ts#L3546) | `PUT /roles/{role_id}/data-scope` | JSON body |
| [setRolePermissionCondition:3490](../../frontend-web/apps/web/src/lib/iam-api.ts#L3490) | `PUT /roles/{role_id}/permissions/{permission_code}/condition` | JSON { condition } |
| [startEmailVerification:2622](../../frontend-web/apps/web/src/lib/iam-api.ts#L2622) | `POST /email-verifications/requests` | 无JSON体/查询/透传；精确构造见源码链接 |
| [startPasswordRecovery:2591](../../frontend-web/apps/web/src/lib/iam-api.ts#L2591) | `POST /password-recoveries/requests` | JSON { identifier, ...(entry ? { entry } : {} |
| [startPhoneVerification:2614](../../frontend-web/apps/web/src/lib/iam-api.ts#L2614) | `POST /phone-verifications/requests` | 无JSON体/查询/透传；精确构造见源码链接 |
| [submitClosureRequest:2420](../../frontend-web/apps/web/src/lib/iam-api.ts#L2420) | `POST /principals/{principal_id}/closure-request` | JSON { proof, version } |
| [systemAccountOwners:1607](../../frontend-web/apps/web/src/lib/iam-api.ts#L1607) | `GET /system/account-owners` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemAuditEvent:3839](../../frontend-web/apps/web/src/lib/iam-api.ts#L3839) | `GET /system/audit/events/{event_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemAuditEvents:3828](../../frontend-web/apps/web/src/lib/iam-api.ts#L3828) | `GET /system/audit/events` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemAuditExport:3849](../../frontend-web/apps/web/src/lib/iam-api.ts#L3849) | `GET /system/audit/exports` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemAuditIntegrity:3845](../../frontend-web/apps/web/src/lib/iam-api.ts#L3845) | `GET /system/audit/integrity-verifications` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemDepartments:1840](../../frontend-web/apps/web/src/lib/iam-api.ts#L1840) | `GET /system/departments` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemMember:1865](../../frontend-web/apps/web/src/lib/iam-api.ts#L1865) | `GET /system/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemMemberRoles:1893](../../frontend-web/apps/web/src/lib/iam-api.ts#L1893) | `GET /system/members/{principal_id}/roles` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemMembers:1863](../../frontend-web/apps/web/src/lib/iam-api.ts#L1863) | `GET /system/members` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemMenus:3600](../../frontend-web/apps/web/src/lib/iam-api.ts#L3600) | `GET /system/menus` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemNavigation:3598](../../frontend-web/apps/web/src/lib/iam-api.ts#L3598) | `GET /system/navigation` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemOwnerDepartments:1678](../../frontend-web/apps/web/src/lib/iam-api.ts#L1678) | `GET /system/operating-platforms/{owner_id}/departments`<br>`GET /system/tenants/{owner_id}/departments` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemOwnerMember:1628](../../frontend-web/apps/web/src/lib/iam-api.ts#L1628) | `GET /system/operating-platforms/{owner_id}/members/{principal_id}`<br>`GET /system/tenants/{owner_id}/members/{principal_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemOwnerMemberRoles:1698](../../frontend-web/apps/web/src/lib/iam-api.ts#L1698) | `GET /system/operating-platforms/{owner_id}/members/{principal_id}/roles`<br>`GET /system/tenants/{owner_id}/members/{principal_id}/roles` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemOwnerMembers:1618](../../frontend-web/apps/web/src/lib/iam-api.ts#L1618) | `GET /system/operating-platforms/{owner_id}/members`<br>`GET /system/tenants/{owner_id}/members` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemOwnerRoles:1688](../../frontend-web/apps/web/src/lib/iam-api.ts#L1688) | `GET /system/operating-platforms/{owner_id}/roles`<br>`GET /system/tenants/{owner_id}/roles` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemPermissions:1899](../../frontend-web/apps/web/src/lib/iam-api.ts#L1899) | `GET /system/permissions` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemPrincipals:1770](../../frontend-web/apps/web/src/lib/iam-api.ts#L1770) | `GET /system/principals` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemRoleMembers:1822](../../frontend-web/apps/web/src/lib/iam-api.ts#L1822) | `GET /system/roles/{role_id}/members` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemRolePermissionCatalog:1798](../../frontend-web/apps/web/src/lib/iam-api.ts#L1798) | `GET /system/role-permission-catalog` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemRolePermissionGrants:1800](../../frontend-web/apps/web/src/lib/iam-api.ts#L1800) | `GET /system/roles/{role_id}/permission-grants` | 无JSON体/查询/透传；精确构造见源码链接 |
| [systemRoles:1784](../../frontend-web/apps/web/src/lib/iam-api.ts#L1784) | `GET /system/roles` | 无JSON体/查询/透传；精确构造见源码链接 |
| [tenantPage:2916](../../frontend-web/apps/web/src/lib/iam-api.ts#L2916) | `GET /tenants` | 无JSON体/查询/透传；精确构造见源码链接 |
| [tenantRegistrationPolicy:2206](../../frontend-web/apps/web/src/lib/iam-api.ts#L2206) | `GET /tenant-registrations/options` | 无JSON体/查询/透传；精确构造见源码链接 |
| [trashFileEntry:2174](../../frontend-web/apps/web/src/lib/iam-api.ts#L2174) | `DELETE /files/{file_id}` | 无JSON体/查询/透传；精确构造见源码链接 |
| [updateDepartment:3217](../../frontend-web/apps/web/src/lib/iam-api.ts#L3217) | `PATCH /departments/{department_id}` | JSON body |
| [updateEntity:3019](../../frontend-web/apps/web/src/lib/iam-api.ts#L3019) | `PATCH /entities/{entity_id}` | JSON body |
| [updateGroup:3255](../../frontend-web/apps/web/src/lib/iam-api.ts#L3255) | `PATCH /principal-groups/{group_id}` | JSON body |
| [updateMember:2960](../../frontend-web/apps/web/src/lib/iam-api.ts#L2960) | `PATCH /tenants/{tenant_id}/members/{principal_id}` | JSON body |
| [updateMenu:3676](../../frontend-web/apps/web/src/lib/iam-api.ts#L3676) | `PATCH /menus/{menu_id}` | JSON body |
| [updateOAuthClient:3713](../../frontend-web/apps/web/src/lib/iam-api.ts#L3713) | `PUT /oauth-clients/{client_id}` | JSON body |
| [updateOperatingPlatform:1961](../../frontend-web/apps/web/src/lib/iam-api.ts#L1961) | `PATCH /operating-platforms/{platform_id}` | JSON body |
| [updateRole:3460](../../frontend-web/apps/web/src/lib/iam-api.ts#L3460) | `PATCH /roles/{role_id}` | JSON body |
| [updateSCIMDirectory:3794](../../frontend-web/apps/web/src/lib/iam-api.ts#L3794) | `PUT /scim-directories/{directory_id}` | JSON body |
| [updateSCIMTarget:3754](../../frontend-web/apps/web/src/lib/iam-api.ts#L3754) | `PUT /scim-targets/{target_id}` | JSON body |
| [updateServiceAccount:2833](../../frontend-web/apps/web/src/lib/iam-api.ts#L2833) | `PUT /service-accounts/{service_account_id}` | JSON body |
| [updateSystemDepartment:1846](../../frontend-web/apps/web/src/lib/iam-api.ts#L1846) | `PATCH /system/departments/{department_id}` | JSON body |
| [updateSystemMember:1881](../../frontend-web/apps/web/src/lib/iam-api.ts#L1881) | `PATCH /system/members/{principal_id}` | JSON body |
| [updateSystemMenu:3607](../../frontend-web/apps/web/src/lib/iam-api.ts#L3607) | `PATCH /system/menus/{menu_id}` | JSON body |
| [updateSystemOwnerMember:1657](../../frontend-web/apps/web/src/lib/iam-api.ts#L1657) | `PATCH /system/operating-platforms/{owner_id}/members/{principal_id}`<br>`PATCH /system/tenants/{owner_id}/members/{principal_id}` | JSON body |
| [updateSystemPrincipal:1939](../../frontend-web/apps/web/src/lib/iam-api.ts#L1939) | `PATCH /system/principals/{principal_id}` | JSON body |
| [updateSystemRole:1789](../../frontend-web/apps/web/src/lib/iam-api.ts#L1789) | `PATCH /system/roles/{role_id}` | JSON body |
| [updateTenant:2941](../../frontend-web/apps/web/src/lib/iam-api.ts#L2941) | `PATCH /tenants/{tenant_id}` | JSON body |
| [uploadFile:2100](../../frontend-web/apps/web/src/lib/iam-api.ts#L2100) | `POST /system/files`<br>`POST /entities/{entity_id}/files`<br>`POST /principals/{principal_id}/files`<br>`POST /tenants/{tenant_id}/files` | multipart FormData：parent_id, kind, file |
| [verifyAuditArchive:3937](../../frontend-web/apps/web/src/lib/iam-api.ts#L3937) | `POST /system/audit/archives/{archive_id}/verifications`<br>`POST /tenants/{tenant_id}/audit/archives/{archive_id}/verifications` | 无JSON体/查询/透传；精确构造见源码链接 |
| [verifyLoginMfa:2525](../../frontend-web/apps/web/src/lib/iam-api.ts#L2525) | `POST /login/mfa` | JSON body |
| [verifyReauthentication:2309](../../frontend-web/apps/web/src/lib/iam-api.ts#L2309) | `POST /principals/{principal_id}/reauthentication/verifications` | JSON body |
| [withdrawAccessRequest:3396](../../frontend-web/apps/web/src/lib/iam-api.ts#L3396) | `PUT /role-access-requests/{request_id}/withdrawal` | JSON { reason } |

## 跨文件闭环与不含 form 的专门边界

1. `app/login/page.tsx` → `components/auth-provider.tsx` → iamApi：四种form之外 passkey、SMS重发、logout；`lib/captcha.ts` → createLoginCaptcha/createRegistrationCaptcha，经 `issueTextCaptcha` 选择 type。第三方 Turnstile widget 仅获取证明，不请求应用业务API；证明最终在login/register载荷验证。
2. `components/file-browser.tsx` → `app/files/page.tsx` → iamApi：mkdir/create-empty/upload/rename/trash/restore/copy/move。`fileCollectionRoute` 的 `entityID===null` 为个人、非空为实体、undefined为租户，system优先；`fileOperationRoute`实体路径含tenant。multipart 的 kind/name/parent_id/scope/file 与 JSON 操作的 sources/decisions[]/revision/header 分别描述，不做 JSON 往返。
3. `components/directory-members-dialog.tsx` 的 onPut/onDelete 唯一应用调用方是 groups/page；映射 putGroupMember/deleteGroupMember。`directory-role-bindings-dialog` 则直接 add/removeRoleDirectoryBinding。
4. departments 的 `api` 替换 systemDepartments/createSystemDepartment/updateSystemDepartment/deleteSystemDepartment；roles 的 system alias替换create/update/delete、permission grants/catalog、member grants；menus 的 `menuApi` list/create/update/remove 替换 systemMenus/createSystemMenu/updateSystemMenu/deleteSystemMenu。普通方法不能替system方法拿OPTIONS；system/tenant共用UI也不代表schema一样。
5. `components/system-membership-dialog.tsx` 的 api.member/assigned 是本地alias：systemOwnerMember/systemMember 与 systemOwnerMemberRoles/systemMemberRoles。scope.kind进一步选择 /system/operating-platforms/{owner_id} 或 /system/tenants/{owner_id}。这些不是未解析API。
6. `components/account-view-dialog.tsx` → `lib/account-view.ts:158` issueAccountView → **POST /account-views**（JSON selection），退出 → `lib/account-view.ts:142` **DELETE /account-views/{account_view_id}**。POST原始fetch接共同metadata/issue适配，dialog保留cause并支持语言切换；打开dialog预取metadata，保持首个await前同步window.open，异步预检置于既有开窗/握手内并保留失败撤销。DELETE退出仍先清本地view、best-effort，继续抑制原撤销失败，不增加OPTIONS等待或阻断校验。POST当前非2xx丢弃data，错误用通用Error；这是独立漏项风险。
7. `organization-scope.ts` 的 organizationRouteURL 对部分 /tenants/{tenant_id} 模板在运营平台上下文映射 /operating-platforms/{operating_platform_id}；真实最终route才是OPTIONS key，不能只读上表字符串拼固定地址。path string ID不转换Number。
8. `components/avatar-editor.tsx` 文件裁剪最终是JSON image_data而非multipart；`lib/file-browser.ts` 的maxDepth64/maxEntries1000、下载preview尺寸限制、响应ID完整性校验属于客户端资源/协议防护，不能因移除重复API输入规则而删除。
9. `app/entities/page.tsx` / `app/roles/page.tsx` → `lib/policy-condition.ts`：trustedContextCondition把ACR Number、client/network trim、time范围+timezone组合为version/all对象。最终对象必须验证，UI字段与数组path需要映射；单独检验六个字符串不足。
10. 只读请求也可能有用户输入：audit查询/导出、device code lookup、entities explain/constraint、tenant/account search。纯本地 workspaceFilter/theme/locale 不受后端schema约束；按真实网络边界区分，不能把GET一律排除。

## 三语与错误消息登记

应用现有消息目录：`apps/web/src/messages/{zh-CN,en-US,ms-MY}.json`，文件浏览器附加 `messages/files/{zh-CN,en-US,ms-MY}.json`；共享UI为 `packages/ui/src/i18n/messages/{zh-CN,en-US,ms-MY}.json`。所有 FE-ID 的 UI-F 都必须逐条关闭，不能因为三个文件存在就标PASS。

业务消息如 passwordMismatch、endAfterStart、grantAndScopeRequired、redirectRequired、avatarInvalidImage、accountViewOpenFailed 要分类：确认/解码/状态留前端，真正API约束由后端声明及可重译issue承接；本轮未审计所有翻译值正确性。后端三语消息完整性与同源tag规则请与后端台账一起验收。

## 结构排除清单与未验证边界

以下文件也经过结构扫描，但不另创建API表单迁移任务；如已有上述链路引用的helper，必须按对应FE-ID实施，不视为永久豁免。

| 文件 | 分类 |
|---|---|
| `app/account/files.tsx` | FileBrowser账户模式入口wrapper；由files/page台账覆盖 |
| `app/account/layout.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/alert.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/app-sidebar.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/auth-entrance-links.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/auth.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/file-preview.tsx` | 下载onDownload回调由files/page提供；只读blob路径及错误已纳入FE-files，不新增写表单 |
| `components/file-type-icon.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/navigation-group.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/open-file-viewer-renderer.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/page-header.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/pending-route.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/route-error.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/session-verification.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/theme-mode-button.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/theme-provider.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `components/turnstile.tsx` | 第三方captcha证明widget；应用最终payload见login/register/AuthProvider |
| `lib/account-view.ts` | 非排除：直接fetch写入，跨文件边界第6项 |
| `lib/api-contract.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/api-route.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/auth-entrance.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/branding.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/captcha.ts` | 非排除：验证码创建接口，跨文件边界第1项 |
| `lib/dev-login-accounts.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/file-browser.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/file-collections.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/file-operation.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/i18n.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/iam-api.ts` | 所有操作transport/序列化：上面全量方法索引；由共享设施台账覆盖 |
| `lib/membership-presentation.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/navigation.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/office-preview-safety.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/office-preview.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/organization-scope.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/permissions.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `lib/policy-condition.ts` | 非排除：嵌套条件请求构造，跨文件边界第9项 |
| `lib/webauthn.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `main.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `route-loaders.ts` | 读请求/列表加载及文件view loader，附录API索引覆盖；无独立用户写表单 |
| `router.tsx` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `shims/emf-converter-browser.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |
| `vite-env.d.ts` | 支持层/纯数据或展示；无独立原生form或输入边界 |

结构上未解析的 iamApi 成员：**0**（fileContentURL、session、multipart helpers及2个本地alias已人工展开）；接口表中所有直接apiRoute/pagedRoute字面量均存在于当前OpenAPI对应方法。动态组织重写、account-view原始fetch、条件payload在上节显式登记，不能被静态字面量检查代替。

尚未完成的是**实施和语义运行验收**，不是遗漏清单：每FE条目实际约束与后端声明的一致性、所有locale渲染、浏览器字段聚焦/IME/断网、auth及account-view切换、现有数据兼容需要实施阶段逐项执行。本附件不宣称本次已经零语义差异。

## 本次文档验证与复核

- TypeScript AST扫描：96个源文件；48 form / 33 files；54个UI候选全部有FE编号和行动说明。
- 方法映射：262个实际iamApi方法/辅助成员；未映射0。与端点数分开计数：这些已引用成员内部共有271次apiRoute/pagedRoute字面量调用，按小写method+path去重为264对，全部存在于OpenAPI。264不是“262成员+2账号视图请求”；该集合不包括成员调用的外层helper中的字面量。
- 独立AST复算：整个iam-api.ts（含外层helper及尚未引用成员）的字面量集合292对；account-view.ts直接fetch使用的apiRoute集合2对；两者并集294对。这里是静态路由模板集合，不含组织上下文运行时重写，不能冒称294个已验证用户流程。全部字面量方法/路径与当前OpenAPI核对PASS。
- 本次复算工具：`/tmp/recount-frontend-routes.cjs`；集合证据：`/tmp/frontend-route-sets.json`；引用成员输入：`/tmp/frontend-impact.json`，由`/tmp/frontend-impact.cjs`生成。它们是本次临时审计工具，不属于仓库交付物。
- 额外扫描 FormData / Input / Select / raw fetch / mutation / 条件函数引用，补充按钮、上传、第三方证明、query和原始fetch入口。
- 文档内路径与48个form编号连续性检查；限定本文件 git diff --check。
- 未运行前端build/test或浏览器；无生产代码变更。自审范围、无生产编辑、跨文件回调、非form边界已核对；最终接受仅限本影响盘点。

下一步：实施方将此台账与后端/共享组件台账合并，逐FE和FORM编号记录UI-A–H实际证据；全部处理前不能宣称动态规则功能完成。
