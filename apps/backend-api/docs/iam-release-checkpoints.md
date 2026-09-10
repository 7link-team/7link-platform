# IAM 发布前检查点、解决方案与验收

文档日期：2026-09-10。审计基线：`IAM@df8677b`；本轮修复随本文一同提交交付，提交标识以Git记录为准。
本文合并第一次审计、相关性二次评审、方案复审及实施证据。

**最新范围调整（2026-09-10）**：用户要求一次完成IAM应用功能与代码验收，暂不纳入生产部署、灾备、
HA演练和运维签字。应用所需的Redis Cluster/Sentinel配置、客户端连接及故障处理明确纳入，不能推给运维。
**本轮结论：IAM在约定的应用功能与代码范围内已就绪。** 个人能力、现有归属展示、完整界面矩阵、
Redis拓扑代码及相关门禁已经验收；此结论不包含生产部署或灾备演练。
第2.2节记录当前候选证据，第3节单列`34778e7`历史基线；生产运维条目不作为本轮应用验收退出条件。

## 1. 二次复核与相关性结论

| 原条目 | 复核判定 | 纠正与处置 |
|---|---|---|
| SCIM 多副本竞态 | 真，安全/生命周期阻塞 | 不仅多副本：单实例旧同步快照也可在 DELETE 后 PUT。首次 PUT 没有 mapping 时，还可能绕过 target 删除和 principal erasure 检查。不能仅加会过期的租约；显式重新 provisioning 则是合法既有能力 |
| 注销伪成功 | 真，安全相关高优先级 | 前端吞网络错误之外，后端 Logout 也忽略数据库撤销失败。修复须贯穿 HTTP、前端和 SAML SLO，不能只改界面 |
| Consent撤销后的access token | 真实联测追加，安全缺陷 | 列表删除、refresh/code撤销不等于access撤销；新建MySQL UAT确认introspection仍active。必须修复真实Bearer验证与重新授权隔离，不能调低测试期望 |
| 普通成员租户发现 | 真，功能阻塞 | 只请求 platform-only 目录且跨账号残留选择；不构成已证实的后端越权。原 P0 定级过度 |
| 主体管理空白 | 真，但原标题错误 | 实际为“有父节点的子主体设置页空白”。设置页应展示当前单节点，不擅自扩为子树管理；属于功能阻塞，不能等同数据丢失 P0 |
| 认证探测/菜单故障 | 真 | 网络、503、403不能一概当未登录；同作用域迟到菜单响应也不能覆盖新结果。失败、空数据与权限边界须分开 |
| Introspection故障伪装失效 | 真，同类追加 | grant存储不可用、取消和未知认证错误原来均返回200 inactive；现保留500 server_error，只有明确无效凭证返回inactive，不泄露内部错误 |
| UI-04 归属 | 有业务选择，非一处代码 bug | 部门/组/岗位当前只有 TenantID；主体角色/资源绑定是真实能力，租户级名称 lookup 不等于伪造主体数据。独立主体组织与继承规则没有现成契约 |
| UI-06 个人能力 | 必须拆分 | OAuth consent已有API，可直接补前端；个人绑定、PAT、自助注销缺完整自助API，不能用服务账号/管理员永久擦除替代 |
| 登录限流 / captcha HA | 真，部署有条件 | Redis共享单主可修应用多副本状态；不能由此宣称Redis复制故障切换严格一次性或已具备设备限流 |
| OIDC prompt=none / azp | 真 | 合法回调应返回协议错误/state，非法URI必须本地拒绝；多audience和存在azp均需校验 |
| Ledger readiness | 原处置过度 | 永久擦除是可选能力，默认关闭不应使所有登录副本不ready。启用时独立可选健康检查；实际擦除仍先写不可变义务并回读 |
| SCIM worker/readiness | 部分误诊 | 无界扫描和吞同步错误成立；远端故障应反映在独立指标/告警，而非移除所有本地登录副本 |
| 缺GitHub/K8s/Dockerfile | 不能据此判代码阻塞 | origin在Codeup。平台无关可复跑门禁已补；生产资产可在外部受控仓库，需实际证据，不强制特定云平台/编排器 |
| react-leaflet许可证 | 撤销IAM制品阻塞 | 用原Vite配置、write:false收集最终chunk.modules：478个emitted modules无leaflet/map-picker/location-field。shared UI其他使用地图的制品仍须另审，不能宣称整个workspace许可证通过 |
| make ci包含全部发布门禁 | 原描述错误 | 实际为fmt/lint/coverage/integration/build/openapi。race、真实MySQL、generate幂等、前端和告警验证是独立目标，本轮由release-check串联 |
| 前次migration超时 | 无干净失败结论 | 存在并发migration干扰；不能认定资源竞争为唯一根因，更不能沿用该次运行宣称完整CI通过 |

