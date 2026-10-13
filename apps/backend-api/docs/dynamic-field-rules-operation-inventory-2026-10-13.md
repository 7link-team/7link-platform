# 动态字段规则：全操作与真实路由对账（2026-10-13）

这是影响范围证据，不是新功能实现状态。每个操作都有处理分类、源码位置、输入与验收族；具体模块规则修复见后端账本，实际页面对应见前端账本。路径相对 `apps/backend-api`。

## 1. 可复核的集合边界

- 已提交规范 SHA-256：`dd107e10468db84b59dfd8df0b3cbf9cf8da143d3d052b237932cc4de5e6aa40`。
- 基线规范：398 操作 / 275 路径。实际装配记录的 operationId + method + published path 与其逐项相等。
- 启用全部可选诊断模块：411 文档操作 / 288 文档路径；398 Adapter.Handle 注册；chi.Walk 425 method-path 项。
- Adapter.Handle 分类：372 Huma typed，26 手动 adapter。另 13 项为 RawHTTPRoutes 的文档描述。
- Huma 原生 OperationDocumenter 标记与临时审计调用栈分类逐项相等（372/372）；未来实现用接口标记，严禁生产调用栈识别。
- 唯一业务运行时/文档路径改写：`GET /files/{root_file_id}/-/*` → `GET /files/{root_file_id}/-/{path}`。
- 6 个 SkipValidateParams 的 OAuth 操作在下表明确标 H-S；不存在 hidden typed operation。
- 源码位置为 operationId 字面量或已核对的组合生成点；动态平台/租户与 archive 前缀由公共注册器展开，并非无实现操作。
- 全量登记不等于所有输入边界已实测；本轮实际执行的是装配与路由/规范集合核对，规则语义仍须各实施验收用例证明。

## 2. 分类与验收族

| 标记 | 必须实施的处理 | 验收族 |
|---|---|---|
| H | Huma typed，读取声明和真实注册信息；服务层差异同源化。H 不代表全部领域规则已被 Schema 捕获 | RT01 + IN01 |
| H-S | typed 但跳过参数自动校验；其中5个form body是在注册后补文档，SkipValidateBody=false亦不能证明执行；保留OAuth协议解析 | RT01 + PR01 |
| P | 原始 adapter：SCIM/SAML/federation，保留协议解析/错误 envelope；描述公开参数，管理 UI 使用同源规则 | RT01 + PR01 |
| R | 可选 infra RawHTTPRoutes；按已有模块开关隔离，不能把文档当 typed Huma；UI/静态资源不套表单规则 | RT02 |
| JS01 | JSON 请求：同一序列化快照、required/null/default、嵌套/数组/$ref、结构化错误、三语、前后端共同边界样例 | JS01 |
| MP01 | multipart/octet-stream：文件名/大小/声明字段统一，保留边界/流/校验和，不把文件内容当 JSON | MP01 |
| RT01 | 真路由/文档 operationId 关联、OPTIONS 独立注册、权限/预检/无副作用、wildcard、同路径多方法 | RT01 |
| RT02 | 所有 optional infra 开关组合、wildcard 静态 UI 与 OPTIONS 冲突、raw 路由保留；元数据按明确分类处理 | RT02 |
| IN01 | path/query/header/cookie 的位置、required/style/explode、缺省/转义/重复键与最终编码一致 | IN01 |
| PR01 | OAuth/SCIM/SAML 专属媒体/协议错误、skip flags、重定向及签名语义不被 respx 包装破坏；token/consent/device/revoke/introspect后补form schema不得误标Huma执行 | PR01 |

所有业务操作共同要求：错误可本地化且不泄露值；没有当前 Web 调用也不能遗漏后端契约来源。GET/无 body 的操作仍可能有参数；无输入操作不虚构字段规则。

## 3. 全操作矩阵

参数列：P=path，Q=query，H=header，C=cookie。请求体列以媒体类型和根 Schema 引用定位精确字段。

