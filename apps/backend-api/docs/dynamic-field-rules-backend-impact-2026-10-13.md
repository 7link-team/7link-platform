# 动态字段规则：后端全模块影响账本（2026-10-13）

本文件是设计影响审计，不是已实施功能证明。配套 `dynamic-field-rules-design-2026-10-13.md`；路径除另注明外相对 `apps/backend-api`。所有下面的动作均属于后续实现必做，不以“先只做邀请”结项。不得批量删除领域校验来满足“单一来源”。

## 1. 覆盖口径与分类

已枚举 `internal` 下排除测试、query/model/dbgen/proto 目录后的 **357 个生产 Go 文件**，扫描 `len/RuneCount/TrimSpace/regexp/Parse/Valid/validate/normalize`，并独立扫描全部 HTTP 注册、Schema、Unmarshal、RawBody、SkipValidate 标志。全部 **20 个拥有 HTTP 注册能力的模块**纳入下面清单；`internal/app` 是装配入口另列。357 是文件枚举/模式扫描覆盖，不是逐行人工审查数量，也不表示每个匹配都是字段校验。文末保留逐模块文件枚举，避免只按 IAM 或表单标签自称全覆盖。

校验必须分为：

- **D 声明规则**：长度、单位、格式、范围、enum、数组、规范化后的非空、无状态跨字段关系。统一模块自有 tag/声明，HTTP、服务入口及 OPTIONS 消费同源；常见可执行规则不能挂 server_only 逃避实现。
- **S 状态/安全规则**：唯一性、权限、租户与实体归属、版本、当前时间、状态转换、密码验证、签名、DNS/SSRF、资源预算。保留服务校验及既有状态码，提供安全可本地化字段或表单错误；静态 OPTIONS 不公开运行时安全事实。
- **P 原始协议**：OAuth/SCIM/SAML/WebAuthn/上传内容等。描述可公开形状和可执行字段规则，协议内容验证保留专属执行及响应格式。P 不等于“整模块不处理”：管理表单仍需 D 改造。
- **N 非请求输入**：启动配置、生成值、仓储完整性、后台任务、内部监测。保留；如同一函数兼供 HTTP 则其请求字段约束归 D/S，而不是按文件名排除。

## 2. 不能只添加 OPTIONS 的框架阻断

### B01 单一 tag 来源必须贯穿服务调用

证据：`todo/http.go:134` 的匿名 Body tag 与 `todo/todo.go:112` 的字节/trim 校验独立；`iam/authn/http.go:94` 与 `registration.go:56` 同样独立。服务同时被 HTTP、SCIM、邀请、引导及内部事务能力调用。只删服务 `len` 会让非 HTTP 调用失守；只给 OPTIONS 增加扩展而保留常数会维持三套政策。

实施：在**拥有模块根包**把共享字段声明提取到可复用的具名输入/字段类型，保留 JSON wire 字段形状及 PATCH presence。模块服务使用同一声明编译的校验入口；底层共用执行器可置现有 humax 扩展，不能创建中央业务 DTO/规则表。标准 Huma tag 负责标准约束，自定义 tag/类型声明明确 UTF-8 bytes、UTF-16 units、trim/NFC 校验投影、控制字符、跨字段等。现有 Schema 方法中的业务数值/enum 也要归同源声明，不能称“tag 唯一”却留第二张手写 schema。内部入口与 HTTP 有意不同的接受范围使用显式 profile/composition，不能盲目让内部服务继承全部 HTTP required/default。

规范化顺序必须冻结：HTTP 原始字段规则 → 服务现有规范化投影 → 服务字段规则 → 领域状态检查。验证投影不代表前端擅自改 payload。例如 raw title 256 字符两侧带空格当前会在 Huma 被拒，不能只验证 trimmed title 后放行；SCIM replacement 默认、todo 缺省/null、邮箱 display-name 排除同理。

验收 **BE01**：同一字段更改唯一声明后，OPTIONS、真实 HTTP、模块直接服务调用同步变化；三个入口旧接受集合的共同样例保持；变异恢复旧常数必须使漂移门禁失败。

### B02 Huma 注册期间冻结执行信息，不能事后改文档冒充运行时