不以缺陷数量替代优先级，不替服务负责人制定“不得豁免”政策。发现依据为实际调用链、回归测试与制品；
未经实测的生产条件明确保留为待验收。

评审分工：认证/协议/安全，SCIM并发/事务/SQL/迁移，前端隔离/产品流程三组复核；主线程承担
相关性证伪、方案整合、OpenAPI契约、架构/复用、依赖/制品、命名、门禁和最终验收。
数值金额视角未触发：本轮没有金额、税费或汇率计算。跨平台重点覆盖三数据库方言、迁移前缀与生成物；
未将本机通过扩大为任意生产平台通过。A类反向核对见第6节。

## 2. 解决方案与方案二次评审

| 工作包 | 采用的解决方案 | 方案评审/兼容性边界 |
|---|---|---|
| SCIM一致性 | target持久操作token；远端I/O前提交claim与首次push映射intent；自动同步claim后重验live；retiring阻新push/replace，分页退役 | 通过：不自动到期接管未知HTTP；timeout、202、408、所有5xx/未知响应或本地提交失败保留占用，防旧PUT反超DELETE。真实gateway504先返回/provider迟到写入的双HTTP服务回归与变异通过。显式reprovision保留。v6升降级必须停全部writer/worker并排空远端请求，COUNT守卫不是在线屏障 |
| SCIM恢复 | 保留target、凭据、操作类型/资源/时间；提供只读查询和provider证据核对流程 | 通过但有代价：未知结果冻结整个target。等待超时、重启或单次GET资源不存在均不足以清锁；无自动清锁/盲重试。见[运行说明](iam-scim-operations.md) |
| SCIM worker | 50 mappings/批、8 targets/tick、30秒预算、稳定游标及失败退避；错误聚合和独立Prometheus指标 | 通过：不把远端故障传播为登录readiness故障；已补告警规则与触发/恢复测试，实际通知路由仍需部署验收 |
| 真实注销 | Logout改为内部(string,error)契约；HTTP/SAML失败503且不清cookie、不返回成功；前端仅成功后清状态并消费logout_url | 通过：无cookie/未知/已撤销会话仍幂等；数据库失败与网络失败可重试。保持HttpOnly/SameSite安全属性 |
| 租户/主体 | 接本人租户目录并保留平台目录权限，账号所有者/迟到响应隔离；子主体设置直接展示当前节点、保留parent_id | 通过：不改变后端授权和树归属；不自动引入主体子树或角色继承 |
| 会话/菜单 | 仅401转anonymous；其他验证故障为可重试error，登录后验证失败不跳成功；菜单绑定subject+tenant并使用请求序号 | 通过：不假造authenticated，不让旧作用域或旧请求覆盖当前展示 |
| 列表错误态 | principals、tenants、oauth-clients分别保存加载故障与写入错误；仅成功空结果显示empty/0计数，并提供重试 | 通过：三页真实403已复现；复用既有请求、Alert/Button和翻译，不改变角色授权模型，不将其替代全UI-05验收 |
| Consent / UI状态 | 接现有个人consent列表/撤销，确认与写后读回，三语言；nullable OAuth数组在请求边界归一化；错误使用当前应用语言 | 通过：撤销粒度是本人principal+client，跨本人租户；使用现有CSRF/请求/组件设施 |
| Consent实际访问撤销（真实联测追加） | 每代授权使用独立grant ID，consent/code/refresh及JWT传递同一代；认证链核对client/principal/tenant与当前有效grant，撤销后旧代永久失效 | 方案复审及定向/真实联测通过：只查consent存在会复活旧JWT，只改introspection会漏资源API，全局提升credential_version会误撤其他应用。v10三方言旧数据升级/回退及迁移锁已通过；升级须停止旧writer/认证实例，旧无grant OAuth令牌须重新授权。回退保留旧撤销状态，但旧binary可能重新接受已签JWT；必须封闭OAuth流量并移除旧签名信任或等其硬过期。外部仅离线验JWT的资源服务器需在线introspection等机制才能即时感知撤销 |
| OIDC | 先验证client及redirect，再返回login_required/consent_required与原state；校验多audience及显式azp | 通过：非法client/URI仍本地400，无开放重定向；保持已有正常授权流程 |
| Redis共享安全 | Redis6.2+原子GETDEL+5分钟TTL覆盖text/slide/click；Lua原子共享global与socket-peer额度，429/Retry-After；deadline与file-backed URL | 通过技术设计：启用时必须显式closed/open策略，不擅自选择。captcha始终失败关闭，shared模式拒绝test_answer。代理后的真实客户端IP仍由可信ingress限流 |
| Ledger健康 | enabled-only Optional健康检查，限时只读GetObjectLockConfig；擦除写入回读仍fail closed | 通过：需s3:GetBucketObjectLockConfiguration；健康探针不证明Put权限，也不会自动建桶或写探测对象 |
| 发布门禁 | make release-check串行组合ci、race、generate-check、frontend-check、test-alerts、真实MySQL/schema-lock及Redis两拓扑矩阵 | 通过：避免共享coverage/embeddedPG并发；不自动在远端安装Docker，不假造生产部署/签字 |

