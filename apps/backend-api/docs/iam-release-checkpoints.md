# IAM 发布前检查点、解决方案与验收

文档日期：2026-09-10。审计基线：`IAM@df8677b`；本轮修复随本文一同提交交付，提交标识以Git记录为准。
本文合并第一次审计、相关性二次评审、方案复审及实施证据。**当前发布结论仍为 NOT READY**：
已修复代码缺陷与生产部署验收是不同的完成条件，最终门禁、业务选择及运行环境证据见下文。

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
| 发布门禁 | make release-check串行组合ci、race、generate-check、frontend-check、test-alerts、真实MySQL和schema-lock | 通过：避免共享coverage/embeddedPG并发；不自动在远端安装Docker，不假造生产部署/签字 |

### 2.1 UI-04 / UI-06 的待定部分及可执行方案

- **UI-04归属**：推荐维持租户拥有部门、组、岗位、角色及菜单定义，主体/资源只展示明确绑定与生效来源。
  若要求每主体独立拥有组织和菜单，则需要新外键、查询/迁移和继承规则；这是另一种业务模型。
  已向服务负责人询问，未收到选择前不改变授权语义。
- **个人关联账户**：使用purpose、当前session和principal绑定的一次性reauth证明；绑定只写当前本人链接，
  不调用会自动建号/授角色的普通federation登录分支。解绑需事务锁定principal并检查剩余本地密码、
  passkey或有效provider登录方式；本人并发解绑也必须保留至少一种方式。仅session.AuthTime较新不足以证明重新认证。
  SAML跨站POST需专用Secure SameSite=None交易cookie及同源最终确认，不降低主session安全属性。
  这套完整安全流程尚未实现，不能用只新增列表接口宣称关闭。
- **个人PAT**：独立个人令牌契约，随机令牌仅展示一次、仅存哈希、本人撤销、最小scope和显式到期；
  必须定义可授权资源/租户范围及期限运营规则，不能复用权限更大的service-account凭据。
- **自助注销**：必须明确申请销毁全局principal还是退出当前tenant，已发出问题。
  全局注销复用retention/legal-hold/远端义务检查，采用本人申请与可追踪处理；不允许自助调用platform管理员擦除接口。
- **Redis故障策略**：D24/D26保留该业务选择，已询问closed/open。代码支持两者并强制显式配置，
  不将“继续”解释为任一风险选择或生产配置确认。

## 3. 本轮实施与测试证据

当前阶段：本轮代码修复验收通过，生产验收仍为NOT READY。最终CI于2026-09-10 04:32 UTC完成。
以下结果适用于本轮冻结工作区；后续代码变化必须重跑相关门禁。

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

## 4. 生产环境与业务验收（不能由代码测试代签）

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

## 5. 发布退出条件

- [x] 当前修复通过完整ci及独立race、真实数据库、生成幂等、OpenAPI、schema-lock；没有未解释的失败。全量race后唯一后端改动为Introspect错误分类，其定向race另已通过。
- [ ] SCIM跨实例/首次PUT/旧快照/timeout/202/退役/擦除不变量及v6迁移升级回退通过；旧writer停机升级和未知结果恢复流程已演练。
- [x] 注销的前端网络失败、后端DB失败、SAML失败及恢复重试验证通过；成功后原cookie确实不可认证。
- [x] 多租户发现、子主体设置、consent通过真实后端联测；会话故障/菜单隔离通过独立浏览器故障注入回归。
- [ ] UI-04和剩余UI-06业务方案已确认、实施并验收；不以局部consent功能代替整项。
- [ ] UI-05/07全路由三语言、桌面/移动、有数据、多权限矩阵通过。
- [ ] 生产Redis策略/代理额度已配置，单主故障/故障切换语义已验收；告警规则及实际接收路由通过。
- [ ] 正式环境、RTO/RPO、隔离归档恢复、外部IdP/SCIM/通知与交付账号证据齐全。
- [ ] 最终制品漏洞/许可证核查完成，产品/安全/QA/运维共同签署同一个RC。

## 6. A1–A8 风险模式复核

| 模式 | 判定 |
|---|---|
| A1 本地暂存 | 无离线业务提交队列；租户选择是UI状态，本轮已按账号隔离 |
| A2 大批量 | SCIM Bulk已有100操作/1MiB边界；同步新增有界分页，真实大目录/部署网关预算仍须验收 |
| A3 中断路径 | 退役持久状态/续跑、未知远端结果保留claim；完整恢复需provider证据，不自动过期 |
| A4 隔离 | 236 IAM guard精确映射经最终CI通过；真实cookie注销、Bearer撤销、regrant旧令牌拒绝、其他应用隔离与跨origin拒绝已通过 |
| A5 数值对账 | 无金额、税费、汇率或资金分摊，不适用 |
| A6 幂等 | SCIM/验证码/注销新增跨实例或失败重试断言；显式重新provisioning不算旧任务复活 |
| A7 兼容 | provisioning v6及oauth v10真实三方言DDL/旧数据回退再升级与207文件迁移锁已通过；不支持旧writer混跑。v10回退保留旧撤销标记，但旧binary信任已签JWT的问题需按第2节封闭流量处理 |
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

原请求验收：清单复核、相关性纠正、解决方案补充及二次评审、必要技术修复、测试验收和结果合并已完成。
**IAM整体仍不具备生产放行结论**：第2.1、4、5节的业务选择、完整产品流程及生产证据尚未关闭。
用户已授权本轮全部可交付改动提交并推送至`origin/IAM`；生产放行条件不因推送而改变。
临时Redis/PostgreSQL及MySQL门禁容器已清理，最新UAT及历史备份保留。