依赖源 `github.com/danielgtaylor/huma/v2@v2.39.1/huma.go:800-895` 在注册时解析输入参数、defaults、resolvers 并捕获 handler；`:1008/:1153` 执行 Schema 校验；`:1450-1490` 获取输入 schema 并 PrecomputeMessages。`schema.go:246` 有预编译消息/正则内部字段；`validate.go:1040` 的 ModelValidator 使用 **ModeReadFromServer**，不能直接拿来充当请求验证（readOnly/writeOnly/required 方向不同）。

实施：声明编译及扩展执行接入须在注册/服务入口阶段完成，后置元数据步骤只做不可变快照；绝不运行时修改已发布 schema 指针。复用 `huma.Validate` 时显式 `ModeWriteToServer` 并使用正确 JSON/参数表示与并发隔离的 PathBuffer/ValidateResult。服务原生 Go struct 不能直接当 `map[string]any` 的 JSON 语义；序列化/适配必须保留 optional、null、默认与 ID 精度。扩展 tag 不会自动被 Huma 执行，必须有明确服务/请求校验挂接；只注册自定义 Schema.Extensions 不算实现。

OAuth 请求体的具体反例：`iam/oauth/http.go:806` 的 `documentOAuthForm` 在注册后补写五个 form body 的 RequestBody；Huma 已在注册时捕获 `hasInputBody/inSchema`。因此最终 schema 存在且 `SkipValidateBody=false` 仍不代表自动 body 校验已启用。`validation_coverage` 必须依据注册时实际输入与协议执行器记录，而非最终文档倒推；BE11 加入 consent/device/introspect/revoke/token 五个真实 form 请求的执行覆盖验证。

验收 **BE02**：注册前声明编译与 Huma 请求一致；并发校验无共享可变状态；readOnly/writeOnly、默认、PATCH 三态与字节扩展有真实请求测试。

### B03 runtime path、document path、typed/raw 三者不可混同

`files/http.go:434` 的 `documentFilePath` 把真实 catch-all path 从 Paths 删除、复制成常规 `{path}` 文档路径。`iam/provisioning/http.go:574`、`iam/federation/http.go:141`、`saml_http.go:136` 直接 AddOperation + Adapter.Handle；infra metrics/configinspector/migrationstatus/databasemonitor/pprof 则 AddOperation 后通过 RawHTTPRoutes 挂载。OnAddOperation 对上述所有来源都会触发，不能区分 typed 和 raw。

实施：在 humax 的 API/Adapter 装饰器记录实际 `Adapter.Handle` 的 method、runtimePath、operationId；最终按 operationId 连接完成后的公开 schema，保留 runtimePath 与 documentedPath。**仅 Adapter.Handle 记录仍不能区分 typed/raw**：可利用 Huma `OperationDocumenter.DocumentOperation`（huma.go:812）标记通过 huma.Register 的操作，同时透明转发默认文档行为；raw direct AddOperation/Handle 显式分类为协议，不靠是否存在 requestBody 猜测。RawHTTPRoutes 也需装配时登记，包含空 Method 的全方法 handler，避免 OPTIONS 被 raw route 覆盖或制造不存在的业务端点。该方案是待实现设计，需接口保持与三装配入口实测。无需业务模块批量换注册函数。

验收 **BE03**：文件多段路径 OPTIONS 正确匹配；SCIM/SAML 标为 raw 协议；infra docs-only/raw 无伪 typed 规则；同 operationId 映射冲突启动失败；无 Method raw 路由与 OPTIONS 冲突可检测；disabled/hidden 不从影子登记泄漏。

## 3. 全 HTTP 模块处理清单

每行列出具体规则责任文件，完整扫描文件全集见 §6。表中测试编号均为**后续必做，当前未执行**。