### 2.1 本轮应用补齐方案及复审

- **UI-04归属**：沿用现有租户拥有部门、组、岗位、角色及菜单定义，主体/资源只展示明确绑定与生效来源。
  若要求每主体独立拥有组织和菜单，则需要新外键、查询/迁移和继承规则；这是另一种业务模型。
  本轮据现有契约补齐归属文案及真实绑定展示，独立主体组织模型不作为应用就绪前置条件。
- **个人关联账户**：使用purpose、当前session和principal绑定的一次性reauth证明；绑定只写当前本人链接，
  不调用会自动建号/授角色的普通federation登录分支。解绑需事务锁定principal并检查剩余本地密码、
  passkey或有效provider登录方式；本人并发解绑也必须保留至少一种方式。仅session.AuthTime较新不足以证明重新认证。
  SAML跨站POST不依赖任何跨站cookie：回调端点（`/federation/{provider_id}/callback`）是公开端点，
  只按RelayState定位本次操作并记录经签名验证的断言属性，不读取会话或交易cookie；随后以303跳转到同源的
  `/federation/{provider_id}/identity-continuations`，在那里用既有的Lax会话cookie与交易cookie完成绑定校验
  （本人、会话哈希、凭据版本、已验签时间）。因此**不引入SameSite=None**，主session安全属性不变；
  该设计比"跨站可读的交易cookie"更强，不要在后续改动中退回前者。
  共享reauth和完整上游流程已实施；真实OIDC浏览器闭环及真实签名SAML的Go/HTTP测试通过，未宣称浏览器SAML验收。
- **个人PAT**：v15独立个人令牌；随机原文只在创建成功时返回一次，只存SHA256；限定一个本人有效租户、
  当前已获授的非platform权限集合及用户显式到期时间。创建准备接口返回规范化参数指纹，reauth精确绑定
  名称、租户、权限及期限；证明消费、创建和审计同事务。每次请求仍经过现有成员、接口、主体/数据/字段授权。
  只有真正执行权限检查的路由可接受PAT，authentication-only、个人安全、platform及OAuth换发路径拒绝，
  entity adapter依据实际query selector绑定租户。本人列表分页且不返回digest，撤销幂等。方案复审：
  普通JWT携带scope但未全链约束会留下换发绕过；可信路由上下文限定opaque PAT入口。响应丢失不再次披露secret，
  用户从列表撤销后重新创建。服务/真实Registrar、三方言迁移及三语言双端真实浏览器闭环通过。
