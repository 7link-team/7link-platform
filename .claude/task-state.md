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
10.9 ③ 已完成并推送。
下一步：先给 @ClaudeWEB 补上 `7link-api openapi --out` （他的 CI 卡在这一条），然后 10.9 ④。

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
- [ ] 10.9 ④ authn 签名密钥环：JWKS 同时发布 active + previous，签用 active，验收任一把
- [ ] 10.9 ⑤ federation replay registry 换成跨副本共享状态
- [x] 注册验证码可配置（频道 task #5）
- [ ] 10.3 principal 管理权限语义（等用户明确拍板）
- [ ] 10.4 数据权限端到端（缺可挂载的租户级业务资源，等用户定目标）
- [ ] 10.5 / 10.7 / 10.8 / 10.10 / 10.11

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
10.9 ③：`make ci` exit 0，总体 90.1%，authn 90.1 / federation 91.2 / provisioning 90.8 /
keyringx 94.6 / core 90.1。（首轮 fmt-check 报 CRLF，lint 报 misspell `enrol`，
`internal/iam/core` 三份冻结接口清单需同步登记新增的三个 `SecretRewrapper`，均已处理。）
10.9 ②：`make ci` exit 0，总体 90.2%，authn 90.2 / federation 91.4 / provisioning 91.0 / secretx 96.1。
（首轮 lint 报 misspell 与两个轮换后不再使用的 `*CipherVersion` 常量，已改。）
10.9 ①：`make ci` exit 0，`pkg/extension/keyringx` 95.2%（门槛 90.0）。
验证码：`make ci` exit 0，`internal/iam/authn` 90.2%，总体 90.1%（首轮 lint 报 `real` 遮蔽内建标识符，已改名为 `solved`）。

## Last Checkpoint
2026-09-05 · 10.9 ③ `iam rewrap-secrets` 已推送，`make ci` 绿。
下一步先插一件 @ClaudeWEB 卡住的小事：`7link-api openapi --out <path>` 离线导出 OpenAPI 文档，
让前端 CI 能重算 spec（目前 spec 只有跑起来的服务在 `/openapi.json` 上供，仓库里没有落地文件）。
之后是 10.9 ④——authn 签名密钥环。
@gankcode 问到能否整体换成 go-captcha：已答——它本来就在用（滑块/点选），
但 v2 只有 click/slide/rotate，没有打字那一种，全换等于去掉唯一键盘可完成的验证码；
可选项是加 `rotate` 作第四种。等回话。
10.3 与 10.4 仍卡在 @gankcode 的决定上。
