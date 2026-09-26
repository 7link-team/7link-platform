# API 迁移方案评审

日期：2026-09-25

结论：当前方案不通过整体实施评审。资源边界整理有价值，但不能把这份 URI
映射直接当成完整迁移规格。下列问题补齐之前，不应按清单批量替换运行接口。
本报告是评审结果，不修改运行契约，也不撤销 D74、D76、D77 已确认的设计规则。

## 基线与证据

- 基线提交：529e2cb，D78 已实现生成 OpenAPI paths 对请求模板、方法和参数的约束。
- 当前生成 OpenAPI：206 个路径、295 个操作。
- 对迁移映射逐行校验：295 行，缺失 0、过期路径 0、重复 0。
- 处置统计与清单一致：保持 173、改名 89、合并 24、拆分 5、删除 3、未决 1。
- 检查了目标资源表、迁移映射、D74-D78、现有 HTTP/service/model/SQL、客户端调用和测试。
- 这证明操作清点完整，不证明响应、授权、事务和存量迁移完整。
- 未评估部署中的存量行数、外部调用者或数据库容量；不假定这些数据为零。

## 发现

严重级别针对方案实施风险；不是宣称当前服务存在已证实的越权漏洞。

### R1 / P1：统一目标主体地址缺少授权与字段投影矩阵

位置：proposed-api-manifest.md:121、142、165、219。

`/me/*`、tenant principal、platform principal 合并到显式 principal ID 后，
同一条路由必须区分本人、同租户管理员、其他租户管理员、平台管理员，以及
Cookie、普通 Bearer、Personal Token。目标表只列资源和地址，未逐操作规定这些组合。

当前再认证与 Personal Token 接口依赖当前 Cookie，会拒绝 Bearer；个人文件列表
也验证当前主体，而非允许任意目标主体。全局 Principal 的字段过滤目前依赖租户
profileConstraint。删除 URI 的租户上下文后，这些限制不会自动变成正确的授权。

证据：internal/iam/authn/reauthentication_http.go:38；
internal/iam/authn/personal_token_http.go:93；
internal/iam/identity/http.go:154；internal/files/http.go:139。

要求：为每个迁移操作列出 actor/target、凭据类型、评估上下文、权限、字段投影、
fresh-auth、审计和拒绝语义。保留调用者绑定的再认证，不引入替目标主体证明凭据的能力。

### R2 / P1：Principal/Membership 拆分遗漏原子开户的失败语义

位置：proposed-api-manifest.md:165、639。

当前创建在一个数据库事务中写 Principal、密码凭据、Membership、策略版本和审计。
目标映射把它拆成 POST /principals 和 POST /tenants/{tenant_id}/memberships。
第二步失败或重试时如何处理已创建主体、如何避免重复创建，方案没有定义。
当前 PATCH 还同时更新全局 profile 与选定租户的成员副本，拆分后字段同步规则也未定义。

证据：internal/iam/identity/service.go:76、106、376、387。

要求：先定义全局字段与租户局部字段的写入规则；显式决定保留原子 onboarding
工作流，或采用具有幂等键、可恢复中间状态和完整审计的两步流程。不能由前端随意
删除第一步创建的主体来补偿，因为主体可能已被其他操作使用。

### R3 / P1：删除 Token 权限目录会使现有权限选择流程缺少替代契约

位置：proposed-api-manifest.md:157、351、507。

创建 Token 时校验权限，并不能替代创建之前发现可选权限。
现有页面依赖 personalTokenPermissions 返回当前租户实际允许的 Action metadata。
服务还过滤 platform 权限并检查成员资格。目标 /permissions 返回哪些数据、
/permission-definitions 是否允许普通成员访问，以及如何计算可委托集合，均未明确。

证据：apps/frontend-web-admin/apps/iam/src/app/account/tokens.tsx:109；
internal/iam/authn/personal_token.go:54、119。

要求：先定义普通成员可用的共享权限发现契约，保留 metadata、租户成员约束、
platform 过滤与创建时再次校验。替代能力完成前保留现有目录。无需创建第二个 Token 专用目录。

### R4 / P1：Session 的迁移不只是改名，目标表示缺失

位置：proposed-api-manifest.md:147、148、516。

当前 GET /me/session 与 GET /me 都调用 sessionView，返回 meOutput：subject、issuer、
认证信息及受控 profile 字段，没有 session ID、到期时间。它也接受 Bearer。
目标却定义 GET /sessions/current 为未知 ID 时的发现入口，并增加 GET /sessions/{id}。
直接换名字无法让客户端得到 ID，也无法定义 Bearer 不对应浏览器会话时的响应。

