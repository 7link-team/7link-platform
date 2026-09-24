# 优化后 API 定义清单

状态：提案，确认前不得修改运行时接口

日期：2026-09-23（2026-09-24 补齐逐条映射）

清单分两部分：前半是目标契约，按 13 个 Swagger 域给出优化后的资源和 URI；
末尾的「旧接口到新接口的逐条映射」把当前生成文档里的 295 个操作逐个写明去向，
零条未映射。两部分合起来才构成一份可实施的清单 —— 只有目标而没有映射，
说明不了迁移中有没有东西被漏掉。

## 契约规则

- URI 只表达业务资源、关系、表示或协议资源，动作由 HTTP Method 表达。
- `GET /me` 只用于登录后发现当前 `principal_id`，不建立第二套 `/me/*` 资源树。
- 本人和管理员使用同一套带目标 ID 的资源接口，差异由授权、字段过滤、二次认证和审计控制。
- 集合的创建和查询放在经过模型确认的直接 owner 下；不能根据可选外键猜测 owner。
- 文件、菜单这类可以挂在多个组织层级上的资源，每个挂载点是一个独立集合，各自只返回自己那一层；上级看得到下级体现在他能打开下级的集合，不体现在某个集合把底下的都合并返回（D76）。
- 每个资源只有一套 representation、版本、授权和写入语义。
- Snowflake、opaque ID、分页、并发控制和错误响应使用共享 OpenAPI components。
- 普通列表统一使用 `limit`、`offset`、`sort` 和明确的过滤参数；文件目录可以使用 cursor。
- 有丢失更新风险的修改统一使用 `If-Match`。
- OAuth/OIDC、SAML、SCIM 和健康检查保留规范路径。

## 资源语义字典

| 资源名 | 明确定义 | 不表示什么 |
| --- | --- | --- |
| Principal | 平台级可认证主体及其全局 profile。 | 不是 Tenant 的成员记录。 |
| Tenant Membership | Principal 与 Tenant 之间具有状态和租户局部属性的成员关系。 | 不是 Principal，也不是 Role assignment。 |
| Entity | Tenant 下的业务/法律实体目录节点。 | 不是任意数据库实体。 |
| Department | Tenant 下的组织部门；可以关联 Entity，但 Entity 不是必需 owner。 | 不是文件目录。 |
| Group | Tenant 下用于目录归组和授权派生的主体集合。 | 不是 Role。 |
| Position | Tenant 下的组织岗位，成员关系可以带有效期。 | 不是 Principal profile 字段。 |
| Role | Tenant 下的 Permission Grant、Data Scope 和主体/目录 assignment 集合。 | 不是单个 Permission。 |
| Authorization Relationship | Subject 与业务 Resource 之间带 relation、条件和有效期的授权关系。 | 不是普通组织关系。 |
| Tenant Membership Invitation | 邀请某邮箱成为 Tenant Member 的安全资源。 | 不是泛化的项目/活动邀请。 |
| Role Access Request | Principal 申请 Tenant Role 及有效期的可审批资源。 | 不是通用工单或任意资源访问申请。 |
| Menu | Tenant 配置的导航定义，Permission 只影响可见投影。 | 不是授权机制。 |
| Authentication Transaction | 未认证调用者建立 Session 的多阶段登录事务。 | 不是已登录用户的二次认证。 |
| Reauthentication Transaction | 当前调用者为敏感操作证明自身凭据的短期事务。 | 不能验证或代替另一个 Principal。 |
| Session | 一次已认证浏览器会话；`current` 只是在未知 ID 时的 credential-bound alias。 | 不是 Principal。 |
| Account Closure | 每个 Principal 唯一一份的账号关闭状态。 | 当前不是具有历史记录和独立 ID 的 Request。 |
| File Operation | 以目标目录和 source IDs 执行的一次同步 copy/move mutation。 | 当前不是可轮询的后台 Job。 |

## 双地址规则

双地址是按实际使用场景选择的能力，不为所有 tenant-owned 资源机械生成。

以 `/instances/{instance_id}` 和
`/tenants/{tenant_id}/instances/{instance_id}` 为例：

1. 两条路径都先按同一个 `instance_id` 查询资源。
2. 短路径从资源反向解析真实 `tenant_id` 后授权。
3. scoped 路径额外比较真实 owner 与路径中的 `tenant_id`。
4. owner 不一致时，在业务执行前返回标准 `404 resource_not_found`。
5. 两条路径必须共用 schema、ETag/version、授权、Service、事务和审计事件。
6. 路径中的 tenant 只能用于校验，不能覆盖或补充资源的真实 owner。

当前只保留两个具有明确租户目录导航价值的双地址：

| 租户上下文地址 | 短地址 | 中文说明 |
| --- | --- | --- |
| `/tenants/{tenant_id}/entities/{entity_id}` | `/entities/{entity_id}` | 同一个实体；前者额外校验实体属于该租户。 |
| `/tenants/{tenant_id}/departments/{department_id}` | `/departments/{department_id}` | 同一个部门；前者额外校验部门属于该租户。 |

Group、Position、Role、Menu、Membership、Invitation、Service Account 和应用配置资源没有已证明的双地址需求，因此不生成 scoped item 路径。

## 共享参数与响应

| Component | 定义 |
| --- | --- |
| `SnowflakeId` | 十进制字符串，`^[1-9][0-9]{0,18}$`。 |
| `OpaqueId` | 按资源分别定义的 opaque string，不冒充 Snowflake。 |
| `Limit` | 整数，默认 50，最小 1，最大 200。 |
| `Offset` | 整数，默认 0，最小 0。 |
| `If-Match` | 有并发覆盖风险的更新和删除必须提交。 |
| `EvaluationContext` | `context_type=platform|tenant|entity`；非 platform 时必须提供对应 Snowflake `context_id`。 |
| 列表响应 | 统一为 `items`、`page.limit`、`page.offset`、`page.total`。 |
| 错误响应 | 统一错误 envelope 和稳定 error code。 |

### 状态码规则

| 场景 | 状态码 |
| --- | --- |
| 同步创建可寻址资源 | `201 Created`，返回资源和 `Location`。 |
| 已接受但异步处理 | `202 Accepted`，返回可查询的任务/事务引用。 |
| 同步计算、凭据消费或协议交换 | `200 OK`；无响应体时使用 `204 No Content`。 |
| PUT/PATCH 成功 | 返回表示时 `200 OK`，无表示时 `204 No Content`。 |
| DELETE 成功 | `204 No Content`；需要返回状态表示时才使用 `200 OK`。 |
| owner scoped item 的 owner 不匹配 | `404 resource_not_found`，避免泄露跨租户资源。 |

所有 Operation ID 全局唯一；双地址使用不同 Operation ID，但必须调用同一 Service 方法并引用同一 request/response schemas。

## `system`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET | `/healthz` | 聚合模块健康状态。 |
| GET | `/livez` | 进程存活检查。 |
| GET | `/readyz` | 依赖就绪检查。 |

删除公开的 `GET /guid` 和 `GET /guid/{count}`。ID 分配保留为内部能力，除非确认存在外部消费者。