| # | operationId / METHOD 路径 | 注册类别 | 参数 | 请求体 | 源码 | 验收 |
|---|---|---|---|---|---|---|
| OP001 | `oauth-jwks`<br>`GET /.well-known/jwks.json` | H | — | — | `internal/iam/oauth/http.go:421` | RT01/IN01 |
| OP002 | `oauth-discovery`<br>`GET /.well-known/openid-configuration` | H | — | — | `internal/iam/oauth/http.go:401` | RT01/IN01 |
| OP003 | `authn-create-account-view`<br>`POST /account-views` | H | H:Origin | application/json: AccountViewRequest | `internal/iam/authn/account_view_http.go:64` | RT01/IN01/JS01 |
| OP004 | `iam-account-view-workspace`<br>`GET /account-views/workspace` | H | — | — | `internal/iam/core/account_view.go:36` | RT01/IN01 |
| OP005 | `authn-delete-account-view`<br>`DELETE /account-views/{account_view_id}` | H | H:Origin; P:account_view_id | — | `internal/iam/authn/account_view_http.go:77` | RT01/IN01 |
| OP006 | `iam-scope-model`<br>`GET /authorization-scope-model` | H | — | — | `internal/iam/core/http.go:504` | RT01/IN01 |
| OP007 | `get-effective-configuration`<br>`GET /debug/config` | R | — | — | `internal/infra/configinspector/module.go:64` | RT02 |
| OP008 | `get-datasource-diagnostics`<br>`GET /debug/datasources` | R | — | — | `internal/infra/databasemonitor/module.go:346` | RT02 |
| OP009 | `get-legacy-migration-status`<br>`GET /debug/migrations` | R | — | — | `internal/infra/migrationstatus/module.go:79` | RT02 |
| OP010 | `get-pprof-index`<br>`GET /debug/pprof/` | R | — | — | `internal/infra/pprof/module.go:80` | RT02 |
| OP011 | `get-pprof-cmdline`<br>`GET /debug/pprof/cmdline` | R | — | — | `internal/infra/pprof/module.go:87` | RT02 |
| OP012 | `capture-pprof-cpu-profile`<br>`GET /debug/pprof/profile` | R | Q:seconds | — | `internal/infra/pprof/module.go:94` | RT02 |
| OP013 | `resolve-pprof-symbols`<br>`GET /debug/pprof/symbol` | R | — | — | `internal/infra/pprof/module.go:102` | RT02 |
| OP014 | `capture-pprof-trace`<br>`GET /debug/pprof/trace` | R | Q:seconds | — | `internal/infra/pprof/module.go:109` | RT02 |
| OP015 | `open-pprof-web-ui`<br>`GET /debug/pprof/ui/` | R | — | — | `internal/infra/pprof/module.go:144` | RT02 |
| OP016 | `get-pprof-runtime-profile`<br>`GET /debug/pprof/{profile}` | R | P:profile; Q:debug; Q:gc; Q:seconds | — | `internal/infra/pprof/module.go:117` | RT02 |
| OP017 | `get-sql-diagnostics`<br>`GET /debug/sql` | R | — | — | `internal/infra/databasemonitor/module.go:361` | RT02 |
| OP018 | `organization-delete-department`<br>`DELETE /departments/{department_id}` | H | P:department_id; Q:version | — | `internal/iam/organization/http.go:366` | RT01/IN01 |
| OP019 | `organization-get-department`<br>`GET /departments/{department_id}` | H | P:department_id | — | `internal/iam/organization/http.go:322` | RT01/IN01 |
| OP020 | `organization-update-department`<br>`PATCH /departments/{department_id}` | H | P:department_id | application/json: UpdateDepartmentInputBody | `internal/iam/organization/http.go:339` | RT01/IN01/JS01 |
| OP021 | `list-department-files`<br>`GET /departments/{department_id}/files` | H | P:department_id; Q:after; Q:limit; Q:state | — | `internal/files/http.go:161` | RT01/IN01 |
| OP022 | `create-department-file`<br>`POST /departments/{department_id}/files` | H | P:department_id | multipart/form-data: {file, kind, name, parent_id, scope} | `internal/files/http.go:216` | RT01/IN01/MP01 |
| OP023 | `deployment-identity`<br>`GET /deployment` | H | — | — | `internal/infra/deployment/module.go:74` | RT01/IN01 |
| OP024 | `authn-create-email-verification`<br>`POST /email-verifications` | H | — | application/json: {code, email, password, token} | `internal/iam/authn/http.go:933` | RT01/IN01/JS01 |
| OP025 | `authn-create-email-verification-request`<br>`POST /email-verifications/requests` | H | — | — | `internal/iam/authn/http.go:909` | RT01/IN01 |
| OP026 | `iam-delete-entity`<br>`DELETE /entities/{entity_id}` | H | P:entity_id | — | `internal/iam/core/http.go:649` | RT01/IN01 |
| OP027 | `iam-get-entity`<br>`GET /entities/{entity_id}` | H | P:entity_id | — | `internal/iam/core/http.go:611` | RT01/IN01 |
| OP028 | `iam-update-entity`<br>`PATCH /entities/{entity_id}` | H | P:entity_id | application/json: UpdateEntityInputBody | `internal/iam/core/http.go:626` | RT01/IN01/JS01 |
| OP029 | `list-entity-files`<br>`GET /entities/{entity_id}/files` | H | P:entity_id; Q:after; Q:limit; Q:state | — | `internal/files/http.go:154` | RT01/IN01 |
| OP030 | `create-entity-file`<br>`POST /entities/{entity_id}/files` | H | P:entity_id | multipart/form-data: {file, kind, name, parent_id, scope} | `internal/files/http.go:209` | RT01/IN01/MP01 |
| OP031 | `iam-list-entity-role-bindings`<br>`GET /entities/{entity_id}/role-bindings` | H | P:entity_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:663` | RT01/IN01 |
| OP032 | `iam-delete-entity-role-binding`<br>`DELETE /entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | H | P:entity_id; P:role_id; P:principal_id | — | `internal/iam/core/http.go:705` | RT01/IN01 |
| OP033 | `iam-put-entity-role-binding`<br>`PUT /entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | H | P:entity_id; P:role_id; P:principal_id | application/json: PutEntityRoleBindingInputBody | `internal/iam/core/http.go:678` | RT01/IN01/JS01 |
| OP034 | `list-todos`<br>`GET /entities/{entity_id}/todos` | H | Q:title; Q:description; Q:status; Q:priority; Q:due_at; Q:completed_at; Q:created_at; Q:updated_at; Q:created_by; P:entity_id; Q:search; Q:deleted; Q:fields; Q:sort; Q:offset; Q:limit; Q:meta | — | `internal/todo/http.go:279` | RT01/IN01 |
| OP035 | `create-todo`<br>`POST /entities/{entity_id}/todos` | H | P:entity_id | application/json: CreateInputBody<br>application/octet-stream: string | `internal/todo/http.go:260` | RT01/IN01/JS01/MP01 |
| OP036 | `export-todos`<br>`GET /entities/{entity_id}/todos/export` | H | Q:title; Q:description; Q:status; Q:priority; Q:due_at; Q:completed_at; Q:created_at; Q:updated_at; Q:created_by; P:entity_id; Q:search; Q:deleted; Q:fields; Q:sort; Q:offset; Q:limit; Q:meta; Q:format | — | `internal/todo/http.go:552` | RT01/IN01 |
| OP037 | `import-todos`<br>`POST /entities/{entity_id}/todos/import` | H | P:entity_id; Q:format | multipart/form-data: {file} | `internal/todo/http.go:599` | RT01/IN01/MP01 |
| OP038 | `download-todo-import-template`<br>`GET /entities/{entity_id}/todos/import-template` | H | P:entity_id; Q:format; Q:sample | — | `internal/todo/http.go:528` | RT01/IN01 |
| OP039 | `delete-file`<br>`DELETE /files/{file_id}` | H | P:file_id; Q:revision | — | `internal/files/http.go:318` | RT01/IN01 |
| OP040 | `get-file`<br>`GET /files/{file_id}` | H | P:file_id; Q:disposition | — | `internal/files/http.go:224` | RT01/IN01 |
| OP041 | `update-file`<br>`PATCH /files/{file_id}` | H | P:file_id | application/json: FileUpdateBody | `internal/files/http.go:290` | RT01/IN01/JS01 |
| OP042 | `download-file`<br>`GET /files/{file_id}/content` | H | P:file_id; Q:disposition | — | `internal/files/http.go:263` | RT01/IN01 |
| OP043 | `list-file-items`<br>`GET /files/{file_id}/items` | H | P:file_id; Q:after; Q:limit | — | `internal/files/http.go:173` | RT01/IN01 |
| OP044 | `get-file-by-path`<br>`GET /files/{root_file_id}/-/{path}` | H | P:root_file_id; P:path | — | `internal/files/http.go:241` | RT01/IN01 |
| OP045 | `next-guid`<br>`GET /guid` | H | — | — | `internal/infra/guid/http.go:70` | RT01/IN01 |
| OP046 | `next-guid-batch`<br>`GET /guid/{count}` | H | P:count | — | `internal/infra/guid/http.go:87` | RT01/IN01 |
| OP047 | `check-health`<br>`GET /healthz` | H | — | — | `internal/infra/health/http/handler.go:37` | RT01/IN01 |
| OP048 | `federation-delete-identity-provider`<br>`DELETE /identity-providers/{provider_id}` | H | P:provider_id | — | `internal/iam/federation/http.go:113` | RT01/IN01 |
| OP049 | `federation-update-identity-provider`<br>`PUT /identity-providers/{provider_id}` | H | P:provider_id | application/json: ProviderBody | `internal/iam/federation/http.go:97` | RT01/IN01/JS01 |
| OP050 | `federation-start-login`<br>`GET /identity-providers/{provider_id}/authorization` | P | P:provider_id; Q:return_url | — | `internal/iam/federation/http.go:132` | RT01/PR01 |
| OP051 | `federation-callback-login`<br>`GET /identity-providers/{provider_id}/callback` | P | P:provider_id; Q:code; Q:state | — | `internal/iam/federation/http.go:162` | RT01/PR01 |
| OP052 | `federation-callback-saml-login`<br>`POST /identity-providers/{provider_id}/callback` | P | P:provider_id | application/x-www-form-urlencoded: {RelayState, SAMLResponse} | `internal/iam/federation/http.go:204` | RT01/PR01 |
| OP053 | `federation-complete-personal-identity-transaction`<br>`GET /identity-providers/{provider_id}/identity-continuation` | H | H:Cookie; H:Origin; P:provider_id; Q:state | — | `internal/iam/federation/identity_links_http.go:126` | RT01/IN01 |
| OP054 | `check-liveness`<br>`GET /livez` | H | — | — | `internal/infra/health/http/handler.go:30` | RT01/IN01 |
| OP055 | `authn-create-session`<br>`POST /login` | H | — | application/json: LoginInputBody | `internal/iam/authn/http.go:586` | RT01/IN01/JS01 |
| OP056 | `authn-create-login-captcha`<br>`POST /login/captchas` | H | — | — | `internal/iam/authn/http.go:535` | RT01/IN01 |
| OP057 | `authn-create-session-verification`<br>`POST /login/mfa` | H | — | application/json: MfaLoginInputBody | `internal/iam/authn/http.go:702` | RT01/IN01/JS01 |
| OP058 | `authn-create-mfa-verification-request`<br>`POST /login/mfa/requests` | H | — | application/json: MfaPhoneCodeInputBody | `internal/iam/authn/http.go:618` | RT01/IN01/JS01 |
| OP059 | `authn-login-options`<br>`GET /login/options` | H | — | — | `internal/iam/authn/http.go:524` | RT01/IN01 |
| OP060 | `authn-create-passkey-login-options`<br>`POST /login/passkey/options` | H | — | application/json: PasskeyLoginBeginInputBody | `internal/iam/authn/http.go:720` | RT01/IN01/JS01 |
| OP061 | `authn-verify-passkey-login`<br>`POST /login/passkey/verify` | H | — | application/json: PasskeyFinishInputBody | `internal/iam/authn/http.go:747` | RT01/IN01/JS01 |
| OP062 | `authn-create-phone-session`<br>`POST /login/phone` | H | — | application/json: PhoneLoginCompleteInputBody | `internal/iam/authn/http.go:671` | RT01/IN01/JS01 |
| OP063 | `authn-create-phone-login-request`<br>`POST /login/phone/requests` | H | — | application/json: PhoneLoginStartInputBody | `internal/iam/authn/http.go:643` | RT01/IN01/JS01 |
| OP064 | `authn-me`<br>`GET /me` | H | — | — | `internal/iam/authn/account_view_http.go:48`<br>`internal/iam/authn/http.go:495` | RT01/IN01 |
| OP065 | `organization-accept-invitation`<br>`POST /membership-invitations/acceptances` | H | — | application/json: AcceptInvitationInputBody | `internal/iam/organization/http.go:720` | RT01/IN01/JS01 |
| OP066 | `organization-revoke-invitation`<br>`DELETE /membership-invitations/{invitation_id}` | H | P:invitation_id | — | `internal/iam/organization/http.go:687` | RT01/IN01 |
| OP067 | `organization-resend-invitation`<br>`POST /membership-invitations/{invitation_id}/deliveries` | H | P:invitation_id | application/json: ResendInvitationInputBody | `internal/iam/organization/http.go:703` | RT01/IN01/JS01 |
| OP068 | `iam-delete-menu`<br>`DELETE /menus/{menu_id}` | H | P:menu_id | — | `internal/iam/core/http.go:1253` | RT01/IN01 |
| OP069 | `iam-get-menu`<br>`GET /menus/{menu_id}` | H | P:menu_id | — | `internal/iam/core/http.go:1176` | RT01/IN01 |
| OP070 | `iam-update-menu`<br>`PATCH /menus/{menu_id}` | H | P:menu_id | application/json: UpdateMenuInputBody | `internal/iam/core/http.go:1187` | RT01/IN01/JS01 |
| OP071 | `get-prometheus-metrics`<br>`GET /metrics` | R | — | — | `internal/infra/metrics/module.go:309` | RT02 |
| OP072 | `get-migration-status`<br>`GET /migration-status` | R | — | — | `internal/infra/migrationstatus/module.go:78` | RT02 |
| OP073 | `oauth-delete-client`<br>`DELETE /oauth-clients/{client_id}` | H | P:client_id | — | `internal/iam/oauth/http.go:665` | RT01/IN01 |
| OP074 | `oauth-update-client`<br>`PUT /oauth-clients/{client_id}` | H | P:client_id | application/json: ClientUpdateInputBody | `internal/iam/oauth/http.go:630` | RT01/IN01/JS01 |
| OP075 | `oauth-rotate-client-secret`<br>`POST /oauth-clients/{client_id}/credentials` | H | P:client_id | — | `internal/iam/oauth/http.go:649` | RT01/IN01 |
| OP076 | `oauth-delete-exchange-grant`<br>`DELETE /oauth-exchange-grants/{grant_id}` | H | P:grant_id | — | `internal/iam/oauth/http_device.go:169` | RT01/IN01 |
| OP077 | `oauth-authorize`<br>`GET /oauth/authorize` | H-S | Q:client_id; Q:redirect_uri; Q:response_type; Q:scope; Q:state; Q:code_challenge; Q:code_challenge_method; Q:nonce; Q:prompt | — | `internal/iam/oauth/http.go:281`<br>`internal/iam/oauth/http.go:434` | RT01/PR01 |
| OP078 | `oauth-consent-page`<br>`GET /oauth/consent` | H | Q:consent_challenge | — | `internal/iam/oauth/http.go:281`<br>`internal/iam/oauth/http.go:312` | RT01/IN01 |
| OP079 | `oauth-consent-decision`<br>`POST /oauth/consent` | H-S | — | application/x-www-form-urlencoded: {consent_challenge, decision} | `internal/iam/oauth/http.go:281`<br>`internal/iam/oauth/http.go:342` | RT01/PR01 |
| OP080 | `oauth-consent-request`<br>`GET /oauth/consent/request` | H | Q:consent_challenge | — | `internal/iam/oauth/http.go:281`<br>`internal/iam/oauth/http.go:329` | RT01/IN01 |
| OP081 | `oauth-device-authorization`<br>`POST /oauth/device_authorization` | H-S | — | application/x-www-form-urlencoded: {client_id, scope} | `internal/iam/oauth/http_device.go:61` | RT01/PR01 |
| OP082 | `oauth-introspect`<br>`POST /oauth/introspect` | H-S | — | application/x-www-form-urlencoded: {token, token_type_hint} | `internal/iam/oauth/http.go:529` | RT01/PR01 |
| OP083 | `oauth-revoke`<br>`POST /oauth/revoke` | H-S | — | application/x-www-form-urlencoded: {client_id, client_secret, token, token_type_hint} | `internal/iam/oauth/http.go:504` | RT01/PR01 |
| OP084 | `oauth-token`<br>`POST /oauth/token` | H-S | — | application/x-www-form-urlencoded: {client_id, client_secret, code, code_verifier, device_code, grant_type, redirect_uri, refresh_token, requested_token_type, scope, subject_token, subject_token_type} | `internal/iam/oauth/http.go:455` | RT01/PR01 |
| OP085 | `oauth-userinfo`<br>`GET /oauth/userinfo` | H | H:Authorization | — | `internal/iam/oauth/http.go:566` | RT01/IN01 |
| OP086 | `organization-list-operating-platforms`<br>`GET /operating-platforms` | H | Q:include_deleted; Q:search; Q:offset; Q:limit | — | `internal/iam/organization/platform_http.go:48` | RT01/IN01 |
| OP087 | `organization-create-operating-platform`<br>`POST /operating-platforms` | H | — | application/json: CreateTenantInputBody | `internal/iam/organization/platform_http.go:60` | RT01/IN01/JS01 |
| OP088 | `organization-delete-operating-platform`<br>`DELETE /operating-platforms/{platform_id}` | H | P:platform_id; Q:version | — | `internal/iam/organization/platform_http.go:111` | RT01/IN01 |
| OP089 | `organization-get-operating-platform`<br>`GET /operating-platforms/{platform_id}` | H | P:platform_id | — | `internal/iam/organization/platform_http.go:77` | RT01/IN01 |
| OP090 | `organization-update-operating-platform`<br>`PATCH /operating-platforms/{platform_id}` | H | P:platform_id | application/json: UpdatePlatformInputBody | `internal/iam/organization/platform_http.go:94` | RT01/IN01/JS01 |
| OP091 | `organization-platform-departments`<br>`GET /operating-platforms/{platform_id}/departments` | H | P:platform_id; Q:offset; Q:limit | — | `internal/iam/organization/platform_directory_http.go:46` | RT01/IN01 |
| OP092 | `organization-platform-department-create`<br>`POST /operating-platforms/{platform_id}/departments` | H | P:platform_id | application/json: CreatePlatformDepartmentInputBody | `internal/iam/organization/platform_directory_http.go:53` | RT01/IN01/JS01 |
| OP093 | `iam-platform-members`<br>`GET /operating-platforms/{platform_id}/members` | H | P:platform_id; Q:offset; Q:limit; Q:search; Q:status | — | `internal/iam/core/operating_platform_http.go:136` | RT01/IN01 |
| OP094 | `iam-platform-member-create`<br>`POST /operating-platforms/{platform_id}/members` | H | P:platform_id | application/json: OperatingPlatformMemberCreateInputBody | `internal/iam/core/operating_platform_http.go:158` | RT01/IN01/JS01 |
| OP095 | `iam-platform-member-delete`<br>`DELETE /operating-platforms/{platform_id}/members/{principal_id}` | H | P:platform_id; P:principal_id | — | `internal/iam/core/operating_platform_http.go:192` | RT01/IN01 |
| OP096 | `iam-platform-member`<br>`GET /operating-platforms/{platform_id}/members/{principal_id}` | H | P:platform_id; P:principal_id | — | `internal/iam/core/operating_platform_http.go:147` | RT01/IN01 |
| OP097 | `iam-platform-member-update`<br>`PATCH /operating-platforms/{platform_id}/members/{principal_id}` | H | P:platform_id; P:principal_id | application/json: OperatingPlatformMemberPutInputBody | `internal/iam/core/operating_platform_http.go:174` | RT01/IN01/JS01 |
| OP098 | `iam-platform-menus`<br>`GET /operating-platforms/{platform_id}/menus` | H | P:platform_id | — | `internal/iam/core/operating_platform_http.go:104` | RT01/IN01 |
| OP099 | `iam-platform-menu-create`<br>`POST /operating-platforms/{platform_id}/menus` | H | P:platform_id | application/json: OperatingPlatformMenuInputBody | `internal/iam/core/operating_platform_http.go:111` | RT01/IN01/JS01 |
| OP100 | `iam-platform-navigation`<br>`GET /operating-platforms/{platform_id}/navigation` | H | P:platform_id | — | `internal/iam/core/operating_platform_http.go:122` | RT01/IN01 |
| OP101 | `iam-platform-permissions`<br>`GET /operating-platforms/{platform_id}/permission-catalog` | H | P:platform_id | — | `internal/iam/core/operating_platform_http.go:95` | RT01/IN01 |
| OP102 | `iam-platform-my-permissions`<br>`GET /operating-platforms/{platform_id}/permissions` | H | P:platform_id | — | `internal/iam/core/operating_platform_http.go:129` | RT01/IN01 |
| OP103 | `organization-platform-groups`<br>`GET /operating-platforms/{platform_id}/principal-groups` | H | P:platform_id; Q:offset; Q:limit | — | `internal/iam/organization/platform_directory_http.go:64` | RT01/IN01 |
| OP104 | `organization-platform-group-create`<br>`POST /operating-platforms/{platform_id}/principal-groups` | H | P:platform_id | application/json: CreatePlatformGroupInputBody | `internal/iam/organization/platform_directory_http.go:71` | RT01/IN01/JS01 |
| OP105 | `iam-platform-roles`<br>`GET /operating-platforms/{platform_id}/roles` | H | P:platform_id; Q:offset; Q:limit | — | `internal/iam/core/operating_platform_http.go:81` | RT01/IN01 |
| OP106 | `iam-platform-role-create`<br>`POST /operating-platforms/{platform_id}/roles` | H | P:platform_id | application/json: OperatingPlatformRoleInputBody | `internal/iam/core/operating_platform_http.go:88` | RT01/IN01/JS01 |
| OP107 | `authn-create-password-recovery`<br>`POST /password-recoveries` | H | — | application/json: PasswordRecoveryCompleteInputBody | `internal/iam/authn/http.go:886` | RT01/IN01/JS01 |
| OP108 | `authn-create-password-recovery-request`<br>`POST /password-recoveries/requests` | H | — | application/json: PasswordRecoveryStartInputBody | `internal/iam/authn/http.go:865` | RT01/IN01/JS01 |
| OP109 | `authn-create-phone-verification`<br>`POST /phone-verifications` | H | — | application/json: PhoneVerificationCompleteInputBody | `internal/iam/authn/http.go:977` | RT01/IN01/JS01 |
| OP110 | `authn-create-phone-verification-request`<br>`POST /phone-verifications/requests` | H | — | — | `internal/iam/authn/http.go:954` | RT01/IN01 |
| OP111 | `organization-delete-group`<br>`DELETE /principal-groups/{group_id}` | H | P:group_id; Q:version | — | `internal/iam/organization/http.go:457` | RT01/IN01 |
| OP112 | `organization-get-group`<br>`GET /principal-groups/{group_id}` | H | P:group_id | — | `internal/iam/organization/http.go:420` | RT01/IN01 |
| OP113 | `organization-update-group`<br>`PATCH /principal-groups/{group_id}` | H | P:group_id | application/json: UpdateGroupInputBody | `internal/iam/organization/http.go:437` | RT01/IN01/JS01 |
| OP114 | `organization-list-group-members`<br>`GET /principal-groups/{group_id}/members` | H | P:group_id; Q:offset; Q:limit | — | `internal/iam/organization/http.go:473` | RT01/IN01 |
| OP115 | `organization-delete-group-member`<br>`DELETE /principal-groups/{group_id}/members/{principal_id}` | H | P:group_id; P:principal_id | — | `internal/iam/organization/http.go:513` | RT01/IN01 |
| OP116 | `organization-put-group-member`<br>`PUT /principal-groups/{group_id}/members/{principal_id}` | H | P:group_id; P:principal_id | application/json: GroupMembershipInputBody | `internal/iam/organization/http.go:490` | RT01/IN01/JS01 |
| OP117 | `identity-my-avatar`<br>`GET /principals/{principal_id}/avatar` | H | P:principal_id; H:Cookie; H:Origin | — | `internal/iam/identity/avatar.go:503` | RT01/IN01 |
| OP118 | `identity-set-my-avatar`<br>`PUT /principals/{principal_id}/avatar` | H | P:principal_id; H:Cookie; H:Origin | application/json: AvatarInput | `internal/iam/identity/avatar.go:518` | RT01/IN01/JS01 |
| OP119 | `identity-avatar-candidates`<br>`POST /principals/{principal_id}/avatar-candidates` | H | P:principal_id; H:Cookie; H:Origin | application/json: CandidateInputBody | `internal/iam/identity/avatar.go:616` | RT01/IN01/JS01 |
| OP120 | `identity-avatar-candidate-image`<br>`GET /principals/{principal_id}/avatar-candidates/{candidate_id}` | H | P:principal_id; H:Cookie; H:Origin; P:candidate_id | — | `internal/iam/identity/avatar.go:595` | RT01/IN01 |
| OP121 | `identity-avatar-image`<br>`GET /principals/{principal_id}/avatar/content` | H | P:principal_id; Q:tenant_id | — | `internal/iam/identity/avatar.go:543` | RT01/IN01 |
| OP122 | `identity-cancel-closure-request`<br>`DELETE /principals/{principal_id}/closure-request` | H | P:principal_id; H:Cookie; H:Origin | application/json: ClosureCancelInputBody | `internal/iam/identity/closure_http.go:67` | RT01/IN01/JS01 |
| OP123 | `identity-my-closure-request`<br>`GET /principals/{principal_id}/closure-request` | H | P:principal_id; H:Cookie; H:Origin | — | `internal/iam/identity/closure_http.go:49` | RT01/IN01 |
| OP124 | `identity-submit-closure-request`<br>`POST /principals/{principal_id}/closure-request` | H | P:principal_id; H:Cookie; H:Origin | application/json: ClosureSubmitInputBody | `internal/iam/identity/closure_http.go:56` | RT01/IN01/JS01 |
| OP125 | `create-file-operation-self`<br>`POST /principals/{principal_id}/file-operations` | H | P:principal_id; H:Idempotency-Key | application/json: FileOperationInputBody | `internal/files/http.go:334` | RT01/IN01/JS01 |
| OP126 | `list-my-files`<br>`GET /principals/{principal_id}/files` | H | P:principal_id; Q:after; Q:limit; Q:state | — | `internal/files/http.go:139` | RT01/IN01 |
| OP127 | `create-my-file`<br>`POST /principals/{principal_id}/files` | H | P:principal_id | multipart/form-data: {file, kind, name, parent_id, scope} | `internal/files/http.go:194` | RT01/IN01/MP01 |
| OP128 | `federation-list-personal-identity-providers`<br>`GET /principals/{principal_id}/identity-link-providers` | H | P:principal_id; H:Cookie; H:Origin | — | `internal/iam/federation/identity_links_http.go:78` | RT01/IN01 |
| OP129 | `federation-create-personal-identity-transaction`<br>`POST /principals/{principal_id}/identity-link-transactions` | H | P:principal_id; H:Cookie; H:Origin | application/json: IdentityOperationInputBody | `internal/iam/federation/identity_links_http.go:88` | RT01/IN01/JS01 |
| OP130 | `federation-list-personal-identity-links`<br>`GET /principals/{principal_id}/identity-links` | H | P:principal_id; H:Cookie; H:Origin | — | `internal/iam/federation/identity_links_http.go:68` | RT01/IN01 |
| OP131 | `federation-delete-personal-identity-link`<br>`DELETE /principals/{principal_id}/identity-links/{link_id}` | H | P:principal_id; H:Cookie; H:Origin; P:link_id | application/json: IdentityUnlinkInputBody | `internal/iam/federation/identity_links_http.go:114` | RT01/IN01/JS01 |
| OP132 | `iam-my-memberships`<br>`GET /principals/{principal_id}/memberships` | H | P:principal_id; H:Cookie; Q:offset; Q:limit | — | `internal/iam/core/member_exit_http.go:32` | RT01/IN01 |
| OP133 | `iam-leave-membership`<br>`DELETE /principals/{principal_id}/memberships/{tenant_id}` | H | P:principal_id; H:Cookie; H:Origin; P:tenant_id | application/json: LeaveMembershipInputBody | `internal/iam/core/member_exit_http.go:42` | RT01/IN01/JS01 |
| OP134 | `authn-get-mfa`<br>`GET /principals/{principal_id}/mfa` | H | P:principal_id | — | `internal/iam/authn/account_view_http.go:48`<br>`internal/iam/authn/http.go:998` | RT01/IN01 |
| OP135 | `authn-replace-recovery-codes`<br>`PUT /principals/{principal_id}/mfa/recovery-codes` | H | P:principal_id | application/json: MfaVerificationInputBody | `internal/iam/authn/http.go:1035` | RT01/IN01/JS01 |
| OP136 | `authn-delete-totp-factor`<br>`DELETE /principals/{principal_id}/mfa/totp` | H | P:principal_id | application/json: MfaVerificationInputBody | `internal/iam/authn/http.go:1045` | RT01/IN01/JS01 |
| OP137 | `authn-create-totp-factor`<br>`POST /principals/{principal_id}/mfa/totp` | H | P:principal_id | application/json: MfaCodeInputBody | `internal/iam/authn/http.go:1018` | RT01/IN01/JS01 |
| OP138 | `authn-create-totp-enrollment`<br>`POST /principals/{principal_id}/mfa/totp-enrollments` | H | P:principal_id | application/json: MfaPasswordInputBody | `internal/iam/authn/http.go:1005` | RT01/IN01/JS01 |
| OP139 | `oauth-list-consents`<br>`GET /principals/{principal_id}/oauth-consents` | H | P:principal_id; H:Cookie | — | `internal/iam/oauth/http.go:686` | RT01/IN01 |
| OP140 | `oauth-revoke-consent`<br>`DELETE /principals/{principal_id}/oauth-consents/{client_id}` | H | P:principal_id; H:Cookie; P:client_id | — | `internal/iam/oauth/http.go:698` | RT01/IN01 |
| OP141 | `organization-my-operating-platforms`<br>`GET /principals/{principal_id}/operating-platforms` | H | P:principal_id | — | `internal/iam/organization/platform_http.go:34` | RT01/IN01 |
| OP142 | `authn-create-passkey-registration-challenge`<br>`POST /principals/{principal_id}/passkey-registration-challenges` | H | P:principal_id | application/json: MfaPasswordInputBody | `internal/iam/authn/http.go:1095` | RT01/IN01/JS01 |
| OP143 | `authn-list-passkeys`<br>`GET /principals/{principal_id}/passkeys` | H | P:principal_id | — | `internal/iam/authn/account_view_http.go:48`<br>`internal/iam/authn/http.go:1085` | RT01/IN01 |
| OP144 | `authn-create-passkey`<br>`POST /principals/{principal_id}/passkeys` | H | P:principal_id | application/json: PasskeyRegistrationFinishInputBody | `internal/iam/authn/http.go:1114` | RT01/IN01/JS01 |
| OP145 | `authn-delete-passkey`<br>`DELETE /principals/{principal_id}/passkeys/{passkey_id}` | H | P:principal_id; P:passkey_id | application/json: PasskeyDeleteInputBody | `internal/iam/authn/http.go:1146` | RT01/IN01/JS01 |
| OP146 | `authn-update-passkey`<br>`PATCH /principals/{principal_id}/passkeys/{passkey_id}` | H | P:principal_id; P:passkey_id | application/json: PasskeyMutationInputBody | `internal/iam/authn/http.go:1132` | RT01/IN01/JS01 |
| OP147 | `authn-update-password`<br>`PATCH /principals/{principal_id}/password` | H | P:principal_id | application/json: PasswordChangeInputBody | `internal/iam/authn/http.go:852` | RT01/IN01/JS01 |
| OP148 | `iam-effective-platform-permissions`<br>`GET /principals/{principal_id}/permissions` | H | P:principal_id | — | `internal/iam/core/http.go:1332` | RT01/IN01 |
| OP149 | `authn-list-personal-token-permissions`<br>`GET /principals/{principal_id}/personal-token-permissions` | H | P:principal_id; H:Cookie; Q:tenant_id | — | `internal/iam/authn/personal_token_http.go:118` | RT01/IN01 |
| OP150 | `authn-create-personal-token-preparation`<br>`POST /principals/{principal_id}/personal-token-preparations` | H | P:principal_id; H:Cookie | application/json: PersonalTokenRequest | `internal/iam/authn/personal_token_http.go:65` | RT01/IN01/JS01 |
| OP151 | `authn-list-personal-tokens`<br>`GET /principals/{principal_id}/personal-tokens` | H | P:principal_id; H:Cookie; Q:offset; Q:limit | — | `internal/iam/authn/account_view_http.go:48`<br>`internal/iam/authn/personal_token_http.go:91` | RT01/IN01 |
| OP152 | `authn-create-personal-token`<br>`POST /principals/{principal_id}/personal-tokens` | H | P:principal_id; H:Cookie | application/json: PersonalTokenCreateInputBody | `internal/iam/authn/personal_token_http.go:78` | RT01/IN01/JS01 |
| OP153 | `authn-delete-personal-token`<br>`DELETE /principals/{principal_id}/personal-tokens/{token_id}` | H | P:principal_id; H:Cookie; P:token_id | — | `internal/iam/authn/personal_token_http.go:103` | RT01/IN01 |
| OP154 | `authn-create-reauthentication-challenge`<br>`POST /principals/{principal_id}/reauthentication/challenges` | H | P:principal_id; H:Cookie; H:Origin | application/json: ReauthenticationBeginInputBody | `internal/iam/authn/reauthentication_http.go:50` | RT01/IN01/JS01 |
| OP155 | `authn-list-reauthentication-methods`<br>`GET /principals/{principal_id}/reauthentication/methods` | H | P:principal_id | — | `internal/iam/authn/reauthentication_http.go:39` | RT01/IN01 |
| OP156 | `authn-create-reauthentication-verification`<br>`POST /principals/{principal_id}/reauthentication/verifications` | H | P:principal_id; H:Cookie; H:Origin | application/json: ReauthenticationVerifyInputBody | `internal/iam/authn/reauthentication_http.go:66` | RT01/IN01/JS01 |
| OP157 | `authn-delete-sessions`<br>`DELETE /principals/{principal_id}/sessions` | H | P:principal_id | — | `internal/iam/authn/http.go:839` | RT01/IN01 |
| OP158 | `authn-list-sessions`<br>`GET /principals/{principal_id}/sessions` | H | P:principal_id | — | `internal/iam/authn/account_view_http.go:48`<br>`internal/iam/authn/http.go:815` | RT01/IN01 |
| OP159 | `organization-my-tenants`<br>`GET /principals/{principal_id}/tenants` | H | P:principal_id | — | `internal/iam/organization/http.go:537` | RT01/IN01 |
| OP160 | `check-readiness`<br>`GET /readyz` | H | — | — | `internal/infra/health/http/handler.go:44` | RT01/IN01 |
| OP161 | `authn-create-registration`<br>`POST /registrations` | H | — | application/json: RegistrationInputBody | `internal/iam/authn/http.go:559` | RT01/IN01/JS01 |
| OP162 | `authn-create-registration-captcha`<br>`POST /registrations/captchas` | H | — | — | `internal/iam/authn/http.go:547` | RT01/IN01 |
| OP163 | `governance-set-access-request-decision`<br>`PUT /role-access-requests/{request_id}/decision` | H | P:request_id | application/json: DecisionInputBody | `internal/iam/governance/http.go:168` | RT01/IN01/JS01 |
| OP164 | `governance-revoke-access-grant`<br>`PUT /role-access-requests/{request_id}/grant-revocation` | H | P:request_id | application/json: RevokeInputBody | `internal/iam/governance/http.go:189` | RT01/IN01/JS01 |
| OP165 | `governance-withdraw-access-request`<br>`PUT /role-access-requests/{request_id}/withdrawal` | H | P:request_id | application/json: RevokeInputBody | `internal/iam/governance/http.go:205` | RT01/IN01/JS01 |
| OP166 | `governance-get-access-review`<br>`GET /role-access-reviews/{review_id}` | H | P:review_id | — | `internal/iam/governance/http.go:259` | RT01/IN01 |
| OP167 | `governance-update-access-review`<br>`PATCH /role-access-reviews/{review_id}` | H | P:review_id | application/json: ReviewUpdateInputBody | `internal/iam/governance/http.go:295` | RT01/IN01/JS01 |
| OP168 | `governance-decide-access-review-item`<br>`PUT /role-access-reviews/{review_id}/items/{item_id}/decision` | H | P:review_id; P:item_id | application/json: ReviewItemInputBody | `internal/iam/governance/http.go:275` | RT01/IN01/JS01 |
| OP169 | `iam-delete-role`<br>`DELETE /roles/{role_id}` | H | P:role_id | — | `internal/iam/core/http.go:816` | RT01/IN01 |
| OP170 | `iam-get-role`<br>`GET /roles/{role_id}` | H | P:role_id | — | `internal/iam/core/http.go:756` | RT01/IN01 |
| OP171 | `iam-update-role`<br>`PATCH /roles/{role_id}` | H | P:role_id | application/json: UpdateRoleInputBody | `internal/iam/core/http.go:802` | RT01/IN01/JS01 |
| OP172 | `iam-get-role-data-scope`<br>`GET /roles/{role_id}/data-scope` | H | P:role_id | — | `internal/iam/core/http.go:770` | RT01/IN01 |
| OP173 | `iam-set-role-data-scope`<br>`PUT /roles/{role_id}/data-scope` | H | P:role_id | application/json: SetRoleDataScopeInputBody | `internal/iam/core/http.go:784` | RT01/IN01/JS01 |
| OP174 | `iam-list-role-directory-bindings`<br>`GET /roles/{role_id}/directory-bindings` | H | P:role_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:959` | RT01/IN01 |
| OP175 | `iam-remove-role-directory-binding`<br>`DELETE /roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}` | H | P:role_id; P:assignee_type; P:assignee_id | — | `internal/iam/core/http.go:991` | RT01/IN01 |
| OP176 | `iam-add-role-directory-binding`<br>`PUT /roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}` | H | P:role_id; P:assignee_type; P:assignee_id | — | `internal/iam/core/http.go:973` | RT01/IN01 |
| OP177 | `iam-list-role-member-grants`<br>`GET /roles/{role_id}/member-grants` | H | P:role_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:913` | RT01/IN01 |
| OP178 | `iam-list-role-members`<br>`GET /roles/{role_id}/members` | H | P:role_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:899` | RT01/IN01 |
| OP179 | `iam-remove-role-member`<br>`DELETE /roles/{role_id}/members/{principal_id}` | H | P:role_id; P:principal_id | — | `internal/iam/core/http.go:945` | RT01/IN01 |
| OP180 | `iam-add-role-member`<br>`PUT /roles/{role_id}/members/{principal_id}` | H | P:role_id; P:principal_id | application/json: MemberGrantInputBody | `internal/iam/core/http.go:931` | RT01/IN01/JS01 |
| OP181 | `iam-list-role-permission-grants`<br>`GET /roles/{role_id}/permission-grants` | H | P:role_id | — | `internal/iam/core/http.go:829` | RT01/IN01 |
| OP182 | `iam-revoke-role-permission`<br>`DELETE /roles/{role_id}/permissions/{permission_code}` | H | P:role_id; P:permission_code | — | `internal/iam/core/http.go:857` | RT01/IN01 |
| OP183 | `iam-grant-role-permission`<br>`PUT /roles/{role_id}/permissions/{permission_code}` | H | P:role_id; P:permission_code | — | `internal/iam/core/http.go:843` | RT01/IN01 |
| OP184 | `iam-clear-role-permission-condition`<br>`DELETE /roles/{role_id}/permissions/{permission_code}/condition` | H | P:role_id; P:permission_code | — | `internal/iam/core/http.go:885` | RT01/IN01 |
| OP185 | `iam-set-role-permission-condition`<br>`PUT /roles/{role_id}/permissions/{permission_code}/condition` | H | P:role_id; P:permission_code | application/json: SetPermissionConditionInputBody | `internal/iam/core/http.go:871` | RT01/IN01/JS01 |
| OP186 | `federation-delete-saml-service-provider`<br>`DELETE /saml-service-providers/{sp_id}` | H | P:sp_id | — | `internal/iam/federation/saml_http.go:96` | RT01/IN01 |
| OP187 | `federation-update-saml-service-provider`<br>`PUT /saml-service-providers/{sp_id}` | H | P:sp_id | application/json: SamlSPBody | `internal/iam/federation/saml_http.go:82` | RT01/IN01/JS01 |
| OP188 | `federation-saml-metadata`<br>`GET /saml/{tenant_id}/metadata` | P | P:tenant_id | — | `internal/iam/federation/saml_http.go:216` | RT01/PR01 |
| OP189 | `federation-saml-slo`<br>`GET /saml/{tenant_id}/slo` | P | P:tenant_id; Q:SAMLRequest; Q:RelayState | — | `internal/iam/federation/saml_http.go:169` | RT01/PR01 |
| OP190 | `federation-saml-slo-post`<br>`POST /saml/{tenant_id}/slo` | P | P:tenant_id | application/x-www-form-urlencoded: {RelayState, SAMLRequest} | `internal/iam/federation/saml_http.go:193` | RT01/PR01 |
| OP191 | `federation-saml-sso`<br>`GET /saml/{tenant_id}/sso` | P | P:tenant_id; Q:SAMLRequest; Q:RelayState | — | `internal/iam/federation/saml_http.go:124` | RT01/PR01 |
| OP192 | `federation-saml-sso-post`<br>`POST /saml/{tenant_id}/sso` | P | P:tenant_id | application/x-www-form-urlencoded: {RelayState, SAMLRequest} | `internal/iam/federation/saml_http.go:147` | RT01/PR01 |
| OP193 | `provisioning-replace-scim-directory`<br>`PUT /scim-directories/{directory_id}` | H | P:directory_id | application/json: DirectoryReplaceInputBody | `internal/iam/provisioning/http.go:109` | RT01/IN01/JS01 |
| OP194 | `provisioning-list-scim-credentials`<br>`GET /scim-directories/{directory_id}/credentials` | H | P:directory_id; Q:offset; Q:limit | — | `internal/iam/provisioning/http.go:121` | RT01/IN01 |
| OP195 | `provisioning-create-scim-credential`<br>`POST /scim-directories/{directory_id}/credentials` | H | P:directory_id | application/json: CredentialCreateInputBody | `internal/iam/provisioning/http.go:133` | RT01/IN01/JS01 |
| OP196 | `provisioning-revoke-scim-credential`<br>`DELETE /scim-directories/{directory_id}/credentials/{credential_id}` | H | P:directory_id; P:credential_id | — | `internal/iam/provisioning/http.go:145` | RT01/IN01 |
| OP197 | `provisioning-delete-scim-target`<br>`DELETE /scim-targets/{target_id}` | H | P:target_id | — | `internal/iam/provisioning/http.go:749` | RT01/IN01 |
| OP198 | `provisioning-replace-scim-target`<br>`PUT /scim-targets/{target_id}` | H | P:target_id | application/json: TargetReplaceInputBody | `internal/iam/provisioning/http.go:737` | RT01/IN01/JS01 |
| OP199 | `provisioning-deprovision-scim-resource`<br>`POST /scim-targets/{target_id}/deprovisioning-jobs` | H | P:target_id | application/json: TargetPushInputBody | `internal/iam/provisioning/http.go:788` | RT01/IN01/JS01 |
| OP200 | `provisioning-push-scim-resource`<br>`POST /scim-targets/{target_id}/provisioning-jobs` | H | P:target_id | application/json: TargetPushInputBody | `internal/iam/provisioning/http.go:772` | RT01/IN01/JS01 |
| OP201 | `provisioning-retire-scim-target`<br>`PUT /scim-targets/{target_id}/retirement` | H | P:target_id | — | `internal/iam/provisioning/http.go:760` | RT01/IN01 |
| OP202 | `scim-bulk`<br>`POST /scim/v2/Bulk` | P | — | application/scim+json: {Operations, failOnErrors, schemas} | `internal/iam/provisioning/http.go:222` | RT01/PR01/JS01 |
| OP203 | `scim-list-groups`<br>`GET /scim/v2/Groups` | P | Q:filter; Q:startIndex; Q:count | — | `internal/iam/provisioning/http.go:380` | RT01/PR01 |
| OP204 | `scim-create-group`<br>`POST /scim/v2/Groups` | P | — | application/scim+json: {displayName, externalId, members, schemas} | `internal/iam/provisioning/http.go:397` | RT01/PR01/JS01 |
| OP205 | `scim-delete-group`<br>`DELETE /scim/v2/Groups/{id}` | P | P:id | — | `internal/iam/provisioning/http.go:495` | RT01/PR01 |
| OP206 | `scim-get-group`<br>`GET /scim/v2/Groups/{id}` | P | P:id | — | `internal/iam/provisioning/http.go:421` | RT01/PR01 |
| OP207 | `scim-patch-group`<br>`PATCH /scim/v2/Groups/{id}` | P | P:id | application/scim+json: {Operations, schemas} | `internal/iam/provisioning/http.go:467` | RT01/PR01/JS01 |
| OP208 | `scim-replace-group`<br>`PUT /scim/v2/Groups/{id}` | P | P:id | application/scim+json: {displayName, externalId, members, schemas} | `internal/iam/provisioning/http.go:439` | RT01/PR01/JS01 |
| OP209 | `scim-list-resource-types`<br>`GET /scim/v2/ResourceTypes` | P | — | — | `internal/iam/provisioning/http.go:532` | RT01/PR01 |
| OP210 | `scim-get-resource-type`<br>`GET /scim/v2/ResourceTypes/{id}` | P | P:id | — | `internal/iam/provisioning/http.go:536` | RT01/PR01 |
| OP211 | `scim-list-schemas`<br>`GET /scim/v2/Schemas` | P | — | — | `internal/iam/provisioning/http.go:519` | RT01/PR01 |
| OP212 | `scim-get-schema`<br>`GET /scim/v2/Schemas/{id}` | P | P:id | — | `internal/iam/provisioning/http.go:523` | RT01/PR01 |
| OP213 | `scim-service-provider-config`<br>`GET /scim/v2/ServiceProviderConfig` | P | — | — | `internal/iam/provisioning/http.go:518` | RT01/PR01 |
| OP214 | `scim-list-users`<br>`GET /scim/v2/Users` | P | Q:filter; Q:startIndex; Q:count | — | `internal/iam/provisioning/http.go:242` | RT01/PR01 |
| OP215 | `scim-create-user`<br>`POST /scim/v2/Users` | P | — | application/scim+json: {active, displayName, emails, externalId, name, schemas, userName} | `internal/iam/provisioning/http.go:259` | RT01/PR01/JS01 |
| OP216 | `scim-delete-user`<br>`DELETE /scim/v2/Users/{id}` | P | P:id | — | `internal/iam/provisioning/http.go:357` | RT01/PR01 |
| OP217 | `scim-get-user`<br>`GET /scim/v2/Users/{id}` | P | P:id | — | `internal/iam/provisioning/http.go:283` | RT01/PR01 |
| OP218 | `scim-patch-user`<br>`PATCH /scim/v2/Users/{id}` | P | P:id | application/scim+json: {Operations, schemas} | `internal/iam/provisioning/http.go:329` | RT01/PR01/JS01 |
| OP219 | `scim-replace-user`<br>`PUT /scim/v2/Users/{id}` | P | P:id | application/scim+json: {active, displayName, emails, externalId, name, schemas, userName} | `internal/iam/provisioning/http.go:301` | RT01/PR01/JS01 |
| OP220 | `identity-delete-service-account`<br>`DELETE /service-accounts/{service_account_id}` | H | P:service_account_id; Q:version | — | `internal/iam/identity/http.go:387` | RT01/IN01 |
| OP221 | `identity-get-service-account`<br>`GET /service-accounts/{service_account_id}` | H | P:service_account_id | — | `internal/iam/identity/http.go:342` | RT01/IN01 |
| OP222 | `identity-replace-service-account`<br>`PUT /service-accounts/{service_account_id}` | H | P:service_account_id | application/json: ServiceAccountReplaceInputBody | `internal/iam/identity/http.go:363` | RT01/IN01/JS01 |
| OP223 | `identity-list-service-account-credentials`<br>`GET /service-accounts/{service_account_id}/credentials` | H | P:service_account_id | — | `internal/iam/identity/http.go:407` | RT01/IN01 |
| OP224 | `identity-create-service-account-credential`<br>`POST /service-accounts/{service_account_id}/credentials` | H | P:service_account_id | application/json: ServiceAccountCredentialCreateInputBody | `internal/iam/identity/http.go:428` | RT01/IN01/JS01 |
| OP225 | `identity-delete-service-account-credential`<br>`DELETE /service-accounts/{service_account_id}/credentials/{credential_id}` | H | P:service_account_id; P:credential_id | — | `internal/iam/identity/http.go:452` | RT01/IN01 |
| OP226 | `authn-delete-current-session`<br>`DELETE /sessions/current` | H | — | — | `internal/iam/authn/http.go:795` | RT01/IN01 |
| OP227 | `authn-get-current-session`<br>`GET /sessions/current` | H | — | — | `internal/iam/authn/http.go:770` | RT01/IN01 |
| OP228 | `authn-create-step-up-challenge`<br>`POST /sessions/current/step-up-challenges` | H | — | — | `internal/iam/authn/http.go:1055` | RT01/IN01 |
| OP229 | `authn-create-step-up-verification`<br>`POST /sessions/current/step-up-verifications` | H | — | application/json: StepUpVerifyInputBody | `internal/iam/authn/http.go:1070` | RT01/IN01/JS01 |
| OP230 | `authn-delete-session`<br>`DELETE /sessions/{session_id}` | H | P:session_id | — | `internal/iam/authn/http.go:826` | RT01/IN01 |
| OP231 | `authn-get-session`<br>`GET /sessions/{session_id}` | H | P:session_id | — | `internal/iam/authn/account_view_http.go:48`<br>`internal/iam/authn/http.go:784` | RT01/IN01 |
| OP232 | `iam-system-account-owners`<br>`GET /system/account-owners` | H | Q:category; Q:offset; Q:limit | — | `internal/iam/core/system_scope_http.go:164` | RT01/IN01 |
| OP233 | `iam-list-platform-administrators`<br>`GET /system/administrators` | H | Q:offset; Q:limit | — | `internal/iam/core/platform_http.go:69` | RT01/IN01 |
| OP234 | `iam-delete-platform-administrator`<br>`DELETE /system/administrators/{principal_id}` | H | P:principal_id | — | `internal/iam/core/platform_http.go:96` | RT01/IN01 |
| OP235 | `iam-put-platform-administrator`<br>`PUT /system/administrators/{principal_id}` | H | P:principal_id | application/json: PutPlatformAdministratorInputBody | `internal/iam/core/platform_http.go:80` | RT01/IN01/JS01 |
| OP236 | `audit-platform-archive-status`<br>`GET /system/audit/archive-status` | H | — | — | `internal/iam/audit/archive_http.go:78`<br>`internal/iam/audit/archive_http.go:187` | RT01/IN01 |
| OP237 | `audit-platform-list-archives`<br>`GET /system/audit/archives` | H | Q:offset; Q:limit | — | `internal/iam/audit/archive_http.go:91`<br>`internal/iam/audit/archive_http.go:196` | RT01/IN01 |
| OP238 | `audit-platform-request-archive`<br>`POST /system/audit/archives` | H | — | — | `internal/iam/audit/archive_http.go:112`<br>`internal/iam/audit/archive_http.go:213` | RT01/IN01 |
| OP239 | `audit-platform-retry-archive`<br>`POST /system/audit/archives/{archive_id}/attempts` | H | P:archive_id | — | `internal/iam/audit/archive_http.go:125`<br>`internal/iam/audit/archive_http.go:235` | RT01/IN01 |
| OP240 | `audit-platform-download-archive`<br>`GET /system/audit/archives/{archive_id}/content` | H | P:archive_id | — | `internal/iam/audit/archive_http.go:159`<br>`internal/iam/audit/archive_http.go:261` | RT01/IN01 |
| OP241 | `audit-platform-verify-archive`<br>`POST /system/audit/archives/{archive_id}/verifications` | H | P:archive_id | — | `internal/iam/audit/archive_http.go:142`<br>`internal/iam/audit/archive_http.go:248` | RT01/IN01 |
| OP242 | `audit-system-list-events`<br>`GET /system/audit/events` | H | Q:principal_id; Q:event_type; Q:outcome; Q:target_type; Q:target_id; Q:from; Q:to; Q:offset; Q:limit | — | `internal/iam/audit/system_http.go:58` | RT01/IN01 |
| OP243 | `audit-system-get-event`<br>`GET /system/audit/events/{event_id}` | H | P:event_id | — | `internal/iam/audit/system_http.go:73` | RT01/IN01 |
| OP244 | `audit-system-export-events`<br>`GET /system/audit/exports` | H | Q:principal_id; Q:event_type; Q:outcome; Q:target_type; Q:target_id; Q:from; Q:to | — | `internal/iam/audit/system_http.go:90` | RT01/IN01 |
| OP245 | `audit-system-verify-integrity`<br>`GET /system/audit/integrity-verifications` | H | — | — | `internal/iam/audit/system_http.go:111` | RT01/IN01 |
| OP246 | `audit-platform-set-retention`<br>`PUT /system/audit/retention-policy` | H | — | application/json: PlatformRetentionInputBody | `internal/iam/audit/archive_http.go:223`<br>`internal/iam/audit/http.go:186` | RT01/IN01/JS01 |
| OP247 | `identity-list-closure-requests`<br>`GET /system/closure-requests` | H | Q:status; Q:offset; Q:limit | — | `internal/iam/identity/closure_http.go:95` | RT01/IN01 |
| OP248 | `organization-system-departments`<br>`GET /system/departments` | H | Q:offset; Q:limit | — | `internal/iam/organization/system_http.go:122` | RT01/IN01 |
| OP249 | `organization-system-department-create`<br>`POST /system/departments` | H | — | application/json: SystemDepartmentCreateInputBody | `internal/iam/organization/system_http.go:133` | RT01/IN01/JS01 |
| OP250 | `organization-system-department-delete`<br>`DELETE /system/departments/{department_id}` | H | P:department_id; Q:version | — | `internal/iam/organization/system_http.go:188` | RT01/IN01 |
| OP251 | `organization-system-department`<br>`GET /system/departments/{department_id}` | H | P:department_id | — | `internal/iam/organization/system_http.go:149` | RT01/IN01 |
| OP252 | `organization-system-department-update`<br>`PATCH /system/departments/{department_id}` | H | P:department_id | application/json: UpdateDepartmentInputBody | `internal/iam/organization/system_http.go:164` | RT01/IN01/JS01 |
| OP253 | `create-system-file-operation`<br>`POST /system/file-operations` | H | H:Idempotency-Key | application/json: FileOperationInputBody | `internal/files/system.go:53` | RT01/IN01/JS01 |
| OP254 | `list-system-files`<br>`GET /system/files` | H | Q:after; Q:limit; Q:state | — | `internal/files/system.go:31` | RT01/IN01 |
| OP255 | `create-system-file`<br>`POST /system/files` | H | — | multipart/form-data: {file, kind, name, parent_id, scope} | `internal/files/system.go:37` | RT01/IN01/MP01 |
| OP256 | `iam-system-members`<br>`GET /system/members` | H | Q:offset; Q:limit; Q:search; Q:status | — | `internal/iam/core/system_access_http.go:320` | RT01/IN01 |
| OP257 | `iam-system-member-create`<br>`POST /system/members` | H | — | application/json: SystemMemberCreateInputBody | `internal/iam/core/system_access_http.go:358` | RT01/IN01/JS01 |
| OP258 | `iam-system-member`<br>`GET /system/members/{principal_id}` | H | P:principal_id | — | `internal/iam/core/system_access_http.go:339` | RT01/IN01 |
| OP259 | `iam-system-member-update`<br>`PATCH /system/members/{principal_id}` | H | P:principal_id | application/json: SystemMemberUpdateInputBody | `internal/iam/core/system_access_http.go:382` | RT01/IN01/JS01 |
| OP260 | `iam-system-member-roles`<br>`GET /system/members/{principal_id}/roles` | H | P:principal_id | — | `internal/iam/core/system_access_http.go:430` | RT01/IN01 |
| OP261 | `iam-system-menu-catalog`<br>`GET /system/menu-catalog` | H | — | — | `internal/iam/core/system_menu_http.go:54` | RT01/IN01 |
| OP262 | `iam-system-menus`<br>`GET /system/menus` | H | — | — | `internal/iam/core/system_menu_http.go:43` | RT01/IN01 |
| OP263 | `iam-system-menu-create`<br>`POST /system/menus` | H | — | application/json: SystemMenuCreateInputBody | `internal/iam/core/system_menu_http.go:72` | RT01/IN01/JS01 |
| OP264 | `iam-system-menu-delete`<br>`DELETE /system/menus/{menu_id}` | H | P:menu_id | — | `internal/iam/core/system_menu_http.go:181` | RT01/IN01 |
| OP265 | `iam-system-menu`<br>`GET /system/menus/{menu_id}` | H | P:menu_id | — | `internal/iam/core/system_menu_http.go:92` | RT01/IN01 |
| OP266 | `iam-system-menu-update`<br>`PATCH /system/menus/{menu_id}` | H | P:menu_id | application/json: UpdateMenuInputBody | `internal/iam/core/system_menu_http.go:107` | RT01/IN01/JS01 |
| OP267 | `iam-system-navigation`<br>`GET /system/navigation` | H | — | — | `internal/iam/core/system_menu_http.go:57` | RT01/IN01 |
| OP268 | `organization-system-platform-departments`<br>`GET /system/operating-platforms/{owner_id}/departments` | H | P:owner_id; Q:offset; Q:limit | — | `internal/iam/organization/system_http.go:66` | RT01/IN01 |
| OP269 | `iam-system-platform-members`<br>`GET /system/operating-platforms/{owner_id}/members` | H | P:owner_id; Q:offset; Q:limit; Q:search; Q:status | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP270 | `iam-system-platform-member-create`<br>`POST /system/operating-platforms/{owner_id}/members` | H | P:owner_id | application/json: SystemScopeCreateInputBody | `internal/iam/core/system_scope_http.go:174` | RT01/IN01/JS01 |
| OP271 | `iam-system-platform-member-disable`<br>`DELETE /system/operating-platforms/{owner_id}/members/{principal_id}` | H | P:owner_id; P:principal_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP272 | `iam-system-platform-member`<br>`GET /system/operating-platforms/{owner_id}/members/{principal_id}` | H | P:owner_id; P:principal_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP273 | `iam-system-platform-member-update`<br>`PATCH /system/operating-platforms/{owner_id}/members/{principal_id}` | H | P:owner_id; P:principal_id | application/json: SystemScopeUpdateInputBody | `internal/iam/core/system_scope_http.go:174` | RT01/IN01/JS01 |
| OP274 | `iam-system-platform-member-roles`<br>`GET /system/operating-platforms/{owner_id}/members/{principal_id}/roles` | H | P:owner_id; P:principal_id; Q:offset; Q:limit | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP275 | `iam-system-platform-roles`<br>`GET /system/operating-platforms/{owner_id}/roles` | H | P:owner_id; Q:offset; Q:limit; Q:search; Q:status | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP276 | `iam-system-platform-role-member-remove`<br>`DELETE /system/operating-platforms/{owner_id}/roles/{role_id}/members/{principal_id}` | H | P:owner_id; P:principal_id; P:role_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP277 | `iam-system-platform-role-member-add`<br>`PUT /system/operating-platforms/{owner_id}/roles/{role_id}/members/{principal_id}` | H | P:owner_id; P:principal_id; P:role_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP278 | `iam-platform-permission-catalog`<br>`GET /system/permission-catalog` | H | — | — | `internal/iam/core/platform_http.go:62` | RT01/IN01 |
| OP279 | `iam-system-my-permissions`<br>`GET /system/permissions` | H | — | — | `internal/iam/core/platform_http.go:47` | RT01/IN01 |
| OP280 | `identity-system-list-principals`<br>`GET /system/principals` | H | Q:category; Q:owner_id; Q:status; Q:search; Q:limit; Q:offset | — | `internal/iam/identity/system_accounts.go:196` | RT01/IN01 |
| OP281 | `identity-system-create-principal`<br>`POST /system/principals` | H | — | application/json: SystemPrincipalCreateInputBody | `internal/iam/identity/system_accounts.go:219` | RT01/IN01/JS01 |
| OP282 | `identity-system-get-principal`<br>`GET /system/principals/{principal_id}` | H | P:principal_id | — | `internal/iam/identity/system_accounts.go:230` | RT01/IN01 |
| OP283 | `identity-system-update-principal`<br>`PATCH /system/principals/{principal_id}` | H | P:principal_id | application/json: SystemPrincipalUpdateInputBody | `internal/iam/identity/system_accounts.go:244` | RT01/IN01/JS01 |
| OP284 | `identity-update-closure-request`<br>`PATCH /system/principals/{principal_id}/closure-request` | H | P:principal_id | application/json: ClosureUpdateInputBody | `internal/iam/identity/closure_http.go:79` | RT01/IN01/JS01 |
| OP285 | `identity-platform-get-principal-lifecycle`<br>`GET /system/principals/{principal_id}/lifecycle` | H | P:principal_id | — | `internal/iam/identity/lifecycle_http.go:59` | RT01/IN01 |
| OP286 | `identity-platform-anonymize-principal`<br>`PUT /system/principals/{principal_id}/lifecycle/anonymization` | H | P:principal_id | application/json: PlatformAnonymizeInputBody | `internal/iam/identity/lifecycle_http.go:106` | RT01/IN01/JS01 |
| OP287 | `identity-platform-set-principal-retention`<br>`PUT /system/principals/{principal_id}/lifecycle/retention-policy` | H | P:principal_id | application/json: PlatformRetentionInputBody1 | `internal/iam/identity/lifecycle_http.go:83` | RT01/IN01/JS01 |
| OP288 | `identity-platform-restore-principal`<br>`DELETE /system/principals/{principal_id}/suspension` | H | P:principal_id | — | `internal/iam/identity/platform_status.go:132` | RT01/IN01 |
| OP289 | `identity-platform-suspend-principal`<br>`PUT /system/principals/{principal_id}/suspension` | H | P:principal_id | — | `internal/iam/identity/platform_status.go:118` | RT01/IN01 |
| OP290 | `identity-system-principal-workspaces`<br>`GET /system/principals/{principal_id}/workspaces` | H | P:principal_id | — | `internal/iam/identity/system_accounts.go:179` | RT01/IN01 |
| OP291 | `iam-system-role-permission-catalog`<br>`GET /system/role-permission-catalog` | H | — | — | `internal/iam/core/system_access_http.go:180` | RT01/IN01 |
| OP292 | `iam-system-roles`<br>`GET /system/roles` | H | Q:offset; Q:limit | — | `internal/iam/core/system_access_http.go:82` | RT01/IN01 |
| OP293 | `iam-system-role-create`<br>`POST /system/roles` | H | — | application/json: SystemRoleCreateInputBody | `internal/iam/core/system_access_http.go:97` | RT01/IN01/JS01 |
| OP294 | `iam-system-role-delete`<br>`DELETE /system/roles/{role_id}` | H | P:role_id | — | `internal/iam/core/system_access_http.go:163` | RT01/IN01 |
| OP295 | `iam-system-role`<br>`GET /system/roles/{role_id}` | H | P:role_id | — | `internal/iam/core/system_access_http.go:112` | RT01/IN01 |
| OP296 | `iam-system-role-update`<br>`PATCH /system/roles/{role_id}` | H | P:role_id | application/json: UpdateRoleInputBody | `internal/iam/core/system_access_http.go:127` | RT01/IN01/JS01 |
| OP297 | `iam-system-role-members`<br>`GET /system/roles/{role_id}/members` | H | P:role_id; Q:offset; Q:limit | — | `internal/iam/core/system_access_http.go:247` | RT01/IN01 |
| OP298 | `iam-system-role-member-remove`<br>`DELETE /system/roles/{role_id}/members/{principal_id}` | H | P:role_id; P:principal_id | — | `internal/iam/core/system_access_http.go:278` | RT01/IN01 |
| OP299 | `iam-system-role-member-add`<br>`PUT /system/roles/{role_id}/members/{principal_id}` | H | P:role_id; P:principal_id | — | `internal/iam/core/system_access_http.go:278` | RT01/IN01 |
| OP300 | `iam-system-role-permission-grants`<br>`GET /system/roles/{role_id}/permission-grants` | H | P:role_id | — | `internal/iam/core/system_access_http.go:189` | RT01/IN01 |
| OP301 | `iam-system-role-permission-revoke`<br>`DELETE /system/roles/{role_id}/permissions/{permission_code}` | H | P:role_id; P:permission_code | — | `internal/iam/core/system_access_http.go:220` | RT01/IN01 |
| OP302 | `iam-system-role-permission-grant`<br>`PUT /system/roles/{role_id}/permissions/{permission_code}` | H | P:role_id; P:permission_code | — | `internal/iam/core/system_access_http.go:220` | RT01/IN01 |
| OP303 | `federation-rotate-saml-signing-key`<br>`POST /system/saml-signing-keys` | H | — | — | `internal/iam/federation/saml_http.go:109` | RT01/IN01 |
| OP304 | `organization-system-tenant-departments`<br>`GET /system/tenants/{owner_id}/departments` | H | P:owner_id; Q:offset; Q:limit | — | `internal/iam/organization/system_http.go:63` | RT01/IN01 |
| OP305 | `iam-system-tenant-members`<br>`GET /system/tenants/{owner_id}/members` | H | P:owner_id; Q:offset; Q:limit; Q:search; Q:status | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP306 | `iam-system-tenant-member-create`<br>`POST /system/tenants/{owner_id}/members` | H | P:owner_id | application/json: SystemScopeCreateInputBody | `internal/iam/core/system_scope_http.go:174` | RT01/IN01/JS01 |
| OP307 | `iam-system-tenant-member-disable`<br>`DELETE /system/tenants/{owner_id}/members/{principal_id}` | H | P:owner_id; P:principal_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP308 | `iam-system-tenant-member`<br>`GET /system/tenants/{owner_id}/members/{principal_id}` | H | P:owner_id; P:principal_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP309 | `iam-system-tenant-member-update`<br>`PATCH /system/tenants/{owner_id}/members/{principal_id}` | H | P:owner_id; P:principal_id | application/json: SystemScopeUpdateInputBody | `internal/iam/core/system_scope_http.go:174` | RT01/IN01/JS01 |
| OP310 | `iam-system-tenant-member-roles`<br>`GET /system/tenants/{owner_id}/members/{principal_id}/roles` | H | P:owner_id; P:principal_id; Q:offset; Q:limit | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP311 | `iam-system-tenant-roles`<br>`GET /system/tenants/{owner_id}/roles` | H | P:owner_id; Q:offset; Q:limit; Q:search; Q:status | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP312 | `iam-system-tenant-role-member-remove`<br>`DELETE /system/tenants/{owner_id}/roles/{role_id}/members/{principal_id}` | H | P:owner_id; P:principal_id; P:role_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP313 | `iam-system-tenant-role-member-add`<br>`PUT /system/tenants/{owner_id}/roles/{role_id}/members/{principal_id}` | H | P:owner_id; P:principal_id; P:role_id | — | `internal/iam/core/system_scope_http.go:174` | RT01/IN01 |
| OP314 | `organization-register-tenant`<br>`POST /tenant-registrations` | H | — | application/json: Organization-register-tenantRequest | `internal/iam/organization/tenant_registration.go:27` | RT01/IN01/JS01 |
| OP315 | `organization-tenant-registration-policy`<br>`GET /tenant-registrations/options` | H | — | — | `internal/iam/organization/tenant_registration.go:21` | RT01/IN01 |
| OP316 | `organization-list-tenants`<br>`GET /tenants` | H | Q:include_deleted; Q:search; Q:offset; Q:limit | — | `internal/iam/organization/http.go:552` | RT01/IN01 |
| OP317 | `organization-create-tenant`<br>`POST /tenants` | H | — | application/json: CreateTenantInputBody | `internal/iam/organization/http.go:564` | RT01/IN01/JS01 |
| OP318 | `organization-delete-tenant`<br>`DELETE /tenants/{tenant_id}` | H | P:tenant_id; Q:version | — | `internal/iam/organization/http.go:619` | RT01/IN01 |
| OP319 | `organization-get-tenant`<br>`GET /tenants/{tenant_id}` | H | P:tenant_id | — | `internal/iam/organization/http.go:583` | RT01/IN01 |
| OP320 | `organization-update-tenant`<br>`PATCH /tenants/{tenant_id}` | H | P:tenant_id | application/json: UpdateTenantInputBody | `internal/iam/organization/http.go:600` | RT01/IN01/JS01 |
| OP321 | `audit-anchor-current`<br>`POST /tenants/{tenant_id}/audit/anchors` | H | P:tenant_id | — | `internal/iam/audit/http.go:152` | RT01/IN01 |
| OP322 | `audit-archive-status`<br>`GET /tenants/{tenant_id}/audit/archive-status` | H | P:tenant_id | — | `internal/iam/audit/archive_http.go:78`<br>`internal/iam/audit/archive_http.go:187` | RT01/IN01 |
| OP323 | `audit-list-archives`<br>`GET /tenants/{tenant_id}/audit/archives` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/audit/archive_http.go:91`<br>`internal/iam/audit/archive_http.go:196` | RT01/IN01 |
| OP324 | `audit-request-archive`<br>`POST /tenants/{tenant_id}/audit/archives` | H | P:tenant_id | — | `internal/iam/audit/archive_http.go:112`<br>`internal/iam/audit/archive_http.go:213` | RT01/IN01 |
| OP325 | `audit-retry-archive`<br>`POST /tenants/{tenant_id}/audit/archives/{archive_id}/attempts` | H | P:tenant_id; P:archive_id | — | `internal/iam/audit/archive_http.go:125`<br>`internal/iam/audit/archive_http.go:235` | RT01/IN01 |
| OP326 | `audit-download-archive`<br>`GET /tenants/{tenant_id}/audit/archives/{archive_id}/content` | H | P:tenant_id; P:archive_id | — | `internal/iam/audit/archive_http.go:159`<br>`internal/iam/audit/archive_http.go:261` | RT01/IN01 |
| OP327 | `audit-verify-archive`<br>`POST /tenants/{tenant_id}/audit/archives/{archive_id}/verifications` | H | P:tenant_id; P:archive_id | — | `internal/iam/audit/archive_http.go:142`<br>`internal/iam/audit/archive_http.go:248` | RT01/IN01 |
| OP328 | `audit-list-events`<br>`GET /tenants/{tenant_id}/audit/events` | H | P:tenant_id; Q:principal_id; Q:event_type; Q:outcome; Q:target_type; Q:target_id; Q:from; Q:to; Q:offset; Q:limit | — | `internal/iam/audit/http.go:80` | RT01/IN01 |
| OP329 | `audit-get-event`<br>`GET /tenants/{tenant_id}/audit/events/{event_id}` | H | P:tenant_id; P:event_id | — | `internal/iam/audit/http.go:116` | RT01/IN01 |
| OP330 | `audit-export-events`<br>`GET /tenants/{tenant_id}/audit/exports` | H | P:tenant_id; Q:principal_id; Q:event_type; Q:outcome; Q:target_type; Q:target_id; Q:from; Q:to | — | `internal/iam/audit/http.go:267` | RT01/IN01 |
| OP331 | `audit-get-governance`<br>`GET /tenants/{tenant_id}/audit/governance-policy` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/audit/http.go:168` | RT01/IN01 |
| OP332 | `audit-verify-integrity`<br>`GET /tenants/{tenant_id}/audit/integrity-verifications` | H | P:tenant_id | — | `internal/iam/audit/http.go:136` | RT01/IN01 |
| OP333 | `audit-set-retention`<br>`PUT /tenants/{tenant_id}/audit/retention-policy` | H | P:tenant_id | application/json: RetentionInputBody | `internal/iam/audit/archive_http.go:223`<br>`internal/iam/audit/http.go:186` | RT01/IN01/JS01 |
| OP334 | `audit-sign-root`<br>`POST /tenants/{tenant_id}/audit/root-signatures` | H | P:tenant_id | — | `internal/iam/audit/http.go:202` | RT01/IN01 |
| OP335 | `iam-check-authorization`<br>`POST /tenants/{tenant_id}/authorization-checks` | H | P:tenant_id | application/json: AuthorizationCheckInputBody | `internal/iam/core/relationship_http.go:184` | RT01/IN01/JS01 |
| OP336 | `iam-authorization-constraint`<br>`POST /tenants/{tenant_id}/authorization-constraint-evaluations` | H | P:tenant_id | application/json: AuthorizationConstraintInputBody | `internal/iam/core/http.go:524` | RT01/IN01/JS01 |
| OP337 | `iam-explain-authorization`<br>`POST /tenants/{tenant_id}/authorization-explanations` | H | P:tenant_id | application/json: AuthorizationExplanationInputBody | `internal/iam/core/http.go:542` | RT01/IN01/JS01 |
| OP338 | `iam-delete-relationship`<br>`DELETE /tenants/{tenant_id}/authorization-relationships` | H | P:tenant_id; Q:entity_id; Q:subject_type; Q:subject_id; Q:subject_relation; Q:relation; Q:resource_type; Q:resource_id | — | `internal/iam/core/relationship_http.go:154` | RT01/IN01 |
| OP339 | `iam-list-relationships`<br>`GET /tenants/{tenant_id}/authorization-relationships` | H | P:tenant_id; Q:entity_id; Q:resource_type; Q:resource_id; Q:offset; Q:limit | — | `internal/iam/core/relationship_http.go:112` | RT01/IN01 |
| OP340 | `iam-put-relationship`<br>`POST /tenants/{tenant_id}/authorization-relationships` | H | P:tenant_id | application/json: RelationshipFields | `internal/iam/core/relationship_http.go:139` | RT01/IN01/JS01 |
| OP341 | `organization-list-departments`<br>`GET /tenants/{tenant_id}/departments` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/organization/http.go:269` | RT01/IN01 |
| OP342 | `organization-create-department`<br>`POST /tenants/{tenant_id}/departments` | H | P:tenant_id | application/json: CreateDepartmentInputBody | `internal/iam/organization/http.go:295` | RT01/IN01/JS01 |
| OP343 | `iam-effective-fields`<br>`GET /tenants/{tenant_id}/effective-fields` | H | P:tenant_id; Q:resource; Q:entity_id | — | `internal/iam/core/field_policy_http.go:91` | RT01/IN01 |
| OP344 | `iam-list-entities`<br>`GET /tenants/{tenant_id}/entities` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:576` | RT01/IN01 |
| OP345 | `iam-create-entity`<br>`POST /tenants/{tenant_id}/entities` | H | P:tenant_id | application/json: CreateEntityInputBody | `internal/iam/core/http.go:590` | RT01/IN01/JS01 |
| OP346 | `create-file-operation`<br>`POST /tenants/{tenant_id}/entities/{entity_id}/file-operations` | H | P:tenant_id; P:entity_id; H:Idempotency-Key | application/json: FileOperationInputBody | `internal/files/http.go:334` | RT01/IN01/JS01 |
| OP347 | `iam-effective-entity-permissions`<br>`GET /tenants/{tenant_id}/entities/{entity_id}/principals/{principal_id}/permissions` | H | P:principal_id; P:tenant_id; P:entity_id | — | `internal/iam/core/http.go:1366` | RT01/IN01 |
| OP348 | `iam-field-catalog`<br>`GET /tenants/{tenant_id}/field-catalog` | H | P:tenant_id; Q:resource; Q:entity_id | — | `internal/iam/core/field_policy_http.go:51` | RT01/IN01 |
| OP349 | `iam-list-field-policies`<br>`GET /tenants/{tenant_id}/field-policies` | H | P:tenant_id; Q:resource; Q:entity_id; Q:offset; Q:limit | — | `internal/iam/core/field_policy_http.go:55` | RT01/IN01 |
| OP350 | `iam-delete-field-policy`<br>`DELETE /tenants/{tenant_id}/field-policies/{resource}/{field}` | H | P:tenant_id; P:resource; P:field; Q:version | — | `internal/iam/core/field_policy_http.go:79` | RT01/IN01 |
| OP351 | `iam-set-field-policy`<br>`PUT /tenants/{tenant_id}/field-policies/{resource}/{field}` | H | P:tenant_id; P:resource; P:field | application/json: FieldPolicyInputBody | `internal/iam/core/field_policy_http.go:67` | RT01/IN01/JS01 |
| OP352 | `create-file-operation-tenant`<br>`POST /tenants/{tenant_id}/file-operations` | H | P:tenant_id; H:Idempotency-Key | application/json: FileOperationInputBody | `internal/files/http.go:334` | RT01/IN01/JS01 |
| OP353 | `list-tenant-files`<br>`GET /tenants/{tenant_id}/files` | H | P:tenant_id; Q:after; Q:limit; Q:state | — | `internal/files/http.go:147` | RT01/IN01 |
| OP354 | `create-tenant-file`<br>`POST /tenants/{tenant_id}/files` | H | P:tenant_id | multipart/form-data: {file, kind, name, parent_id, scope} | `internal/files/http.go:202` | RT01/IN01/MP01 |
| OP355 | `federation-list-connectors`<br>`GET /tenants/{tenant_id}/identity-provider-connectors` | H | P:tenant_id | — | `internal/iam/federation/http.go:68` | RT01/IN01 |
| OP356 | `federation-list-identity-providers`<br>`GET /tenants/{tenant_id}/identity-providers` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/federation/http.go:71` | RT01/IN01 |
| OP357 | `federation-create-identity-provider`<br>`POST /tenants/{tenant_id}/identity-providers` | H | P:tenant_id | application/json: ProviderBody | `internal/iam/federation/http.go:82` | RT01/IN01/JS01 |
| OP358 | `iam-list-tenant-members`<br>`GET /tenants/{tenant_id}/members` | H | P:tenant_id; Q:search; Q:status; Q:offset; Q:limit | — | `internal/iam/core/http.go:1008` | RT01/IN01 |
| OP359 | `iam-create-tenant-member`<br>`POST /tenants/{tenant_id}/members` | H | P:tenant_id | application/json: CreateTenantMemberInputBody<br>application/octet-stream: string | `internal/iam/core/http.go:1033` | RT01/IN01/JS01/MP01 |
| OP360 | `iam-get-tenant-member`<br>`GET /tenants/{tenant_id}/members/{principal_id}` | H | P:tenant_id; P:principal_id | — | `internal/iam/core/http.go:1059` | RT01/IN01 |
| OP361 | `iam-update-tenant-member`<br>`PATCH /tenants/{tenant_id}/members/{principal_id}` | H | P:tenant_id; P:principal_id | application/json: UpdateTenantMemberInputBody<br>application/octet-stream: string | `internal/iam/core/http.go:1080` | RT01/IN01/JS01/MP01 |
| OP362 | `iam-list-member-authorization-sources`<br>`GET /tenants/{tenant_id}/members/{principal_id}/authorization-sources` | H | P:tenant_id; P:principal_id | — | `internal/iam/core/http.go:1134` | RT01/IN01 |
| OP363 | `iam-list-tenant-member-roles`<br>`GET /tenants/{tenant_id}/members/{principal_id}/roles` | H | P:tenant_id; P:principal_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:1122` | RT01/IN01 |
| OP364 | `organization-list-invitations`<br>`GET /tenants/{tenant_id}/membership-invitations` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/organization/http.go:637` | RT01/IN01 |
| OP365 | `organization-create-invitation`<br>`POST /tenants/{tenant_id}/membership-invitations` | H | P:tenant_id | application/json: CreateInvitationInputBody | `internal/iam/organization/http.go:654` | RT01/IN01/JS01 |
| OP366 | `iam-menu-catalog`<br>`GET /tenants/{tenant_id}/menu-catalog` | H | P:tenant_id | — | `internal/iam/core/http.go:514` | RT01/IN01 |
| OP367 | `iam-list-menus`<br>`GET /tenants/{tenant_id}/menus` | H | P:tenant_id | — | `internal/iam/core/http.go:1150` | RT01/IN01 |
| OP368 | `iam-create-menu`<br>`POST /tenants/{tenant_id}/menus` | H | P:tenant_id | application/json: CreateMenuInputBody | `internal/iam/core/http.go:1161` | RT01/IN01/JS01 |
| OP369 | `oauth-list-clients`<br>`GET /tenants/{tenant_id}/oauth-clients` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/oauth/http.go:598` | RT01/IN01 |
| OP370 | `oauth-create-client`<br>`POST /tenants/{tenant_id}/oauth-clients` | H | P:tenant_id | application/json: ClientCreateInputBody | `internal/iam/oauth/http.go:612` | RT01/IN01/JS01 |
| OP371 | `oauth-list-exchange-grants`<br>`GET /tenants/{tenant_id}/oauth-exchange-grants` | H | P:tenant_id | — | `internal/iam/oauth/http_device.go:129` | RT01/IN01 |
| OP372 | `oauth-create-exchange-grant`<br>`POST /tenants/{tenant_id}/oauth-exchange-grants` | H | P:tenant_id | application/json: ExchangeGrantCreateInputBody | `internal/iam/oauth/http_device.go:145` | RT01/IN01/JS01 |
| OP373 | `oauth-device-request`<br>`GET /tenants/{tenant_id}/oauth/device-requests` | H | P:tenant_id; Q:user_code | — | `internal/iam/oauth/http_device.go:90` | RT01/IN01 |
| OP374 | `oauth-device-decision`<br>`POST /tenants/{tenant_id}/oauth/device-requests/decisions` | H | P:tenant_id | application/json: DeviceDecisionInputBody | `internal/iam/oauth/http_device.go:103` | RT01/IN01/JS01 |
| OP375 | `iam-permission-catalog`<br>`GET /tenants/{tenant_id}/permission-catalog` | H | P:tenant_id | — | `internal/iam/core/http.go:494` | RT01/IN01 |
| OP376 | `organization-list-groups`<br>`GET /tenants/{tenant_id}/principal-groups` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/organization/http.go:384` | RT01/IN01 |
| OP377 | `organization-create-group`<br>`POST /tenants/{tenant_id}/principal-groups` | H | P:tenant_id | application/json: CreateGroupInputBody | `internal/iam/organization/http.go:400` | RT01/IN01/JS01 |
| OP378 | `identity-list-principals`<br>`GET /tenants/{tenant_id}/principals` | H | P:tenant_id; Q:search; Q:limit; Q:offset | — | `internal/iam/identity/http.go:133` | RT01/IN01 |
| OP379 | `identity-create-principal`<br>`POST /tenants/{tenant_id}/principals` | H | P:tenant_id | application/json: PrincipalCreateInputBody<br>application/octet-stream: string | `internal/iam/identity/http.go:166` | RT01/IN01/JS01/MP01 |
| OP380 | `identity-get-principal`<br>`GET /tenants/{tenant_id}/principals/{principal_id}` | H | P:tenant_id; P:principal_id | — | `internal/iam/identity/http.go:196` | RT01/IN01 |
| OP381 | `identity-update-principal`<br>`PATCH /tenants/{tenant_id}/principals/{principal_id}` | H | P:tenant_id; P:principal_id | application/json: PrincipalUpdateInputBody<br>application/octet-stream: string | `internal/iam/identity/http.go:220` | RT01/IN01/JS01/MP01 |
| OP382 | `iam-member-entities`<br>`GET /tenants/{tenant_id}/principals/{principal_id}/entities` | H | P:tenant_id; P:principal_id; Q:permission | — | `internal/iam/core/http.go:1273` | RT01/IN01 |
| OP383 | `iam-effective-menus`<br>`GET /tenants/{tenant_id}/principals/{principal_id}/menus` | H | P:tenant_id; P:principal_id | — | `internal/iam/core/http.go:1310` | RT01/IN01 |
| OP384 | `iam-effective-tenant-permissions`<br>`GET /tenants/{tenant_id}/principals/{principal_id}/permissions` | H | P:tenant_id; P:principal_id | — | `internal/iam/core/http.go:1347` | RT01/IN01 |
| OP385 | `governance-list-my-access-requests`<br>`GET /tenants/{tenant_id}/principals/{principal_id}/role-access-requests` | H | P:tenant_id; P:principal_id | — | `internal/iam/governance/http.go:129` | RT01/IN01 |
| OP386 | `identity-delete-principal-suspension`<br>`DELETE /tenants/{tenant_id}/principals/{principal_id}/suspension` | H | P:tenant_id; P:principal_id | — | `internal/iam/identity/http.go:271` | RT01/IN01 |
| OP387 | `identity-create-principal-suspension`<br>`PUT /tenants/{tenant_id}/principals/{principal_id}/suspension` | H | P:tenant_id; P:principal_id | — | `internal/iam/identity/http.go:247` | RT01/IN01 |
| OP388 | `governance-list-requestable-roles`<br>`GET /tenants/{tenant_id}/requestable-roles` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/governance/http.go:86` | RT01/IN01 |
| OP389 | `governance-list-access-requests`<br>`GET /tenants/{tenant_id}/role-access-requests` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/governance/http.go:148` | RT01/IN01 |
| OP390 | `governance-create-access-request`<br>`POST /tenants/{tenant_id}/role-access-requests` | H | P:tenant_id | application/json: CreateRequestInputBody | `internal/iam/governance/http.go:101` | RT01/IN01/JS01 |
| OP391 | `governance-list-access-reviews`<br>`GET /tenants/{tenant_id}/role-access-reviews` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/governance/http.go:223` | RT01/IN01 |
| OP392 | `governance-create-access-review`<br>`POST /tenants/{tenant_id}/role-access-reviews` | H | P:tenant_id | application/json: CreateReviewInputBody | `internal/iam/governance/http.go:238` | RT01/IN01/JS01 |
| OP393 | `iam-list-roles`<br>`GET /tenants/{tenant_id}/roles` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/core/http.go:728` | RT01/IN01 |
| OP394 | `iam-create-role`<br>`POST /tenants/{tenant_id}/roles` | H | P:tenant_id | application/json: CreateRoleInputBody | `internal/iam/core/http.go:742` | RT01/IN01/JS01 |
| OP395 | `federation-list-saml-service-providers`<br>`GET /tenants/{tenant_id}/saml-service-providers` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/federation/saml_http.go:56` | RT01/IN01 |
| OP396 | `federation-create-saml-service-provider`<br>`POST /tenants/{tenant_id}/saml-service-providers` | H | P:tenant_id | application/json: SamlSPBody | `internal/iam/federation/saml_http.go:69` | RT01/IN01/JS01 |
| OP397 | `provisioning-list-scim-directories`<br>`GET /tenants/{tenant_id}/scim-directories` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/provisioning/http.go:87` | RT01/IN01 |
| OP398 | `provisioning-create-scim-directory`<br>`POST /tenants/{tenant_id}/scim-directories` | H | P:tenant_id | application/json: DirectoryCreateInputBody | `internal/iam/provisioning/http.go:98` | RT01/IN01/JS01 |
| OP399 | `provisioning-list-scim-targets`<br>`GET /tenants/{tenant_id}/scim-targets` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/provisioning/http.go:715` | RT01/IN01 |
| OP400 | `provisioning-create-scim-target`<br>`POST /tenants/{tenant_id}/scim-targets` | H | P:tenant_id | application/json: TargetCreateInputBody | `internal/iam/provisioning/http.go:726` | RT01/IN01/JS01 |
| OP401 | `identity-list-service-accounts`<br>`GET /tenants/{tenant_id}/service-accounts` | H | P:tenant_id; Q:search; Q:limit; Q:offset | — | `internal/iam/identity/http.go:297` | RT01/IN01 |
| OP402 | `identity-create-service-account`<br>`POST /tenants/{tenant_id}/service-accounts` | H | P:tenant_id | application/json: ServiceAccountCreateInputBody | `internal/iam/identity/http.go:319` | RT01/IN01/JS01 |
| OP403 | `webhook-list-subscriptions`<br>`GET /tenants/{tenant_id}/webhook-subscriptions` | H | P:tenant_id; Q:offset; Q:limit | — | `internal/iam/webhook/http.go:54` | RT01/IN01 |
| OP404 | `webhook-create-subscription`<br>`POST /tenants/{tenant_id}/webhook-subscriptions` | H | P:tenant_id | application/json: SubscriptionBody | `internal/iam/webhook/http.go:73` | RT01/IN01/JS01 |
| OP405 | `delete-todo`<br>`DELETE /todos/{todo_id}` | H | P:todo_id; H:If-Match | — | `internal/todo/http.go:352` | RT01/IN01 |
| OP406 | `get-todo`<br>`GET /todos/{todo_id}` | H | P:todo_id | — | `internal/todo/http.go:307` | RT01/IN01 |
| OP407 | `update-todo`<br>`PATCH /todos/{todo_id}` | H | P:todo_id; H:If-Match | application/json: PatchInputBody<br>application/octet-stream: string | `internal/todo/http.go:325` | RT01/IN01/JS01/MP01 |
| OP408 | `delete-todo-deletion`<br>`DELETE /todos/{todo_id}/deletion` | H | P:todo_id; H:If-Match | — | `internal/todo/http.go:375` | RT01/IN01 |
| OP409 | `webhook-delete-subscription`<br>`DELETE /webhook-subscriptions/{subscription_id}` | H | P:subscription_id | — | `internal/iam/webhook/http.go:114` | RT01/IN01 |
| OP410 | `webhook-update-subscription`<br>`PUT /webhook-subscriptions/{subscription_id}` | H | P:subscription_id | application/json: SubscriptionBody | `internal/iam/webhook/http.go:93` | RT01/IN01/JS01 |
| OP411 | `webhook-list-deliveries`<br>`GET /webhook-subscriptions/{subscription_id}/deliveries` | H | P:subscription_id; Q:offset; Q:limit | — | `internal/iam/webhook/http.go:134` | RT01/IN01 |