- **自助注销**：提供明确区分的“退出当前租户”和“申请关闭全局账号”。退出复用既有last-admin保护，
  仅撤销所属租户授权凭据，保留全局浏览器会话及其他租户。关闭仅提交可取消申请，不立即停用或擦除；
  平台管理员可见/驳回并沿既有账号状态、retention、legal-hold、远端义务和永久擦除路径处理，最终状态
  从实际生命周期派生。申请不自动填写保留期限，不允许本人调用平台擦除接口。方案复审：现有SetStatus依赖
  tenant JOIN，不能直接用于零租户全局账号；因此增加平台专用pending→processing入口，按version防并发覆盖，
  锁定账号并检查全部租户last-admin及平台权限保护后，原子停用及撤销旧凭据。零租户账号也可处理；
  不自动设置保留期限，继续使用原生命周期接口。申请阶段不修改权限，不能误报为已关闭。
- **Redis拓扑代码**：显式standalone/cluster/sentinel，Cluster单seed仍构造ClusterClient；ACL和file-backed密码、
  Sentinel独立凭据、TLS>=1.2及可选mTLS、严格配置冲突校验、完整请求deadline。旧URL方式保留且不与新拓扑字段混用。
  禁止未知网络结果自动重放，同时保留MOVED/ASK；双key Lua同slot。真实plain及全拓扑mTLS矩阵、
  Cluster重定向/副本提升、Sentinel重发现、错误ACL/TLS、已执行丢响应及三项变异已通过。
  全局限流与captcha使用一个namespace槽，支持拓扑不等于认证负载跨槽分片；异步复制仍可能丢消费/计数，
  不宣称切主后严格一次性。详见[Redis应用配置与边界](iam-redis-security.md)。
- **Redis故障策略**：代码支持closed/open并强制显式配置，验证码始终失败关闭。
  具体环境选择何种策略属于部署配置，不阻塞两条路径均已实现和验证的应用代码验收。

### 2.2 当前候选验收证据