## `authentication`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET | `/authentication-methods` | 返回启用的公开登录方式，替代 `/login/options`。 |
| POST | `/authentication-transactions` | 创建密码、Passkey 或联邦登录事务，返回事务状态和下一步 options。 |
| GET | `/authentication-transactions/{authentication_transaction_id}` | 读取非敏感事务状态和允许的下一步。 |
| PATCH | `/authentication-transactions/{authentication_transaction_id}` | 提交密码、MFA 或 Passkey assertion；完成时返回创建的 Session。 |
| POST | `/reauthentication-transactions` | 当前调用者为声明的敏感操作创建再认证事务，返回可用方式和 challenge。 |
| GET | `/reauthentication-transactions/{reauthentication_transaction_id}` | 读取当前调用者再认证事务的状态和允许的下一步。 |
| PATCH | `/reauthentication-transactions/{reauthentication_transaction_id}` | 提交密码、MFA 或 Passkey 证明；按 purpose 返回短期 credential 或提升当前 Session。 |
| POST | `/registration-challenges` | 创建注册验证码/挑战。 |
| POST | `/registrations` | 创建待验证的主体注册。 |
| POST | `/email-verification-challenges` | 签发邮箱验证凭据。 |
| POST | `/email-verifications` | 消费邮箱验证凭据。 |
| POST | `/password-recovery-challenges` | 签发密码恢复凭据，不泄露账号是否存在。 |
| POST | `/password-recoveries` | 消费恢复凭据并替换密码。 |
| POST | `/membership-invitation-acceptances` | 消费 Tenant Membership Invitation 凭据，并原子创建或绑定 Principal 与 Membership。 |

`/authentication-transactions` 整体替代 `/login`、`/login/mfa`、`/login/passkey/options` 和 `/login/passkey/verify`，不做零散改名。

`/reauthentication-transactions` 只绑定当前认证调用者和当前 Session，不接受要被验证的 `principal_id`。管理员可以管理目标 Principal，但不能代替目标主体完成再认证。

## `account`

该分组只放主体安全、凭据和个人资源；Principal 目录 CRUD 放在 `directory`，Menu 管理放在 `access-control`。

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET | `/me` | 只返回当前认证主体的发现信息和 `principal_id`。 |
| PUT, DELETE | `/principals/{principal_id}/suspension` | 应用或解除全局主体停用；管理员与本人复用同一路径。 |
| GET, PUT | `/principals/{principal_id}/avatar` | 读取或替换头像。 |
| POST | `/principals/{principal_id}/avatar-candidates` | 生成私有、短期头像候选。 |
| GET | `/principals/{principal_id}/avatar-candidates/{avatar_candidate_id}` | 读取一个私有头像候选。 |
| PATCH | `/principals/{principal_id}/password` | 在满足二次认证后修改密码。 |
| GET | `/principals/{principal_id}/mfa` | 读取 MFA 状态。 |
| POST | `/principals/{principal_id}/mfa/totp-enrollments` | 开始 TOTP 注册。 |
| PUT, DELETE | `/principals/{principal_id}/mfa/totp` | 确认或移除 TOTP factor。 |
| PUT | `/principals/{principal_id}/mfa/recovery-codes` | 替换全部恢复码。 |
| GET, POST | `/principals/{principal_id}/passkeys` | 查询或注册 Passkey。 |
| PATCH, DELETE | `/principals/{principal_id}/passkeys/{passkey_id}` | 重命名或删除 Passkey。 |
| POST | `/principals/{principal_id}/passkey-challenges` | 按声明用途创建 Passkey 注册/验证 challenge。 |
| GET, POST | `/principals/{principal_id}/personal-tokens` | 查询或创建 Personal Token；创建时直接校验可委托权限。 |
| DELETE | `/principals/{principal_id}/personal-tokens/{token_id}` | 撤销一个 Personal Token。 |
| POST | `/principals/{principal_id}/token-preparations` | 将 Token 权限与有效期绑定到一次二次认证。 |
| GET | `/principals/{principal_id}/sessions` | 查询主体的活动 Session。 |
| DELETE | `/principals/{principal_id}/sessions` | 撤销主体的全部 Session。 |
| GET, DELETE | `/sessions/current` | 当浏览器只有 Session Cookie、尚不知道 `session_id` 时读取或退出当前 Session。 |
| GET, DELETE | `/sessions/{session_id}` | 按返回的 Session ID 读取或撤销一个 Session。 |
| GET | `/principals/{principal_id}/identity-link-providers` | 查询可用于身份绑定的 Provider。 |
| POST | `/principals/{principal_id}/identity-link-transactions` | 创建身份绑定或重新认证事务。 |
| GET | `/principals/{principal_id}/identity-links` | 查询已绑定身份。 |
| DELETE | `/principals/{principal_id}/identity-links/{identity_link_id}` | 删除一个绑定，同时保证仍有可用登录方式。 |
| GET | `/principals/{principal_id}/oauth-consents` | 查询应用授权同意。 |
| DELETE | `/principals/{principal_id}/oauth-consents/{client_id}` | 撤回对一个应用的授权。 |
| GET, PUT, PATCH, DELETE | `/principals/{principal_id}/account-closure` | 读取、申请、处理或取消一主体一份的账号关闭状态；PATCH 的处理状态变更需要额外授权，但不另开 Swagger 分组。 |

删除 `/me/personal-token-permissions`，不增加换名字后的 Token 专用权限目录。

## `directory`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET, POST | `/tenants` | 查询或创建 Tenant。 |
| GET, PATCH, DELETE | `/tenants/{tenant_id}` | 读取、更新状态或软删除 Tenant。 |
| GET, POST | `/principals` | 查询有权查看的全局 Principal，或创建全局 Principal。Membership 单独创建。 |
| GET, PATCH | `/principals/{principal_id}` | 读取或更新全局 Principal profile，并执行字段过滤。 |
| GET, POST | `/tenants/{tenant_id}/memberships` | 查询租户 Membership，或为已有 Principal 创建 Tenant Membership。 |
| GET | `/principals/{principal_id}/memberships` | 查询一个 Principal 的全部 Tenant Membership。 |
| GET, PATCH, DELETE | `/tenant-memberships/{membership_id}` | 读取、更新、停用/退出或删除一个明确的 Tenant Membership。 |
| GET, POST | `/tenants/{tenant_id}/entities` | 查询或创建租户直属 Entity。 |
| GET, PATCH, DELETE | `/entities/{entity_id}` | 短地址：读取、更新或删除 Entity。 |
| GET, PATCH, DELETE | `/tenants/{tenant_id}/entities/{entity_id}` | scoped 地址：执行同一操作并校验真实 owner tenant。 |
| GET, POST | `/tenants/{tenant_id}/departments` | 查询或创建 Tenant Department；`entity_id` 仅为可选关联/过滤。 |
| GET, PATCH, DELETE | `/departments/{department_id}` | 短地址：读取、移动、更新或删除 Department。 |
| GET, PATCH, DELETE | `/tenants/{tenant_id}/departments/{department_id}` | scoped 地址：执行同一操作并校验真实 owner tenant。 |
| GET, POST | `/tenants/{tenant_id}/groups` | 查询或创建 Group。 |
| GET, PATCH, DELETE | `/groups/{group_id}` | 读取、更新或删除 Group；不生成 tenant scoped item 重复路径。 |
| GET | `/groups/{group_id}/members` | 查询 Group 与 Principal 的成员关系。 |
| PUT, DELETE | `/groups/{group_id}/members/{principal_id}` | 创建/更新或删除一个 Group-Member 关系。 |
| GET, POST | `/tenants/{tenant_id}/positions` | 查询或创建 Position。 |
| GET, PATCH, DELETE | `/positions/{position_id}` | 读取、更新或删除 Position；不生成 tenant scoped item 重复路径。 |
| GET | `/positions/{position_id}/members` | 查询 Position 与 Principal 的成员关系。 |
| PUT, DELETE | `/positions/{position_id}/members/{principal_id}` | 创建/更新或删除一个 Position-Member 关系。 |
| GET, POST | `/tenants/{tenant_id}/membership-invitations` | 查询或创建 Tenant Membership Invitation。 |
| DELETE | `/tenants/{tenant_id}/membership-invitations/{membership_invitation_id}` | 撤销待处理的 Membership Invitation，并校验它属于路径 Tenant。 |
| POST | `/tenants/{tenant_id}/membership-invitations/{membership_invitation_id}/deliveries` | 为该 Membership Invitation 创建轮换后的 Delivery credential。 |
| GET, POST | `/tenants/{tenant_id}/service-accounts` | 查询或创建 Service Account。 |
| GET, PUT, DELETE | `/service-accounts/{service_account_id}` | 读取、整体替换或删除 Service Account。 |
| GET, POST | `/service-accounts/{service_account_id}/credentials` | 查询或创建 Credential。 |
| DELETE | `/service-accounts/{service_account_id}/credentials/{credential_id}` | 撤销 Credential。 |

