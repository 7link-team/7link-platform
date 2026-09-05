# Task State

## Task
IAM Phase 10 后端推进。10.1、10.2、10.6、§2.5.2 均已推送。当前：10.9 密钥轮换（第 ① 步已推送），
以及 @gankcode 拍板的注册验证码可配置化（频道 task #5，已推送）。

## Objective
10.9：现在 federation / provisioning / authn 各自拿一把密钥，密文信封里不记录是哪把密钥封的，
所以换密钥等于让库里所有密文永久打不开——「安全的动作」和「做得到的动作」是两个动作。
目标是让密钥可以被替换：一把在封，一圈都能开，旧密钥在没有东西依赖它之后才离开配置。

验证码：注册页以前在文字 / 滑块 / 点选里随机三选一，其中两种不是所有人都能完成；
另外自动化验收脚本没有任何办法通过注册流程。

## Current Phase
IN PROGRESS（10.9 第 ① 步与验证码均已推送；10.3、10.4 等 @gankcode 拍板）

## Current Step
10.9 ④ authn 签名密钥环已完成。
下一步：10.9 ⑤ federation replay registry 换跨副本共享状态；随后按 D17 依次做 10.3 → 10.4 → 10.5。

## Overall Progress
- [x] Phase 10.2（提交 24f2483）
- [x] Phase 10.1（提交 d253c5e）：`iam bootstrap-platform-admin` + `iam reset-admin-password`
- [x] 品牌 cookie 清理（提交 348a37f）
- [x] §2.5.2 存量零成员租户修复命令（提交 20bae43）
- [x] CLI flag 下划线/中划线在所有子命令一致（提交 5d7e9d0，@ClaudeWEB 报的第 4 条）
- [x] `allowed_return_urls` 改为 origin 精确 + 路径前缀（提交 1ecc9cb，@ClaudeWEB 报的第 3 条）
- [x] create/update 可选外键 schema 对称 + invitation 空值线上形状（提交 69187ab）
- [x] Phase 10.6 OAuth 同意自助列出与撤销（提交 0b96fb9）
- [x] 10.9 ① 密钥环原语 `pkg/extension/keyringx`（提交 51ac8f4）
- [x] 10.9 ② federation / provisioning / authn 接入密钥环 + `previous_*_keys` 配置
- [x] 10.9 ③ `iam rewrap-secrets` + 「还有多少行挂在旧密钥上」报告
- [x] `7link-api openapi` 离线导出 + `make openapi` / `make openapi-check` + 落地 `docs/openapi.json`
- [x] 10.9 ④ authn 签名密钥环：JWKS 同时发布 active + previous，签用 active，验收任一把
- [ ] 10.9 ⑤ federation replay registry 换成跨副本共享状态
- [x] 注册验证码可配置（频道 task #5）
- [x] **10.10 IAM gRPC：决定不做，本项关闭（D17）**
- [ ] 10.3 principal 管理权限语义（**不再等待**，按 D17 双平面方案做）
- [ ] 10.4 数据权限端到端（**不再等待**，按 D17 以 `internal/todo` 为参照实现；用户已确认用 todo）
- [ ] 10.5 字段权限（**不再等待**，按 D17 对标 Keycloak User Profile + SCIM `mutability`/`returned`）
- [ ] 10.7 audit archive / 10.8 principal 生命周期（按 D17 SCIM+GDPR 停用→匿名化→墓碑）/ 10.11

## 本轮交付其一：10.9 ① 密钥环原语（已推送 51ac8f4）
新包 `pkg/extension/keyringx`。一把 active 负责封，整圈都能开；
`NeedsRewrap` 回答「这个值还挂在旧密钥上吗」，退役旧密钥不再靠猜。

- 新信封 `v2.<keyid>.<b64url>`；库里已有的 `v1.<b64url>` 靠逐把试解兼容——
  AES-GCM 会认证，错的密钥只会失败，不会解出错的明文，所以试解是明确的。
- 派生逐字节保持 `HMAC-SHA256(material, domain+purpose)` 不变，否则现存密文全部作废。
- 配置密钥接受四种 base64 拼法（RawStd / Std / RawURL / URL），与被它替换的三处解析器一致，
  不能让任何一个部署的密钥「因为软件变了」而突然读不出来。