| 检查 | 已执行结果与剩余边界 |
|---|---|
| 本人认证与PAT | 完整authn测试通过，覆盖率90.4%；包含密码/必需MFA、真实UV passkey、精确参数proof、撤销和逐存储阶段回滚。最终CI整体结果另计 |
| 绑定/解绑 | federation全包90.9%，OIDC/SAML签名协议、同会话continuation、无JIT/角色提升和last-method并发测试通过；真实本地RS256 IdP覆盖纯联邦fresh reauth，18次fresh上游认证、24次code兑换、0协议错误 |
| 退租/关闭 | core90.3%、identity90.6%、oauth90.2%，定向race与失败回滚通过；零租户processing、restore不复活、last-admin/proof/OAuth原子回滚、迟到leave/closure授权交错，真实PG六场景通过16.415s，真实MySQL组合回归通过206.545s |
| Redis | 最终make test-redis-security EXIT0：plain4.10s、全mTLS9.30s，Cluster/Sentinel/standalone真实矩阵、ACL/TLS拒绝、MOVED/ASK、已执行丢响应不重放及切主重发现通过；纳入release-check |
| 前端 | 59 IAM+85共享UI测试、lint/typecheck/build及58个fixture场景通过。真实矩阵追加发现3处嵌套Alert漏翻译，已按类修复并通过真实三语言与9个定向fixture验证 |
| 真实页面矩阵 | 最终792/792 PASS：22路由×3语言×1440/390×admin/reader/denied×2租户；真实有数据请求、实体/租户隔离、权限与写拒绝、翻译、console和横向溢出检查。首轮脚本错误和产品嵌套Alert错误均已修正，首轮FAIL保留，不冒充PASS |
| 个人生命周期浏览器 | 6/6 PASS：三语言×双端，退租后cookie仍有效、零成员→申请/取消/重申请→平台processing→旧cookie401→队列读回→真正打开零租户生命周期详情200；suspended=true、anonymized=false，不自动擦除 |
| 个人安全浏览器 | 12/12 PASS：三语言×双端×密码/纯联邦；PAT仅一次展示、真实scope/跨租户/撤销验证，本人链接无JIT/角色授予/session替换，最后登录方式DELETE409。PAT成功对话框补grid，最终12格含不重叠几何断言与截图复核 |
| 实际OpenAPI | 21个新增operation、0个删除，总271（其中IAM257）；新增18个本人敏感入口仅sessionCookie，3个平台入口要求platform_administer。既有schema无变化/删除；仅3个既有federation start/callback操作补303响应，其余既有operation不变；state必填和ID边界通过真实HTTP验证 |
| 迁移与冻结 | 真实MySQL8.4固定镜像完整脚本EXIT0，迁移409.699s、生命周期23.168s，组合/性能/SCIM/重放/归档均通过；authn13→15→13→15、federation5→6→5→6、identity4→5→4→5，保留旧凭据/链接、校验9999到期及owner/墓碑守卫。schema.lock为219文件、72表、33跨模块FK，旧207哈希逐项不变，新增12 |
| CI各阶段与失败项复验 | fmt/lint及覆盖率通过，总90.1%、全部模块≥90%；goose108.634s、lifecycle387.688s、performance121.604s通过。原make ci EXIT2：架构漏登记两项共享能力/两项测试命名，以及4组迁移测试仍断言旧里程碑/版本。修正后架构3.425s、4组真实三引擎迁移146.127s全部通过；没有删除/放宽断言。build及openapi-check独立补跑通过，不伪称原聚合命令EXIT0 |
| 生成与规范冻结 | make generate-check及额外包含untracked新增生成物的hash比较PASS；前端API类型再生成逐字节不变；schema-lock正常verify为219/72/33，工具测试通过 |
| 最后前端检查 | PAT布局定向eslint、三个新增浏览器脚本syntax/prettier检查PASS；不把仅支持TS/TSX的eslint称为mjs脚本lint |
| 全量race | make test-race PASS、无DATA RACE；app133.366s、authn144.298s、core93.687s、federation53.204s、identity39.058s、oauth61.512s，全部主模块包通过 |

真实PostgreSQL复核发现两类SQLite未暴露的缺陷，已按类修复：OAuth跨表查询复用GORM statement
导致principal id条件泄漏到membership，改为各表独立Session；七处显式UpdatedAt叠加自动时间赋值
导致PG重复SET列（42601），authn六处及core角色更新一处改用UpdateColumnSimple。
真实PG闭环由FAIL转PASS；五个authn SQL场景和core精确时间回归通过，恢复旧实现的变异均被检出。

Redis最终复验曾暴露测试同步缺陷：Sentinel宣布新主不代表双方客户端已收到switch-master，旧主尚未
降级时SET成功也不证明请求已到新主。测试改为只读CLIENT INFO确认两条实际连接均到新主后，再执行
原单次SET/GETDEL与不能再次消费断言；不重试消费、不改生产逻辑。修正后两模式通过，旧FAIL日志保留。

浏览器报告在`.temp/iam-uat/browser/{route-matrix,personal-security,personal-lifecycle}/report.json`，
实际后端binary SHA256为`70b55f3af4e092c0037708d2b93d7d9da80b376bebbce0b8fc31d4f46513e52b`。
三组finally清理均通过；生命周期合成账号停用、租户软删除，保留processing元数据，不声称物理清空。
临时盘与Docker故障已恢复，后续真实数据库和浏览器验收正常完成，未豁免任何必要验证。

## 3. 历史修复基线实施与测试证据（34778e7）

以下为34778e7历史修复验收，最终CI于2026-09-10 04:32 UTC完成。
不替代当前新增个人能力和Redis拓扑的候选验收；本轮证据见第2.2节。