| 模块 / 路由源 | 已确认 tag 外约束与兼容风险 | 必须实施 / 保留 | 验收 ID |
|---|---|---|---|
| `todo`：`http.go`, `module.go` | `todo.go:112` trim 后非空、255 **bytes**，priority 1–5/status 重复声明；FieldError 是硬编码英文；`http.go:193` If-Match 接受 weak/quoted/bare、缺失 428；dueAtPatch 自定义 Schema/Unmarshal 三态。`list.go`, `columns.go`, `transfer.go` 有查询 DSL、CSV/XLSX 字段解析、导入上限/日期格式。 | D 同源 title/status/priority；转换 FieldError 为三语结构化 issue，不更改导入行错误协议。S 保留 ETag 并发、字段授权、数据范围。P 描述导入 format/file、内容规则服务端执行；查询 DSL 走既有 pkg/query 解析，不按普通自由字符串假定全部合法。 | BE04：255 ASCII/多字节、全空白、raw/trim 边界，due_at absent/null/value，If-Match 四种形态，导入 row errors 不被覆盖。 |
| `files`：`http.go`, `system.go` | `name.go:19` NFC、UTF-8/UTF-16 双上限255、控制/路径字符、尾空格点、Windows 保留名；multipart name maxLength 不涵盖它们。`http.go` createFile 有 kind/file/name/parent/scope 条件。`operation.go:51`, `operation_immediate.go:97` action/policy/ID/key128、source/decisions预算、observed_revision 与 decisions 联动；`browser.go:67` cursor/limit；`checksum.go:30` digest headers。 | D 文件名称和无状态操作形状同源并支持 UI；保留规范化行为与文件名 fallback。S 保留 quota/可写根/冲突/版本/预算及路径防穿越；预算不是静态 maxItems=1000 的同义词。P 上传内容/MIME/digest/大小保留流式限制；multipart 元数据与 JSON 不混用。 | BE05：NFC 等价、255 bytes vs UTF16、CON/COM¹、非法尾字符、空文件/目录、copy/move 分支、冲突业务 data 与字段 issue 共存、多段路由。 |
| `iam/authn`：`http.go`, `account_view_http.go`, `reauthentication_http.go`, `personal_token_http.go` | 注册 tag min8 codepoints，而 `registration.go:56` password min12 bytes/max1024 bytes；`web.go:735`, `recovery.go:226`, `bootstrap.go:141`, `verification.go:127`、`invitation.go`不同入口。`web.go:1389` normalizeLogin；`passkey.go:615` trim/name200 bytes；`personal_token.go:92` name128 runes/NUL、permission数组128、期限；`reauthentication.go:123` target512、purpose enum；`phone_verification.go:245` code digits；`account_view.go:92` scope/expiry组合。 | D 每入口接受集合分别声明；登录密码上限200 codepoints不可被“统一新密码强度”覆盖。EmailVerificationCredential custom Schema 条件分支、WebAuthn custom Schema/Unmarshal列入执行能力。S captcha/TOTP/recovery token/passkey cryptography/session/returnURL allowlist/时间窗/CSRF 保留，失败不得泄露账号或凭据。 | BE06：8字符≥12bytes 注册、11ASCII拒绝、1024bytes边界；注册/登录/改密/重置/验证/邀请/内部调用分开；phone标准化和验证码；passkey/captcha错误三语且无secret。 |
| `iam/identity`：`http.go`, `lifecycle_http.go`, `closure_http.go`, `system_accounts.go` | `service.go:103/164/259/400` 登录名200、密码12–1024bytes、display128、email320、ParseAddress精确地址；`:359` NormalizePhone 去格式/国际码4–15位。`service_account.go:739/750` 非空display、description1000、scopes128及词法；`avatar.go:268` image data2800000、decoded2MiB/图片尺寸与style；`lifecycle.go:142` reference200；`provisioning.go:168`, `bootstrap.go:110`内部入口。 | D profile/account/service-account/lifecycle 的可公开字段规则同源，跨模块通过 identity 能力复用而非反向引用 authn DTO。S 保留手机号唯一性/在用、账户状态、关闭/删除、头像内容解码资源限制。RawBody 字段写权限检测保留 presence。 | BE07：三语profile错误、phone display与规范值、PATCH/null、scope元素、avatar外层/内容分别报错、HTTP/SCIM/bootstrap兼容。 |
| `iam/organization`：`http.go`, `platform_http.go`, `platform_directory_http.go`, `system_http.go` | `domain.go:110` trim/name128 bytes/no controls、sort≤1000000；`group.go:118/145/180` description1024 bytes、dynamic/static rule及时间窗；`tenant.go:48/401` slug regex/小写trim，HTTP min3而内部生成允许短slug；`invitation.go:405` ParseAddress exact/320bytes/TTL/ID，`:326` password12bytes等；`MembershipRule.Schema`手写conditions≤16/values≤16/value320和core实际编译两处来源。 | D 共同规则声明及规范化；保留短内部slug profile，不能强制所有租户来源min3。动态组规则结构从owner声明导出，实际解释/字段依赖同源。S 保留租户存在、parent循环、实体归属、成员/role有效、邀请状态/期限。 | BE08：汉字name/description、control、slug1/2/3内部和HTTP分别锁定；邀请expires/roles/email；静态组禁止rule/动态组规则、时间前后序。 |
| `iam/core`：`http.go`, `platform_http.go`, `operating_platform_http.go`, `field_policy_http.go`, `member_exit_http.go`, `system_menu_http.go`, `system_access_http.go`, `system_scope_http.go`, `relationship_http.go` | `service.go:200/257` role name128bytes/description4096；`service_admin.go:46/666` profile、menu label128bytes/route512/icon64，translations≤32、locale≤63/valid language、label128runes；`entity.go:555/658` type词法64/name200；`relationship.go:391/401` window/subject-resource组合，policyCondition Schema仅开放object；`data_scope.go:262` scope/department组合；`field_policy.go`, `dynamic_group.go`委派权限DSL；`tenantbootstrap.go:288`内部初始成员。 | D 无状态结构、词法、菜单翻译、跨字段关系同源；不要把整个 condition 标未知而漏掉已知 AST 结构校验。S 受信认证上下文求值、权限目录是否存在、防提升/最后管理员/owner保护、租户隔离、关系循环/深度/成员继承、field write授权保留。 | BE09：role/menu/entity多字节、locale与翻译key path、data_scope组合、relationship AST错误/时间窗、旧权限拒绝及内部bootstrap未放宽。 |
| `iam/governance`：`http.go` | `governance.go:147/241/320` trim reason3–500bytes/note500、access expiry now..maxAccessDuration；`review.go:112/219` name3–128bytes、review due未来上限、decision/note。 | D 长度/trim/enum/固定跨字段同源；S 当前时间及可申请角色、审批主体、已有授予、幂等decision/状态转换保留；静态schema不能固化启动时“now”。 | BE10：trim后短字符串、多字节、keep/revoke/note，时间边界使用注入时钟，过期/重复决定原状态码。 |
| `iam/oauth`：`http.go`, `http_device.go` | `service.go:1132` client name128bytes、public禁止secret-only grants、authorization_code需redirect、URI语义、scope词法；`exchange.go:205` audience prefix128与期限；token/consent/device form schema供文档，RawBody url.ParseQuery路径，多个SkipValidateParams=true；`service.go:1230` PKCE字母表/长度。 | D 管理API同源完整执行；P OAuth form schema标注真实执行能力，并复用声明执行无状态字段规则且映射RFC error，不把文档tag当既有执行器。S client_secret/redirect注册匹配/consent挑战/device时序/token验证保留。OAuth error/error_description不能变平台422 envelope。 | BE11：public+client_credentials、缺redirect、URI fragment、scope whitespace、PKCE、重复form key/无效编码；真实token路径同源限制与RFC响应；内置consent页不绕过原CSRF。 |
| `iam/federation`：`http.go`, `identity_links_http.go`, `saml_http.go` | `service.go:819` name128bytes/clientID128/secret2048/scopes255、provider类型依赖字段、issuer/outbound校验；`binding_trust.go:89` evidence4096；`identity_links.go:61/399`复合ID/base64/claim限长；`connectors.go:526`第三方claims；SAML服务配置与XML协议在`saml.go`。 | D provider/SAML管理表单声明、类型依赖、key/certificate外层形状同源；S issuer discovery/DNS SSRF、signature/replay/issuer/audience、绑定凭据保留；P callback/SAML redirect/post与XML协议保持专属状态码/响应。 | BE12：各provider必填分支、scopes/issuer边界、callback原错误协议、有效/非法SAML、证据与证书不回显。 |
| `iam/provisioning`：`http.go`（target操作实现在`target_operations.go`） | `service.go:129/168/227` directory/credential trim/name128bytes/status/version/expiry；`target.go:105/151/652` target name128/token4096/url1024/SSRF；`scim.go:249/294` UserName200/DisplayName128/ExternalID512/email数组10/group members1000与presence，mail.ParseAddress；`patch.go:100`操作数/路径；`bulk.go` bulkID128/依赖；`filter.go`DSL。 | D 管理接口与原始SCIM的可公开字段结构同源，直接Adapter.Handle路径需显式调用执行器；P SCIM schemas/patch/filter/bulk仍使用既有解析器，错误映射SCIM detail/scimType，禁止套平台envelope。S bearer权限/版本/service account/出站URL策略/事务批量依赖保留。 | BE13：管理表单与SCIM full replace/patch absent/null一致、数组边界/邮箱display-name、bulk依赖/limit/filter、协议错误媒体类型与status，URL安全不退化。 |
| `iam/webhook`：`http.go` | `service.go:341/361/379` trim name128bytes、URL规范化2048bytes/安全策略、secret512bytes、event每项64无空白、去重后的joined1024bytes，status。 | D URL词法/长度、事件元素与集合聚合规则同源，不能仅把event_types设maxItems了事；S DNS/出站IP限制、订阅状态/重试/签名密钥保留；delivery/repository reason truncation是内部输出N。 | BE14：重复event去重前后/累计1024、中文name、URL trim/fragment、secret不回显，既有交付安全。 |
| `iam/audit`：`http.go`, `system_http.go`, `archive_http.go` | `audit.go:485` event/target64bytes、outcome、from<to及paging；`archive_http.go:34` retention1..36500与`governance.go`策略；`audit.go:531`内部EventInput/metadata限制；`archive.go`, `archive_jobs.go`, `erasure_ledger.go`证据链/存储检查。 | D 查询/导出同源filter/日期/retention基本规则；S from/to相对关系可声明，链完整性、retention政策及legal hold保留服务端。内部event生成限制N，不暴露成用户表单字段。 | BE15：from=to/反序、Unicode filter、无limit与分页差异、retention组合、stream/archive原响应不破坏。 |
| `infra/guid`：`http.go`, `id.go` | count1..100 tag；RequireID/OptionalID/guid.Parse 对positive int64字符串解析；regex19digits并不证明≤9223372036854775807。 | D 引用同一个ID语义声明，元数据与TS用字符串精确比较，optional空值profile保留；S ID服务可用性保留。 | BE16：0/-1/19digit溢出/leading zero/空optional；count边界。 |
| `infra/health`：`http/handler.go`, `health.go` | live/ready/health读取checker，无业务写入字段。 | 自动登记公开只读操作；无虚构字段规则；运行时健康状态为S。 | BE17：空请求契约、三个健康路由与禁用/导出一致。 |
| `infra/deployment`：`module.go` | 公开部署信息GET，无输入字段。 | 自动登记空请求契约；部署配置N不纳入用户字段规则。 | BE17：不泄漏配置/内部字段。 |
| `infra/metrics`：`module.go` | 手动AddOperation + RawHTTPRoutes，Prometheus文本。 | P记录真实raw来源、无JSON请求校验；不要生成typed请求假规则。 | BE18：开关/advertise/内容类型/raw OPTIONS冲突。 |
| `infra/pprof`：`module.go`, `ui.go` | 手动文档与raw profiler/UI、多路径，Go runtime协议query。 | P/N保留profiler协议与暴露控制；未公开的raw路径不得通过OPTIONS扩展暴露。 | BE18：profile duration等真实query仍由协议处理，无旁路暴露。 |
| `infra/configinspector`：`module.go` | 手动文档与raw GET、脱敏配置。 | P/N空输入，不生成配置编辑schema；保留脱敏及开关。 | BE18。 |
| `infra/migrationstatus`：`module.go` | 手动文档、raw当前/legacy别名。 | P/N按运行时别名登记，不重复出伪业务操作。 | BE18：legacy route及deprecated信息映射。 |
| `infra/databasemonitor`：`module.go`, `report.go`, `sql.go`, `ui.go` | raw JSON snapshot/SQL统计/console；SQL模板4096是内部观测上限，不是可提交SQL执行器。 | P/N保留监测行为，不把输出SQL统计tag当输入规则，不新增SQL运行接口。 | BE18：三类raw端点空输入、UI资源正常。 |