- 密钥标识是 `HMAC(material, "keyringx:key-id\0"+domain)` 截断 8 字节，按 domain 分离，
  不是密钥本身的摘要——信封里明文携带的东西不该是那个秘密的短哈希。
- `ErrUnknownKey` 与 `ErrOpen` 分开：前者的修法是把密钥放回配置，不是承认数据没了。
- 配置报错只说密钥的位置（`previous key 2`），绝不说值；有测试断言错误串里不含密钥材料。
- 覆盖率 95.2%，未覆盖的都是 32 字节 HMAC 输出上不可能失败的防御分支。

## 本轮交付其二：注册验证码（频道 task #5，已推送）
承接 @gankcode 在 `#sevenlink-platform-win:e8731271` 的拍板：按市面通行做法，
再加一个 Turnstile 测试密钥式的机制。

配置（`authn.registration.captcha`）：
- `kinds: []` — 空 = 只发文字验证码（新默认）。可填 text / slide / click。
- `test_answer: ""` — 空 = 关闭。填了之后这个字符串可以作为任何验证码的答案，至少 8 字符。

改动文件：
- `pkg/extension/authn/verifier.go`：`RegistrationConfig.Captcha`、新增 `RegistrationCaptchaConfig`。
- `internal/iam/authn/captcha.go`：`captchaOffering`、`parseCaptchaOffering`、`(*Service).IssueCaptcha`、
  `(*Service).VerifyCaptcha`。
- `internal/iam/authn/gocaptcha.go`：`issueCaptcha()` → `issueCaptchaOf(kind)`，
  `VerifyCaptcha` → 不导出的 `verifyCaptchaOf(kind, id, answer)`。
- `internal/iam/authn/web.go`：`Service.captcha`，在 `NewWebService` 里解析（在 enabled 判断之前）。
- `internal/iam/authn/http.go`：两个调用点改成走 Service。

## 本轮交付其三：10.9 ② 三个模块接入密钥环
federation（client-secret / state / saml-key）、provisioning（scim-target-bearer）、
authn（mfa 加密 + 四个 HMAC 用途）全部改成走 `keyringx`，
并各自新增 `previous_encryption_keys` / `previous_encryption_key_files`。

- **纯增量**：派生逐字节没动，旧的 `v1.` 信封照常打开，旧的 HMAC 摘要照常对上。
  三个模块各有一个测试手写出「上一版代码会写成什么样」，派生一旦被改动就是红的，
  而不是等到某个部署发现库里的东西全打不开。
- **`secretx.ResolveList`**：`previous_*_keys` 与 `previous_*_key_files` 二选一，
  单个报错点名到 `name_files[2]`，`Resolve` 的老报错串一个字没变。
- **按摘要的用途分两类 API**，这是 authn 那边形状的由来：
  恢复码 / 找回密码 token / 邮箱验证 token 都是**按摘要查行**的，查询不可能对密钥不可知，
  所以用 `MACs` 出候选集 + SQL `IN`；邀请 token 是先按 ID 查到行再比对，用 `MACMatches`。
  为此跨模块契约 `InvitationCredentials` 从「给我摘要」改成「这个 token 对得上吗」。
- **五个 MFA 用途一起覆盖，不做半套**：半套更糟——运维换了密钥，看到登录正常，
  几周后才发现恢复码全废，而恢复码正是丢了验证器之后唯一的退路。
- **`ErrUnknownKey` 单独一条错误**，三个模块各自给了一句不同的话：
  修法是把退役密钥放回配置，不是让人承认数据没了，也不是叫用户重新录入他拿不出来的东西。
- **provisioning 只配 previous 不配 active 直接拒**：那是一次做了一半的轮换，
  静悄悄地把出站 SCIM 关掉不是那个人想干的事。完全不配密钥仍然合法（= 不用出站 SCIM）。
- **keyringx 的报错不再提 domain**：`"federation:key:"` 是派生标签，不是任何一个配置项的名字，
  最底层只说哪里不对（`active key must decode to 32 bytes`），由调用方补上是哪一项。

## 本轮交付其四：10.9 ③ `iam rewrap-secrets`
一条命令回答运维在轮换密钥时唯一关心的问题：旧密钥现在可以拿掉了吗。