| 项目 | 结果 | 证据与边界 |
|---|---|---|
| 前端lint/typecheck/test/build --force | PASS | 最终IAM58、shared UI85；完整强制执行，不沿用缓存 |
| Chromium回归 | PASS 27/27 | 真实React/路由/浏览器交互，覆盖注销500/断网/重试、普通/平台租户、换账号/旧响应、子主体桌面/移动、consent三语言与失败重试、nullable OAuth、会话503/401恢复、菜单迟到隔离，另含三页×桌面/移动加载→403→重试空数据→有数据；HTTP fixture隔离，不冒充真实DB/IdP |
| 前端变异 | PASS（检出） | 恢复吞logout异常、恢复子主体旧树均触发失败；三页分别移除empty保护的浏览器响应变异均被检出；正常源码回归通过 |
| OIDC/ledger定向race | PASS | federation37.340s、oauth33.674s、audit实际23.328s；azp、错误回调、缓存ledger三变异均检出 |
| Redis共享安全 | PASS | miniredis常规测试和真实Redis7.4双client测试；三种captcha并发12次仅一次消费，错误答案消费、过期、故障；global/peer额度、并发60请求限20；真实Redis定向race5.855s |
| Redis变异 | PASS（检出） | GETDEL改GET使12个消费者成功；跳过429使超额请求200，均被断言检出且已恢复 |
| SCIM定向SQLite/race | PASS | 两独立连接/Service、首次PUT保护、过期24h不可接管、202、退役续跑、50+3分页和跨target分页；live重验/持久claim两变异已实测检出 |
| 完整make ci | PASS，EXIT0 | 最终fmt/lint/coverage/integration/build/openapi-check全通过；总90.1%、authn90.2%、oauth91.5%、provisioning90.3%，全部模块达到90%阈值。架构3.114s、goose68.572s、lifecycle215.443s、migration579.919s、performance77.800s。前轮sync_after模型约定及测试文件命名失败已修复并被本轮完整门禁覆盖 |
| 全量make test-race | PASS | 最新主模块全部包通过，internal/app151.989s、authn144.835s、oauth58.363s、provisioning68.750s；最后Introspect错误分类修改另有HTTP定向race5.352s通过 |
| OAuth grant与故障分类 | PASS（定向） | 真实JWT/DI/parser、client停用/删除、并发签名后撤销、同毫秒regrant隔离及变异通过；Introspect HTTP错误集合回归、旧行为overlay变异、lint通过，oauth全包覆盖率91.5% |
| 后端真实注销 | PASS（本地） | authn/federation/oauth race通过；数据库失败仍有效、恢复重试失效，SAML失败无成功响应；新MySQL UAT真实代理验证注销后原cookie失效 |
| 真实MySQL/PostgreSQL | PASS | MySQL8.4固定镜像完整脚本EXIT0：组合/并发擦除66.070s、迁移89.533s、性能38.516s、重放1.768s、SCIM多连接10.798s、归档1.369s、生命周期15.446s。SQLite/PG旧OAuth v9→10→9→10保留撤销状态通过；PG SCIM七并发场景24.118s、有数据v6回退/再升级44.408s通过。目标role级联及其他租户权限保持已复验 |
| OpenAPI/生成/schema-lock | PASS | make generate-check及最终openapi-check EXIT0；真实MySQL通过后锁更新并复核207文件、68表、29跨模块FK，原201哈希未改。实际规范172 paths/250 operations、IAM236精确映射通过，无增删操作或schema变化；18处响应声明补429/503及1处SCIM描述，参数/请求体/security不变，前端类型已生成 |
| 告警规则 | PASS（规则） | promtool3.5.0真实执行[触发/恢复测试](iam-alerts.test.yml)成功；实际告警接收路由尚未验收 |
| 漏洞扫描 | PASS（当前扫描） | 新增Redis依赖后govulncheck：0 reachable、13 module-only。公共npm registry实查发现开发生成器js-yaml通告GHSA-2883-xcg3-v3hh，固定兼容parent补丁@redocly/openapi-core1.34.20后audit退出0、零项；API生成哈希不变。不把开发路径误报为浏览器运行时漏洞 |
| 新建MySQL UAT | HTTP九项、release浏览器10/10 PASS | 最终源码构建SHA256 `e35bb88bdab3db58c480c1a039bfd4c53fa041da28198c9682a36f4ca15d871d`，2026-09-10 04:13:18 UTC报告。真实API/console代理；管理员/成员×桌面/移动登录、租户、注销；子主体保留parent；consent撤销后真实Bearer资源401、introspection inactive、refresh拒绝；regrant旧token仍失效、其他client仍200、跨origin撤销403；三页403重试与无空态通过，六张拒绝截图已检查。旧UAT以preserve-old保留 |
| IAM许可证相关性 | PASS（移除误报） | 原Vite配置write:false实际构建478 emitted modules，无Leaflet；不覆盖其他workspace制品 |