## 4. 输入引用闭包（基线全部 398 操作）

这些引用只从 parameters/requestBody 起点遍历，不用响应字段冒充输入规则覆盖。每个字段的精确约束以提交的 OpenAPI 对应组件为证据；以下列出完整引用集合及直接字段。

| Schema | 直接属性/组合 | required |
|---|---|---|
| AcceptInvitationInputBody | display_name, login_name, password, token | token, login_name, password |
| AccountViewRequest | expires_at, mode, principal_id, scope_id, scope_type, target_type | scope_type, scope_id |
| AuthenticatorAssertionResponse | authenticatorData, clientDataJSON, signature, userHandle | authenticatorData, signature, clientDataJSON |
| AuthenticatorAttestationResponse | attestationObject, authenticatorData, clientDataJSON, publicKey, publicKeyAlgorithm, transports | authenticatorData, publicKey, publicKeyAlgorithm, attestationObject, clientDataJSON |
| AuthorizationCheckInputBody | entity_id, permission_code, resource_id, resource_type, subject | entity_id, permission_code, subject |
| AuthorizationConstraintInputBody | permission_code, subject | permission_code, subject |
| AuthorizationExplanationInputBody | entity_id, permission_code, resource_id, resource_type, subject | entity_id, permission_code, subject |
| AvatarInput | candidate_url, image_data, style | — |
| BulkOperation | bulkId, data, method, path, version | method, path |
| CandidateInputBody | style | — |
| ClientCreateInputBody | consent_mode, grant_types, name, public_client, redirect_uris, scopes | name, grant_types, scopes, public_client |
| ClientUpdateInputBody | consent_mode, grant_types, name, public_client, redirect_uris, scopes, status | name, grant_types, scopes, public_client, status |
| ClosureCancelInputBody | version | version |
| ClosureSubmitInputBody | proof, version | proof, version |
| ClosureUpdateInputBody | status, version | version, status |
| CreateDepartmentInputBody | entity_id, name, parent_id, sort_order, status | entity_id, name |
| CreateEntityInputBody | metadata, name, parent_id, status, type | type, name |
| CreateGroupInputBody | description, membership_rule, name, sort_order, status, type | name |
| CreateInputBody | description, due_at, priority, status, title | title |
| CreateInvitationInputBody | department_id, email, expires_in_hours, role_ids | email |
| CreateMenuInputBody | icon, label, parent_id, permission_code, route, sort_order, status, translations | label |
| CreatePlatformDepartmentInputBody | name, parent_id, sort_order, status | name |
| CreatePlatformGroupInputBody | description, membership_rule, name, sort_order, status, type | name |
| CreateRequestInputBody | access_expires_at, reason, role_id | role_id, reason, access_expires_at |
| CreateReviewInputBody | due_at, name | name, due_at |
| CreateRoleInputBody | description, name | name |
| CreateTenantInputBody | name, owner_principal_id, slug | slug, name, owner_principal_id |
| CreateTenantMemberInputBody | department_id, display_name, email, status, subject | subject, display_name |
| CredentialAssertionResponse | authenticatorAttachment, clientExtensionResults, id, rawId, response, type | response, rawId, id, type |
| CredentialCreateInputBody | expires_at, name | name |
| CredentialCreationResponse | authenticatorAttachment, clientExtensionResults, id, rawId, response, type | response, rawId, id, type |
| DecisionInputBody | decision, note | decision |
| DeviceDecisionInputBody | approve, user_code | user_code, approve |
| DirectoryCreateInputBody | name, service_account_id | name |
| DirectoryReplaceInputBody | name, service_account_id, status, version | name, status, version |
| ExchangeGrantCreateInputBody | client_id, scope, status, subject_prefix | client_id, subject_prefix, scope |
| FieldAccess | allow_roles, default, deny_roles | default, allow_roles, deny_roles |
| FieldPolicy | mask, read, write | read, write, mask |
| FieldPolicyInputBody | policy, version | version, policy |
| FileOperationDecisionInput | conflict_key, policy | conflict_key, policy |
| FileOperationInputBody | action, decisions, destination_id, directory_policy, file_policy, observed_revision, sources, type_policy | action, destination_id, sources, file_policy, directory_policy, type_policy |
| FileUpdateBody | name, revision, state | revision |
| GroupMembershipInputBody | ends_at, starts_at | — |
| IdentityOperationInputBody | existing_link_id, proof, provider_id, return_url | provider_id, proof |
| IdentityUnlinkInputBody | proof | proof |
| LeaveMembershipInputBody | proof | proof |
| LoginInputBody | captcha_answer, captcha_id, captcha_type, login_name, password, return_url | login_name, password, captcha_type, captcha_id, captcha_answer |
| MemberGrantInputBody | expires_at | — |
| MenuPlacementInput | kind, relative_to | kind |
| MfaCodeInputBody | code | code |
| MfaLoginInputBody | challenge_id, code | challenge_id, code |
| MfaPasswordInputBody | current_password | current_password |
| MfaPhoneCodeInputBody | challenge_id | challenge_id |
| MfaVerificationInputBody | code, current_password | current_password, code |
| OperatingPlatformMemberCreateInputBody | department_id, display_name, email, status, subject | subject, display_name |
| OperatingPlatformMemberPutInputBody | department_id, display_name, email, status | display_name |
| OperatingPlatformMenuInputBody | icon, label, parent_id, permission_code, route, sort_order, status, translations | label |
| OperatingPlatformRoleInputBody | description, name | name |
| Organization-register-tenantRequest | name, slug | slug, name |
| PasskeyDeleteInputBody | current_password | current_password |
| PasskeyFinishInputBody | challenge_id, credential | challenge_id, credential |
| PasskeyLoginBeginInputBody | return_url | — |
| PasskeyMutationInputBody | name | name |
| PasskeyRegistrationFinishInputBody | challenge_id, credential, name | challenge_id, name, credential |
| PasswordChangeInputBody | current_password, new_password | current_password, new_password |
| PasswordRecoveryCompleteInputBody | new_password, token | token, new_password |
| PasswordRecoveryStartInputBody | entry, identifier | identifier |
| PatchInputBody | description, due_at, priority, status, title | — |
| PatchOperation | op, path, value | op |
| PersonalTokenCreateInputBody | expires_at, name, permissions, proof, tenant_id | proof, name, tenant_id, permissions, expires_at |
| PersonalTokenRequest | expires_at, name, permissions, tenant_id | name, tenant_id, permissions, expires_at |
| PhoneLoginCompleteInputBody | code, phone, return_url | phone, code |
| PhoneLoginStartInputBody | captcha_answer, captcha_id, captcha_type, phone | phone, captcha_type, captcha_id, captcha_answer |
| PhoneVerificationCompleteInputBody | code | code |
| PlatformAnonymizeInputBody | confirmation, version | version, confirmation |
| PlatformRetentionInputBody | archive_after_days, min_days | min_days, archive_after_days |
| PlatformRetentionInputBody1 | legal_hold, reference, retain_until, version | version, retain_until, legal_hold, reference |
| PrincipalCreateInputBody | display_name, email, login_name, password | login_name, password |
| PrincipalUpdateInputBody | display_name, email, phone | — |
| ProviderBody | auto_provision, client_id, client_secret, default_role_id, issuer, name, provider_type, scopes, status | name, issuer, client_id |
| PutEntityRoleBindingInputBody | effect, expires_at | effect |
| PutPlatformAdministratorInputBody | full_administrator, permissions | full_administrator |
| ReauthenticationBeginInputBody | method, purpose, target | purpose, target, method |
| ReauthenticationVerifyInputBody | code, password, proof, response | proof |
| RegistrationInputBody | captcha_answer, captcha_id, captcha_type, display_name, email, entry, password | email, password, captcha_type, captcha_id, captcha_answer |
| RelationshipFields | condition, ends_at, entity_id, relation, resource_id, resource_type, starts_at, subject_id, subject_relation, subject_type | subject_type, subject_id, relation, resource_type, resource_id |
| ResendInvitationInputBody | expires_in_hours | — |
| RetentionInputBody | archive_after_days, min_days | min_days, archive_after_days |
| ReviewItemInputBody | decision, note | decision |
| ReviewUpdateInputBody | status | status |
| RevokeInputBody | reason | — |
| SCIMGroupMember | $ref, value | value |
| SamlSPBody | entity_id, metadata_xml, name, status | name, entity_id, metadata_xml |
| ServiceAccountCreateInputBody | description, display_name, expires_at, login_name | login_name |
| ServiceAccountCredentialCreateInputBody | expires_at, name, scopes | name, scopes |
| ServiceAccountReplaceInputBody | description, display_name, expires_at, status, version | display_name, status, version |
| SetPermissionConditionInputBody | condition | condition |
| SetRoleDataScopeInputBody | department_ids, scope | scope |
| StepUpVerifyInputBody | challenge_id, code | challenge_id, code |
| SubscriptionBody | event_types, name, secret, status, url | name, url |
| SystemDepartmentCreateInputBody | name, parent_id, sort_order, status | name |
| SystemMemberCreateInputBody | department_id, display_name, email, principal_id, status | principal_id, display_name |
| SystemMemberUpdateInputBody | department_id, display_name, email, status | — |
| SystemMenuCreateInputBody | icon, label, parent_id, permission_code, route, sort_order, status, translations | label |
| SystemPrincipalCreateInputBody | display_name, email, login_name, password | login_name, password |
| SystemPrincipalUpdateInputBody | display_name, email, phone | — |
| SystemRoleCreateInputBody | description, name | name |
| SystemScopeCreateInputBody | department_id, display_name, email, status, subject | subject, display_name |
| SystemScopeUpdateInputBody | department_id, display_name, email, status | — |
| TargetCreateInputBody | base_url, bearer_token, name, service_account_id | name, base_url, bearer_token |
| TargetPushInputBody | resource_id, resource_type | resource_type, resource_id |
| TargetReplaceInputBody | base_url, bearer_token, name, service_account_id, status, version | name, base_url, status, version |
| UpdateDepartmentInputBody | name, parent_id, sort_order, status, version | version |
| UpdateEntityInputBody | metadata, name, parent_id, status, type | — |
| UpdateGroupInputBody | description, membership_rule, name, sort_order, status, version | version |
| UpdateMenuInputBody | icon, label, parent_id, permission_code, placement, route, sort_order, status, translations | — |
| UpdatePlatformInputBody | name, status, version | version |
| UpdateRoleInputBody | description, name | — |
| UpdateTenantInputBody | name, status, version | version |
| UpdateTenantMemberInputBody | department_id, display_name, email, status | — |
| UserEmail | primary, type, value | value |
| UserName | formatted | — |