## 4. 跨模块及错误消息补充

1. **三语不是只补respx**：todo FieldError 的英文、模块 ErrInvalid 被 httpError 粗粒度映射、HTTP handler直接生成的 errors，必须提供稳定 code/message_key/安全 params，再接 zh-CN/en-US/ms-MY。各模块 `i18n/i18n.go` 与三份locales归模块所有。不可从翻译后的 message 反推 keyword。**BE19**：所有 D 规则均三语并可切换语言重译；S/P协议错误保留兼容字段，UI adapter映射可翻译安全提示，不输出key、不回显value。
2. **字段权限与presence**：todo/identity/core 的 RawBody submittedFields/requireProfileWrites/requireMemberProfileWrites不是冗余校验，不能被公共请求序列化吞掉。`field_policy.go` 以及`pkg/extension/authz`的写字段授权保留服务端；静态OPTIONS不能泄露可写字段动态授权。**BE20**：被拒字段含false/0/null时仍拦截，metadata不得绕过tenant/account-view权限。
3. **DSL并非没有规则**：`pkg/query`的filters/sort/fields，`pkg/extension/policyx/{member_rule,condition}.go`的dynamic-group/condition编译器由现有owner继续执行。两者raw JSON上限4096bytes，member-rule value320bytes/16conditions/16values，condition有operator形状、深度预算、标量/列表长度和timezone解析；这些不能被开放object schema掩盖。公开结构约束纳入声明，可信上下文求值留S；不要另造第二个解释器或把所有JSON object宣称本地可验证。**BE21**：原DSL正反例、未知字段、嵌套预算、object级错误回填。
4. **分页行为有意不同**：core/org/oauth等normalizePage允许内部默认/裁剪；外部tag对offset/limit先拒绝。不让同源重构改变内部服务调用政策，也不能UI自行clamp。**BE22**：HTTP omitted/default/0/negative/over-limit和直接服务调用分别冻结。
5. **数据存储约束是独立防线**：query/model/SQL被排除于本次请求规则逐文件审查，但字节长度往往保护列上限。不能为了“同步schema”放宽接受值而不检查数据库兼容。没有要求改表/迁移；当前方案保留实际接受集合，用声明表达交集。密码12bytes vs 12characters必须保留入口区别。