历史基线曾验证：OpenAPI172 paths/250 operations，IAM236映射；schema.lock201/201、68表、29跨模块FK；
后端覆盖率90.1%；真实MySQL8.4恢复和本地UAT。**这些历史数字不替代本轮新增v6/v10及新代码验收。**

可复跑证据入口：[SCIM并发与未知响应回归](../internal/iam/provisioning/target_operations_test.go)、
[真实Bearer授权代际回归](../internal/iam/oauth/service_grant_test.go)、
[Introspection HTTP错误集合](../internal/iam/oauth/http_test.go)、
[旧OAuth数据迁移回归](../tests/migration/iam_oauth_grant_test.go)、
[真实MySQL完整脚本](../scripts/test-mysql8.sh)。本地门禁日志保存在 `.temp/release-*-final.log`，
浏览器报告位于 `.temp/iam-uat/browser/release-report.json`；日志、截图、凭据和二进制不纳入提交。

## 4. 生产环境与业务验收（历史参考；运维工作不纳入本轮）

本表保留原发布审计的职责划分。UI-04归属、PAT范围/期限和关闭对象已由D34/D35收敛并实现；
前后端应用验收以第2.2、5节为准。正式环境的配置值、演练和签字另行安排，不再列为代码缺口。

| 责任方 | 交付/验收要求 |
|---|---|
| 后端 | 当前代码门禁、SCIM三方言迁移与多连接、真实注销/CSRF、OIDC错误流程、Redis故障策略实现、ledger可选健康 |
| 前端/QA | 已修流程的真实后端联测；UI-05/07全业务路由、有数据、多租户/主体/权限、三语言桌面/移动矩阵 |
| 产品/服务负责人 | UI-04归属、PAT资源/期限、自助注销对象、Redis故障策略、RTO/RPO目标 |
| 运维/平台 | 同一RC生产交付证据（本仓或外部受控仓库）、TLS/密钥/配置、账户交付、Redis故障切换、SCIM停旧writer升级/未知结果恢复、告警路由 |
| 运维/后端 | 隔离副本归档恢复、复制/failover、外部IdP/SCIM和真实通知；现有本地单库演练不能代替生产拓扑 |
| 产品/安全/QA/运维 | 基于同一release candidate共同签字 |

Redis单主GETDEL保证不能扩大为异步复制failover下严格一次消费；需要部署验证与明确风险边界。
SCIM指标与promtool通过也不证明有人收到告警。永久擦除需启用外部义务账本并覆盖可恢复备份时窗。

## 5. 本轮应用与代码验收退出条件

- [x] 当前候选ci各阶段及独立race、生成幂等、OpenAPI检查全部完成；原CI失败及定向修后复验见第2.2节，不沿用历史源码结果。
- [x] SCIM并发/未知响应/退役/擦除不变量及真实数据库迁移回归通过；部署停writer和provider取证说明保留。
- [x] 注销失败与恢复、真实cookie失效、多租户/主体/consent及会话/菜单故障隔离通过。
- [x] UI-04现有归属和UI-06个人PAT、本人关联账户、退租与关闭申请全部实现并验收。
- [x] UI-05/07真实全路由三语言、双端、有数据、多权限、两租户792格通过。
- [x] 新增个人安全12格、个人生命周期6格真实流程通过；Go测试补充MFA/passkey/SAML和事务失败路径。
- [x] Redis standalone/cluster/sentinel、TLS/mTLS/ACL、显式故障策略、同slot Lua、未知结果不重放均实现并真实测试；异步复制边界明确。
- [x] 三方言新增迁移、旧数据保留、owner/墓碑守卫通过，219文件迁移锁保留旧207哈希。
- [x] 最终自审与原请求验收通过；全部可交付文件按已有授权提交推送，提交标识以本分支Git记录为准。