- **七个密文列全覆盖**：authn 四（totp_secret / mfa_enrollments.secret_ciphertext /
  passkeys.credential_ciphertext / notification_outbox.payload_ciphertext）、
  federation 两（client_secret_ciphertext / saml_idp_keys.key_ciphertext）、
  provisioning 一（scim_targets.bearer_token_ciphertext）。
  每一列都得单独报名：一个静静没跑的列和一个真的没东西可换的列，在输出里长得一模一样。
- **`--dry-run` 是标志位而不是另一条命令**：两种跑法必须是同一条代码路径，
  否则预演的结果就不能当作真跑会发生什么的依据。报告首行始终写 `mode:`，
  免得一份预演输出贴进工单后被当成「已经换完了」的证据。
- **摆不平的那一类单独报**：恢复码 / 找回密码 token / 邮箱验证 token 存的是摘要不是密文，
  重封没用，只能等过期或让人重新生成。**恢复码永不过期**，
  所以 `clear_after` 在这种情况下写 `never` 而不是一个日期——
  一个看起来像「等到这天就能删密钥」的日期，比没有日期危险得多。
- **三个模块一个事务**：任何一处失败，所有行原样不动，不会出现跨模块半转。
  另取跨进程锁：两个进程同时跑不会弄坏数据（写入按读到的密文匹配），
  但会得出两份互相少算的报告，而这个决定只能有一份依据。
- **三个 `SecretRewrapper` 而不是一个共享接口**：`module.Services` 按类型为键，
  同名会让最后注册的那个静静覆盖前面的，报告少了一个模块也不会说一声。
  三个新接口已登记进 `internal/iam/core` 的三份冻结清单。
- **顺手修掉一个真实缺陷**：`countDerived` 用 `SELECT MAX(expires_at)` 统计到期时间，
  聚合表达式没有列类型，SQLite 按文本返回，扫描直接失败；PostgreSQL 上完全正常。
  改成取最晚一行而不是聚合值。这个报告偏偏只在轮换密钥的当口才会被跑到。

## 本轮交付其五：`7link-api openapi` 与落地的 API 文档

前端的 `generate-api-types.mjs` 默认读 `apps/backend-api/docs/openapi.json`，而这个文件从来没有存在过——
spec 以前只有跑起来的服务在 `/openapi.json` 上供，所以脚本里写着「后端还没发布，先指向一个跑着的实例」。
人可以这么干，CI 不行。

- `internal/app/openapi.go`：`RunOpenAPIExport` 按配置组合出真正的应用（不是「所有模块全开」的超集，
  否则文档会描述这个部署实际返回 404 的接口），注册 HTTP 之后取 `api.OpenAPI()`，全程不监听端口。
- `info.version` 取配置里的版本，取不到就退到常量 `0.0.0`，**不取构建号**：构建号每次提交都变，
  落地的文档就永远对不上新导出的，真正的 API 变更会被淹在噪音里。
- 日志改指 stderr（新增 `processConsoleErrorWriter`）。原来 `configureProcessRuntime` 把 slog 指向 stdout，
  `openapi > spec.json` 会把日志写进文档中间，产出一个谁都解析不了的文件——这是真 bug，不是洁癖。
- `--format json|yaml` 显式指定，不按扩展名猜；认不出的格式直接拒绝，不默默当成 JSON。
- `--out` 走临时文件 + rename：中断的导出不能留下半个文档，否则下一步 diff 出来的是「命令被打断」而不是「API 变了」。
- `make openapi` 重新生成，`make openapi-check` 在文档与代码不一致时失败并给出修法，已接进 `make ci`。
  应用名默认中性的 `IAM`（文档标题是名字 + " API"），可用 `OPENAPI_NAME` 覆盖；导出用的密钥每次现生成在
  `.temp/` 里，不进仓库，也不出现在文档里。
- 落地文档：152 条路径 / 227 个操作 / 294 个 schema，约 1.0 MB；全文搜不到任何品牌字样。

## Important Decisions
- 跨模块只走窄事务能力（`func(ctx, *gorm.DB, ...)`，Provide 阶段发布、Inject 阶段解析）。
- 修复命令是 create-only：只要租户已有任何 member 或 role 就拒绝，绝不改动已有归属。
- 密码永不作为命令行参数（10.1 已确立）。
- 退出码按运维下一步动作区分：3 已 bootstrap / 4 冲突 / 5 未迁移 / 6 暂时不可用 / 7 尚未 bootstrap。
- 不在代码里硬编品牌相关字符串，标识符里带品牌也算（CLAUDE.md §4）。
- 同意管理放 `/iam/me/*` 而不是 `/oauth/*`；用 `RegisterAuthenticated` 而非 `Register`。
- 撤销必须连带作废未兑换的 authorization code，否则这条授权一分钟后自己复活。
- **验证码不新增 `test` 类型**（@ClaudeWEB 提的，采纳）：发新 type 会逼前端认识「测试模式」这件事。
  改成万能答案之后，验证码照常发、照常是真图，只有结果是定的，前端一行都不用改。