证据：internal/iam/authn/http.go:20、366、534；
internal/iam/authn/field_policy.go:87；pkg/extension/authn/model.go:91。

要求：分别定义 IdentityDiscovery 与 Session 的 schema；Session ID 必须是可公开的
会话标识，不能泄露原始 Cookie 凭据。明确认证类型、current 与 item 的一致表示、
过期和不存在语义、退出后的 Cookie 清理及重定向信息。前端身份读取迁移到身份契约。

### R5 / P1：认证事务合并缺少可实施的状态和一次性凭据契约

位置：proposed-api-manifest.md:103-121、355。

目标用 POST/GET/PATCH 事务取代密码、MFA、Passkey、联邦继续和 Session step-up，
但未给出状态枚举、允许的迁移、分支请求 schema、终态、过期、重复提交和并发竞争。
尤其“短期操作证明”与“提升 Session 并轮换 Cookie”有不同消费语义，不能只合并 URL。
当前接口有 Cookie/Origin 校验、限流和 purpose/target 绑定，必须逐项保留。

证据：internal/iam/authn/reauthentication_http.go:15、51、74；
internal/iam/authn/http.go:756、783。

要求：先给出状态机与请求/响应契约，以及 CSRF、限流、challenge 单次消费、
purpose/target、Session 轮换、旧凭据失效的验收条件。保留既有安全 service，
不因资源抽象引入重放敏感凭据的通用 PATCH 重试。

### R6 / P1：共享规则隐藏了映射表未标出的破坏性变更

位置：proposed-api-manifest.md:26、27、72、74、418。

方案要求统一列表 items/page、sort 和 If-Match；“保持”只保证路径/方法不变。
现有列表可能是 data 数组加 meta.page，也可能是 data.items/total；文件有 cursor。
现有并发检查使用 body version/revision 或查询参数。直接套用共享规则会影响
许多标为“保持”的操作、客户端和错误处理，89 条改名不是总迁移工作量。

证据：pkg/extension/huma/respx/envelope.go:20、30、62；
internal/iam/identity/http.go:179、111、117；internal/files/http.go:89、102。

要求：每个操作分别记录路径、请求、响应、授权、并发和副作用变化。
保留共享 respx；未证明收益前不要同时重写所有列表 envelope。
If-Match 若实施，必须定义 ETag 生成、读取返回、缺失/不匹配状态码、原子比较，
以及与旧 version/revision 的关系。修复具体丢失更新问题优先于统一参数位置。

### R7 / P1：文件操作按目的地寻址缺少上下文与幂等约束

位置：proposed-api-manifest.md:249。

三种挂载合并到 /files/{destination_file_id}/operations 有明确寻址收益，但现有
授权根据 claims 的 account/tenant/entity 上下文选择不同 authorizer；幂等记录
以 space、actor、key 为边界。仅把 destination 从 body 移到路径，不能证明新入口
推导的上下文与旧入口一致，也不能因此允许跨空间 copy/move。

证据：internal/files/operation_service.go:40、60、81、88、114。

要求：明确由目的地 owner resolver 推导上下文，逐一校验 sources 与目的地的权限，
保持现有跨空间限制、404 隐藏策略、冲突策略、空间版本和幂等键语义。保留同步执行性质。

### R8 / P2：Relationship ID 的论证夸大当前歧义，迁移规格仍不足

位置：proposed-api-manifest.md:235、382、398。

空字符串参与元组身份，不等于现有查询无法唯一定位。当前 DELETE 进行精确元组匹配，
必填 selector 有 Huma required 校验；group/position 缺 member relation 也会校验失败。
匹配不到返回 changed:false 不能单独证明删除错误。ID 的实际收益是减少客户端重构
元组的负担，并提供稳定 item 地址；D77 的 ID 方向可以保留，但论证应改正。

另一个事实错误：两张表的主键分别为 7 列和 8 列，不是都为 7 列。
当前 POST 是 upsert；新增 ID 必须保证更新既有关系时 ID 不变。方案还缺少跨两表
的 ID 唯一性、回填、并发写入、约束创建、回滚与前后行数校验。

证据：internal/iam/core/relationship_http.go:82；
internal/iam/core/relationship.go:210、398；internal/iam/core/model/relationship.go:9、28。

要求：保留原复合主键和唯一性，新增稳定 ID；补齐可测试的三数据库迁移方案。
把数据迁移影响关联到 collection GET/POST 和 item GET/PATCH/DELETE，不只标 DELETE 未决。

### R9 / P2：personal scope 待决项已经过期

位置：proposed-api-manifest.md:401。