## 5. 实施门禁与实际验证

实施顺序：先冻结 BE01–BE22 旧行为样例 → 编译/登记基础设施 → 各模块同源声明和服务入口调用 → P协议适配 → 三语错误 → 前端同一案例集。所有20模块标记为已处理前，不接受整体任务PASS；BE01/02/03是基础阻断，不能留到“推广后”。

本次实际执行：rg全量枚举/注册/规则模式扫描、直接阅读表中关键函数、读取Huma v2.39.1依赖实现与当前OpenAPI。`go test ./pkg/extension/policyx ./pkg/query` PASS（分别2.012s/0.862s）；27组357个清单路径逐一存在性、BE01–BE22完整性、LF及限定文档diff空白检查PASS。这些是既有DSL基线与文档检查。没有修改任何生产代码，没有运行新的动态OPTIONS功能测试；所有BE测试编号是未来验收要求。文档引用路径/文件清单及git diff --check作为文档检查，不替代功能验证。实现完成按主方案跑backend make ci，并对每个模块执行直接服务与HTTP共享样例；原始协议和multipart要真实请求测试，不能仅检查schema字符串。

审计限制：全量文件模式扫描不能证明所有语义分支已逐行验证。本文提供完整模块/文件枚举与具体已确认规则责任；实施时必须以逐操作的实际请求样例补齐语义覆盖并记录处理状态。没有审计外部库所有内部安全检查、全部SQL列类型或生成代码，也没有修改这些范围。其存在不构成“前端已经支持全部规则”的证据。