- **万能答案由部署方配置，不是源码里的常量**：常量意味着「在哪个部署忘了关就是同一个后门」，
  而且读代码的人会把它当成仓库在保守的秘密。
- **验证与发放两侧都受配置集合约束**：类型是请求方在 body 里报的，只把某类型从「发放」里去掉，
  它仍然可以被点名到达。没开的类型现在直接拒，答案看都不看。
- 万能答案不改变一次性：命中时挑战照样被消费。
- 「生产环境启动即拒」做不到并且不做：这个代码库里没有环境（prod/dev）这个概念，
  为一个小功能引入一个全局概念不划算。防线是默认关 + 必须显式配 8 字符以上 + 每次启动 warn。

## Confirmed Constraints
- 提交必须逐文件显式暂存；工作区有若干非本轮改动的文件等用户处置
  （`.gitignore`、`README.md`、`pkg/interpreter/interpreter.go`、3 个已暂存的文档改名、未跟踪的 `docs/README.md`）。
- 前端目录归 @ClaudeWEB，禁止碰。
- migration 不写固定账号或默认密码。
- `tools/iam-preflight` 与迁移方案文档里的 chaosplus 路径是迁移来源，保留。
- `apps/backend-api/.temp/` 下的草稿目录（backup-10.1、backup-10.2、10.6、10.9、captcha）不得提交。

## Known Issues / Risks
- 逐包 90% 覆盖率门禁余量极薄（authn、organization、core 都贴着 90.0–90.2），新增生产代码必须同批补测。
- 10.3 principal 归属未决：6 个全局账号接口挂在租户注册器上，其中 3 个要 `platform:administer`，
  平台管理员与租户管理员都不可达。方案 A = 整体挪到平台注册器。已三次说明，等用户一句明确同意。
- 10.4 无处落地：唯一的业务模块 `internal/todo` 既没有 `tenant_id` 列，9 个接口也全走裸 `huma.Register`。
  要么给 todo 加租户维度当样板，要么等真实业务模块，需用户定。
- `organization` 模块的 ID 字段一律 `maxLength:"128"` 无 pattern，`core` 模块一律
  `pattern:"^[1-9][0-9]*$"`。两套写法并存但各自内部自洽，未擅自统一。
- `internal/iam/oauth/service.go:1028` 的 `clientFromRow` 有与 69187ab 修掉的同一个空切片坑：
  `var redirects []string` 在 `RedirectURIs` 为空或解析失败时保持 nil，`redirect_uris` 序列化成 `null`。
- 10.9 剩下四步里，④（签名密钥轮换）会短暂影响所有在途 token 的验签路径，需要一个 TTL 长度的双读窗口；
  ⑤（replay registry）目前是进程内 map，多副本部署下 nonce 每副本各花一次，重放保护实际是关的。

## Verification
Build: PASS  Tests: PASS  Lint: PASS  Integration: PASS
OpenAPI 导出：`make ci` exit 0（已含新增的 `openapi-check`），总体 90.1%。
落地文档 152 路径 / 227 操作 / 294 schema，`make openapi-check` 在人为改坏文档时如实失败并给出修法。
（首轮 CLI 测试因 cobra 本地 flag 跨 `Execute()` 粘连而串味——`--format toml` 漏给了下一个用例，
已按 rewrap 的同一套路加 `keepOpenAPIFlagsFromLeaking` 复位。）
10.9 ③：`make ci` exit 0，总体 90.1%，authn 90.1 / federation 91.2 / provisioning 90.8 /
keyringx 94.6 / core 90.1。（首轮 fmt-check 报 CRLF，lint 报 misspell `enrol`，
`internal/iam/core` 三份冻结接口清单需同步登记新增的三个 `SecretRewrapper`，均已处理。）
10.9 ②：`make ci` exit 0，总体 90.2%，authn 90.2 / federation 91.4 / provisioning 91.0 / secretx 96.1。
（首轮 lint 报 misspell 与两个轮换后不再使用的 `*CipherVersion` 常量，已改。）
10.9 ①：`make ci` exit 0，`pkg/extension/keyringx` 95.2%（门槛 90.0）。
验证码：`make ci` exit 0，`internal/iam/authn` 90.2%，总体 90.1%（首轮 lint 报 `real` 遮蔽内建标识符，已改名为 `solved`）。