闭包共 122 个具名输入组件；遍历 1588 个输入 schema 节点（同一引用只展开一次，内联参数按出现计数）。

出现的全部输入 schema 键：`$ref, additionalProperties, contentEncoding, contentMediaType, default, description, enum, examples, format, items, maxItems, maxLength, maximum, minItems, minLength, minimum, oneOf, pattern, properties, required, type`。

输入 keywords 覆盖包含 contentEncoding/contentMediaType、oneOf、additionalProperties，不只是 min/max。未来新增关键字必须进入支持度与验收门禁，不能静默忽略。

## 5. 非业务路由附录与明确处理

开启全部可选诊断模块后，下列 chi 路由不来自 Adapter.Handle。它们不等于 27 个缺文档业务接口：包含 raw 模块本体、重定向、静态 UI 的通配方法和 Huma 自带 schema endpoint。

| METHOD 路径 | 处理 |
|---|---|
| `CONNECT /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `DELETE /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/config` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/database` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/database/` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/datasources` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/migrations` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/cmdline` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/profile` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/symbol` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/trace` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/ui` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /debug/sql` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /metrics` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /migration-status` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `GET /schemas/{schema}` | Huma 自带 schema endpoint：保留原行为，不作为业务表单元数据源；OpenAPI.Enabled 当前并不关闭该 endpoint |
| `HEAD /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `OPTIONS /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `PATCH /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `POST /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `PUT /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `QUERY /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |
| `TRACE /debug/pprof/ui/*` | 已有 raw/诊断/静态 UI 路由：保留模块开关和实际 handler，OPTIONS 不覆盖其通配协议 |

## 6. 实际验证

临时 Go overlay 测试复用 bootstrapComposedApp/newOpenAPIExportConfig、生产模块注册和 chi.Walk；测试文件位于 /tmp，没有写入应用源码。
执行：`go test -overlay /tmp/dynamic-rules-audit/overlay.json ./internal/app -run "^TestImpactInventory$" -count=1 -v`，以及设置 `IMPACT_ALL_INFRA=1` 的变体。两种装配 PASS。
临时 overlay 另加 OperationDocumenter 标记并逐项比对记录：PASS（372 typed 与 26 raw 分类相符）；其目的是验证推荐接线能力存在，不是实现 OPTIONS。
覆盖矩阵生成校验：411 个唯一操作、398 基线集合一致、源码位置存在、输入引用闭包均可解析。新功能验收 RT/IN/JS/MP/PR 尚未执行。
未执行所有 API 的业务请求；生产配置、外部供应商和真实反向代理行为未被装配测试证明。所有分类均有明确实施/保留边界，不以不可测的“绝无遗漏”作为结论。