## 6. 全量文件枚举与明确排除

下面为扫描时的生产文件全集（路径为internal下，子目录保留），用于核对新增/遗漏；已实现HTTP模块未排除任何一个。非HTTP infra/audit/telemetry 是中间件与观测，不构成独立表单；dlock/wuid 是内部服务。maintenance/principallifecycle没有HTTP registrar，但其中密码/身份入口与事务能力已纳入上述跨入口兼容要求。N文件保留原实现，不因扫描命中自动改动。

- `app` (12): `app.go`, `banner.go`, `bootstrap.go`, `config.go`, `debug.go`, `grpc.go`, `http.go`, `migration.go`, `module.go`, `moduleconfig.go`, `openapi.go`, `shutdown.go`.

- `files` (30): `account.go`, `account_cleanup.go`, `account_resource.go`, `basic.go`, `browser.go`, `checksum.go`, `http.go`, `i18n/i18n.go`, `import.go`, `local.go`, `metadata.go`, `migrate.go`, `module.go`, `move.go`, `name.go`, `operation.go`, `operation_execute.go`, `operation_immediate.go`, `operation_preflight.go`, `operation_service.go`, `permission.go`, `permission_store.go`, `resource_binding.go`, `root.go`, `scope.go`, `storage.go`, `system.go`, `trash.go`, `tree.go`, `version.go`.