建议为 Membership 增加全局 `membership_id`：它具有独立状态、租户局部字段、时间戳、授权投影和生命周期，不应再拿 `principal_id` 冒充 Membership ID。迁移后删除整个 `/tenants/{tenant_id}/principals*` 家族。

## `access-control`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET, POST | `/tenants/{tenant_id}/roles` | 查询或创建 Role。 |
| GET, PATCH, DELETE | `/roles/{role_id}` | 读取、更新或删除 Role。 |
| GET | `/roles/{role_id}/members` | 查询直接 Principal assignment。 |
| PUT, DELETE | `/roles/{role_id}/members/{principal_id}` | 添加或删除 Principal assignment。 |
| GET | `/roles/{role_id}/permission-grants` | 查询包含 condition 的 Permission Grant。 |
| PUT, PATCH, DELETE | `/roles/{role_id}/permission-grants/{permission_code}` | 授予、更新 condition 或撤销 Permission Grant。 |
| GET, PUT | `/roles/{role_id}/data-scope` | 读取或整体替换 Role Data Scope。 |
| GET | `/roles/{role_id}/group-bindings` | 查询 Group assignment。 |
| PUT, DELETE | `/roles/{role_id}/group-bindings/{group_id}` | 添加或删除 Group assignment。 |
| GET | `/roles/{role_id}/position-bindings` | 查询 Position assignment。 |
| PUT, DELETE | `/roles/{role_id}/position-bindings/{position_id}` | 添加或删除 Position assignment。 |
| GET | `/entities/{entity_id}/role-bindings` | 查询 Entity 直接角色关系。 |
| PUT, DELETE | `/entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | 创建或删除复合 Entity-Role-Principal 关系。 |
| GET, POST | `/tenants/{tenant_id}/authorization-relationships` | 查询或创建租户拥有的 Authorization Relationship。 |
| GET, PATCH, DELETE | `/authorization-relationships/{authorization_relationship_id}` | 读取、更新或删除 Authorization Relationship。 |
| GET | `/platform/permission-definitions` | 查询平台 Permission 定义。 |
| GET | `/tenants/{tenant_id}/permission-definitions` | 查询租户 Permission 定义。 |
| GET | `/tenants/{tenant_id}/field-definitions` | 查询租户上下文中声明的字段能力。**未决**：见待确认决策 8，本行当前同时是 `field-catalog` 和 `effective-fields` 的唯一继任者，而两者授权模型不同。 |
| GET | `/tenants/{tenant_id}/field-policies` | 查询 Field Policy。 |
| GET, PUT, DELETE | `/tenants/{tenant_id}/field-policies/{resource}/{field}` | 按明确 tuple 读取、替换或恢复默认 Policy，不虚构 `policy_id`。 |
| GET | `/authorization-model` | 读取 Scope 与 Relationship 模型元数据。 |
| POST | `/tenants/{tenant_id}/authorization-evaluations` | 一次返回 decision、permissions、constraints、field capabilities 和可选 explanation。 |
| GET | `/principals/{principal_id}/permissions` | 使用共享 `EvaluationContext` 查询有效 Permission。 |
| GET | `/principals/{principal_id}/menus` | 使用明确 `tenant_id` 查询该租户上下文的有效导航。 |
| GET, POST | `/tenants/{tenant_id}/menus` | 查询或创建 Menu 管理资源。 |
| GET, PATCH, DELETE | `/tenants/{tenant_id}/menus/{menu_id}` | 读取、更新或删除 Tenant Menu，并校验 owner。 |
| GET | `/tenants/{tenant_id}/menu-definitions` | 查询当前租户可配置的 Menu metadata。 |
| GET | `/tenant-memberships/{membership_id}/roles` | 查询 Tenant Membership 的有效 Role。 |
| GET | `/tenant-memberships/{membership_id}/authorization-sources` | 解释 Tenant Membership 的直接和目录派生授权来源。 |
| GET | `/tenants/{tenant_id}/requestable-roles` | 查询可申请的 Role。 |
| GET, POST | `/tenants/{tenant_id}/role-access-requests` | 查询或创建 Tenant Role Access Request。 |
| PUT | `/role-access-requests/{role_access_request_id}/decision` | 写入审批决定。 |
| PUT | `/role-access-requests/{role_access_request_id}/withdrawal` | 撤回申请或主动放弃 Role Access。 |
| PUT | `/role-access-requests/{role_access_request_id}/grant-revocation` | 撤销已批准的临时 Role Grant。 |
| GET, POST | `/tenants/{tenant_id}/access-reviews` | 查询或创建 Tenant Access Review。 |
| GET, PATCH | `/access-reviews/{access_review_id}` | 读取、完成或取消 Review。 |
| PUT | `/access-reviews/{access_review_id}/items/{review_item_id}/decision` | 决定一个快照授权项。 |

建议为 Authorization Relationship 增加全局 `authorization_relationship_id`。当前七段复合键不适合作为稳定 item URI，并迫使 DELETE 接收八个 selector 参数。

## `files`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET, POST | `/principals/{principal_id}/files` | 查询主体文件根，或在主体空间创建目录/上传文件。 |
| GET, POST | `/tenants/{tenant_id}/files` | 查询租户文件根，或在租户空间创建目录/上传文件。 |
| GET, POST | `/entities/{entity_id}/files` | 查询实体文件根，或在实体空间创建目录/上传文件。 |
| GET, POST | `/departments/{department_id}/files` | 查询部门文件根，或在部门空间创建目录/上传文件。 |
| GET, PATCH, DELETE | `/files/{file_id}` | 读取 metadata、更新或移入回收站。 |
| GET | `/files/{file_id}/content` | 下载文件内容。 |
| GET | `/files/{file_id}/items` | 使用 cursor 查询目录分页。 |
| GET | `/files/{root_file_id}/-/{path}` | 从已授权根目录解析相对路径 metadata。**存量例外**：`/-/` 是分隔 root 与相对路径的哨兵段，不表达资源，与本文件第一条契约规则冲突；保留是因为相对路径必须与 `{file_id}` 段区分开，替代写法（查询参数）尚未评审。 |
| POST | `/files/{destination_file_id}/operations` | 对已授权 source IDs 同步执行 copy/move。Body 不重复 destination ID。 |

该 operation 返回已完成的同步 mutation，不伪装成可查询的后台 Job。

## `applications`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET, POST | `/tenants/{tenant_id}/identity-providers` | 查询或创建 Identity Provider。 |
| PUT, DELETE | `/identity-providers/{identity_provider_id}` | 整体替换或删除 Provider。 |
| GET, POST | `/identity-providers/{identity_provider_id}/callback` | OIDC/SAML 协议回调；只推进由 state 绑定的 Authentication/Identity-Link Transaction。 |
| GET, POST | `/tenants/{tenant_id}/oauth-clients` | 查询或创建 OAuth Client。 |
| PUT, DELETE | `/oauth-clients/{client_id}` | 整体替换或删除 Client。 |
| POST | `/oauth-clients/{client_id}/credentials` | 通过创建新 Credential 完成密钥轮换。 |
| GET, POST | `/tenants/{tenant_id}/saml-service-providers` | 查询或创建 SAML Service Provider。 |
| PUT, DELETE | `/saml-service-providers/{service_provider_id}` | 整体替换或删除 Service Provider。 |
| POST | `/tenants/{tenant_id}/saml-signing-keys` | 创建并激活 Signing Key。 |
| GET, POST | `/tenants/{tenant_id}/scim-directories` | 查询或创建入站 SCIM Directory。 |
| PUT | `/scim-directories/{scim_directory_id}` | 整体替换 Directory。 |
| GET, POST | `/scim-directories/{scim_directory_id}/credentials` | 查询或创建 Credential。 |
| DELETE | `/scim-directories/{scim_directory_id}/credentials/{credential_id}` | 撤销 Credential。 |
| GET, POST | `/tenants/{tenant_id}/scim-targets` | 查询或创建出站 SCIM Target。 |
| PUT, DELETE | `/scim-targets/{scim_target_id}` | 整体替换或删除 Target。 |
| POST | `/scim-targets/{scim_target_id}/provisioning-jobs` | 创建 Provisioning Job。 |
| POST | `/scim-targets/{scim_target_id}/deprovisioning-jobs` | 创建 Deprovisioning Job。 |
| PUT | `/scim-targets/{scim_target_id}/retirement` | 创建或继续 Retirement singleton。 |

## `audit`

Tenant 使用 `/tenants/{tenant_id}/audit/...`，Platform 使用 `/platform/audit/...`；两者共享以下 suffix 契约：

| Method | Suffix | 中文说明 |
| --- | --- | --- |
| GET | `/events`、`/events/{event_id}` | 查询不可变 Audit Event。 |
| GET | `/exports` | 导出已验证快照表示。 |
| GET | `/archive-status` | 读取归档执行状态。 |
| GET, POST | `/archives` | 查询或排队创建 Archive Job。 |
| POST | `/archives/{archive_id}/attempts` | 为失败/过期任务创建重试 Attempt。 |
| GET | `/archives/{archive_id}/content` | 下载已验证的不可变 Archive 内容。 |
| POST | `/archives/{archive_id}/verifications` | 创建归档对象 Verification。 |
| GET | `/governance-policy` | 读取治理状态。 |
| PUT | `/retention-policy` | 整体替换 Retention Policy。 |
| POST | `/anchors` | 创建外部 Audit Head Anchor。 |
| GET | `/integrity-verifications` | 计算并返回 Hash Chain Verification。 |
| POST | `/root-signatures` | 创建签名 Root Commitment。 |

Platform 当前只开放已实现的子集：archive status、archives、attempts、content、verifications 和 retention policy。

## `platform`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET | `/platform/administrators` | 查询 Platform Administrator 关系。 |
| PUT, DELETE | `/platform/administrators/{principal_id}` | 授予/替换或撤销 Platform 授权。 |
| GET | `/account-closures` | 供运营人员跨主体查询 Account Closure singleton。单个 closure 的读写在 `account` 分组的 `/principals/{principal_id}/account-closure` 上，不在此重复。 |
| GET | `/principals/{principal_id}/lifecycle` | 读取 Retention 和 Erasure blocker。 |
| PUT | `/principals/{principal_id}/lifecycle/retention-policy` | 替换 Retention/Legal Hold 状态。 |
| PUT | `/principals/{principal_id}/lifecycle/anonymization` | 满足前置条件后执行不可逆匿名化。 |

全局 suspension 复用 `account` 中的 `/principals/{principal_id}/suspension`，并保持唯一 `account` tag；不能为了管理员受众再创建 `/platform/principals` 或重复 Swagger operation。

## `oauth`、`saml`、`scim`

以下规范规定或已形成协议契约的路径保持不变：

- `/.well-known/openid-configuration`、`/.well-known/jwks.json`；
- `/oauth/authorize`、`/oauth/token`、`/oauth/revoke`、`/oauth/introspect`、`/oauth/userinfo`；
- `/saml/{tenant_id}/metadata`、`/sso`、`/slo`；
- `/scim/v2/Bulk`、`/Groups*`、`/Users*`、`/ResourceTypes*`、`/Schemas*`、`/ServiceProviderConfig`。

规范规定的大小写、参数、状态码和 media type 不受应用 URI 规则影响。

`/oauth/consent` 和 `/oauth/consent/request` 不是规范端点，按应用资源重新设计：

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET | `/oauth/authorization-requests/{authorization_request_id}` | 读取被暂停、等待用户决定的 Authorization Request。 |
| PUT | `/oauth/authorization-requests/{authorization_request_id}/decision` | 原子写入同意或拒绝决定，并返回后续重定向信息。 |

后端渲染的 Consent HTML 页面属于 UI 路由，不作为 JSON API 写入 OpenAPI。

## `todos`

| Method | URI | 中文说明 |
| --- | --- | --- |
| GET | `/entities/{entity_id}/todos` | 默认查询 JSON；`Accept: text/csv` 导出同一过滤结果。 |
| POST | `/entities/{entity_id}/todos` | `application/json` 创建一个 Todo；支持的导入 media type 导入集合。 |
| GET, PATCH, DELETE | `/todos/{todo_id}` | 读取、更新或标记删除 Todo。 |
| DELETE | `/todos/{todo_id}/deletion` | 删除 deletion marker，即恢复 Todo。 |
| GET | `/todo-import-template/content` | 下载导入模板表示。 |

通过 canonical Todo collection 的 `Content-Type`/`Accept` 完成导入导出，不再创建 `/export`、`/import` 动作路径。

## 删除或合并的接口

| 当前接口家族 | 处理结果 |
| --- | --- |
| `/me/*` | 只保留 `GET /me`，子资源迁移到真实 owner。 |
| `/tenants/{tenant_id}/me/*` | 删除；显式指定目标 Principal 和 evaluated context。 |
| `/tenants/{tenant_id}/principals*` | Principal/Membership 拆分后删除。 |
| `/platform/principals*` | 删除；特权调用者使用同一套 Principal 资源。 |
| 三套 scoped `*/file-operations` | 合并为 destination-file operation。 |
| `/me/personal-token-permissions` | 删除且不提供换名接口。 |
| `/roles/{role_id}/permissions` | 合并到 Permission Grant。 |
| Permission `/condition` 子资源 | condition 作为 Permission Grant 字段。 |
| authorization checks/explanations/constraints | 合并为一次 Authorization Evaluation。 |
| Session step-up challenge/verification 与 Principal reauthentication 三套接口 | 合并为调用者绑定的 Reauthentication Transaction；不允许对任意目标 Principal 做再认证。 |
| Identity Provider `/authorization` 和 `/identity-continuation` | 登录启动/状态统一由 Authentication Transaction 管理；Provider 只保留协议 callback。 |
| `/oauth/consent`、`/oauth/consent/request` | 改为 Authorization Request 和 Decision 资源；HTML 页面移出应用 OpenAPI。 |
| Todo `/export`、`/import` | 由 canonical collection content negotiation 取代。 |
| `/guid*` | 从公开 API 删除。 |

## 自审结论

- 已删除无依据的 Group、Position、Role 等 scoped item 双地址，只保留 Entity 和 Department 两组。
- Principal CRUD 只在 `directory` 定义一次；`account` 只管理安全和个人资源。
- Menu 管理已从 `account` 移到 `access-control`。
- Tenant-owned 的 Relationship、Access Request、Access Review 继续在 Tenant collection 下创建和查询。
- Field Policy 保留已经明确的 `(tenant_id, resource, field)` identity，不虚构 `policy_id`。
- Tenant Membership 同时连接 Tenant 与 Principal：两侧 collection 分别表达“租户成员列表”和“主体加入的租户”，item 始终使用同一个 `membership_id` 和 `/tenant-memberships/{membership_id}`，不建立两套 item 资源。
- Invitation 已明确命名为 Membership Invitation，管理接口始终保留 Tenant owner 路径，不提供语义过泛的 `/invitations` 或顶层 item。
- Scoped item 必须先按 item ID 找资源，再反查和比对 owner；禁止用路径 owner 改写查询结果。
- Session step-up 与 reauthentication 已合并为一套 Reauthentication Transaction，避免重复 ceremony，并修正“管理员可对目标 Principal 发起再认证”的错误资源语义。

## 待确认决策

1. 为 Tenant Membership 增加 `membership_id`；Tenant/Principal 两侧提供 collection，item 统一使用 `/tenant-memberships/{membership_id}`。
2. 为 Authorization Relationship 增加 `authorization_relationship_id`，item 使用 `/authorization-relationships/{authorization_relationship_id}`。
3. 一次性用 `/authentication-transactions` 替换完整 Login 家族，不做零散改名。
4. 按真实模型把 Role Directory Binding 拆成 Group Binding 和 Position Binding。
5. `/principals/{principal_id}/account-closure` 是本人/管理员共用 singleton；`/account-closures` 只做跨主体管理列表。
6. Todo 导入导出复用 canonical collection 的 media type。
7. 只为 Entity 和 Department 保留 tenant-scoped/global 双地址；其他资源有具体需求后再单独评审。

8. **Field 能力查询不能合并。** 现状是两个操作而非一个：`GET /tenants/{tenant_id}/field-catalog`
   经 `authz.Register` 注册，受 `Guard{Resource: "field_policy", Verb: "view"}` 保护，是管理员视角的
   声明目录；`GET /tenants/{tenant_id}/effective-fields` 经 `authz.RegisterTenantMember` 注册，任何
   租户成员都可以读自己的有效字段能力（`internal/iam/core/field_policy_http.go:50` 与 `:90`）。把两者
   并成一个 `field-definitions` 会把管理员目录和自助投影压进同一个授权模型。需要决定：保留两个资源
   并分别命名，还是定义一个带 `subject` 语义的资源并明确其授权。本清单在决定前不得按单行实施。
9. **D74 的措辞需要修正。** D74 写的是 `GET /me` 是唯一的当前主体别名，而本清单保留
   `GET, DELETE /sessions/current` 作为第二个凭据绑定别名并给出了理由。两者必须一致：要么修订 D74
   承认 Session 发现这一例外，要么去掉 `/sessions/current`。当前以未解决状态记录，不默认按任一方实施。

### 哪条决策挡住哪些操作

逐条映射里有 11 个操作标为**未决**。它们不是还没想，是目标取决于上面某条决策；
那条决策定下来之前，这 11 条不能实施，其余 284 条可以。

| 决策 | 挡住的操作数 | 具体是哪些 |
| --- | --- | --- |
| 1（Membership 身份） | 5 | `DELETE /me/memberships/{tenant_id}`、`GET`/`PATCH /tenants/{tenant_id}/members/{subject}`、该成员的 `/roles` 与 `/authorization-sources` |
| 2（Relationship 身份） | 1 | `DELETE /tenants/{tenant_id}/relationships`（当前靠查询参数选中要删的关系） |
| 4（Role Directory Binding 拆分） | 3 | `GET`/`PUT`/`DELETE /roles/{role_id}/directory-bindings*` |
| 8（Field 能力查询） | 2 | `GET /tenants/{tenant_id}/field-catalog` 与 `/effective-fields` |

其余五条（3 一次性替换 Login 家族、5 Account Closure singleton、6 Todo media type、
7 双地址范围、9 D74 措辞）是契约取舍，不挡任何操作的实施路径 —— 逐条映射里它们
已经有确定去向。

以上决策和本清单未明确确认前，不得修改运行时路由。

## 旧接口到新接口的逐条映射

这张表是本清单的完整性证明：当前生成文档里的每一个操作都在这里出现一次，
并写明它在新契约里的去向。没有这张表，前面的分组表只说明了目标长什么样，
说明不了有没有东西在迁移中被漏掉。

当前操作总数 295，保持 173，删除 3，合并 24，拆分 2，改名 82，未决 11。

处置的含义：**保持**指路径与方法不变（参数命名与共享 component 仍会统一）；
**改名**指同一个资源换到规范 URI；**合并**指多个操作收敛成一个；
**拆分**指一个操作按真实资源分成多个；**删除**指不再公开；
**未决**指目标依赖尚未确认的决策，实施前必须先把那条决策定下来。

| 当前分组 | 方法 | 当前 URI | 处置 | 去向 |
| --- | --- | --- | --- | --- |
| `access-control` | PUT | `/access-requests/{request_id}/decision` | 改名 | PUT /role-access-requests/{role_access_request_id}/decision |
| `access-control` | PUT | `/access-requests/{request_id}/grant-revocation` | 改名 | PUT /role-access-requests/{role_access_request_id}/grant-revocation |
| `access-control` | PUT | `/access-requests/{request_id}/withdrawal` | 改名 | PUT /role-access-requests/{role_access_request_id}/withdrawal |
| `access-control` | GET | `/access-reviews/{review_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | PATCH | `/access-reviews/{review_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | PUT | `/access-reviews/{review_id}/items/{item_id}/decision` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | DELETE | `/menus/{menu_id}` | 改名 | DELETE /tenants/{tenant_id}/menus/{menu_id} |
| `access-control` | GET | `/menus/{menu_id}` | 改名 | GET /tenants/{tenant_id}/menus/{menu_id} |
| `access-control` | PATCH | `/menus/{menu_id}` | 改名 | PATCH /tenants/{tenant_id}/menus/{menu_id} |
| `access-control` | GET | `/platform/permission-catalog` | 改名 | GET /platform/permission-definitions |
| `access-control` | DELETE | `/roles/{role_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/roles/{role_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | PATCH | `/roles/{role_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/roles/{role_id}/data-scope` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | PUT | `/roles/{role_id}/data-scope` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/roles/{role_id}/directory-bindings` | 未决 | 拆成 GET /roles/{role_id}/group-bindings 与 /position-bindings，等决策 4 |
| `access-control` | DELETE | `/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}` | 未决 | 拆成 DELETE /roles/{role_id}/group-bindings/{group_id} 与 /position-bindings/{position_id}，等决策 4 |
| `access-control` | PUT | `/roles/{role_id}/directory-bindings/{assignee_type}/{assignee_id}` | 未决 | 拆成 PUT /roles/{role_id}/group-bindings/{group_id} 与 /position-bindings/{position_id}，等决策 4 |
| `access-control` | GET | `/roles/{role_id}/members` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | DELETE | `/roles/{role_id}/members/{subject}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | PUT | `/roles/{role_id}/members/{subject}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/roles/{role_id}/permission-grants` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/roles/{role_id}/permissions` | 改名 | GET /roles/{role_id}/permission-grants |
| `access-control` | DELETE | `/roles/{role_id}/permissions/{permission_code}` | 改名 | DELETE /roles/{role_id}/permission-grants/{permission_code} |
| `access-control` | PUT | `/roles/{role_id}/permissions/{permission_code}` | 改名 | PUT /roles/{role_id}/permission-grants/{permission_code} |
| `access-control` | DELETE | `/roles/{role_id}/permissions/{permission_code}/condition` | 合并 | PATCH /roles/{role_id}/permission-grants/{permission_code}，condition 置空 |
| `access-control` | PUT | `/roles/{role_id}/permissions/{permission_code}/condition` | 合并 | PATCH /roles/{role_id}/permission-grants/{permission_code}，condition 是 grant 的字段 |
| `access-control` | GET | `/scope-model` | 改名 | GET /authorization-model |
| `access-control` | GET | `/tenants/{tenant_id}/access-requests` | 改名 | GET /tenants/{tenant_id}/role-access-requests |
| `access-control` | POST | `/tenants/{tenant_id}/access-requests` | 改名 | POST /tenants/{tenant_id}/role-access-requests |
| `access-control` | GET | `/tenants/{tenant_id}/access-reviews` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | POST | `/tenants/{tenant_id}/access-reviews` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | POST | `/tenants/{tenant_id}/authorization-checks` | 合并 | POST /tenants/{tenant_id}/authorization-evaluations |
| `access-control` | POST | `/tenants/{tenant_id}/authorization-constraint-evaluations` | 合并 | POST /tenants/{tenant_id}/authorization-evaluations |
| `access-control` | POST | `/tenants/{tenant_id}/authorization-explanations` | 合并 | POST /tenants/{tenant_id}/authorization-evaluations |
| `access-control` | GET | `/tenants/{tenant_id}/effective-fields` | 未决 | 决策 8：本人自助投影，与 field-catalog 授权模型不同，不能并成一条 |
| `access-control` | GET | `/tenants/{tenant_id}/entities/{entity_id}/me/permissions` | 改名 | GET /principals/{principal_id}/permissions，entity 求值上下文 |
| `access-control` | GET | `/tenants/{tenant_id}/field-catalog` | 未决 | 决策 8：管理员目录，与 effective-fields 授权模型不同，不能并成一条 |
| `access-control` | GET | `/tenants/{tenant_id}/field-policies` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | DELETE | `/tenants/{tenant_id}/field-policies/{resource}/{field}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | PUT | `/tenants/{tenant_id}/field-policies/{resource}/{field}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/tenants/{tenant_id}/me/access-requests` | 合并 | GET /tenants/{tenant_id}/role-access-requests，按申请人过滤 |
| `access-control` | GET | `/tenants/{tenant_id}/me/menus` | 改名 | GET /principals/{principal_id}/menus |
| `access-control` | GET | `/tenants/{tenant_id}/me/permissions` | 改名 | GET /principals/{principal_id}/permissions，tenant 求值上下文 |
| `access-control` | GET | `/tenants/{tenant_id}/menu-catalog` | 改名 | GET /tenants/{tenant_id}/menu-definitions |
| `access-control` | GET | `/tenants/{tenant_id}/menus` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | POST | `/tenants/{tenant_id}/menus` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/tenants/{tenant_id}/permission-catalog` | 改名 | GET /tenants/{tenant_id}/permission-definitions |
| `access-control` | DELETE | `/tenants/{tenant_id}/relationships` | 未决 | DELETE /authorization-relationships/{authorization_relationship_id}，等决策 2 |
| `access-control` | GET | `/tenants/{tenant_id}/relationships` | 改名 | GET /tenants/{tenant_id}/authorization-relationships |
| `access-control` | POST | `/tenants/{tenant_id}/relationships` | 改名 | POST /tenants/{tenant_id}/authorization-relationships |
| `access-control` | GET | `/tenants/{tenant_id}/requestable-roles` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | GET | `/tenants/{tenant_id}/roles` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `access-control` | POST | `/tenants/{tenant_id}/roles` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `account` | GET | `/me` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `account` | GET | `/me/avatar` | 改名 | GET /principals/{principal_id}/avatar |
| `account` | PUT | `/me/avatar` | 改名 | PUT /principals/{principal_id}/avatar |
| `account` | POST | `/me/avatar-candidates` | 改名 | POST /principals/{principal_id}/avatar-candidates |
| `account` | GET | `/me/avatar-candidates/{candidate_id}` | 改名 | GET /principals/{principal_id}/avatar-candidates/{avatar_candidate_id} |
| `account` | DELETE | `/me/closure-request` | 改名 | DELETE /principals/{principal_id}/account-closure |
| `account` | GET | `/me/closure-request` | 改名 | GET /principals/{principal_id}/account-closure |
| `account` | POST | `/me/closure-request` | 改名 | PUT /principals/{principal_id}/account-closure |
| `account` | GET | `/me/identity-link-providers` | 改名 | GET /principals/{principal_id}/identity-link-providers |
| `account` | POST | `/me/identity-link-transactions` | 改名 | POST /principals/{principal_id}/identity-link-transactions |
| `account` | GET | `/me/identity-links` | 改名 | GET /principals/{principal_id}/identity-links |
| `account` | DELETE | `/me/identity-links/{link_id}` | 改名 | DELETE /principals/{principal_id}/identity-links/{identity_link_id} |
| `account` | GET | `/me/memberships` | 改名 | GET /principals/{principal_id}/memberships |
| `account` | DELETE | `/me/memberships/{tenant_id}` | 未决 | DELETE /tenant-memberships/{membership_id}，等决策 1 定下 membership 身份 |
| `account` | GET | `/me/mfa` | 改名 | GET /principals/{principal_id}/mfa |
| `account` | PUT | `/me/mfa/recovery-codes` | 改名 | PUT /principals/{principal_id}/mfa/recovery-codes |
| `account` | DELETE | `/me/mfa/totp` | 改名 | DELETE /principals/{principal_id}/mfa/totp |
| `account` | POST | `/me/mfa/totp` | 改名 | PUT /principals/{principal_id}/mfa/totp |
| `account` | POST | `/me/mfa/totp-enrollments` | 改名 | POST /principals/{principal_id}/mfa/totp-enrollments |
| `account` | GET | `/me/oauth-consents` | 改名 | GET /principals/{principal_id}/oauth-consents |
| `account` | DELETE | `/me/oauth-consents/{client_id}` | 改名 | DELETE /principals/{principal_id}/oauth-consents/{client_id} |
| `account` | POST | `/me/passkey-registration-challenges` | 改名 | POST /principals/{principal_id}/passkey-challenges |
| `account` | GET | `/me/passkeys` | 改名 | GET /principals/{principal_id}/passkeys |
| `account` | POST | `/me/passkeys` | 改名 | POST /principals/{principal_id}/passkeys |
| `account` | DELETE | `/me/passkeys/{passkey_id}` | 改名 | DELETE /principals/{principal_id}/passkeys/{passkey_id} |
| `account` | PATCH | `/me/passkeys/{passkey_id}` | 改名 | PATCH /principals/{principal_id}/passkeys/{passkey_id} |
| `account` | PATCH | `/me/password` | 改名 | PATCH /principals/{principal_id}/password |
| `account` | GET | `/me/permissions` | 改名 | GET /principals/{principal_id}/permissions |
| `account` | GET | `/me/personal-token-permissions` | 删除 | Token 创建时直接校验可委托权限，不另设目录 |
| `account` | POST | `/me/personal-token-preparations` | 改名 | POST /principals/{principal_id}/token-preparations |
| `account` | GET | `/me/personal-tokens` | 改名 | GET /principals/{principal_id}/personal-tokens |
| `account` | POST | `/me/personal-tokens` | 改名 | POST /principals/{principal_id}/personal-tokens |
| `account` | DELETE | `/me/personal-tokens/{token_id}` | 改名 | DELETE /principals/{principal_id}/personal-tokens/{token_id} |
| `account` | POST | `/me/reauthentication/challenges` | 合并 | POST /reauthentication-transactions |
| `account` | GET | `/me/reauthentication/methods` | 合并 | GET /reauthentication-transactions/{reauthentication_transaction_id} |
| `account` | POST | `/me/reauthentication/verifications` | 合并 | PATCH /reauthentication-transactions/{reauthentication_transaction_id} |
| `account` | DELETE | `/me/session` | 改名 | DELETE /sessions/current |
| `account` | GET | `/me/session` | 改名 | GET /sessions/current |
| `account` | POST | `/me/session/step-up-challenges` | 合并 | POST /reauthentication-transactions |
| `account` | POST | `/me/session/step-up-verifications` | 合并 | PATCH /reauthentication-transactions/{reauthentication_transaction_id} |
| `account` | DELETE | `/me/sessions` | 改名 | DELETE /principals/{principal_id}/sessions |
| `account` | GET | `/me/sessions` | 改名 | GET /principals/{principal_id}/sessions |
| `account` | DELETE | `/me/sessions/{session_id}` | 改名 | DELETE /sessions/{session_id} |
| `account` | GET | `/me/tenants` | 合并 | GET /principals/{principal_id}/memberships，成员表示里带租户摘要 |
| `applications` | DELETE | `/identity-providers/{provider_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | PUT | `/identity-providers/{provider_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/identity-providers/{provider_id}/authorization` | 合并 | POST /authentication-transactions |
| `applications` | GET | `/identity-providers/{provider_id}/callback` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/identity-providers/{provider_id}/callback` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/identity-providers/{provider_id}/identity-continuation` | 合并 | GET /authentication-transactions/{authentication_transaction_id} |
| `applications` | DELETE | `/oauth-clients/{client_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | PUT | `/oauth-clients/{client_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/oauth-clients/{client_id}/credentials` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | DELETE | `/saml-service-providers/{sp_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | PUT | `/saml-service-providers/{sp_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | PUT | `/scim-directories/{directory_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/scim-directories/{directory_id}/credentials` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/scim-directories/{directory_id}/credentials` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | DELETE | `/scim-directories/{directory_id}/credentials/{credential_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | DELETE | `/scim-targets/{target_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | PUT | `/scim-targets/{target_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/scim-targets/{target_id}/deprovisioning-jobs` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/scim-targets/{target_id}/provisioning-jobs` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | PUT | `/scim-targets/{target_id}/retirement` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/tenants/{tenant_id}/identity-providers` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/tenants/{tenant_id}/identity-providers` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/tenants/{tenant_id}/oauth-clients` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/tenants/{tenant_id}/oauth-clients` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/tenants/{tenant_id}/saml-service-providers` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/tenants/{tenant_id}/saml-service-providers` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/tenants/{tenant_id}/saml-signing-keys` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/tenants/{tenant_id}/scim-directories` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/tenants/{tenant_id}/scim-directories` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | GET | `/tenants/{tenant_id}/scim-targets` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `applications` | POST | `/tenants/{tenant_id}/scim-targets` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `audit` | GET | `/platform/audit/archive-status` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/platform/audit/archives` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/platform/audit/archives` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/platform/audit/archives/{archive_id}/attempts` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/platform/audit/archives/{archive_id}/content` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/platform/audit/archives/{archive_id}/verifications` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | PUT | `/platform/audit/retention-policy` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/tenants/{tenant_id}/audit/anchors` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/archive-status` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/archives` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/tenants/{tenant_id}/audit/archives` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/tenants/{tenant_id}/audit/archives/{archive_id}/attempts` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/archives/{archive_id}/content` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/tenants/{tenant_id}/audit/archives/{archive_id}/verifications` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/events` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/events/{event_id}` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/exports` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/governance-policy` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | GET | `/tenants/{tenant_id}/audit/integrity-verifications` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | PUT | `/tenants/{tenant_id}/audit/retention-policy` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `audit` | POST | `/tenants/{tenant_id}/audit/root-signatures` | 保持 | audit 共享后缀契约，tenant 与 platform 同形 |
| `authentication` | POST | `/email-verification-requests` | 改名 | POST /email-verification-challenges |
| `authentication` | POST | `/email-verifications` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `authentication` | POST | `/invitation-acceptances` | 改名 | POST /membership-invitation-acceptances |
| `authentication` | POST | `/login` | 合并 | POST /authentication-transactions |
| `authentication` | POST | `/login/mfa` | 合并 | PATCH /authentication-transactions/{authentication_transaction_id} |
| `authentication` | GET | `/login/options` | 改名 | GET /authentication-methods |
| `authentication` | POST | `/login/passkey/options` | 合并 | POST /authentication-transactions |
| `authentication` | POST | `/login/passkey/verify` | 合并 | PATCH /authentication-transactions/{authentication_transaction_id} |
| `authentication` | POST | `/password-recoveries` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `authentication` | POST | `/password-recoveries/requests` | 改名 | POST /password-recovery-challenges |
| `authentication` | POST | `/registrations` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `authentication` | POST | `/registrations/captchas` | 改名 | POST /registration-challenges |
| `directory` | DELETE | `/departments/{department_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/departments/{department_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PATCH | `/departments/{department_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/entities/{entity_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/entities/{entity_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PATCH | `/entities/{entity_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/entities/{entity_id}/role-bindings` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PUT | `/entities/{entity_id}/role-bindings/{role_id}/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/groups/{group_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/groups/{group_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PATCH | `/groups/{group_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/groups/{group_id}/members` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/groups/{group_id}/members/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PUT | `/groups/{group_id}/members/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/invitations/{invitation_id}` | 改名 | DELETE /tenants/{tenant_id}/membership-invitations/{membership_invitation_id} |
| `directory` | POST | `/invitations/{invitation_id}/deliveries` | 改名 | POST /tenants/{tenant_id}/membership-invitations/{membership_invitation_id}/deliveries |
| `directory` | DELETE | `/positions/{position_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/positions/{position_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PATCH | `/positions/{position_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/positions/{position_id}/members` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/positions/{position_id}/members/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PUT | `/positions/{position_id}/members/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/principals/{principal_id}/avatar` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/service-accounts/{service_account_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/service-accounts/{service_account_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PUT | `/service-accounts/{service_account_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/service-accounts/{service_account_id}/credentials` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/service-accounts/{service_account_id}/credentials` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/service-accounts/{service_account_id}/credentials/{credential_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/tenants` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | DELETE | `/tenants/{tenant_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants/{tenant_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | PATCH | `/tenants/{tenant_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants/{tenant_id}/departments` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/tenants/{tenant_id}/departments` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants/{tenant_id}/entities` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/tenants/{tenant_id}/entities` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants/{tenant_id}/groups` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/tenants/{tenant_id}/groups` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants/{tenant_id}/invitations` | 改名 | GET /tenants/{tenant_id}/membership-invitations |
| `directory` | POST | `/tenants/{tenant_id}/invitations` | 改名 | POST /tenants/{tenant_id}/membership-invitations |
| `directory` | GET | `/tenants/{tenant_id}/members` | 改名 | GET /tenants/{tenant_id}/memberships |
| `directory` | POST | `/tenants/{tenant_id}/members` | 改名 | POST /tenants/{tenant_id}/memberships |
| `directory` | GET | `/tenants/{tenant_id}/members/{subject}` | 未决 | GET /tenant-memberships/{membership_id}，等决策 1 |
| `directory` | PATCH | `/tenants/{tenant_id}/members/{subject}` | 未决 | PATCH /tenant-memberships/{membership_id}，等决策 1 |
| `directory` | GET | `/tenants/{tenant_id}/members/{subject}/authorization-sources` | 未决 | GET /tenant-memberships/{membership_id}/authorization-sources，等决策 1 |
| `directory` | GET | `/tenants/{tenant_id}/members/{subject}/roles` | 未决 | GET /tenant-memberships/{membership_id}/roles，等决策 1 |
| `directory` | GET | `/tenants/{tenant_id}/positions` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/tenants/{tenant_id}/positions` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | GET | `/tenants/{tenant_id}/principals` | 拆分 | GET /principals 查全局目录；GET /tenants/{tenant_id}/memberships 查租户成员 |
| `directory` | POST | `/tenants/{tenant_id}/principals` | 拆分 | POST /principals 创建主体；POST /tenants/{tenant_id}/memberships 建立成员关系 |
| `directory` | GET | `/tenants/{tenant_id}/principals/{principal_id}` | 改名 | GET /principals/{principal_id} |
| `directory` | PATCH | `/tenants/{tenant_id}/principals/{principal_id}` | 改名 | PATCH /principals/{principal_id} |
| `directory` | DELETE | `/tenants/{tenant_id}/principals/{principal_id}/suspension` | 改名 | DELETE /principals/{principal_id}/suspension |
| `directory` | PUT | `/tenants/{tenant_id}/principals/{principal_id}/suspension` | 改名 | PUT /principals/{principal_id}/suspension |
| `directory` | GET | `/tenants/{tenant_id}/service-accounts` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `directory` | POST | `/tenants/{tenant_id}/service-accounts` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | GET | `/departments/{department_id}/files` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | POST | `/departments/{department_id}/files` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | GET | `/entities/{entity_id}/files` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | POST | `/entities/{entity_id}/files` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | DELETE | `/files/{file_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | GET | `/files/{file_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | PATCH | `/files/{file_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | GET | `/files/{file_id}/content` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | GET | `/files/{file_id}/items` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | GET | `/files/{root_file_id}/-/{path}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | POST | `/me/file-operations` | 合并 | POST /files/{destination_file_id}/operations |
| `files` | GET | `/me/files` | 改名 | GET /principals/{principal_id}/files |
| `files` | POST | `/me/files` | 改名 | POST /principals/{principal_id}/files |
| `files` | POST | `/tenants/{tenant_id}/entities/{entity_id}/file-operations` | 合并 | POST /files/{destination_file_id}/operations |
| `files` | POST | `/tenants/{tenant_id}/file-operations` | 合并 | POST /files/{destination_file_id}/operations |
| `files` | GET | `/tenants/{tenant_id}/files` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `files` | POST | `/tenants/{tenant_id}/files` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `oauth` | GET | `/.well-known/jwks.json` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `oauth` | GET | `/.well-known/openid-configuration` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `oauth` | GET | `/oauth/authorize` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `oauth` | GET | `/oauth/consent` | 改名 | GET /oauth/authorization-requests/{authorization_request_id} |
| `oauth` | POST | `/oauth/consent` | 改名 | PUT /oauth/authorization-requests/{authorization_request_id}/decision |
| `oauth` | GET | `/oauth/consent/request` | 合并 | GET /oauth/authorization-requests/{authorization_request_id} |
| `oauth` | POST | `/oauth/introspect` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `oauth` | POST | `/oauth/revoke` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `oauth` | POST | `/oauth/token` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `oauth` | GET | `/oauth/userinfo` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `platform` | GET | `/platform/administrators` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `platform` | DELETE | `/platform/administrators/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `platform` | PUT | `/platform/administrators/{principal_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `platform` | GET | `/platform/closure-requests` | 改名 | GET /account-closures |
| `platform` | PATCH | `/platform/principals/{principal_id}/closure-request` | 改名 | PATCH /principals/{principal_id}/account-closure |
| `platform` | GET | `/platform/principals/{principal_id}/lifecycle` | 改名 | GET /principals/{principal_id}/lifecycle |
| `platform` | PUT | `/platform/principals/{principal_id}/lifecycle/anonymization` | 改名 | PUT /principals/{principal_id}/lifecycle/anonymization |
| `platform` | PUT | `/platform/principals/{principal_id}/lifecycle/retention-policy` | 改名 | PUT /principals/{principal_id}/lifecycle/retention-policy |
| `platform` | DELETE | `/platform/principals/{principal_id}/suspension` | 改名 | DELETE /principals/{principal_id}/suspension |
| `platform` | PUT | `/platform/principals/{principal_id}/suspension` | 改名 | PUT /principals/{principal_id}/suspension |
| `saml` | GET | `/saml/{tenant_id}/metadata` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `saml` | GET | `/saml/{tenant_id}/slo` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `saml` | POST | `/saml/{tenant_id}/slo` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `saml` | GET | `/saml/{tenant_id}/sso` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `saml` | POST | `/saml/{tenant_id}/sso` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | POST | `/scim/v2/Bulk` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/Groups` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | POST | `/scim/v2/Groups` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | DELETE | `/scim/v2/Groups/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/Groups/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | PATCH | `/scim/v2/Groups/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | PUT | `/scim/v2/Groups/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/ResourceTypes` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/ResourceTypes/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/Schemas` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/Schemas/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/ServiceProviderConfig` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/Users` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | POST | `/scim/v2/Users` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | DELETE | `/scim/v2/Users/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | GET | `/scim/v2/Users/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | PATCH | `/scim/v2/Users/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `scim` | PUT | `/scim/v2/Users/{id}` | 保持 | 规范固定的协议路径，不受应用 URI 规则影响 |
| `system` | GET | `/guid` | 删除 | ID 分配不再作为公开接口 |
| `system` | GET | `/guid/{count}` | 删除 | ID 分配不再作为公开接口 |
| `system` | GET | `/healthz` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `system` | GET | `/livez` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `system` | GET | `/readyz` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `todos` | GET | `/entities/{entity_id}/todos` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `todos` | POST | `/entities/{entity_id}/todos` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `todos` | GET | `/entities/{entity_id}/todos/export` | 合并 | GET /entities/{entity_id}/todos，Accept: text/csv |
| `todos` | POST | `/entities/{entity_id}/todos/import` | 合并 | POST /entities/{entity_id}/todos，导入 media type |
| `todos` | GET | `/entities/{entity_id}/todos/import-template` | 改名 | GET /todo-import-template/content |
| `todos` | DELETE | `/todos/{todo_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `todos` | GET | `/todos/{todo_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `todos` | PATCH | `/todos/{todo_id}` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
| `todos` | DELETE | `/todos/{todo_id}/deletion` | 保持 | 同名同形，仅参数命名与共享 component 统一 |