方案称 personal 根仍可创建但不可列出。当前 ownerScope 已不包含 personal，
createFile 拒绝其创建，TestFileHTTPAuthorizedBasicRoutes 显式断言 422。
不过 multipart schema 仍列 account,personal，前端也保留 createPersonalFileRoot；
这是当前声明/客户端与实现不一致，不能继续写成待决定是否删除的产品选项。

证据：internal/files/scope.go:22；internal/files/http.go:64、387；
internal/files/http_test.go:302；apps/frontend-web-admin/apps/iam/src/lib/iam-api.ts:1463。

要求：更新方案事实。运行时已经拒绝的输入应从声明和无效客户端入口中清理；
是否还存在历史数据必须单独通过数据库审计确认，不能由拒绝新建推断存量为空。

### R10 / P2：多份规范互相冲突，实施者会得到相反指令

位置：http-resource-routing-design.md:3、47、63；
openapi-contract-review.md:3、47、68；proposed-api-manifest.md:3、17、51。

旧设计文档仍标 APPROVED，并允许 global/scoped 双地址、只允许 GET /me 发现别名；
新 D77 与 manifest 明确禁止双地址并允许 /sessions/current。旧评审还保留
Membership identity 未决等已被 D77 决定的内容。代码任务会因读取不同文档走向不同结果。

要求：保留历史记录，但标明被哪个决策取代、适用范围与唯一实施依据。
按 D77 使用一个 canonical URI，不创建兼容 alias。不要把设计规则的确认
误写成整份运行接口迁移已经通过实施评审。

## 收益、成本与建议

| 改动 | 保留现状的代价 | 迁移收益 | 本次评审建议 |
| --- | --- | --- | --- |
| Principal/Membership 边界 | 全局 profile 与租户成员写入耦合，多租户行为难理解 | 明确字段与生命周期，降低误用成本 | 保留目标；R1/R2 满足后才实施 |
| 显式主体与评估上下文 | 客户端学习多种本人/管理员入口，上下文容易隐含 | 统一寻址与授权测试 | 保留目标；权限矩阵是实施门槛 |
| Relationship ID | 操作关系要重构多个 selector，缺少稳定 item 地址 | 简化客户端，支持稳定读取/修改/撤销 | 保留 D77 方向；先完成迁移规格，不宣称现有 tuple 本身有歧义 |
| 身份与 Session 分开 | 两个地址返回同样身份快照，缺少真正 Session discovery | 表示与生命周期更清楚 | 明确 schema 后成组迁移；不只换名字 |
| 文件 operation 以目的地寻址 | 客户端根据挂载选择入口 | 从目标资源推导 owner，减少入口 | 保留目标；R7 满足后实施 |
| 统一认证事务 | 客户端处理多种认证流程 | 状态接口统一，扩展认证方式较方便 | 不纳入机械地址迁移；现有流程保持，状态机完成后单独评估实施 |
| 全量 envelope/If-Match/sort 统一 | 参数与响应位置不一致，客户端存在适配 | 减少部分适配、支持标准条件请求 | 不与地址迁移捆绑；保留 respx 与现有有效并发检查 |
| catalog/definitions、members/memberships 等纯改名 | 名称一致性较低 | 文档更明确 | 不单独立项；必要语义改造时采用目标命名 |
| 删除 personal scope | 方案状态过期；声明仍允许运行时拒绝的值 | 声明与实现一致 | 纠正事实与无效声明；历史数据处理以存量证据决定 |

## 可执行边界

1. 接受当前操作清点的完整性，拒绝把它当成完整契约和实施规格。
2. 保留已确认的资源模型方向；URI 美观不是重写安全流程或响应基础设施的理由。
3. 当前明确需要处理的是 R9 的契约事实漂移和 R10 的规范冲突；本次只记录评审发现。
4. R1-R8 的规格问题解决前，不批准对应迁移；不是默认排到“下一次”。
5. 实施采用既有 pre-production 规则：同一 checkpoint 更新后端、OpenAPI、前端与测试，
   不同时发布旧新应用路径。数据变更有独立可验证的迁移与恢复步骤。
6. 验收覆盖资源表示、本人/管理员/跨租户拒绝、Cookie/Bearer、字段过滤、并发、
   凭据单次消费、Cookie 轮换、幂等、审计，以及必要的数据迁移。D78 typecheck
   能发现路径错误，但不会替这些检查作证。

## 验证

- 静态映射核验：PASS，295/295，无缺失、过期路径或重复。
- 文件 Contract/AuthorizedBasicRoutes 定向测试：PASS。
- SessionAndTokenProfileEnforcement 定向测试：PASS。
- Relationship repository 两项定向测试：PASS（LifecycleIsolationAndNullableWindow、
  UsesOwningEntityAndExactTenantKey）。
- 未修改业务代码、生成契约、数据库或运行路由。未声称已验证目标方案运行行为。