## 本轮交付其六：10.9 ④ authn 签名密钥环

`internal/iam/authn/signingkeys.go` 新增 `signingKeyring`：一把私钥签名，环里任一把公钥验签。
`Service` 上原来的 `privateKey`/`publicKey`/`kid` 三元组被这一个 ring 取代（web.go 15 处调用点）。

三个关键取舍，都是「换早了会怎样」推出来的：

1. **验签按 `kid` 选 key，不是逐把试。**逐把试也能通过所有测试，但那样一来「把旧 key 从配置里
   拿掉」这个动作就不再有任何效果——旧 token 仍会被剩下的某把接住。选 key 才让退场是真的退场。
2. **retired key 只保留公钥。**私钥留着没有任何用途，只会让「不小心用旧 key 签名」变成可能。
3. **previous key 解析失败是启动错误，不是跳过。**跳过会得到一个「启动正常、健康检查绿、
   但恰好把该 key 对应的在途 token 全判无效」的部署——正是轮换窗口本身要防的那个故障。

新增配置 `authn.previous_signing_keys` / `authn.previous_signing_key_files`
（命名沿用既有的 `previous_encryption_keys` 先例）。`/.well-known/jwks.json` 现在发布整个环，
active 在前；`oauth/http.go` 的 `jwksDocument.Keys` 本来就是 slice，不需要改。

轮换步骤写进 `README.md` 新增的「Rotating keys」一节，四种密钥（签名 / MFA / federation /
provisioning）一张表列全，并写清签名密钥和存储密钥的差别：存储密钥换完跑 `rewrap-secrets`
按剩余行数决定何时退场；签名密钥没有 rewrap，token 不落库、只会过期，所以旧 key 必须在配置里
留满一个 `access_token_ttl`（外加 relying party 缓存 JWKS 的时间，如果更长）才能拿掉。

测试 `signingkeys_test.go` 六个，含一个端到端：用旧 key 签一个 token，再用
「新 key active + 旧 key previous」的 Service 去验，必须通过；反过来只持新 key 的 Service
必须拒绝旧环签的 token。

## Last Checkpoint
2026-09-05 · 10.9 ④ authn 签名密钥环完成，`make ci` 绿。

**本轮最重要的一件事不是代码，是 D17。**@gankcode 明确指出：10.3/10.4/10.5/10.8/10.10 这五项
不该拿去问他，IAM 领域都有成熟答案，要「按标准的、通用的、大家共识的做法」自己定。
这个批评成立——这五项被标为「待产品确认」已阻塞多轮，而它们并不是本项目特有的产品分歧。
五条方案已写入 `.claude/decisions.md` D17 并在频道公示（msg 4c5cc55c），
用户可否决任意一条，未否决即执行。10.4 用 `internal/todo` 当参照实现已获用户明确确认。
**后续不要再把这五项列为「等用户拍板」。**

上一轮（10.9 ③ + OpenAPI 离线导出，已推送）
本轮还按 @gankcode「做完了文档要么更新一致、要么删掉清理」把文档对齐了实际：
方案 10.9 条目改成「五步已完成三步」并写清剩下的 ④⑤ 各是什么问题；
`docs/README.md` 的「端点去哪看」改指落地的 `openapi.json` 并声明它是唯一机器生成、不得手改的文件；
`README.md` 新增 API document 一节。
下一步：10.9 ④——authn 签名密钥环（JWKS 同时发布 active + previous，旧 key 一个 token TTL 后退场）。
@gankcode 问到能否整体换成 go-captcha：已答——它本来就在用（滑块/点选），
但 v2 只有 click/slide/rotate，没有打字那一种，全换等于去掉唯一键盘可完成的验证码；
可选项是加 `rotate` 作第四种。等回话。
10.3 与 10.4 仍卡在 @gankcode 的决定上。
CLAUDE.md §12「Line endings」那段与仓库实际不符（曾提过一次，未获回应），未擅自改。