- `iam/audit` (18): `anchor.go`, `archive.go`, `archive_http.go`, `archive_jobs.go`, `archive_management.go`, `archive_worker.go`, `audit.go`, `config.go`, `erasure_ledger.go`, `governance.go`, `http.go`, `i18n/i18n.go`, `metrics.go`, `migrate.go`, `module.go`, `repository.go`, `stream.go`, `system_http.go`.

- `iam/authn` (48): `account_view.go`, `account_view_http.go`, `bootstrap.go`, `captcha.go`, `captcha_turnstile.go`, `contracts.go`, `credentialstore.go`, `field_policy.go`, `foreign_tables.go`, `gocaptcha.go`, `http.go`, `i18n/i18n.go`, `invitation.go`, `lifecycle.go`, `metrics.go`, `mfa.go`, `migrate.go`, `module.go`, `passkey.go`, `personal_token.go`, `personal_token_http.go`, `phone_login.go`, `phone_verification.go`, `reauthentication.go`, `reauthentication_http.go`, `recovery.go`, `registration.go`, `repository.go`, `repository_account_view.go`, `repository_credentials.go`, `repository_identity.go`, `repository_mfa.go`, `repository_passkey.go`, `repository_recovery.go`, `repository_session.go`, `repository_stepup.go`, `repository_verification.go`, `returnurl.go`, `rewrap.go`, `shared_security.go`, `shared_security_development.go`, `shared_security_redis.go`, `signingkeys.go`, `sms_factor.go`, `stepup.go`, `tables.go`, `verification.go`, `web.go`.

- `iam/core` (47): `account_view.go`, `administrator.go`, `authorization.go`, `authorizer.go`, `authorizer_system.go`, `contracts.go`, `core.go`, `data_scope.go`, `directory_binding.go`, `domain.go`, `dynamic_group.go`, `entity.go`, `escalation.go`, `field_policy.go`, `field_policy_http.go`, `foreign_tables.go`, `http.go`, `i18n/i18n.go`, `lifecycle.go`, `member_authorization.go`, `member_exit.go`, `member_exit_http.go`, `membership.go`, `migrate.go`, `module.go`, `operating_platform_http.go`, `owner_kind.go`, `pagination.go`, `platform.go`, `platform_http.go`, `relationship.go`, `relationship_http.go`, `repository.go`, `repository_admin.go`, `review_grant.go`, `revision.go`, `service.go`, `service_admin.go`, `system_access.go`, `system_access_http.go`, `system_menu.go`, `system_menu_http.go`, `system_scope_http.go`, `tables.go`, `temporary_grant.go`, `tenantbootstrap.go`, `transaction.go`.

- `iam/federation` (25): `binding_trust.go`, `connector_alipay.go`, `connector_catalog.go`, `connector_qq.go`, `connector_wecom.go`, `connectors.go`, `domain.go`, `foreign_tables.go`, `http.go`, `i18n/i18n.go`, `identity_links.go`, `identity_links_http.go`, `legacy_startup.go`, `lifecycle.go`, `migrate.go`, `module.go`, `pagination.go`, `replay.go`, `repository.go`, `rewrap.go`, `saml.go`, `saml_http.go`, `service.go`, `state.go`, `verifier.go`.