生产部署、灾备、RTO/RPO、正式告警接收和运维签字不属于上述退出条件。

## 6. A1–A8 风险模式复核

| 模式 | 判定 |
|---|---|
| A1 本地暂存 | 无离线业务提交队列；租户选择是UI状态，本轮已按账号隔离 |
| A2 大批量 | SCIM Bulk已有100操作/1MiB边界；同步新增有界分页，真实大目录/部署网关预算仍须验收 |
| A3 中断路径 | 退役持久状态/续跑、未知远端结果保留claim；完整恢复需provider证据，不自动过期 |
| A4 隔离 | IAM257精确映射，新增18个cookie-only与3个平台入口；PAT权限上限、跨租户拒绝、退租及关闭撤销、迟到OAuth围栏及792格真实权限矩阵通过 |
| A5 数值对账 | 无金额、税费、汇率或资金分摊，不适用 |
| A6 幂等 | SCIM/验证码/注销新增跨实例或失败重试断言；显式重新provisioning不算旧任务复活 |
| A7 兼容 | 新增authn v14/v15、federation v6及identity v5三方言升降级保留数据，219文件锁保留旧207哈希；既有OpenAPI schema无删除/变化。不支持旧writer混跑，回退新表会丢失新功能数据，不能据测试升降级推断在线无损回滚；旧OAuth binary信任问题仍按第2节处理 |
| A8 异步 | audit保留既有租约/fencing；SCIM新增持久claim/游标/退避/指标，实际告警与恢复仍需运维验收 |

## 7. 已关闭历史项

保留2026-09-07审计的已关闭存证，避免重复报告：SSRF统一出站策略；SCIM live mapping阻止直接删target；
platform-only Principal retention/legal-hold/anonymization API/UI；历史格式/coverage/迁移fixture问题；
本地MySQL8.4 UAT、不可变擦除义务账本及备份恢复对账。详情见
[历史审计](iam-production-readiness-audit-2026-09-07.md)、[恢复runbook](runbook-migration-recovery.md)、
[本地UAT](iam-local-uat.md)及根目录 `.claude/decisions.md` D22–D31。

旧SCIM删除责任绕过与本轮远端并发问题不同；历史单库恢复完成也不等于生产RTO/RPO和HA签字。
原工期估计未包含本轮新增工作，不作为承诺日期。

## 8. 本轮最终复审与交付结论

主线程已完成自审：改动限于清单内已实证缺陷、必要兼容性和回归门禁；复用模块能力注入、生成查询、
既有认证/授权与前端组件，没有另造授权框架或替用户决定业务模型。真实迁移、并发、故障和浏览器证据
与最终方案一致，文档区分可复跑本地验收和生产签字。deep-review要求的证伪、同类扫描、变异验证及独立门禁
已纳入本轮工作。`release-check`各组成门禁已分别实跑，本轮未将未执行的聚合命令冒称为已执行。

原请求中的清单复核、相关性纠正、解决方案补充及二次评审、必要代码修复、真实功能验收和最终门禁
均已完成。**应用/代码验收：PASS；本范围内无剩余已知阻塞项。** Redis集群所需配置、客户端、TLS/ACL
和故障处理包含在结论内；不以已排除的生产部署、灾备和签字阻塞应用就绪。
自审中的范围、复用、架构、复杂度、回归、状态记录与提交卫生通过；三组独立复核结果由主线程整合验收。
用户已授权全部可交付改动提交推送`origin/IAM`，临时日志、密钥、截图和构建产物不纳入提交。
临时Redis/PostgreSQL及MySQL门禁容器已清理，最新UAT及历史备份保留。
