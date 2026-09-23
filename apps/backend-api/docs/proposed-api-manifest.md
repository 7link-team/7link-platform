# 优化后 API 定义清单

状态：提案，确认前不得修改运行时接口

日期：2026-09-23

## 契约规则

- URI 只表达业务资源、关系、表示或协议资源，动作由 HTTP Method 表达。
- `GET /me` 只用于登录后发现当前 `principal_id`，不建立第二套 `/me/*` 资源树。
- 本人和管理员使用同一套带目标 ID 的资源接口，差异由授权、字段过滤、二次认证和审计控制。
- 集合的创建和查询放在经过模型确认的直接 owner 下；不能根据可选外键猜测 owner。
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
| GET, PUT, DELETE | `/principals/{principal_id}/account-closure` | 读取、申请或取消一主体一份的账号关闭状态。 |

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
| GET | `/tenants/{tenant_id}/field-definitions` | 查询租户上下文中声明的字段能力。 |
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
| GET | `/files/{root_file_id}/-/{path}` | 从已授权根目录解析相对路径 metadata。 |
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
| GET | `/account-closures` | 供运营人员跨主体查询 Account Closure singleton。 |
| PATCH | `/principals/{principal_id}/account-closure` | 在同一个 singleton 上执行授权后的处理状态变更。 |
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

以上决策和本清单未明确确认前，不得修改运行时路由。