- `iam/governance` (11): `foreign_tables.go`, `governance.go`, `http.go`, `i18n/i18n.go`, `lifecycle.go`, `migrate.go`, `module.go`, `pagination.go`, `repository.go`, `review.go`, `review_grants.go`.

- `iam/identity` (24): `avatar.go`, `avatar_render.go`, `bootstrap.go`, `closure.go`, `closure_http.go`, `closure_process.go`, `contracts.go`, `field_policy.go`, `foreign_tables.go`, `http.go`, `i18n/i18n.go`, `lifecycle.go`, `lifecycle_http.go`, `migrate.go`, `module.go`, `platform_status.go`, `principalstate.go`, `provisioning.go`, `repository.go`, `service.go`, `service_account.go`, `system_accounts.go`, `tables.go`, `tenantowner.go`.

- `iam/oauth` (15): `consent_page.go`, `device.go`, `exchange.go`, `foreign_tables.go`, `http.go`, `http_device.go`, `i18n/i18n.go`, `lifecycle.go`, `migrate.go`, `module.go`, `pagination.go`, `repository.go`, `service.go`, `tables.go`, `tenant_exit.go`.

- `iam/organization` (26): `authorization_source.go`, `contracts.go`, `domain.go`, `foreign_tables.go`, `group.go`, `group_repository.go`, `group_service.go`, `http.go`, `i18n/i18n.go`, `invitation.go`, `lifecycle.go`, `migrate.go`, `module.go`, `pagination.go`, `platform.go`, `platform_directory_http.go`, `platform_http.go`, `provisioning.go`, `repository.go`, `service.go`, `system.go`, `system_http.go`, `tables.go`, `tenant.go`, `tenant_registration.go`, `tenantlookup.go`.

- `iam/provisioning` (22): `bulk.go`, `config.go`, `discovery.go`, `domain.go`, `field_policy.go`, `filter.go`, `foreign_tables.go`, `http.go`, `i18n/i18n.go`, `lifecycle.go`, `metrics.go`, `migrate.go`, `module.go`, `pagination.go`, `patch.go`, `repository.go`, `resources.go`, `rewrap.go`, `scim.go`, `service.go`, `target.go`, `target_operations.go`.

- `iam/webhook` (9): `delivery.go`, `domain.go`, `http.go`, `i18n/i18n.go`, `metrics.go`, `migrate.go`, `module.go`, `repository.go`, `service.go`.

- `infra/audit` (16): `audit.go`, `client.go`, `config.go`, `context.go`, `event.go`, `grpc.go`, `http.go`, `migrate.go`, `module.go`, `operate.go`, `policy.go`, `recorder.go`, `redact.go`, `sink.go`, `sink_gorm.go`, `sink_slog.go`.

- `infra/configinspector` (1): `module.go`.

- `infra/databasemonitor` (4): `module.go`, `report.go`, `sql.go`, `ui.go`.

- `infra/deployment` (1): `module.go`.

- `infra/dlock` (3): `dlock.go`, `migrate.go`, `module.go`.

- `infra/guid` (6): `grpc.go`, `guid.go`, `http.go`, `i18n/i18n.go`, `id.go`, `module.go`.

- `infra/health` (6): `grpc/client.go`, `grpc/server.go`, `health.go`, `http/handler.go`, `i18n/i18n.go`, `service/service.go`.

- `infra/metrics` (1): `module.go`.

- `infra/migrationstatus` (1): `module.go`.

- `infra/pprof` (2): `module.go`, `ui.go`.

- `infra/telemetry` (6): `grpc.go`, `id.go`, `log.go`, `module.go`, `otlp.go`, `propagation.go`.

- `infra/wuid` (4): `migrate.go`, `module.go`, `sysinfo.go`, `wuid.go`.

- `maintenance` (6): `erasureledger.go`, `externalidentity.go`, `platformadmin.go`, `platformadminreset.go`, `secretrewrap.go`, `tenantrepair.go`.

- `principallifecycle` (1): `anonymizer.go`.

- `todo` (12): `authorization.go`, `columns.go`, `field_policy.go`, `http.go`, `i18n/i18n.go`, `list.go`, `migrate.go`, `module.go`, `rawquery.go`, `service.go`, `todo.go`, `transfer.go`.
