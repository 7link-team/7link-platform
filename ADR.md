## 2026-10-15 — 多产品兼容目标与架构建议（未实施）

- Decision：用户将技术服务/协作、未来B2B2C商城及其他产品的兼容性纳入架构规划；完成 docs/multi-product-architecture.md，并关联原Web迁移M00/M01及后续适用门禁。具体架构建议是共同身份、独立产品业务/权限/数据、前端按体验分应用、后端先模块化按实际需要拆服务。
- Reason：全局Principal和OIDC已实现，现有OAuth准入仍要求客户端所属租户成员，受委托token不能调用IAM管理API；产品权益与跨应用退出没有完整现成闭环。pkg/module为进程内能力，不能冒称微服务。
- Alternatives Considered：所有产品塞同一后台/共用业务表；第一天拆全部微服务；保留IAM底座按领域扩展并逐步独立部署。推荐第三种，不改变现有强隔离来求接入便利。
- Tradeoffs：第二产品开放前要完成消费者准入、SSO callback、业务API audience/权限及退出撤权契约；无需为未来商城阻塞当前协作UI迁移或重建IAM。公开页面不强制开通；独立治理商家建议Tenant，组织分支可Entity，待具体业务确认。
- Scope：新增X00–X11工作包、MB01–MB19验收场景；原M00–M20及423需求账本保留。无实现/部署/数据库变更。
- Status：多业务兼容目标已由用户提出；具体建议供确认，商业模型、消费者准入/退出时限等未替用户决定。现有企业member检查和IAM client-token拒绝必须保留，扩展仅走明确新契约。

## 2026-10-15 — 原型转入生产 Web，先交可验收切换方案

- Decision：用户确认以 apps/frontend-web 为后续实现主线，先完成深入盘点、最终交付定义和逐项清单。本轮仅方案交付，不启动迁移/后端开发/发布。
- Reason：生产已有身份/作用域/文件和共享 AutoForm/AutoTable；继续扩建独立原型会重复维护。自动组件当前尚未被生产业务页采用，必须真实集成验收。
- Alternatives Considered：继续独立原型；整目录搬入生产；逐模块复用生产组件。采用第三种，原型保留为设计证据。
- Tradeoffs：前期要关闭表单只读/错误展示和契约接入缺口；避免将模拟持久化/权限带进生产。U交互迁移、S真实服务、R发布分别签收，不能把mock通过当生产完成。
- Scope：见 docs/myrmidon/frontend-web-migration.md 与 frontend-web-migration-checklist.md；423唯一REQ/424编号声明、70场景、74原型浏览器脚本。REQ-EXEC-015覆盖旧ADHOC新建入口，两条REQ-EXEC-012均保留；当前源文优于旧350条历史账本。
- Status：主线方向已由用户确认；具体方案已交付供确认后分批实施。既有完整原型审计仍暂停；未授权自动扩大为全后端即时开发。原生产system/platform/admin/user及账户视角/文件边界必须保留。

## 2026-10-09 ? Organization file roots must be complete in the UI
- User explicitly authorized repair after live UI proved department roots were missing despite permitted collection APIs.
- Keep each backend collection scoped to its owner. Aggregate existing authorized collections in the frontend: tenant/member entity discovery and permitted department catalog, filtered to the selected entity. No expanded grants or aggregate backend contract.
- Walk all root pages; do not limit sidebar roots to the first merged page. Combine trash pages by ascending exact file IDs with the earliest unfinished scan cursor, keeping the visible content page bounded to 100 without skipping another owner's later entries.
- Positive E2E must inspect actual root controls, click into each root, render and download real marker files, survive reload, and show authorized markers in the recycle bin. HTTP 200 or a menu link alone is insufficient.

## 2026-10-09 ? Role E2E fixes preserve granted authority
- User authorized all-role E2E; use live seven-persona desktop/mobile matrix and isolated real SQLite lifecycle. No delegated agents.
- Department-only readers do not need entity_view to load their authorized department list. Gate the optional entity catalog with the existing permission rather than expanding backend grants or failing the whole page.
- FileBrowserBody intentionally remounts across location/page/access changes. Keep that authorization boundary; disable the mobile folder trigger during mutation/loading so it cannot open into a body about to be replaced. Do not remove the remount or add retries/navigation redesign.
- Align old acceptance direct login requests with the already-configured test CAPTCHA answer; do not weaken validation or change production CAPTCHA policy.
- Local Passkey service is unavailable (503). Assert unavailable state and disabled enrollment; no claim of successful enrollment.

# Decisions

## D103 - A Proven Number Is The Enrolment, And Never Both Factors (2026-10-04)

Decision: a code sent to a number somebody has already proven answers a
second-factor challenge. There is no enrolment record behind it: the proven
number on the principal *is* the enrolment -- the same fact the profile screen
shows and the sign-in path reads. An account with no TOTP secret used to face a
challenge it could not answer; now it can answer, and `MFAStatus` says so.

It is the weakest factor here. A number can be moved to another SIM by somebody
who talks a carrier into it, where a passkey or a TOTP secret cannot. It is
offered because the alternative for most people is no second factor at all, it
is off by default, and it is never the only thing offered: an account that can
enrol TOTP is still told to.

**A number is never both factors of one sign-in.** Signing in with a code and
then answering the challenge with another code to the same number is one factor
twice. The challenge records what proved the first factor (`first_factor`,
migration 20), so the rule is enforced where the code is asked for rather than
discovered after one was sent, and the challenge does not list `sms` among its
methods in that case. Both halves are enforced: the offer and the request.

The stored code is tried before a TOTP step, because six digits is the same
shape either way and a value matching a stored code is unambiguous, where a TOTP
comparison would accept a code this account was never sent. An account with no
TOTP secret and a wrong code gets a refusal rather than the TOTP path being
handed an empty secret.

The number is re-read when the code is spent, not trusted from the credential
row: a code proves the number it went to, and that has to still be the number on
the account. Proving a *newer* number while a code is outstanding leaves the
flag true and only the number different, which is the case that check exists for.

Turning the factor off stops codes already in flight from answering, not merely
new ones from being sent.

A wrong code reaches the same failure count a wrong TOTP step does. The first
implementation returned early for an account with no TOTP secret, which meant
such an account counted nothing at all: six digits could be guessed as often as
the code lived. The early return is gone -- a code that does not match falls
through to the one place that counts a failed attempt -- and the four tests whose
expectations encoded the wrong behaviour were corrected, not the code.

The session says which factor opened it, taken from `first_factor` rather than
assumed to be a password. A login opened by reading a message must not claim a
password was typed, or a policy that asks for one is satisfied by something else.

Four of these guards passed their first mutation run. Each time the reason was
the same: the test never reached the guard -- the account had no second factor so
an earlier branch returned, or the fixture changed two things where the guard
only needed one. Every one of those paths was reachable, so the tests were
rewritten to reach them rather than the guards trusted.

A note on the test harness this exposed: the fault injector matched gorm's table
clause, which is empty for a write sent as raw SQL and holds the *alias* for the
principal readers. Both kinds of read and write were therefore injectable by
nothing, and the cases that looked like they covered them passed having proved
nothing. The injector now matches the statement text for raw writes and both
forms of the principal table.
Status: IMPLEMENTED.

## D102 - A Proven Number Is One Factor, And Only One (2026-10-04)

Decision: somebody can sign in with a code sent to a number they have already
proven. It is one factor, exactly as a password is, so the session records
`sms` as what proved it at level 1, and whatever the policy then requires it
still requires: this path hands off to the same gate the password path does.

That gate is now one function. `BeginLogin`'s tail became
`completeFirstFactor`, called by both ways in, because a second way in with a
copied tail is how a second factor quietly stops being required -- the copy is
where the next policy change does not land.

Only a proven number resolves. An unproven one is a string somebody typed into
their own profile, and anybody can type any number. Two accounts that have each
proven the same number -- a carrier reissues them -- resolve to nobody, which is
the answer an ambiguous recovery identifier already gets; the connector returns
up to two and authn decides, as it does there.

Asking for a code never says what it found. A number with an account, a number
without one, and something that is not a number all answer the same way,
because the answer is read by whoever typed the number and they are not always
its owner. The captcha is on that endpoint for the same reason it is on the
password endpoint: it is a pre-authentication surface, and the only failure
counter that exists is per-account, which an attacker spreading attempts across
numbers never trips.

Wrong codes count twice: against the code's own ceiling and against the account
lock a run of wrong passwords sets. The two ways in therefore cannot be attacked
independently, and a locked account is not sent a fresh code.

The purpose is part of the credential's key. Through the API a verify code and a
sign-in code cannot both be live for one account -- a verify code exists only
while the number is unproven and a sign-in code only once it is proven, and the
act that flips that flag consumes the verify code -- so this is not guarding a
reachable attack. It is there so the property is local instead of a consequence
of two flags elsewhere staying in step, and so a direct database write or a
future flow cannot make one code answer the other. The test says that, rather
than claiming a behavioural proof it cannot give.

The constraint D100 and D101 left open comes due here: a proven number signs
somebody in, so it is a credential, and replacing or removing one now bumps the
credential version and revokes the sessions and tokens it opened -- exactly as
replacing an address does. Adding an unproven number for the first time revokes
nothing, because an unproven number reaches nothing.

One check was removed rather than kept as defence: the sign-in flow does not
compare the code's number against the principal's, because the lookup above it
already required a proven number equal to the one typed. A number changed since
the code left stops resolving at all -- changing one unverifies it. The test now
names that as the mechanism instead of implying a comparison does it.
Status: IMPLEMENTED.

## D101 - A Phone Is Proven By A Code, In Its Own Credential (2026-10-04)

Decision: `iam_phone_verification_codes` holds one outstanding code per
principal, with the number it was sent to beside it. A separate table from the
email credential, not a channel column on it: an email credential is addressed
by the link token in its primary key and a phone has no link, so folding one
into the other would mean inventing a primary key for something that has none.
What the two flows genuinely share is shared -- the six-digit generator
(`verificationCode`, no longer named for email), the attempt ceiling, the
notification outbox, and the audit shape.

The primary key makes "one live code" true rather than hoped for: asking again
replaces the code, so the last message somebody received is the one that works,
which is the one they will type.

Completion is authenticated and carries the code alone. The session says whose
number this is, so no request, URL or log line has to hold a phone number --
unlike the email flow, whose link arrives in a mailbox that may be on another
device and therefore must be completable without a session.

The number is stored with the code because a code proves the number it was sent
to and nothing else. A number changed while a code is outstanding leaves that
code proving nothing about the number held now, so it is refused -- and not
counted as a wrong guess, because changing a number is not an attempt at
guessing one.

**The attempt counter is written outside the transaction that promotes the
number.** A refusal leaves that transaction by returning an error, which rolls
back everything it did -- including the increment. A test found this: five wrong
guesses, then the right code still worked. The email flow records its attempts
outside its transaction for the same reason, which is now the reason written
down rather than inferred.

Every property is enforced twice, in Go and in the write predicate
(`consumed_at IS NULL AND expires_at > ? AND attempts < ?` on the consume,
`phone = ? AND phone_verified = ?` on the promotion). Mutation testing had to
remove both halves to turn a test red, which is the point: either half alone
still refuses.

The write to `phone_verified` goes through a connector identity publishes
(`PrincipalStateStore.VerifyPhone`), like `VerifyEmail` before it, because the
column belongs to identity. The number is in its predicate, so the write that
records the proof re-checks the thing it proves.

No SMS vendor: delivery is the POST to the notification service the outbox
already makes, with a new `kind`. The code lives minutes rather than days, and
the configuration refuses anything outside one minute to one hour -- six digits
read off a message already in somebody's hand is worth guessing if it is live
for a day.

Verifying a number still does not make it a recovery or sign-in channel, so the
constraint recorded in D100 -- that a number which can recover an account must
revoke sessions when it changes -- remains open for whoever builds that.
Status: IMPLEMENTED.

## D100 - A Phone Number Is A Contact, Not An Identity (2026-10-04)

Decision: `iam_principals` carries `phone` and `phone_verified`, indexed and not
unique, mirroring the `email` column beside it. `login_name` remains the only
unique identity. A number is PII from the moment it exists: anonymization clears
it with the address, the audit chain records only that it changed, and the
profile field catalog gains `phone` so a field policy can hide or redact it --
and the verification flag follows the field it derives from, so a hidden number
cannot be probed through "is it verified".

Not unique, because a number is a way to reach somebody rather than a way to
name them. Numbers are shared inside a household and reissued by carriers after
release; a uniqueness constraint would let whoever registered one first block an
account that has a better claim to it. Email, the same kind of contact, is
already indexed and not unique here.

Changing the number clears `phone_verified` and revokes nothing else. An address
change revokes sessions and bumps the credential version because the address can
recover the account; a number cannot yet recover or sign in to anything, so there
is nothing for a change to invalidate. **The moment a number becomes a recovery
or sign-in channel, this has to revoke too** -- that is a constraint on the
verification work, not a preference.

Accepted in one shape: a plus sign and four to fifteen digits, which is what
E.164 allows. Spaces, dashes, dots and parentheses are how a number is written
down rather than part of it, so they are removed instead of refused; a number
without a country code is refused, because the same digits name different people
in different countries. An empty value clears it, which is how somebody takes
their number back.

`FieldConstraint.EmailVerified` became `FieldConstraint.Verified(field, value)`.
The rule it encodes -- a verification flag is readable only where the contact is
-- was never about email, and a second copy of it for phone would have been the
same six lines with one word changed.

Nothing writes the number back after anonymization, and nothing new was needed
for that: the tombstone trigger on `iam_principals` already aborts every update
to an anonymized principal, so the number is behind the same schema-level gate as
the address.

Scope: the number itself and its PII handling. Verifying it by code through the
existing notification outbox is the next piece, and will extend the email
verification credential to be channel-aware rather than copy it -- that flow
already snapshots the contact into the credential row so that changing the
contact invalidates an outstanding code, and a phone must keep that property.
No SMS vendor is involved at any point: delivery stays the POST to the
notification service that the outbox already makes.
Status: IMPLEMENTED (number and PII handling); verification outstanding.

## D99 - A Role Assignment May Name The Moment It Stops Granting (2026-10-04)

Decision: `iam_role_members` carries a nullable `expires_at`. NULL is no end,
which is what every assignment written before the column means. Every read that
treats an assignment as authority filters it; reads that manage the row see all
rows, and the grant review reports the date.

The four ways a role reaches a principal were already asymmetric: a temporary
grant has `starts_at`/`ends_at`, a group-derived role is bounded by the group
membership's `ends_at`, an entity-scoped binding has `expires_at` -- and all
three filtered on them. Only the plain tenant-wide assignment had no end to
speak of, and `governance/review_grants.go` selected `NULL AS grant_expires_at`
for it, a column reserved for exactly this.

Not the governance table: `iam_temporary_role_grants` exists for an approved
access request. Its `source_type`/`source_id` point at the request, both of its
times are NOT NULL, and each grant has its own identity so it can be revoked
singly and reviewed as "temporary". An assignment with an end date is the same
relation as one without -- (tenant, role, principal) -- with a bound on it.
Backing it with that table would give one relation two identities and make
"who holds this role" a union.

Read-time, not a sweeper: the authorization answer is already computed against
the request's clock, three of the four paths already filtered this way, and a
sweeper would make correctness depend on a job having run. The row outliving
its grant is also what lets a listing say when it ended.

The durable-administrator guard reads `expires_at IS NULL`, not "has not
ended". It asks whether the tenant would still have an administrator
afterwards, so an administrator whose grant lapses next week is an answer that
expires -- which is what the group branch has required of a timed membership
since it was written.

Granting is a PUT of the whole assignment: an end date replaces whatever the
row carried and no end date removes one. The answer every existing caller
already gets is unchanged, because granting a role somebody already holds on
the same terms still reports no change. The row is read and then written rather
than upserted: an upsert's affected-row count means different things in each
dialect, and `changed` has to mean the assignment actually changed.

Scope: the tenant-wide assignment. `iam_group_role_bindings` keeps no expiry of
its own -- group membership already carries `starts_at`/`ends_at` and is
filtered, which is how a tenant time-bounds the roles a group brings. The
system-member edit view (`systemMemberView`) stays unfiltered: it lists the rows
a console toggles, and hiding a lapsed one would make the toggle a no-op.

Alternatives Considered: a `valid_from` as well -- not added, nothing asked for
a future-dated assignment and the temporary grant already has `starts_at` for
the case that did. Deleting on expiry -- rejected, it destroys the record of a
grant somebody made.

Scope of verification: nine decision sites, each with its own behavioural test
and each mutation-verified (the effective-roles CTE, the durable-administrator
count, the system-role evaluation, the platform-administrator listing,
`ListMemberRoleIDs`, the cached-snapshot bound, `roleGrantedOn`, the
account-closure guard, and the grant review's two halves).
Status: IMPLEMENTED.

## D98 - A Delegation Is A Written-Down Pair, Not A Client Flag (2026-10-04)

Decision: token exchange (RFC 8693) is permitted by a row in
`iam_oauth_exchange_grants` naming one tenant, one client and one subject
prefix with the scopes that delegation may ask for. A client with no row
cannot exchange at all; a client with one cannot step outside its scopes. An
exact prefix wins over a range, an empty prefix covers nobody, and a bare `*`
is refused at the input. Exchange is refused to a public client at both the
creation and the use site, and the issued token carries no refresh token.
The device grant (RFC 8628) stores both codes hashed, enforces the poll
interval server-side rather than trusting the device, and issues for whoever
approved -- with their own assurance -- rather than for whoever polls.

Reason: the alternative shape, a `token_exchange` flag on the client, makes
impersonation open-ended the moment it is set: the client could act as any
principal in the tenant for everything it is itself scoped for. The scopes
being the grant's rather than the client's is what keeps "may act as this
person" from meaning "may do everything it can do, as this person". A public
client authenticates with nothing, so a flag on one would make its identifier
alone sufficient to act as every covered principal -- which is why the two
guards are at both ends rather than only in validation. No refresh token is
issued because a refreshable delegation outlives the authority it derived
from; the holder can exchange again while its own token is valid.

Prefix matching happens in Go rather than in SQL: a `LIKE` over a tenant's
grants would make the stored value a pattern language, and a `%` or `_`
someone typed would then mean something nobody wrote down.

Alternatives Considered: RFC 7591 dynamic client registration, which would
let a client create its own credentials -- not added, because every client
here belongs to a tenant that must authorize it. Trusting the device's
`interval` -- rejected, a device that ignores it would turn the endpoint into
a guessing loop. Admitting other subject token types (SAML, JWT from
elsewhere) -- refused with `unsupported_token_type`, since accepting an
assertion this deployment cannot verify is accepting it from somewhere it does
not trust.

Scope: `oauth` only. Extends D-"A Failed Read Is Not A Refusal" to the new
grants: `deviceClient`, both device-code reads and both exchange-grant reads
collapse only `gorm.ErrRecordNotFound`, so a storage fault no longer reads as
`invalid_grant` (which makes a device discard its code) or as a withdrawn
delegation. `validateClient`'s grant whitelist now admits both grants, without
which no client could ever be created that could use them.
Status: IMPLEMENTED.

## D97 - Webhooks Follow The Audit Chain Rather Than Being Told (2026-10-04)

Decision: outbound event delivery is a module that follows each tenant's audit
chain with a cursor, not a `Publisher` capability other modules call. `audit`
exposes `EventStreamReader`, whose projection selects neither `detail` nor
`ip_address` nor `user_agent`; `webhook` keeps one `after_sequence` per
subscription and advances it in the same transaction that records the work owed.

Reason: the chain is already the complete, ordered, durable record of what
happened, written at thirty call sites. A follower with a cursor therefore cannot
miss an event and no other module has to remember to announce one -- which is the
failure mode of per-call-site instrumentation, where the event added next year is
the one nobody publishes. Uniqueness over (subscription, event) makes a fan-out
pass that is interrupted safe to simply run again.

What may leave is decided in the module that owns the data. The three columns
holding a person's data are absent from the projection's Select rather than
dropped afterwards, so there is no copy for a consumer to forward by mistake. A
receiver is an endpoint somebody else operates: a deployment honoring a
withdrawal can erase what it holds, not what it has already handed out.

Alternatives considered: (a) a `Publisher` capability invoked beside each audit
append - rejected, it doubles the instrumentation and the second copy drifts;
(b) letting webhook read the audit table directly - rejected, the module that
owns a table decides what leaves it; (c) sending the audit detail and asking
receivers to discard it - rejected, that is a promise nobody can keep.

Tradeoffs: a receiver gets identifiers and outcomes, not names, so an integration
that wants detail must call back for it and be authorized to. Delivery is as
timely as the poll interval rather than immediate.

Scope and constraints future work must not reverse without approval:
- `audit.StreamEvent` MUST NOT gain a field holding personal data. A test asserts
  the payload a receiver sees.
- A subscription starts at the tenant's current head, not at zero: registering an
  endpoint asks to hear what happens next, not to receive the whole history.
- Signature headers are generically named; the deployment identifies itself from
  its runtime identity, never from a literal in the catalog or the source.

Status: implemented. `internal/iam/webhook`, 2 tables, 5 operations, 90.4%
coverage. Adding the module required registering it in seven frozen inventories,
one of which (the permission catalog) would otherwise have panicked at startup
rather than failed a test.

## D95 - Irreversible Migration Milestones Refuse Before Rolling Back (2026-10-03)

Decision: `module.MigrationMilestone` gains `Irreversible bool`, and the
orchestrator refuses any downward request that would cross an applied
irreversible milestone before it touches the database. The refusal applies to
both `down-to` and the plain one-milestone `down`. federation declares it on
`binding-trust` (version 7). The per-dialect `RAISE` inside that migration stays
as the backstop for anyone driving goose directly.

Reason: irreversibility used to live only inside the migration's own down
section, so the orchestrator could not know about it in advance. Rolling back
proceeds one version at a time, so the moment a reversible migration existed
above binding trust, a refused `down-to 6` first undid that migration and then
failed - leaving the database between two milestones. An operator meets that
state while already recovering from something else.

Alternatives considered: (a) leave the behaviour and assert the partial state in
tests - rejected, it documents a hazard instead of removing it; (b) make every
migration above an irreversible one also irreversible - rejected, it spreads a
constraint that belongs to one milestone; (c) wrap the whole rollback in one
transaction - rejected, DDL transactionality differs per engine and MySQL cannot
deliver it.

Tradeoffs: a module must now declare irreversibility in two places, the
milestone and its SQL. The duplication is deliberate: the declaration is what
makes the refusal early, the SQL is what still protects a direct goose run.

Scope: `pkg/module/module.go`, `internal/app/migration.go`,
`internal/iam/federation/migrate.go`. Verified by
`TestIrreversibleMilestoneRefusesBeforeRollingBack` and
`TestIrreversibleMilestoneDoesNotBlockWhatWasNeverApplied` over all three
engines, both mutation-checked: removing the guard turns them red.

Status: implemented.

## D96 - One Upstream Identity, Three Protocols (2026-10-03)

Decision: the normalized shape every federation protocol produces is named
`upstreamIdentity` rather than `idTokenClaims`.

Reason: the type was named after OIDC's ID token and then grew two more
producers. SAML already filled it from an assertion, and a social provider fills
it from a profile document. A name that states one producer invites the next
reader to assume the others do not exist, and invites code to treat an absent
field as a fault rather than as what that protocol states.

The fields that belong to one producer are documented as such instead of being
split out: Nonce and AuthorizedParty are ID-token concepts, and AuthTime is
absent from plain OAuth 2.0 entirely, which is the whole reason a social provider
cannot serve as a re-authentication proof.

Alternatives considered: a per-protocol type with a conversion at each seam -
rejected, three nearly identical structs and three conversions to keep in step
for no behavioural gain.

Scope: rename only, confined to `internal/iam/federation`; the type was already
package-private, so nothing outside it changed.

Status: implemented.

## D94 - Social Sign-In Is One Provider Type With a Connector Table (2026-10-03)

Decision: social sign-in is a third federation provider type, `oauth2`. A
connector is a row in a compiled-in table declaring endpoints, parameter names
and dotted claim paths; a provider whose shape the table cannot express supplies
a `profileFlow` instead (QQ is the first). The connector key is stored in the
existing `issuer` column, so no column or table was added. Providers that
publish OpenID Connect discovery stay on the `oidc` type.

Reason: what differs between GitHub, Discord and Feishu is a few URLs and JSON
field names, not the protocol, so it is data. What differs for QQ, Alipay and
WeCom is the shape of the exchange, so that is code. Keeping endpoints compiled
in means a tenant administrator cannot repoint a provider's profile endpoint at
a host they control while keeping the client ID that existing identity links
trust; storing the key in `issuer` makes it part of the provider's security
fingerprint for free, so changing it distrusts those links.

Alternatives considered: (a) one package per provider, as Casdoor and Logto do -
rejected, roughly 7000 lines to express 400 lines of data; (b) endpoints as
tenant configuration - rejected on the takeover path above; (c) routing
OIDC-capable providers through this path for uniformity - rejected, it trades a
verified ID token and an authentication time for an unsigned profile read.

Tradeoffs: a provider's endpoints now change only by changing code, so an API
version bump is a code change. That is accepted because it is reviewable, and
because the alternative is the takeover path.

Scope and constraints that future work must not reverse without approval:
- A social provider MUST NOT serve as a re-authentication proof. Plain OAuth 2.0
  carries no authentication time and social providers routinely answer from an
  existing session. `StartIdentityOperation` and `VerifyIdentityOperation` both
  refuse it, and `AuthTime` MUST NOT be fabricated to pass the freshness window.
  Linking is permitted; its freshness rests on the single-use authorization code,
  PKCE and the replay claim.
- A connector that states no email address cannot provision an account. Eight of
  the twenty-four are in that position and a test freezes the list.
- The outbound User-Agent is read from the runtime identity, never written into
  the catalog.

Status: implemented; 26 connectors, three of them with a flow of their own.
Alipay signs every gateway call with the application's RSA private key, so its
stored "client secret" is a PKCS#8 key rather than a shared secret; WeCom
authenticates the application from its corp credentials before it will identify
the person, and admits members of the organization only. Both were verified
against the live endpoints: the gateway answers `isv.missing-app-id` and WeCom
answers `corpsecret missing`, which confirms the parameter names as well as the
addresses. WeChat Mini Program, Steam, Dropbox, Notion and VK remain
unimplemented, each one `profileFlow` away.

WeCom capitalises `UserId` in its answer while the catalog reads `userid`, so the
flow normalizes the identifier onto the one key the claim path reads. Tolerating
both spellings in the flow's own check while the claim path read only one is how
a login WeCom answered correctly would still have failed; a test now goes through
the mapping rather than the raw document, and removing the normalization turns it
red.

Endpoint verification (2026-10-03, by unauthenticated probe from the development
sandbox): seventeen connectors answer as the catalog expects -- github, gitlab,
gitee, bitbucket, spotify, naver, wechat, qq, weibo, baidu, dingtalk, feishu,
lark, amazon, figma, yandex, douyin. A token endpoint was POSTed an empty body
and had to answer an OAuth-shaped error rather than 404. Four parameter choices
were confirmed from those answers: wechat names its credential `appid` and takes
the profile token as a query parameter, dingtalk names it `clientId`, douyin
nests its payload under `data`, and feishu and lark both serve the v2 token
endpoint.

Seven cannot be reached from this sandbox: discord, facebook, x, kakao,
instagram, coinbase, reddit. They were instead checked against the published
documentation and against two open-source implementations whose source is
reachable -- casdoor/casdoor `idp/` and markbates/goth `providers/` -- which
found six of the twenty-four genuinely wrong:

- instagram: the Basic Display API it used was switched off on 2024-12-04. The
  authorization host moved to www.instagram.com and the scopes were renamed.
  Goth still carries the retired endpoints, so it is not a reference here.
- facebook: the token exchange is a GET with the credentials in the query
  string, not a POST with HTTP Basic. Casdoor builds the same GET.
- discord, kakao, coinbase, instagram: the token endpoint takes the secret as a
  request parameter, not an Authorization header.
- reddit: `/api/v1/me` states whether an address is verified but never sends the
  address, so claiming one told the console that provisioning could apply.
  Goth maps only id and name, which agrees.
- coinbase: the email scope is `identity:user:email`, and every read needs a
  CB-VERSION header.

Cross-checking the CJK connectors against casdoor confirmed every field this
catalog maps, dingtalk and douyin and qq down to the exact names, including qq's
non-zero `ret` being an error. Two places where this catalog is more current than
goth: discord's display name is `global_name`, and kakao's is under
`kakao_account.profile` rather than the legacy `properties`.

Field names for connectors that need a real token to read a profile remain
unverified against a live response; they are verified against documentation and
at least one independent implementation.
## Account view controls follow creation authority - 2026-10-09
- Decision: shared AccountViewDialog entry requires account_view_create, independently of account/organization list browsing permissions.
- Reason: read-only system demo could list accounts but was offered a view action its backend admission refuses.
- Alternatives: grant impersonation merely to make the button work; rejected because browsing does not require target-account authority.
- Scope: shared frontend entry gate and real regression/authorized-popup-exit tests; no backend permission changes.
- Status: IMPLEMENTED / real browser regression and authorized administrator success PASS.

## System demo subadministrator browsing grant - 2026-10-09
- Decision: replace tenant_view-only demo grant with explicit system browsing permissions for accounts, departments, roles, menus, operating platforms, tenants, audit events, and shared files. Retain full_administrator=false; do not grant platform_administer or mutation actions.
- Reason: user requested review then execution to resolve the misleading one-menu system demo. Wider read scope demonstrates system management without turning a subaccount into a full administrator.
- Alternatives: show unauthorized menu links; grant full administrator. Rejected because the former produces dead ends and the latter is broader than needed for browsing.
- Tradeoffs: governance/closure/grant pages requiring platform_administer remain excluded; management writes still return 403. Default landing now follows the first authorized system-account menu.
- Scope: development demo initialization and current local account grant; existing backend authorization and production menus unchanged.
- Status: IMPLEMENTED / 8 real management-page visits and denied write checks PASS; configuration persists through dev startup.

## Concurrent personal root initialization - 2026-10-09
- Decision: harden the existing root-creation API with transaction insert-before-read and conflict-do-nothing using the existing unique principal key. The inserting transaction alone creates root hierarchy; concurrent callers reuse its committed space/root/ACL.
- Reason: user approved review finding that multiple tabs/devices can concurrently initialize despite frontend busy state.
- Alternatives: new implicit-parent API or frontend retries/locks; unnecessary contract changes or insufficient across devices.
- Tradeoffs: uses an ID candidate even when the root already exists; preserves existing transaction, auditing, and authorization. Storage-content creation remains a separate request and can be retried.
- Scope: personal initialization concurrency and regression tests. No navigation/permission/schema changes.
- Status: IMPLEMENTED; concurrent service, read-only HTTP, real SQLite HTTP/browser acceptance, and full files regression PASS.

## Personal initialization scope after rollback - 2026-10-08
- Decision: broad personal/workspace changes were rolled back at user request. Reintroduce only automatic personal root preparation on first write through existing API. Keep GET visits and read-only views free of initialization, and preserve all original navigation/auth behavior.
- Reason: user explicitly requested removing the manual creation step after rollback, without the previous broader redesign.
- Alternatives: eager creation on visits or backend/API redesign; unnecessary for this scoped UX change.
- Tradeoff: root preparation and content write are separate existing requests; initialization failure leaves the editor available for retry.
- Scope: personal file page/browser plus focused tests. System subaccount tenant_view-only grant was confirmed live; no authorization expansion without user-specified scope.
- Status: IMPLEMENTED / real desktop/mobile/retry acceptance PASS.

## D93 - Demo Entrances Only Show Matching Personas (2026-09-27)

User requires normal login pages to show only their own entrance. Entrance tabs
are development/demo tools, enabled by the same configured demo-account boundary
as the selector. Demo personas declare entrances explicitly; unassigned personas
are not offered in any selector. System, platform, tenant and user selectors
contain their corresponding personas. The personal-user fixture has no admin
grant or organization membership. This is display/test configuration, not a new
identity type or an authorization rule. Switching entrance clears credentials
and target IDs. Shared identity and live server authorization remain authoritative.

Verified: normal entrance real E2E, demo exact-option and switch-clearing browser
checks, 7 real persona logins, desktop/mobile layouts, 239 frontend unit tests,
typecheck/lint/build PASS. No further implementation pending.

## D92 - View-Prefixed Web Routes and Browser Test Location (2026-09-27)

User approved complete Web route boundaries: /system, /platform, /admin and
/user. Authentication, workspace selection, internal pages, navigation and
account-view handoffs use the owning prefix. Tenant remains the internal scope
name; admin is its Web prefix. Public registration/recovery/email continuations
exist under admin/user; system/platform expose login only. Old bookmarks redirect
with query/hash retained. Entrances never grant additional permissions.

API resource URLs remain unchanged: Web /admin maps to /tenants and Web /platform
maps to /operating-platforms. Scope detection excludes login/chooser segments.
Account-view credentials remain in tab memory with exact scopes and real actors.

Browser audits, acceptance flows and fixtures belong in apps/iam/tests. Genuine
generation/bootstrap tools and their shared credential reader remain in scripts.
Neither directory is imported by the production entry. Unit tests stay beside
source. Package commands, integration invocation and docs follow the moved paths.

Status: IMPLEMENTED and VERIFIED. Direct execution, architecture/security review
and acceptance PASS. Frontend typecheck/lint/build and 238 unit tests, affected
backend suites, API type/OpenAPI drift checks PASS. Full real SQLite/browser E2E
56/56 PASS (.temp/sqlite-lifecycle-kMOVZj/report.json), zero mocked business
responses and zero page errors. Relocated file-browser UI audit and Chinese
retained-demo selectors/workspaces/logout desktop/mobile PASS. No database
migrations or dependencies introduced; no further implementation is pending.

## D72 - The coverage gate measures the union across test binaries

**Date** 2026-09-22

**Decision / Reason**
`make test-coverage` now passes `-coverpkg` over the gated package set, so a
package exercised by a sibling integration suite is credited instead of reading
as untested. This closes the measurement gap D69 recorded: `internal/app`
measured 86.8% from its own tests and 88.3% once `internal/integration/iam` was
included, and its whole public lifecycle API read 0% despite being exercised
there.

It cannot flatter the result. The same packages are already in the profile and
therefore already in the denominator - Go includes packages with no test files -
so `-coverpkg` only adds covered blocks. Total went 91.0% -> 91.6%, and the run
got faster, 13 minutes to under 6.

**Alternatives Considered / Tradeoffs**
Duplicating the integration lifecycle tests inside `internal/app` would have
moved the number without adding safety, which is the coverage-chasing the rest
of this work avoided.

The per-module check had to be fixed in the same change. With `-coverpkg` every
test binary reports every gated package, so each block appears once per binary
and almost always with a zero count. The total is computed by
`go tool cover -func`, which merges blocks, but the per-module figure was a
line-by-line sum that counted one block as dozens of statements and collapsed
every module to about 2%. It now keys by block and takes the union, which is
what the total was already doing.

**Scope / Status**
Done. `make ci` passes: total 91.6%, every gated module above 90%.

---

## D73 - Guard-layer messages are respx keys, not bare codes

**Date** 2026-09-22

**Decision / Reason**
`authz` reported outcomes as bare strings - `not_found`, `forbidden`,
`unauthorized`, `invalid_id` and four more. Localization resolves
`respx.not_found`, never `not_found`, so every one passed through raw. Because
D66's owner resolution answers an absent globally addressed resource before the
handler runs, this was what a client saw on a 404 across the entire owned
resource surface, which is the regression D69 recorded.

respx already owned four of these keys with text in all three locales; they were
simply unreachable. It now exports them as constants, gains the four it was
missing, and authz reports those constants at its 25 call sites. No new i18n
namespace and no new registration point were introduced.

**Alternatives Considered / Tradeoffs**
Giving authz its own bundle was the first plan and was dropped once respx turned
out to own the vocabulary already: a second namespace for the same four
concepts would have split ownership of one client-visible message set. Having
the resolver defer to the owning module for the message was rejected as a
redesign of the authorization path for a wording problem.

The SAML test that pinned the raw `not_found` was not retargeted to the new key,
which would only have re-encoded the bug. Its harness now registers respx's
bundle and it asserts the localized sentence, plus that the key does not survive
into the body.

**Scope / Status**
Done. This changes the `message` field for guard-layer refusals on every
authorized route.

The one consumer in this repository was checked rather than assumed: no
production code in `apps/frontend-web-admin` compares `.message` at all, and the
places that look like they might are the frontend's own status enum or its own
i18n keys. `sessionFailureStatus` branches on `cause.status === 401`, and the
files page on `cause.status === 403`. Frontend typecheck and its 126 tests pass
unchanged. An external caller matching the bare strings would still break, but
that was never a supported contract: `message` is documented as a
human-readable summary and `Code` is the machine-readable field.

---

## D70 - Cross-module principal anonymization lives outside the IAM namespace

**Date** 2026-09-22

**Decision / Reason**
`internal/iam/lifecycle/anonymizer.go` moves to `internal/principallifecycle`.
It held one file with no `module.go`, no `Module` type, no i18n and no
migrations, so it was never a module, yet it sat directly under the IAM
namespace where `TestIAMModuleArchitecture` requires every directory to be one.
It imports eight IAM modules because it erases principal data across all of
them, which is cross-module composition rather than anything a single module
can own.

Adding "lifecycle" to the `iamModules` allowlist was rejected outright: that
relaxes an architecture rule to accommodate a misplacement, the same move as
excusing a coverage gate. The test was right and the file was in the wrong
place. The name follows the vocabulary the integration suite already uses in
`principallifecycle_test.go`.

**Alternatives Considered / Tradeoffs**
Folding it into an owning IAM module (identity or core) was rejected because it
would make that module depend on seven siblings, which is precisely the coupling
the `module-dependency` rule polices. `internal/maintenance` looked right - it
already holds cross-module operational procedures and imports seven IAM modules
- but it imports `internal/app`, and `app` wires the anonymizer, so that
placement is an import cycle. That was found by building, not by reasoning.

**Scope / Status**
Done. `internal/iam/` now contains exactly the nine modules the allowlist names.

---

## D71 - Table names are bound by their owner, not rebuilt at the call site

**Date** 2026-09-22

**Decision / Reason**
The two `concatenated-table-name` violations are fixed rather than budgeted.
`internal/todo/service.go` now takes its table from the module's own
`query.BindNames`. `principallifecycle` no longer prepends the deployment
prefix itself; `goose.Source.VersionTable()` is exported and does it, which is
already a sanctioned concatenation point. The stale ledger entry
`concatenated-table-name:c7121096622dde53` is deleted and the budget lowered
from 13 to 12, matching the gate's own instruction that the ledger may only
shrink.

**Alternatives Considered / Tradeoffs**
Reading each version table from the owning module's `MigrationSource()` would
have removed the literal names too, and the seven names were verified identical
first. It was reverted: the architecture gate rejected it as
`cross-module-concrete-type`, because reaching for a sibling's concrete `Module`
type is a worse coupling than a literal string. Raising the budget was never
considered - the gate exists to make that a deliberate, documented act.

**Scope / Status**
Done. `make ci` passes end to end for the first time on this branch: fmt, lint,
coverage at 91.0% with every module above 90%, all six integration suites
including `tests/architecture`, build, and the generated OpenAPI.

---

## D67 - The coverage gate is not relaxed to accommodate internal/files

**Date** 2026-09-21

**Decision / Reason**
The total coverage gate now passes at 90.023%. `internal/files` remains at
82.7% against the 90% per-module minimum, so `make test-coverage` still fails on
exactly that one module. The gate is left exactly as it is. No per-module
exception, no lowered threshold, no removal of `internal/files` from
MODULE_COVERAGE_PACKAGES.

A gate that is edited until it passes stops being evidence. `internal/files` is
the newest area of the codebase and the only module that touches user file
content directly, so it is the last place where the signal should be hidden. A
red gate naming one module is more useful than a green gate that quietly
excuses it.

**Alternatives Considered / Tradeoffs**
Granting a dated per-module exception was rejected: it makes CI green while the
gap stays invisible in day-to-day work, and dated exceptions outlive their
dates. Lowering the global minimum was rejected for the same reason plus it
relaxes every other module to buy one. Covering the remaining ~330 statements
now was rejected as disproportionate: they sit in 766 blocks averaging one
statement each, almost entirely `if err != nil` guards on individual database
and storage calls, so closing them means injecting a distinct fault at roughly
330 call sites for assertions that mostly restate that an error propagates.

The cost is that `make ci` stays red until this is resolved, and anyone running
it must know why.

**Scope / Status**
Accepted for now, explicitly unfinished. The efficient way to close it is a
shared SQL fault-injection helper - the mechanism already exists ad hoc in this
package as gorm callbacks - plus table-driven sweeps, rather than one test per
guard. Revisit before treating `make ci` as a release gate.

---

## D68 - Helpers orphaned by the D66 route migration are deleted, not kept

**Date** 2026-09-21

**Decision / Reason**
`tenantResourceIDs` in `internal/iam/governance/http.go` and
`parseOAuthClientIDs` in `internal/iam/oauth/http.go` are removed. Both parse a
tenant plus a resource identifier from the path, which is the shape D66
abolished when existing resources became globally addressed. Both are
package-private with no caller anywhere, including tests, and `golangci-lint`'s
`unused` rejected them. They were the only two lint findings in the repository,
so `make lint` was already failing at HEAD before any of this session's work.

**Alternatives Considered / Tradeoffs**
Keeping them behind a lint suppression was rejected: silencing the linter to
preserve code nothing calls trades a real signal for dead weight, and the next
reader would reasonably assume they are part of the routing contract. Leaving
them and the lint failure alone was rejected because it blocks the whole gate
for no benefit. The risk of deletion is that a future globally addressed route
wants the same parsing; that is a handful of lines to rewrite from the
surviving `parseGovernanceID` and `parseOAuthID`, which both remain in use.

**Scope / Status**
Done. `make lint` and `make fmt-check` are clean; the generated OpenAPI is
byte-identical, confirming neither function participated in route registration.

---

## D69 - Coverage measurement and the localized not-found regression are deferred

**Date** 2026-09-21

**Decision / Reason**
Two known problems are recorded rather than fixed in this pass.

The coverage gate runs each package's own tests with no `-coverpkg`, so code
exercised by a sibling integration package counts as uncovered. `internal/app`
measures 84.9% from its own suite but 86.5% once `internal/integration/iam` is
included, and its entire public lifecycle API reads 0% despite being exercised
there. The fix belongs in the Makefile, and changing measurement semantics
shifts every number in the gate at once, which is not something to do in the
same change that brings the gate to the line.

Separately, D66's owner resolution answers an absent globally addressed resource
with the registrar's raw `not_found` before the handler runs, so module-owned
localized not-found messages are no longer reachable on any owned route, and
authz's own error codes are not localized at all. That changes what every API
consumer sees on a 404 across the whole owned-resource surface, which is a
product decision.

**Alternatives Considered / Tradeoffs**
Duplicating the integration lifecycle tests inside `internal/app` would move the
number without adding safety, which is the coverage-chasing this work otherwise
avoided. Restoring localized messages by having the resolver defer to the module
was not attempted because it would change response bodies platform-wide.

**Scope / Status**
Open. Both are recorded so neither is rediscovered as a surprise.

---

## D66 - Globally unique resources use short canonical item paths

**Date** 2026-09-21

**Decision / Reason**
Every first-class internal resource uses a globally unique snowflake ID. Create
and list operations are nested under the direct owner, while read, update,
delete, direct subresources, and relationships address an existing resource by
its global ID. Thus entities are created and listed at
`/tenants/{tenant_id}/entities`, then addressed at `/entities/{entity_id}`;
departments are created and listed at `/entities/{entity_id}/departments`, then
addressed at `/departments/{department_id}`. The server resolves and verifies
tenant ownership. Scope headers and query parameters are not authorization
selectors.

Menus and files use the same account, tenant, entity, and department contexts.
Current-principal menu projections are `/me/menus`,
`/tenants/{tenant_id}/me/menus`, `/entities/{entity_id}/me/menus`, and
`/departments/{department_id}/me/menus`. Menu visibility reuses the existing
scoped permission evaluator and never replaces endpoint authorization.

**Alternatives Considered / Tradeoffs**
Always retaining the full ancestor chain was rejected because globally unique
IDs make it redundant for existing-resource operations and require callers to
repeat derivable state. Flat collections with scope headers were rejected
because the same URI could identify different resources and headers obscured
the authorization boundary. Optional entity/department query selectors were
rejected for the same reason. Short item paths require middleware to resolve the
resource owner before authorization and to fail cross-tenant references as not
found.

**Scope / Status**
Supersedes D65 where D65 requires full tenant/entity ancestry for every API item
path. Browser workspace URLs may remain fully nested for navigational context;
this decision governs the public HTTP API. Approved; breaking migration in
progress with no compatibility aliases.

---

## D65 - Browser and API resource scope is explicit in canonical paths

**Date** 2026-09-20

**Decision / Reason**
The browser uses `/me/*`, `/tenants/{tenant_id}/*`,
`/tenants/{tenant_id}/entities/{entity_id}/*`, and `/platform/*`. URL parameters
are the active workspace authority; local storage is only a remembered default.
Tenant/entity API authorization derives the same scope from path parameters and
validates entity ownership instead of trusting independently supplied context
headers. Public API routes have no `/iam` or `/authn` namespace prefix.

This makes links reproducible, prevents a selected tenant from displaying a
global account file root, and gives caches, logs, authorization and OpenAPI one
unambiguous resource identity.

**Alternatives Considered / Tradeoffs**
Keeping flat browser routes with local-storage/header scope was rejected because
the same URL could identify different data and incomplete entity context already
caused real isolation confusion. `/profile/{user_id}` was rejected for the
signed-in user's private resources because it implies arbitrary-user access;
`/me` expresses self-service. Compatibility aliases were rejected because the
user explicitly requested a complete breaking cleanup. The migration touches
route callers, menus, scripts, tests and generated clients in one release.

**Scope / Status**
Applies to the IAM admin Web, public HTTP route registration, documentation,
scripts, generated OpenAPI/client types and file-persona acceptance. Internal Go
package paths named `internal/iam` are architectural module names and are not
public URL prefixes. Approved; implementation in progress.

---

## D56 - File browser authorization is page-batched and SQL-bounded

**Decision**
Browser root/child pages load all target ancestor paths in one closure query,
all involved ACL rows in one bounded query, and file Version/Object metadata in
set queries. One authorizer caches IAM constraints by action and validates each
distinct root reference once. The ACL query is capped at
`unique ancestor nodes * maxNodeACLRules + 1`; reaching the cap fails closed.

**Reason**
Per-entry path, ACL, version, object and organization-reference reads made list
latency and database load grow linearly with page size. Batching must preserve
the same per-target root boundary, inheritance interruption, deny precedence and
IAM ceiling instead of widening authorization for performance.

**Alternatives Considered**
Increasing the page limit or caching permissions across requests was rejected:
neither removes N+1 work, and cross-request caching complicates revocation.
Joining every relation into one wide result was rejected because path, ACL and
object cardinalities multiply rows and make authorization budgets harder to
enforce consistently across dialects.

**Tradeoffs**
Each page still performs a fixed set of queries and may do one extra child-page
probe at exactly 100 visible entries. A corrupted over-budget ACL set denies the
whole request. The implementation retains tenant-space revision fencing for
writes; root-level revisions are a separate remaining scale change.

**Scope**
Files root and child browsing, node authorization and metadata projection only.
Search, trash, history, sharing and account-context browsing must independently
prove the same bounded authorization behavior before production acceptance.

**Status**
Implemented and tested: 2 visible files use 18 SELECTs and 100 use 19; Files full
and race suites pass. Real MySQL/PostgreSQL execution plans remain pending.

---

## D55 - File permission reads never acquire mutation locks

**Decision**
Loading a node's workspace path and ACL is a read-only operation and must read
`file_spaces` without a no-op UPDATE or `FOR UPDATE`. Mutation methods acquire
their own space/root lock explicitly before reading the state they fence.

**Reason**
The previous shared helper serialized all reads in one tenant through the single
`file_spaces` row. Keeping mutation locking at write entry points preserves
optimistic conflict behavior without turning browse/download authorization into
a global tenant write hotspot.

**Alternatives Considered**
Keeping one locking loader was rejected because it makes every GET a writer.
Removing locking from both reads and mutations was rejected because concurrent
ACL replacement could lose updates or surface dialect-specific lock errors.

**Tradeoffs**
Read and mutation paths now call different space-loading behavior, so new write
entry points must explicitly acquire their fence. Regression tests cover both
zero-write reads and concurrent ACL replacement.

**Scope**
Files permission persistence and its callers. Browser N+1 batching and moving
the global revision fence to file roots remain separate implementation work.

**Status**
Implemented and race-tested.

---

## D54 - Hierarchical file roots belong to organizations, not their creators

**Decision**
Tenant, Entity, Department, account-private, and tenant-personal roots are
ordinary directory files with an explicit `(root_scope, root_owner_id)` binding;
there is no Workspace resource or table. Tenant/Entity/Department roots belong
to the corresponding organization, never to the human who created it. The
creator is audit metadata only. Organization-wide file actions come from IAM
role permissions constrained to the matching Tenant/Entity/Department scope.
Directory and file ACLs refine that ceiling for principals, roles, groups and
departments; they cannot grant an action IAM denied. Only account-private and
tenant-personal roots follow a principal.

Tenant/Entity/Department roots are provisioned synchronously in the owning
resource creation transaction. Account-private and tenant-personal roots are
created lazily and idempotently on first real use. Every new Department belongs
to exactly one Entity; a child Department must use the same Entity as its parent.
A compatibility migration may leave legacy Department `entity_id` NULL, but
such rows fail closed until explicitly repaired.

**Reason**
Organization ownership must survive creator departure, suspension and transfer.
IAM role scope is the one existing system for whole-resource actions; duplicating
it as creator ACLs would create a second, drifting authorization model. Explicit
root ownership prevents accidental cross-scope writes and lets authorization
validate one root with bounded queries. Lazy personal roots avoid millions of
empty directory and ACL rows.

**Alternatives Considered**
Creator-owned Entity/Department roots were rejected because organizational data
must not follow a person. Tenant Administrator ACL on every descendant root was
rejected because it grants write/manage capability beyond explicitly scoped IAM
permissions. Eager personal root creation was rejected for storage and write-
amplification cost. A separate workspace/root/conflict table and a synthetic
`workspace_key` were rejected; existing file nodes remain the resource, and a
database unique constraint on `(space_id, root_scope, root_owner_id)` serializes
root identity.

**Tradeoffs**
Legacy unassigned Departments require an explicit repair/backfill before they can
own a file root. Required organization roots make resource creation dependent on
file-root provisioning availability. Cross-root writes require deterministic
root locking, and read paths must batch authorization rather than reuse mutation
locks.

**Scope**
IAM Department ownership, Tenant/Entity/Department lifecycle integration, Files
root metadata and ACL enforcement, read authorization performance, public root
metadata, and the corresponding Web file browser. Global route redesign and full
sharing implementation are not included in this phase.

**Status**
Approved by the user; implementation in progress.

---

## D1 – 审计脱敏默认表只覆盖凭据与高危标识，不覆盖联系类 PII

**Decision**
`audit.defaultSensitiveKeys` 保持现有范围（口令、令牌、密钥、授权头、卡号、CVV、
身份证、SSN 等）。邮箱、手机号、姓名、地址等联系类 PII **不**进入默认表，由部署方
按合规要求通过 `audit.sensitive_fields` 配置自行加入。

**Reason**
两类数据的审计价值截然不同。凭据被记录下来毫无用处而泄漏代价无限，全脱敏是纯收益。
联系类 PII 则常常**就是被审计变更的对象本身**（注册、改绑、客服修改联系方式、按邮箱
排查欺诈），默认抹除会削弱审计本身的取证价值。日志中的抹除不可逆：未记录的值无法
事后恢复，因此默认值偏向"记录"，把收紧留给显式配置。

注：审计事件的 `actor_id` / `client_ip` / `operation` 独立于 body，脱敏 body 不影响
"谁在何时做了什么"的追溯，只影响"改成了什么值"。

**Alternatives Considered**
1. 加入默认表 —— 简单，但如上所述削弱审计价值，且是不可逆的默认。
2. 新增掩码能力（`a***@b.com` / `138****5678`）—— 技术上最优，兼顾关联性与暴露面控制。
   **未实施**：属新功能，需先设计 `MaskedFields` 配置与各类型掩码规则，超出当前范围。
   若合规要求出现，这是推荐方向，而非改默认表。

**Tradeoffs**
审计日志会包含联系类 PII，因此审计存储（DB 表 / slog sink）必须与主数据同级的访问
控制和保留期限管理。这是 GDPR/PIPL 对审计日志的通行要求，而非"不准记录"。

**Scope**
`internal/infra/audit`。边界说明已写入 `redact.go` 的 `defaultSensitiveKeys` 文档注释，
避免后续被当作疏漏"修复"。

**Status**
已确认（用户授权代为决定）。若未来实施掩码能力，本决策需一并复审。

---

## D2 — golangci-lint 钉入 tools/go.mod

**Decision**
将 `github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8` 加入 `tools/go.mod`
的 `tool` 块，`make lint` 改用 `go tool -modfile=tools/go.mod golangci-lint run ./...`。

**Reason**
与该模块既有规范一致（buf、protoc-gen-go、protoc-gen-go-grpc 已同样钉版本）。在没有
CI 强制的前提下，版本漂移是唯一的实际风险：不同开发者本地安装不同版本会得到不同发现，
使 lint 结论不可复现。钉版本后无需本地安装即可运行。

**Alternatives Considered**
1. 不钉，依赖开发者本地安装 —— 零成本但不可复现，且与仓库既有工具管理方式不一致。
2. 钉在 CI 而非 tools/go.mod —— 当前无 CI（用户明确不做），不适用。

**Tradeoffs**
`tools/go.mod` 由 104 行增至 272 行（全部为 indirect 依赖）。该模块本就为隔离工具依赖
而独立存在，不进入运行时依赖图，因此代价可接受。已实测：钉定后运行结果与本地 v1.64.8
完全一致（0 findings）。

**Update 2026-09-28**：v1 已停止维护，升级为 v2.14.0（`.golangci.yml` 转为 v2 格式，保持同一 linter 集合：
staticcheck 限定 `SA*`/`S1*`，等同 v1 的 staticcheck+gosimple）。钉版本的方式不变。

**Scope**
`apps/backend-api/tools/go.mod`、`Makefile`、`tools/README.md`。

**Status**
已确认（用户授权代为决定）。

---

## D3 — IAM schema 使用单一迁移流，目标为空库

**Decision**
IAM 域 9 个模块的 63 张表统一由 `internal/iamschema` 迁移流拥有；不按 Go 模块拆分
schema 流。目标数据库为全新空库，不实施存量数据 ETL。

**Reason**
IAM 与 organization 之间存在双向外键，跨流无法形成可执行 DAG；单一迁移流保留
全部数据库约束且无需改变现有迁移执行器。用户已确认目标无存量迁移要求。

**Tradeoffs**
不能单独 down 某个 IAM 子模块；表归属改由 `OWNERSHIP.md` 和集合相等测试维护。

**Scope**
IAM 域全量迁移 Phase 1–9。

**Status**
已被 D10 取代。外键环只能证明需要更细的迁移执行里程碑，不能证明应剥离模块 SQL 所有权。

---

## D4 — IAM 文本比较按语义分类

**Decision**
业务名称唯一键统一存储 `strings.ToLower(strings.TrimSpace(value))` 生成的 `name_key`，
不折叠重音；枚举、协议标识符、哈希/令牌、WebAuthn handle 使用 binary collation。
菜单 `label` 不参与唯一约束，不由 collation 承担国际化。

**Reason**
源库已有四类 `name_key` 使用同一规范。MySQL 的 `utf8mb4_unicode_ci` 会把大小写不同
的协议/安全标识符视为相等，也会令 CHECK 接受大小写变体；PostgreSQL 与 SQLite 默认
行为不同。分类处理可使三方言一致，并修复 WebAuthn handle 的身份匹配风险。

**Tradeoffs**
角色名等业务名称在 PostgreSQL 上从大小写敏感改为不敏感；这是用户确认的预期语义。
动态菜单多语言仍需独立产品决策。

**Scope**
IAM schema R16 及对应写入路径。

**Status**
已确认。

---

## D5 — IAM entity type 在建立关系后不可变

**Decision**
关系表使用包含 `resource_type` 的复合外键，保持源库 `ON DELETE RESTRICT`；实体一旦
被关系引用便不可修改 type。

**Reason**
源库 `iam/entity.go:273-281` 已明确执行该业务规则。数据库复合外键不是新增限制，
而是关闭应用层“先查关系数、再更新”之间的 TOCTOU 窗口。

**Tradeoffs**
未来若产品需要修改已引用实体的 type，必须显式设计关系迁移，不能依赖隐式级联。

**Scope**
IAM schema R9'' 与实体更新服务。

**Status**
已确认。

---

## D6 — IAM 迁移只支持已确认的 Mac/Windows 两机拓扑

**Decision**
迁移环境由 `make iam-preflight` 自动解析。Mac 使用
`/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server` 与本机 Docker；Windows
使用 `D:\Projects\Chaosplus\chaosplus-api\apps\server`，无本机 Docker 时通过专用 SSH key
驱动 `root@10.0.0.100` 的 Docker。源基线固定为 `apps/server` tree
`201052baddd745d1c2727cf29bbc3c68253a1af0`；MySQL 固定为
`mysql:8.4@sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb`。

**Reason**
用户明确只在原 Mac 与当前 Windows 两台设备间轮换。单机路径、Docker Desktop 路径和
`localhost` 容器假设在换机后均失效；自动预检可在继续实现前发现源漂移、CRLF、Docker
transport 与三方言表集合问题。

**Alternatives Considered**
1. 要求两台机器手工维护同一路径与 Docker Desktop —— 容易再次产生陈旧状态。
2. 把 SSH 密码写入脚本 —— 不可接受；专用 key 只保存在机器 `~/.ssh`。
3. 支持任意第三台机器 —— 当前无需求，使用 `IAM_SOURCE_ROOT` 等覆盖项已足够应急。

**Tradeoffs**
Windows 依赖局域网主机 `10.0.0.100` 可达；Mac 的预检与完整门禁仍需回到该设备后实跑一次。
源 `apps/server` 发生任何变更时必须显式复审并更新 tree，不能静默迁入新版本。

**Scope**
IAM 迁移的本机预检、MySQL 8 门禁与换机恢复，不改变生产部署拓扑。

**Status**
已确认（用户明确限定两台设备并要求自动检测处理）。

---

## D7 — IAM 迁移不新增动态菜单多语言表

**Decision**
本次保持固定 source tree 的 63 张表，不新增 `iam_menu_translations`，`iam_menus.label`
继续保持源系统的单值语义。编译期系统消息仍使用现有 `pkg/i18n`，不把它扩展为租户动态
内容翻译系统。

**Reason**
用户要求全量迁移原有功能。源库只有 `iam_menus.label`，目标也不存在动态菜单翻译模型、
回退规则或管理 API；新增第 64 张表会引入新产品行为，不属于兼容迁移。

**Tradeoffs**
迁移后动态菜单仍只能保存一个 label。未来若产品明确需要多语言菜单，必须单独设计 locale
唯一键、默认语言、回退规则、CRUD/API 兼容与存量填充，不能混入本次迁移。

**Scope**
IAM schema 表集合、菜单 model/repository/API 与 Phase 1 baseline。

**Status**
已确认（按“迁移现有功能、不静默新增产品功能”的范围约束关闭）。

---

## D8 — IAM 目标采用 bounded context + 私有 capability 两级结构（已废弃）

**Decision**
9 个 IAM 一级模块继续作为 `pkg/module` bounded context；每个模块内部按业务能力分入
`internal/<capability>`，并把 gorm adapter、Huma adapter、跨能力 workflow 分别放入
模块私有的 `internal/store`、`internal/httpapi`、可选 `internal/workflow`。模块根只
负责生命周期、依赖注入和协议注册。跨模块协作只允许使用按提供方领域分组的
`pkg/port/{id,authz,audit,iam,authn,identity,organization}`，不直接导入对方实现。

**Reason**
源库的一级分模块清楚，但 IAM 等模块内部仍把领域、bun 仓储、REST 和装配放在同一个 Go
package。原样平移只会得到更整齐的大包，不能满足“迁入后结构更清晰”。两级结构保留业务
边界，同时通过 Go nested `internal` 和自动架构测试形成可执行约束。

**Constraints**
- capability 不依赖 gorm、Huma、`pkg/module` 或 adapter。
- repository port 由消费 capability 声明，store 负责 domain/model 转换。
- httpapi 不直接访问 gorm/query/model。
- sibling capability 不直接互相导入；必要的原子流程由 module-local workflow 和消费侧
  声明的 transaction view 协调，store 提供同一事务绑定的 repository 实现，不暴露 gorm DB。
- port DTO 不引用业务模块内部的 domain/model 类型，避免从中立端口反向泄漏实现。
- 不把 capability 注册成独立模块，不拆 schema flow，不为目录整齐制造单函数包。

**Tradeoffs**
相对源库会增加 package 数量和 domain/model 映射代码；代价换取编译期依赖方向、可单测领域
逻辑和更小的变更半径。小模块只拆有独立不变量的能力，避免过度分层。

**Scope**
Phase 2–8 的 9 个业务模块、覆盖率登记与 `tests/architecture`；不改变 63 表单一迁移流、
REST 基线或业务行为。

**Status**
已被 D9 取代。该方案只改 IAM 域为 Ports-and-Adapters，会让仓库同时存在两套模块架构；
用户明确要求若采用该架构必须全项目统一改造，而不是局部引入。

---

## D9 — IAM 迁移沿用项目原生模块，直接按 feature 子包组织

**Decision**
不在本次 IAM 迁移中引入 Ports-and-Adapters、中央 `pkg/port`/`internal/port` 或
`internal/<capability>` 嵌套层。9 个业务模块继续使用现有 `pkg/module` 生命周期、
`model/query/dbgen/i18n` 和 `ProvideServices/InjectServices` 模式；大模块直接按 feature
分包，例如 `internal/iam/{access,authorization,resourcegraph,administration}`。

每个 feature 包按需要使用 `domain.go`、`service.go`、`repository.go`、`http.go`。
跨模块契约沿用 `guid.Source`、`audit.Recorder` 模式，由 provider root 稳定导出，消费者
只导入 provider root，不导入 feature、model、query 或 concrete service。

**Reason**
当前项目已经有一致且经过测试的模块结构。只给 IAM 域增加 ports/adapters 会形成第二套
架构，增加理解和维护成本。另一方面，IAM 源模块规模远大于现有 todo，全部放根包也不够
清晰；直接 feature 子包在不改变项目架构哲学的前提下缩小 package 责任。

**Constraints**
- `internal/app` 是唯一总装配位置。
- 所有真实模块依赖必须同时写入 `ModuleDependencies()`。
- 跨模块只能导入 provider root contract。
- repository 文件负责 gorm/model/query；HTTP 文件负责 Huma；模块根只负责生命周期和汇总。
- feature 不是独立 `pkg/module`，不拥有 migration flow。
- 若未来采用 Ports-and-Adapters，必须另立全项目 ADR、迁移顺序与统一门禁。

**Tradeoffs**
IAM 域模块会比现有小模块多一层 feature package，但不会引入 adapter/port 抽象体系。
feature 间原子流程仍需模块内 workflow 明确协调，不能靠任意 sibling import。

**Scope**
Phase 0 的跨模块契约位置、Phase 2–8 目录结构、依赖门禁与覆盖率登记；不改变 63 表、
REST 基线和业务行为。

**Status**
已被 D10 取代。用户否决按 feature 再拆包，要求 9 个原模块统一归入 IAM 域目录并保持模块自治。

---

## D10 — IAM 域目录下 9 个自治模块，各自拥有 SQL

**Decision**
`internal/iam/` 只作领域命名空间，不含 Go 文件、生命周期或集中迁移。其下固定为
`core`、`authn`、`identity`、`oauth`、`organization`、`audit`、`governance`、
`provisioning`、`federation` 9 个自治 `pkg/module`；`core.ModuleName()` 仍为 `iam`。
每个模块按 `internal/todo` 使用扁平根包与 `model/query/dbgen/i18n`，拥有自己读写表的
`migrate.go`、三方言 SQL 和版本表。跨模块契约直接由 provider 根包声明，不设中央 port。

源 `modules/iam/sql` 混放的 36 表按实际职责拆为 core 13、authn 13、identity 3、oauth 4、
audit 3；organization 14、governance 4、provisioning 5、federation 4 保持原职责，总计 63。
跨模块 FK 仍由拥有子表的模块维护；基础表和 constraints 作为迁移里程碑构成 DAG，解决
iam↔organization 环，不能以环为理由集中 SQL。

**Reason**
`internal/todo` 已确立模块同时拥有生命周期、模型、查询与 migration source。集中 schema、
局部 Ports-and-Adapters 或 direct feature 分包都会制造第二套结构，削弱模块自治并偏离用户
确认的 IAM 域边界。

**Tradeoffs**
需要为现有迁移器增加可选 milestone 编排，并把源 iam SQL 按真实写入模块拆分；换取目录、
Go 包、数据模型、SQL 与版本状态的一致所有权。

**Scope**
IAM 全量迁移的目录、接口、表归属、Phase 1 迁移编排及 Phase 2–9 实现。

**Status**
已确认（用户明确要求 `internal/iam/{...}`、模块 SQL 自治并取消公共 port 包）。

---

## D11 — IAM 第三方依赖按源行为选型并即时引入

**Decision**
按固定源树的 `go.mod` 与生产 import 图保留以下完整模块路径和职责；除已落地的 WebAuthn 外，
只在对应实现任务开始时加入目标 `go.mod`：

| 类别 | 选定模块与源版本 | 引入时机 | 结论 |
|---|---|---|---|
| WebAuthn | `github.com/go-webauthn/webauthn v0.17.4` | Task 0.4（已引入） | `webauthnx` 与 passkey 的协议/凭据实现；迁移期间不升到 v0.18.0 |
| TOTP | `github.com/pquerna/otp v1.5.0` | Task 3 authn | TOTP/HOTP 唯一实现 |
| SAML | `github.com/beevik/etree v1.6.0`；`github.com/crewjam/saml v0.5.1`；`github.com/mattermost/xml-roundtrip-validator v0.1.0`；`github.com/russellhaering/goxmldsig v1.6.0` | Task 7 federation | 分别承担 XML、SAML 协议、XML 圆返攻击校验与 XML 签名，不是重复实现 |
| SCIM filter | `github.com/scim2/filter-parser/v2 v2.3.1` | Task 7 provisioning | SCIM filter/patch 语法解析唯一实现 |
| 文本验证码 | `github.com/mojocn/base64Captcha v1.3.8` | Task 3 authn | digit/string/math captcha 与嵌入字体 |
| 滑块/点选验证码 | `github.com/wenlng/go-captcha/v2 v2.0.5` | Task 3 authn | slide/click captcha；生产代码复用 `base64Captcha` 字体，不能删除前者或后者而保持源功能 |
| 审计对象存储 | `github.com/minio/minio-go/v7 v7.2.1` | Task 6 audit | WORM anchor 对象存储客户端 |
| Redis client | `github.com/redis/go-redis/v9 v9.21.0` | Task 8/9 composition 评审 | 仅由源 app 组合根创建，IAM 模块不直接依赖 |
| Redis rate limit | `github.com/go-redis/redis_rate/v10 v10.0.1` | Task 8/9 composition 评审 | 源 `ratex` 的全局 HTTP middleware；与 Redis client 是上下层关系而非替代关系 |

截至 2026-09-02 的 `go list -m ...@latest` 显示：TOTP、crewjam/saml、XML 圆返校验、SCIM、
两套验证码和 redis_rate 已是源版本；WebAuthn、etree、goxmldsig、MinIO 与 go-redis 有更新版本。
为先证明行为等价，迁移任务固定源版本；任何升级另做兼容性与安全评审，不混入模块移植。

**Reason**
Phase 0 的目标是冻结选择和引入时机，不是预装未来依赖。完整 import 图证明 SAML 四库与两套
验证码库分别覆盖不同职责；贸然删库会缩减用户要求的全量功能。Redis 只出现在源 app/ratex，
提前塞进任一 IAM 自治模块会制造错误所有权。即时引入还能避免未使用的传递依赖污染主模块。

**Alternatives Considered**
1. Phase 0 一次性加入全部依赖 —— 在实现到达前没有调用图或测试证明，增加无用依赖。
2. 只留一套验证码库 —— 会丢失文本或滑块/点选能力，并破坏现有字体复用。
3. 迁移时直接采用所有 latest —— 把依赖升级与 bun→gorm/模块迁移混在一起，无法隔离兼容回归。
4. 把 Redis 注入 authn/audit —— 源 IAM 模块没有该依赖，所有权属于组合根与全局 middleware。

**Tradeoffs**
源版本可能不是最新版本；先保持行为等价，后续升级需要单独门禁。Redis 是否进入最终目标仍须
在 Task 8/9 根据全局 rate-limit 配置兼容范围决定，但不得据此缩减 IAM 模块功能。

**Scope**
Task 0.5 的依赖冻结，以及 Task 3/6/7/8/9 的依赖引入顺序。

**Status**
已确认；依赖路径、源版本、直接 import 与引入任务均已复核。

---

## D12 — HTTP 文件命名与 RESTful 目标契约

**Decision**
IAM 各模块的 HTTP 传输文件和注册入口统一使用 `http.go` / `RegisterHTTP`；不使用
`rest.go` / `RegisterREST`。RESTful 作为接口设计标准：优先复数资源、名词化路径、HTTP method
表达 CRUD、统一路径参数和状态码。源服务 208 条 operation 只作为功能完整性基线，每个模块通过
显式 source→target 映射冻结目标 OpenAPI 契约，不要求复制源 RPC 风格 URL。

源 IAM 没有 gRPC proto/service 契约，本次迁移不创建占位 `grpc.go` 或虚构 gRPC API。未来只有在
出现真实内部调用契约时，才以 `grpc.go` / `RegisterGRPC` 与 HTTP 并列实现。

**Reason**
`http` 准确描述传输协议，也能与 `grpc` 对称；RESTful 则属于 HTTP API 的资源建模规范，二者不是
互斥命名。逐条映射同时保留全量迁移的可追溯性，并允许目标接口纠正源服务的动作式路径。

**Tradeoffs**
目标 URL、method 和 OperationID 可能与未发布的源接口不同，因此不能再用源三元组直接做集合相等；
必须维护 source→target 映射及目标契约测试。当前新项目 authn 尚未发布，不保留旧路径别名。

**Scope**
IAM Phase 2–9 的 HTTP 文件、OpenAPI 契约和验收门禁；不新增 IAM gRPC 功能。

**Status**
已确认（用户明确要求 HTTP 命名并尽可能按 RESTful 标准设计）。

---

## D13 — IAM 性能基线分离可移植计划门禁与同机时延门禁

**Decision**
四条 IAM 热路径使用生产组合和代表性多租户基数捕获实际 GORM SQL，对 PostgreSQL 16 与真 MySQL 8.4
保存 JSON `EXPLAIN`、规范化 SQL fingerprint 和 5 份 Go benchmark 样本。默认门禁始终验证 SQL 集合、
fingerprint 与计划结构，拒绝缺少 tenant/directory/全局唯一键约束的全表扫描，并对授权、session 与 SCIM
关键已填充表强制索引访问。绝对时延阈值取同机 5 份样本最大值的 3 倍并向上取整，只在
`IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1` 时执行；基线更新必须显式设置 `IAM_PERFORMANCE_UPDATE=1`。

**Reason**
SQL 与访问计划回归可跨开发机稳定判断；绝对时延会受 CPU、虚拟化和远程 Docker 网络影响，尤其当前
MySQL 8.4 经 SSH Docker transport 暴露 TCP。把结构正确性设为默认硬门禁、把时延限定为同机显式门禁，
既持续捕获 tenant 条件或关键索引丢失，又避免把机器差异误报为代码回归。

**Tradeoffs**
跨机器默认运行不会因纯时延退化失败，发布机需要显式启用时延门禁。原始 plan 会随数据库版本变化，
引擎升级时必须复审并重新生成对应基线，不能仅接受 artifact diff。

**Scope**
Phase 9 的 authorization、browser session、SCIM bulk 25 和 audit append 性能验收；不改变生产查询、
索引或运行时配置。

**Status**
已确认并由 PostgreSQL 16.9/MySQL 8.4.11 双引擎基线验证。

**Amendment 2026-09-15 —— 时延门禁真正接上了发布路径**
本决策写明「发布机需要显式启用时延门禁」,但 `scripts/test-mysql8.sh` 从未设置过那个变量,
于是 `make test-mysql8-run`(= `release-check` 的一环)长期只校验 SQL 集合与计划指纹,
**时延一直是量而不判**。现已在该脚本的性能步骤加上 `IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1`。
作用域仅此一条命令(前缀赋值不 export),`make ci` 里 `tests/performance` 的 PostgreSQL 那条
继承 `os.Environ()`,拿不到它 —— 与本决策「跨机器默认运行不因纯时延失败」一致,刻意不动。

**验证**:开启后真实 MySQL 8.4 跑 PASS;另做变异验证 —— 把 `scim_bulk_25` 阈值降到 60ms 后
门禁如期 FAIL(`exceeded the same-machine frozen threshold`,5 个样本逐条报),基线随即还原。
**门禁确认是活的,不是空开关。**

**随之暴露的约束(未处置,后续动基线时必须知道)**:本决策设计的「最大值 3 倍」余量,
现已被真实变化吃掉一部分 —— 当前余量 authorization 3.53x / audit_append 3.47x /
session_lookup 2.83x / **scim_bulk_25 仅 2.04x**。且冻结样本本身取自 2026-09-03 的机器状态:
把基线提交 `3277e5e` 的**同一份旧代码**在今天同机同镜像重跑,`scim_bulk_25` 是 58.56ms 而非
冻结的 50.77ms。即这份余量同时在吸收「机器差异」与「累积的真实变化」两件事,
scim 那条已接近设计意图的下沿。重测基线等于抬高门槛,属独立决策,未做。

---

## D14 — SQLite 默认事务使用 `BEGIN IMMEDIATE`

**Decision**
`normalizeSQLiteDSN` 在调用者未显式设置 `_txlock` 时注入 `_txlock=immediate`。调用者的显式配置保持
不变；busy timeout 继续使用 `mattn/go-sqlite3` 的 5 秒默认值或调用者提供的值。PostgreSQL 与 MySQL
配置不受影响。

**Reason**
Mac 最终 CI 在 SCIM ETag 更新和认证 session rotation 两条并发 CAS 路径上稳定暴露
`SQLITE_BUSY_SNAPSHOT`。deferred transaction 先读后升级为写事务时，即使配置 busy timeout，也不能在
另一个 writer 已提交后继续使用旧 snapshot。`BEGIN IMMEDIATE` 在事务开始时取得写入资格，使 SQLite
的单 writer 约束在业务读取前确定，失败方随后能观察新版本并走既有 CAS 冲突语义。

**Alternatives Considered**
1. 只给 provisioning 增加事务重试 —— authn 随后出现同一问题，且会在各模块复制驱动错误处理。
2. 只修改并发测试 DSN —— 会掩盖默认生产配置的相同行为。
3. 强制覆盖调用者显式 `_txlock` —— 不必要地取消 SQLite 驱动已有的可配置能力。

**Tradeoffs**
SQLite 写事务会更早串行化，长事务可能让后续 writer 更早等待；SQLite 本身同一时刻只允许一个 writer，
因此这改变的是锁获取时机而不是写并发上限。显式选择其他事务模式的部署需自行处理相应 contention。

**Scope**
共享 GORM SQLite datasource 配置，以及依赖其事务语义的 IAM 并发 CAS 路径。

**Status**
已确认；datasource 配置测试、SCIM ETag 与 session rotation 各 20 轮并发回归、相关全包 `-race`、
完整 `make ci`、真实 MySQL 8.4 与 live health 均通过。

## D15 — 前端 admin 采用 bun + turborepo workspace，复用 chaosplus `packages/ui`

**Decision**
`apps/frontend-web-admin` 建为 bun 1.3 + turborepo 2 的 workspace（`apps/*` + `packages/*`），而不是
单应用。首个 package 是 `@workspace/ui`，直接取自
`chaosplus-api/apps/admin/packages/ui`，保持包名 `@workspace/ui` 与其 `exports` 映射不变。
`packages/ui` 是**唯一**共享组件库，后续 IAM 应用与商城应用共用，不允许各自 fork 一份。
构建/校验命令统一为 turbo 的 `build` / `lint` / `format` / `typecheck` / `test` 五条。

**Reason**
`chaosplus-api/apps/admin/packages/ui` 与 `aspeed-framework/apps/7link-mall-web/packages/ui` 实测为
同一套组件库：各 145 个源文件、42 个组件，文件名 100% 重合；内容差异在归一化引号、分号和
`prettier-plugin-tailwindcss` 的 class 排序后消失，即只有格式差异。两侧业务前端已按
`apps/*` + `packages/*` 组织（旧 `apps/iam` 的 `vite.config.ts` 注释显示它本身就是从商城前端
`apps/web` 抽离出来的）。沿用同一形状可让 IAM 24 个页面与商城 124 个页面迁入时零结构改造，
并把组件库收敛为一份。

**Alternatives Considered**
1. 单应用（非 workspace）—— 商城 124 个页面迟早要接入，届时必须重做结构。
2. 取商城那份 `packages/ui` 作基线 —— 与迁移源 `apps/iam` 的格式风格不一致，会给 24 个页面的
   迁移额外制造 diff 噪声。两份实质相同，取迁移源那份摩擦更小。
3. 重命名 `@workspace/ui` 为 `@7link/ui` —— 需改动 145 个文件的 import，收益仅为命名偏好。
4. pnpm workspace —— 迁移源与商城均已用 bun，且本机 bun 1.3.12 与源 `packageManager` 声明一致。

**Tradeoffs**
沿用 `@workspace/ui` 这一通用包名，在未来若引入第二个组件库时语义偏泛；但改名的代价是全量
import 改写，当前无第二个组件库的需求。turborepo 增加一层任务编排，对单 package 是额外开销，
在第二个 app 迁入后才体现价值 —— 这是有意的前置投入，因为 workspace 结构本身已按 D15 确定。

**Scope**
`apps/frontend-web-admin/**`。不触及 `apps/backend-api`，不修改 `chaosplus-api`（只读迁移源）。

**Status**
已确认（用户 2026-09-04 授权"按推荐的最优方案"）。Phase A 已实测：`bun install` 1221 包，
`bun run typecheck`、`bun run lint` 通过，`bun run test` 85 pass / 0 fail。
`bun run build` 当前无 task（`@workspace/ui` 是源码包，无 build 脚本），待首个 app 迁入后生效。

## D16 — 前端 API 客户端按新 RESTful 契约重建，不平移旧 `iam-api.ts`

**Decision**
`apps/backend-api` 的运行中 OpenAPI 是前端 API 契约的唯一事实来源。迁移旧
`apps/admin/apps/iam/src/lib/iam-api.ts`（1711 行）时，必须先冻结"旧调用点 → 新 operation"
的逐条映射表，再实现；`/authn/*` 全部重新绑定。类型层由 OpenAPI 生成，业务调用走手写薄封装。

**Reason**
D12 已把 HTTP 目标契约改为 RESTful，源 RPC 风格 path 不再保留。实测比对旧前端调用的路由与
新后端非测试代码注册的路由：`/iam/*`、`/oauth/*` 全部命中，`/authn/*` **18 条全部落空**
（`/authn/login` → `POST /authn/sessions`、`/authn/session` → `GET /authn/sessions/current`、
passkey/邮箱验证/密码找回全部改名）。新后端另有旧前端没有的 `/authn/captchas`、
`/authn/step-up-challenges`、`/authn/step-up-verifications`。平移旧客户端必然全面 404。

**Alternatives Considered**
1. 全手写客户端 —— 与 D12 的契约漂移同源风险再次出现，且 208 条 operation 的类型需人工维护。
2. 全自动 codegen 直接给页面调用 —— 生成的调用风格与旧 24 个页面的既有用法差距大，
   迁移期 diff 噪声过高。
3. 在后端加旧路径兼容层 —— 与 D12 的 RESTful 目标契约直接冲突，且为已废弃的客户端形态续命。

**Tradeoffs**
codegen 引入一个生成步骤和生成物指纹门禁的维护成本，换取契约漂移在 CI 阶段即被发现。
薄封装层需要人工保持与生成类型同步。

**Scope**
`apps/frontend-web-admin/apps/iam` 的 API 客户端层（Phase B）。不改后端契约。

**Status**
已确认方向（用户 2026-09-04 授权）。

**2026-09-04 更新（Phase B 完成，映射表已冻结）**
后端已把「旧 operation → 新 operation」冻结在 `apps/backend-api/internal/app/testdata/iam_http_mappings.txt`
（211 条），并由 `bootstrap_iam_openapi_test.go` 的 `iamHTTPMappingCount = 211` 守住。
据此逐条 join 后：旧 `iam-api.ts` 共 144 个调用点，**只有 20 条契约有实质变化**
（`/authn/*` 18 条 + `/iam/principals/{id}/disable|restore` 2 条），其余 124 条原样可用。
完整表见 `apps/frontend-web-admin/docs/frontend-migration-plan.md` 第八节。

本条原文中「`/authn/session` → `GET /authn/me`」是当时的推断，**是错的**，已就地改正为
`GET /authn/sessions/current`；`authn-me` 是另一个独立 operation，两者并存。
**2026-09-04 再更正（@Claude 指出，已用 git 复核）**
上一段里「211 是 target 侧、208 是 source 侧」是错的。208 和 211 是**同一张表的两个版本**：
`git show HEAD:...iam_http_mappings.txt` 为 208 条、`iamHTTPMappingCount = 208`；
工作区多出 3 条 `oauth-consent-page|request|decision`（@Claude 当日新增的 OAuth 同意页），
故为 211。**该表处于活跃修改中，引用时必须注明版本。**

这 3 条不影响 Phase B 结论：它们是新增接口，旧前端不可能调用过，落在「旧前端未使用」那一桶里。
因此该桶按 HEAD 计为 **67** 条、按当前工作区计为 70 条；144 个调用点的映射一条都不受影响。

---

## D17 — Phase 10 的五个产品缺口按主流 IAM 通行做法定，不再逐条等产品确认

**Date** 2026-09-05

**Decision**
10.3 / 10.4 / 10.5 / 10.8 / 10.10 原先在计划里标记为「需产品确认」，长期空转。用户明确
指示按 IAM 领域的标准、通用、共识做法自行决定（"按标准的 通用的 大家共识的做法"），
下述五条即为所选方案，实施时逐条对照规范与参考实现核对，不凭印象写。

- **10.3 principal 管理权限**：按 Keycloak 的 realm 模型，改人属于 realm 管理员的
  租户内能力。**读代码后更正了本条最初的写法**：涉及的是三条而不是六条 operation
  （`identity-update-principal`、suspend、resume），而且方向相反——它们本就是租户内
  操作（路由带 tenantID，service 方法也带 tenantID），却错挂了 `platform_administer`。
  挪去 platform 平面等于删掉「租户管理员管自己租户的人」，正是要保住的能力。
  实际修法：三条改用权限表里现成的 `user/update`（scope=tenant），与同文件的
  list/create/get 用 `user/view`、`user/create` 一致。结构性的一半照做且更必要：
  让 `authz.Register` 拒绝把 platform scope 的 action 注册到 tenant registrar，
  与 `RegisterPlatform` 早已有的反向检查配对——缺的正是这一半，这个 bug 才漏出去。
- **10.4 数据权限**：范围过滤下沉到 repository 查询层（放在 controller 等于可绕过）。
  以 `internal/todo` 为参照实现接 `DataConstraint` + `RegisterEntity`（用户 2026-09-05
  确认用 todo 模块当用例），覆盖 tenant A/B、owner/部门/上级/显式 deny、分页 count、导出。
- **10.5 字段权限**：对标 Keycloak 声明式 User Profile 的每字段 `view`/`edit` 角色授权，
  与 SCIM 2.0 的 `mutability`/`returned` 属性特征。声明式字段策略、读写分离、deny 优先；
  读端在序列化投影强制，写端在校验阶段拒绝而非静默丢弃。前端只做展示，不做判定。
- **10.8 principal 生命周期**：SCIM + GDPR 的通行组合——停用（`active=false`）→ 匿名化
  PII → 保留主体行作墓碑。审计永不硬删，外键与审计链因此始终成立。
- **10.10 IAM gRPC**：**不做，关闭该项。** 主流 IAM 产品面是 REST + OIDC/OAuth2/SCIM，
  项目已具备。

**Reason**
这五项被标为「待产品确认」已阻塞多轮，而它们在 IAM 领域都有成熟且收敛的答案，不属于
本项目特有的产品分歧。继续等待是把行业已解决的问题当成本项目的开放问题。

**Alternatives Considered**
1. 继续逐条等确认 —— 已实测无效，且把工期消耗在没有实质分歧的地方。
2. 各自另设一套自创模型 —— 违反用户「要标准、要通用」的明确要求，且增加长期维护成本。
3. 10.10 建占位 gRPC service —— 无消费方、surface 未冻结，等于维护两份 API。

**Tradeoffs**
自行决定意味着若与用户预期不符，返工成本落在实现之后而非之前。为此每条都在频道公示并
说明所依据的标准，用户可随时否决其中任意一条；未否决即按此执行。

**Scope**
`apps/backend-api` Phase 10。10.10 关闭后，10.11 最终门禁的范围相应缩小。

**Status**
已决定并在 #sevenlink-platform-win 公示（2026-09-05）。10.4 已获用户明确确认。

---

## D17 implementation refinement - profile claims and SCIM PATCH

**Date** 2026-09-05

**Decision / Reason**
Tenant-controlled profile fields are projected only after a response tenant is
resolved. Global, unscoped sessions and principal tokens omit these attributes;
they do not guess a tenant or bypass tenant policy. Raw internal authentication
and identity provisioning retain the actual values. Email verification follows
email visibility, so hidden or masked email cannot expose a verification flag.
OIDC profile/email scopes further restrict token and userinfo output; openid
alone authorizes the subject identifier, not these optional profile attributes.
ID token issuance receives the consented scope explicitly. Bearer profile views
re-read the current principal rather than treating an old token's masked values
as current source data. Existing signed JWT payloads remain immutable.

SCIM PATCH must use a transactional raw snapshot and retain its version even
without a client If-Match. The public projected GetUser result is never an
input for persistence. PUT and PATCH share the same versioned write operation.

**Alternatives / Tradeoffs / Status**
Returning all global profile data would bypass tenant policies; filtering only
at token issuance would miss later policy revocations in userinfo. Making every
response field required would misrepresent omission. Optional public DTOs require
OpenAPI and frontend updates. Profile catalog registration, SCIM enforcement and
the generated contracts are now integrated; real composed tests cover bound
actors, role policy changes, raw PATCH preservation and outbound projection.
Profile browser acceptance now covers ordinary tenant administrators on desktop
and mobile, masking/omission, partial writes and tenant reset. Full legacy browser
regressions and final production acceptance remain open; this refinement does
not mark 10.5 or production delivery complete.

## D19 - SCIM automation uses explicit tenant-owned service accounts

**Date** 2026-09-05

**Decision**
As a prerequisite for D17 field policies, SCIM directories and outbound targets
bind an existing service account using optional `service_account_id`. Identity
owns the validator capability; provisioning passes its active transaction. The
account must belong to that tenant and have an active principal, membership and
owner tenant, no pending activation, no deletion and no expired lifetime.
No borrowed administrator identity, synthetic principal or automatic role grants.

**Reason**
Core field-policy evaluation requires a real principal with effective tenant
roles. Directory credentials and target configuration previously provided only
tenant/resource scope, not an automation principal. Reusing service accounts
keeps identity ownership and revocation semantics in their existing module.

**Alternatives Considered**
Borrow the invoking administrator (incorrect for background synchronization),
invent actor identifiers (no membership/role lifecycle), or automatically create
and authorize accounts (unrequested privilege grants).

**Tradeoffs / Scope / Status**
Additive provisioning v4 references identity.base. Binding changes use existing
optimistic versions and audit transactions; PUT null/omission clears the binding.
An invalid old binding does not prevent disabling or repairing a configuration.
Inbound audit records the bound actor; outbound records the initiating human,
when present, plus the actor ID, and background work records the bound actor.
NULL is allowed for configuration repair, but no longer permits automation:
inbound SCIM rejects it with 401 and outbound operations with 409 before remote
calls. Deployments must explicitly bind service accounts and configure their
roles before upgrading. Profile enforcement uses the bound actor and the default
catalog now includes those fields. This does not complete product acceptance.
Structural down drops bindings and
is not an operational rollback for an actor-dependent production deployment.

## D21 - Principal lifecycle implementation boundary

**Date** 2026-09-06

**Decision / Reason**
D17's irreversible erasure applies to a global principal, not one membership.
Its management surface therefore requires platform administration; existing
tenant profile and suspension permissions are unchanged. Require prior suspension,
an expired retention deadline and no legal hold. Do not invent a universal legal
retention period: an explicit platform retention policy records the deadline.
Use optimistic lifecycle versions and a principal row lock for lifecycle writes.
Identity owns the lifecycle record and permanent tombstone protection; operational
PII owners must clean their records in the same caller-owned transaction. Missing
cleanup capability fails closed. Historical audit records are not deleted or edited.

**Alternatives / Tradeoffs / Scope / Status**
Tenant-local global erasure would cross tenant authority. Clearing only the main
profile would leave copied PII and credentials. Automatic timed erasure without
an explicit operator action is not required here. This is implementation in
progress, not acceptance: cross-owner cleanup, concurrent-write prevention, UI,
multi-engine verification and final release gates must precede completion.

## D20 - Durable audit archive execution

**Date** 2026-09-06

**Decision**
The audit module owns archive jobs, SQL/model/query code and Start/Stop worker.
There is no reusable application job scheduler in the current repository; use
the established module lifecycle rather than add a generic scheduling framework.
Reuse the existing NDJSON export protocol and S3 object-lock client. Archives use
the configured anchor bucket under a separate prefix; enabling archives does not
implicitly enable anchoring. Each job freezes a contiguous sequence segment,
verified chain head, generation timestamp and minimum-retention deadline.

Jobs persist queued/running/failed/complete state. Shared database time governs
leases and retry eligibility; a unique lease token fences completion. Source
events are never deleted, consistent with D17. Only a full readback of the exact
S3 version, matching whole-file SHA-256/size and COMPLIANCE retention, permits
success. A retry reuses an existing verified artifact; a conflicting artifact
fails closed. No plaintext export temporary files are created.

**Reason / Tradeoffs**
This gives restart and multi-replica recovery without another service dependency.
Two streamed export passes determine the expected digest before upload, trading
database reads for bounded memory and avoiding audit PII on temporary disks.
The audit chain and job records remain in database backups; object versions must
be retained in bucket backups. Structural v3 rollback loses job references and
cursors and is not an operational rollback for an active deployment.

**Scope / Status**
10.7 execution and management API/UI implemented. Tenant management reads use
audit_event/view, requests/retries/downloads use audit_event/export; existing
retention uses audit_event/manage. Mirrored platform routes and platform retention
use RegisterPlatform with platform/administer, reject nonempty tenant headers,
and always scope to zero. The worker now includes platform events. Completed job
verification binds the object key to its tenant/job and enforces the frozen
retention minimum; downloads provide the verified version and content length.
Multi-pool PostgreSQL/SQLite and real MySQL queue/lease recovery now pass. A real
multipart race exposed minio-go v7.2.1 dropping If-None-Match at completion;
archive uploads now use SDK Core multipart APIs with explicit conditional
completion, streamed parts and producer validation before publication. Failure
cleanup uses a bounded uncanceled context. Real tests cover one-version replica
convergence, cancellation after a committed part, failed/changed source, lost
completion responses and 1,003 events across three chained objects. Operators
must configure incomplete-multipart lifecycle cleanup for abrupt process loss;
completed archive versions must not expire. Full real browser acceptance now
passes: an exclusive temporary SQLite file is seeded through the actual audit
service before app bootstrap; the composed app, worker and MinIO drive browser
retry, manual queueing, exact-version verification/download and access negatives.
Desktop/mobile screenshots were inspected.10.7 is closed; final combined release
gates and user acceptance remain after10.8. Do not interpret age-based archive_ready_events as
unarchived events, or this decision as completed 10.7/production acceptance.

---

## D18 — 控制台的租户来自会话，不存在编译期兜底租户

**Backend refinement (2026-09-05)**
The original frontend change assumed a tenant-bearing cookie session, but authn
sessions are global principal sessions. Both `/authn/me` and
`/authn/sessions/current` now resolve a response tenant through core's
`MemberTenantResolver`, which reads active memberships and active tenants from
the writer. An explicit `X-Tenant-Id` must be a current membership (403 otherwise).
Without a choice, exactly one active membership supplies `organization_id`;
zero or multiple memberships leave it absent. Never select the first tenant.
This is a request-scoped projection, not a persisted login-session mutation;
another browser tab is unaffected. A bearer token retains its signed tenant and
rejects a conflicting header. Bad IDs return 422 and an unavailable resolver/store
returns 503, not a guessed tenant. No tenant or role is automatically granted.
The console intentionally fetches its session without a tenant header, adopts
only into an empty selection, and keeps explicit user/deployment choices.
New ordinary-member browser acceptance starts with empty browser storage and
no injected default tenant; it covers desktop/mobile adoption, ambiguous
membership, preserved selection and immediate revocation. This closes D18's
original browser gap, not the still-open 10.5 profile projection requirement.

**Decision**
`getTenant()` 删除硬编的 `"platform"` 兜底，改为三段来源：操作员在控制台里选的（存
localStorage）→ 部署配置的 `VITE_DEFAULT_TENANT` → 空。登录成功后
`adoptSessionTenant(session)` 从 `session.organization_id` 采纳一次，**只填空值**，
操作员已经选过的不覆盖。租户为空时 `X-Tenant-Id` **整个头不发**，而不是发空串。

**Reason**
一次构建产物会部署到任意多个部署实例，构建期不可能知道某个部署有哪些租户——任何编译
进去的租户 slug 都是猜测，且是会静默生效的猜测：请求带着一个不存在或不该访问的租户
发出去，报错发生在服务端，读起来像权限问题而不是配置问题。会话本身携带了权威答案
（`organization_id`），从会话取是 Keycloak/Okta 一类控制台的通行做法。

发空串与不发头是两件不同的事：`X-Tenant-Id: ` 在服务端读起来是「租户名叫空字符串」，
是一个真实的选择；不发头才是「本次请求没有租户维度」。前者会把配置缺失伪装成数据不存在。

**Alternatives Considered**
1. 保留 `"platform"` 兜底 —— 违反 CLAUDE.md §4「不硬编品牌/部署相关字符串」，且如上
   会把配置缺失变成难以归因的运行期错误。
2. 未解析出租户时阻塞所有请求 —— 过严：登录、注册、会话建立这些请求本来就没有租户维度。
3. 每次登录都用会话租户覆盖本地选择 —— 会把操作员刚切过去的租户在下次刷新时弹回来。

**Tradeoffs**
`X-Tenant-Id` 里带的是 **id** 不是 slug（`tenants/page.tsx` 存的是 `tenant.id`），所以
能对上的是 `session.organization_id`，老的 `"platform"` slug 本来就对不上——这条同时修掉
了一个一直没被触发的错值。采纳路径有类型与单测覆盖（含「未解析出租户时不发头」一例），
但没有浏览器审计覆盖：13 个审计都显式传租户。留作已知缺口。

**Scope**
`apps/frontend-web-admin/apps/iam`：`src/lib/iam-api.ts`、`src/components/auth-provider.tsx`、
`src/lib/iam-api.test.ts`。提交 `022e5b7`。

**Status**
已决定并执行（2026-09-05）。依据用户当日指示「按标准的、通用的、大家共识的做法」自行
判定，未逐条等确认；已在 #sevenlink-platform-win 说明，用户可否决。

## D22 — 本轮交付范围锁定为 P0 + 门禁转绿，分四批推进

**Decision**
以 `apps/backend-api/docs/iam-production-readiness-audit-2026-09-07.md` 为权威清单，本轮
只关 P0-1..P0-5 并让发布门禁转绿，**不含 P1/P2**。执行分四批，批内并行、批间由主 agent
集成并跑门禁：批 0 修红线基线（gofmt、tests 子模块 lint、integration migration）；
批 1 关 P0 安全与生命周期（SSRF policy、SCIM 删除责任链、lifecycle API→OpenAPI→前端类型
→UI、七 owner 填充数据与并发验收）；批 2 补覆盖率行为测试；批 3 串行跑全量门禁、冻结
195 个迁移的 schema.lock、真实 MySQL/race/generate、UAT 环境与恢复演练。

**Reason**
当前门禁基线为红（gofmt、覆盖率 89.9%、integration 六种引擎组合全失败并 10 分钟超时）。
在红基线上累积 12-20 工程日的改动再统一验证，失败原因会互相纠缠，定位成本超过并行收益。
且存在硬顺序依赖：lifecycle API → OpenAPI 重新生成 → 前端类型 → UI → 浏览器验收；
所有迁移定稿 → 三引擎 upgrade/down/up 与真实 MySQL 通过 → 才能冻结 schema.lock，
冻早需重冻。`docs/openapi.json`、前端生成类型、`schema.lock`、`internal/iam/*/sql/`、
`tests/migration/*` 是多任务共同写入点，无序并行的合并成本高于串行。

**Alternatives Considered**
1. 一次性全部并行、最后统一测试（用户初始设想）—— 因上述共同写入点冲突与红基线纠缠而否决，
   已向用户说明并获认可。
2. 全部主 agent 串行 —— 最稳但最慢，未采用。
3. 范围扩到 P0+P1（20-30 工程日）或 P0+P1+P2 —— 用户明确选择 P0-only。

**Tradeoffs**
P1 的分布式登录限流、captcha 高可用、OIDC `prompt=none`/`azp` 协议合规、最小权限 UX、
移动端与 a11y **本轮不修**，属已知缺口。其中 OIDC 协议合规若对外声称标准 OIDC Provider
则须在 conformance 前补上；登录限流缺失意味着上线前必须由 ingress/网关承担，否则存在
用户名喷洒与恶意锁定风险。这两条须在交付说明中显式列为遗留项，不得默认已解决。

**Scope**
全 IAM 分支。审计报告为权威清单，`MEMORY.md` 记录批次进度。

**Status**
已决定（2026-09-07，用户确认）。批 0 进行中。

## D23 — SCIM target 删除默认 fail closed，另提供显式 deprovision 操作

**Decision**
target 存在 live remote mapping 时**禁止删除**（fail closed）；同时提供一个显式的
「删除并 deprovision」操作：持久化 deprovision 任务，在远端账号删除成功、或形成受治理的
例外记录之前，不销毁 bearer 凭据、mapping 与审计证据。两条路径都不得让 principal 擦除的
远端映射前置检查被静默绕过。

**Reason**
现状是硬删 target、数据库级联删除 mapping，而 `internal/iam/provisioning/lifecycle.go:26`
的擦除前置检查只看仍存在的 live mapping。删掉 target 后 bearer token 与 remote ID 一起消失，
远端账号和 PII 仍然存在，本地匿名化却会放行——这是一条可绕过的擦除责任链，属 P0。
只做禁止删除能立刻堵住绕过但把清理负担全部推给操作员；只做 deprovision job 则在任务失败、
远端不可达时仍需要一个 fail-closed 兜底。两者互补，缺一都留口子。

**Alternatives Considered**
1. 仅「有 live mapping 即禁止删除」—— 改动最小（1 天内），但大目录逐个手工 deprovision
   不可运营。
2. 仅「持久化 deprovision job」（3-5 天）—— 任务失败/远端不可达时若无 fail-closed 兜底，
   绕过口子仍在。
3. 保持现状并仅在 UI 提示「远端资源不会被删除」—— 提示不构成技术强制，已否决。

**Tradeoffs**
成本约等于方案 2 加半天。deprovision job 需要任务持久化、重试与例外治理面，是本轮新增的
运维语义；例外记录必须可审计，否则等于把绕过合法化。

**Scope**
`internal/iam/provisioning/`（target.go、service.go、repository.go、lifecycle.go、
sql/ 迁移）、`apps/frontend-web-admin/apps/iam/src/app/scim-directories/targets.tsx`。
需补 live mapping + 擦除绕过的负向测试。

**Status**
已决定（2026-09-07，用户确认）。属批 1，未开始。

## D24 — 允许引入 Redis 承载共享限流与 captcha，但属 P1，本轮不实施

**Decision**
用户批准为登录限流与 captcha 高可用引入 Redis。但这两项属 P1，不在 D22 锁定的本轮范围内，
因此**本轮不实施**，决策先行记录，待 P1 迭代直接采用。

**Reason**
仓库现无 Redis、无缓存层、无限流设施。captcha 当前是进程内状态
（`internal/iam/authn/captcha.go:19` 的 `DefaultMemStore`、`gocaptcha.go:81` 的包级 map），
多副本下 issue 与 verify 落到不同副本会随机失败；登录失败只更新账号锁定状态，无 IP/设备/
全局限流。共享存储是正解，用户已批准该依赖，无需再次确认。

**Alternatives Considered**
1. 照 `internal/infra/dlock` 的「数据库行 + owner token + TTL 租约」模式做 DB 支撑的共享
   限流与 captcha one-shot store —— 零新依赖、三引擎通用，但登录路径上的高频写会打到
   主库。用户选择了 Redis，本条作为备选保留。
2. 记为部署前置约束（网关限流 + sticky session）—— 把正确性押在部署配置上，未采用。

**Tradeoffs**
Redis 让生产部署多一个必须高可用的有状态组件，运维成本上升；须同时定义 Redis 不可用时的
降级语义（限流 fail open 还是 fail closed、captcha 是否阻断注册），否则会引入新的可用性
单点。这个降级语义在实施 P1 前仍需确认。

**Scope**
`internal/iam/authn/`（captcha.go、gocaptcha.go、web.go、http.go），新增共享存储设施。
本轮零改动。

**Status**
已实施（2026-09-10）。共享限流与 captcha 存储已交付：见 `apps/backend-api/docs/iam-redis-security.md`
与 `make test-redis-security`；`/authn/*` 与 `/oauth/{token,revoke,introspect}` 分别计自己的预算（D36 之后）。

## D25 — 模块的独立回滚只在自身 milestone 依赖闭包内有定义

**Decision**
一个模块的 down 迁移**允许**引用它已声明依赖的模块所拥有的表。因此「独立回滚」的契约是：
把模块回滚到某个 milestone 以下，要求该 milestone 声明的依赖当时在场。core 回滚到
`principal-erasure`(v13) 以下需要 identity 的 `lifecycle`(v4) 在场，这正是
`internal/iam/core/migrate.go:25` 已经声明的内容。生产回滚走
`app.ExecuteModuleMigrations` 的协调 reverse-DAG 路径，本就遵守该图。
`tests/migration` 中直接调用 `goosex.MigrateDownTo` 的 fixture 必须按依赖闭包组合模块，
不得只迁移单个模块的 source 就跨过带依赖的 milestone 回滚。

**Reason**
`core/sql/*/00013_principal_erasure_guards.sql` 的 down 段首句
`UPDATE iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL`
不是笔误，是**探针**：identity 的 `trg_iam_lifecycle_tombstone_update` 在
`OLD.anonymized_at IS NOT NULL` 时 RAISE(ABORT)，于是只要存在任何已提交的不可逆匿名化，
这条 no-op 更新就会中止回滚；没有则匹配零行放行。它实现的是「擦除已发生后不允许再摘掉
保护」这一安全属性，属 P0 关注范围。identity 自己的 `00004_tombstone_guards.sql:41` down
段用的是完全相同的写法，是项目既有约定而非孤例。

`TestIAMCoreMigrationsAcrossEngines` 在六种 engine×prefix 组合下全部失败，根因是
`tests/migration/iam_core_test.go:43` 只取 `(&iamcore.Module{}).MigrationSource()` 单模块
迁移，随后 `verifyCoreFieldPolicyContracts` 调 `MigrateDownTo(..., 11)` 跨过 v13，
此时 `iam_principal_lifecycles` 从未被建出。这是 fixture 与已声明依赖不一致，不是实现缺陷。

**Alternatives Considered**
1. 让 core 的 down 守卫容忍 lifecycles 表缺失（条件化/忽略错误）—— 会把一个 P0 安全守卫
   改成在表不存在时静默放行，等于用削弱实现去迁就测试，CLAUDE.md §10 明确禁止。
2. 禁止任何跨模块 down 引用，把守卫改到 identity 单侧 —— identity 无法知道 core 何时摘掉
   自己的触发器，保护会出现时间窗；且要推翻 `migrate.go` 里已声明并被协调路径使用的依赖图。
3. 保持 fixture 单模块、仅回滚到 v13 而不到 v11 —— 会丢掉 field policy 表 drop/recreate
   的既有断言覆盖。

**Tradeoffs**
测试 fixture 变重：涉及 v13 以下回滚的用例必须先迁 identity。代价是每个组合多迁一个模块的
迁移，换取安全守卫不被削弱。另需明确：`goosex.MigrateDownTo` 是低层原语，不做依赖检查；
依赖检查在 `app.ExecuteModuleMigrations`。这个分层差异须在 fixture 注释中写清，否则下一个
人会重犯同样的错。

**Scope**
`tests/migration/iam_core_test.go`。`internal/iam/core/sql/` 与
`internal/iam/identity/sql/` **不改**。

**Status**
已决定（2026-09-07）。属批 0，待子 agent 的超时排查返回后与其它 fixture 修复一并落地。

## D26 — 成熟标准方案直接实施，不逐条确认

**Decision**
凡属业界已有通用、标准、成熟解法的技术问题，直接按标准做法实施，不再向用户逐条确认。
仅当某个选择**属于产品或业务定制**、标准给不出答案（例如保留期默认天数、Redis 不可用时
限流 fail open 还是 fail closed）时才提问。

**Reason**
用户 2026-09-07 明确指示：「这些东西又不是什么新鲜的，也不是我强定制的，有通用的标准的
成熟的方案就直接做了」。这与 D17（Phase 10 缺口按主流 IAM 通行做法定）、D18（按标准通用
共识做法自行判定）是同一条授权的重申与扩展。用户不是该领域专家，把标准做法拿去请他裁决
既无信息增益，也拖慢交付。

**Alternatives Considered**
1. 每项设计仍出方案等确认 —— 已被用户明确否决两次。
2. 全部自行决定不再报告 —— 过头：产品级选择仍须用户拍板，且交付时必须说明做了什么。

**Tradeoffs**
风险在于「什么算标准」由我判断。缓解：实施时在代码注释或决策记录里写明依据的是哪条通行
规范（如 OWASP SSRF 防护、RFC 7644 SCIM、GDPR 第 17 条），使判断可被复核而非诉诸权威。
交付说明中仍需列出所有实质决定，用户保留否决权。

**Scope**
本次 IAM 交付全程，及后续同类工作。

**Status**
已决定（2026-09-07，用户明确指示）。

## D27 — SCIM 退役采用同步可续跑，不引入后台重试队列

**Decision**
D23 的第二半「显式删除并注销」实现为 `RetireTarget`：逐个注销 live mapping，全部成功后才
删除 target；任一失败即中止，保留 target、凭据与全部剩余 mapping 并返回已完成数量。
**未实现**后台重试队列与死信治理面。

**Reason**
已注销的 mapping 会被墓碑化，而墓碑化的 mapping 不计为 live，因此重跑天然从中断处继续——
幂等且可续，这已覆盖后台任务的主要价值。而持久化任务需要新增 target 状态，
`iam_scim_targets.status` 上有 `CHECK (status IN ('active','disabled'))`，改它在 SQLite 需
重建表，是一次三方言的重型迁移；在迁移即将冻结（批 3）的窗口内，这笔成本与它带来的增量
收益不成比例。

**Alternatives Considered**
1. 新增 `retiring` 状态 + 复用现有 `reconcileAll` sync 循环作为 worker —— 设计上更好，
   大目录不会阻塞调用方；代价是上述三方言 CHECK 迁移。若用户要求可后续补，属增量改动。
2. 保持仅 fail closed —— 审计允许（close condition 是「或」），但大目录逐个手工注销不可运营。

**Tradeoffs**
同步实现对超大目录会长时间占用一个 HTTP 请求，可能触发网关超时；此时操作员重新调用即可
续跑，不会丢失或重复注销。这一限制**须在交付说明中明示**，不得当作已解决。

**Scope**
`internal/iam/provisioning/target.go`（`RetireTarget`、`ErrTargetHasLiveMappings`、
`DeleteTarget` 守卫）、`repository.go`（`countLiveTargetResources`）、`http.go` 与 i18n、
前端 `targets.tsx` 与 `iam-api.ts`。

**Status**
已决定并执行（2026-09-07）。后台队列变体未实施，需用户决定是否排期。

## D28 - Explicit Integration Suite Time Budget

**Decision**
Set the configurable `INTEGRATION_TEST_TIMEOUT` to 20m in the backend Makefile.
This controls Go's cumulative per-package budget, not individual operation deadlines.

**Reason**
On 2026-09-08 the full CI migration package timed out at 600s while still progressing
through the last status matrix. The same complete package then passed independently
in 582.428s with no assertion failures. Eighteen seconds of headroom is insufficient
for the existing multi-engine, prefix, upgrade/rollback and subprocess matrix.

**Alternatives Considered / Tradeoffs**
Reducing concurrent packages cannot provide useful headroom over an isolated 582s run.
Keep all tests and assertions, expose the package budget through the existing Makefile
variable convention, and retain the new concurrency tests' 5s lock-wait and 30s operation
deadlines. A pre-existing unbounded test may now take longer to report a hang; this
budget is not evidence that any individual operation should take twenty minutes.

**Scope / Status**
Backend integration runner only; no production timeout or migration changes.
Implemented under D26's standard technical-remediation authorization. Normal `make ci`
subsequently passed; migration package took 640.915s, with all assertions intact.

## D28b — 擦除事务对锁冲突有界重试，并承认它可能被围栏反超

（编号说明：本条曾与上面的 `D28 - Explicit Integration Suite Time Budget` 重号，导致"按 D28 引用"无法消歧。
保留两条的历史位置不动，把这一条标为 D28b；全仓没有别处按编号引用 D28。）

**Decision**
`AnonymizePrincipal` 在数据库因锁循环回滚它时重试，最多 3 次；判定收敛在
`gormx.IsTransactionConflict`（pgx SQLState 40001/40P01、MySQL 1213）。**不重试**锁等待
超时（MySQL 1205）。相应地，`TestPrincipalErasureConcurrencyExternalDatabase` 的
`erasure-first` 分支承认「擦除被围栏反超」是合法结局：仅 `delivery-in-flight` 与
`live-remote-mapping` 两个围栏 owner 允许，且必须断言什么都没被擦掉。

**Reason**
擦除先锁 principal 再遍历各 owner 的表；普通请求先拿子表行锁，再经外键触及 principal。
两个方向天然相反，PostgreSQL 会发现环并回滚其中一侧，**挑谁由它决定**。原先擦除若被选中，
运维看到的是裸 `deadlock detected (SQLSTATE 40P01)`。

无法靠加锁顺序消除：写入方是任意并发请求，不受控；也不能把 principal 锁降级为
`FOR NO KEY UPDATE`——`login_name` 有唯一约束（`uq_iam_principals_login`）而擦除要改写它，
它是 key 列。因此重试是唯一正解，也是 PostgreSQL 对 40001/40P01 的官方建议。

重试带来的语义后果是**正确的而非缺陷**：回滚释放了 principal 锁，竞争方得以提交，重试时
重新评估围栏——此刻通知确实在投递中、远端账号确实存活，拒绝才是对的。原测试要求
`erasure-first` 必须成功，等于要求擦除永不输掉锁循环，而不输的唯一办法是不重试。

不重试锁等待超时：那说明对方仍持有行，立即重试只会排进同一个等待，会把容量或长事务问题
埋成静默循环。

**Alternatives Considered**
1. 让死锁上浮为可重试错误交给调用方 —— 运维仍要手工重试，且 HTTP 层要暴露一个纯数据库概念。
2. 统一加锁顺序 —— 不可行，写入方不受控。
3. 降级 principal 锁强度 —— 因 `login_name` 唯一约束不成立。

**Tradeoffs**
重试使「先拿到锁者必胜」不再成立，这一点已写进测试注释。

**验证强度（2026-09-08 更新）**：真实 MySQL 8.4.11 门禁（`make test-mysql8`）单次运行中
触发 **8 次真实死锁**，`TestPrincipalErasureConcurrencyExternalDatabase` 仍 PASS，门禁整体
EXIT=0。死锁语句正是竞争写入方对 `iam_tenant_members` / `iam_oauth_consents` /
`iam_identity_links` 的 UPDATE，与设计分析一致。至此重试吸收真实死锁已被直接观察到，
不再只有注入 40P01 的单元证据。PostgreSQL 侧：修复前失败可复现，修复后约 28 次运行
（含 2 次完整 `make ci`）不再复现。

**Scope**
`pkg/extension/gorm/retry.go`（新增）、`internal/iam/identity/lifecycle.go`、
`internal/app/principallifecycle_test.go`（erasure-first 分支）。

**Status**
已决定并执行（2026-09-08）。

## D29 — 门禁脚本必须对「docker 命令带远端 shell」成立

**Decision**
`scripts/test-mysql8.sh` 中授权 `performance_schema` 的语句改为经 **stdin** 传给
`docker exec --interactive`，不再放在 `--execute=` 参数里。

**Reason**
`DOCKER_CMD` 不保证是本地 `docker`：项目自己的 `make iam-preflight` 会为本机生成
`ssh -i ~/.ssh/iam-docker-<host> root@<host> docker`，中间因此多一层远端 shell。
`--execute="GRANT SELECT ON performance_schema.* TO 'user'@'%';"` 含空格与引号，会被那层
shell 重新分词，`mysql` 收到 `--execute=GRANT` 加散落参数，打印用法后以非零退出，整道门禁失败。

该语句由 `f371e7e` 引入，说明它只在本地 docker 传输下验证过；在本仓既定的远端传输下
**从未通过**。stdin 不会被任何一层 shell 重新分词，因此本地与远端两种传输都成立。

**Alternatives Considered**
1. 在调用方包一层用 `printf %q` 转义的 wrapper —— 只修一台机器，仓库里的缺陷仍在，
   下一个用远端传输的人照样踩。
2. 要求所有人改用本地 docker —— 与 `iam-preflight` 自动生成远端传输的既定设计冲突。

**Tradeoffs**
脚本里其余参数（十六进制口令、`--sql-mode=` 逗号串、`{{.State.Running}}`）均不含空格，
经远端 shell 后 argv 不变，故只此一处需要改。若日后再加入含空格的参数，应同样走 stdin
或显式转义。

**Scope**
`apps/backend-api/scripts/test-mysql8.sh`。

**Status**
已决定并执行（2026-09-08）。修复后 `make test-mysql8` 六个测试组全绿，EXIT=0。

## D30 — 永久擦除先建立数据库外的不可变义务

**Decision**
永久擦除在所有数据库内前置条件和 owner 围栏通过后、事务提交前，必须先把一条不含 PII 的
擦除义务写入外部 S3 Object Lock COMPLIANCE 对象。对象以主体 ID 和目标生命周期版本作幂等键；
未启用、对象锁不可用、写入或回读验证失败时，擦除返回服务不可用并回滚数据库。外部对象一旦
验证成功，即表示平台必须最终完成该次擦除；即使随后的数据库提交失败，也不能删除这条义务，
而应由重试或恢复对账完成。恢复工具必须从外部账本结构化列举义务，不能依赖已恢复数据库中的
墓碑或审计事件。

**Reason**
数据库墓碑、被擦行和 `principal_anonymized` 事件处于同一备份范围，恢复旧备份会同时复活 PII
并抹掉擦除痕迹。现有审计归档按 `archive_after_days` 延迟，默认 730 天且最短一天，不保证最近
擦除已外部化。数据库 outbox 同样会随备份回退；数据库与对象存储也没有原子提交。先建立外部
义务并 fail closed 可以保证不存在“数据库已擦除但外部毫无记录”的结局。数据库提交失败后留下
待完成义务是显式、可对账的状态，比静默丢失擦除事实安全。

**Alternatives Considered**
1. 把 `principal_anonymized` 事件立即走现有归档 —— 归档任务和游标仍在同库，提交与上传之间有
   丢失窗口，且会混淆历史归档与即时合规凭据。
2. 仅依赖 MySQL binlog/PITR —— 可以作为灾备补充，但依赖部署侧连续保留和时间点解析，不能给
   所有支持的数据库引擎提供同一擦除恢复契约。
3. 对象存储写在数据库提交之后 —— 进程在两步之间崩溃就会产生本次要消除的无痕擦除。

**Tradeoffs**
对象存储短暂不可用会阻断新的永久擦除；这是有意的 fail-closed。对象成功而数据库提交失败时会
存在尚未完成的义务，对账必须重新检查当前法律保留和主体状态后完成，不能把它误报为已经擦除。
账本复用 audit 的 S3 凭据、桶与保留期，但使用独立 `audit/erasures/` 前缀，不新增数据库迁移。

**Scope / Status**
`internal/iam/audit`、`internal/iam/identity`、应用配置、恢复 CLI、对应测试与恢复 runbook。
已实现并完成真实恢复演练（2026-09-09，依据 D26 的成熟安全方案授权）。MySQL 8.4.11 的
旧备份恢复确实复活 PII 并移除数据库墓碑/审计事件，外部义务仍以原 VersionID 和 SHA-256 可列举；
重新记录保留决策、停用并擦除后，数据库证据恢复且外部对象未被覆盖。恢复还必须重建 dump 所载
触发器的 definer 身份与权限；仅成功导入触发器不代表其可执行。删除标记后的重试还必须先搜索并
验证全部对象版本，不能因当前版本不可见而发布第二份义务。证据见恢复 runbook。

## D31 — UI-03 展示元数据使用生成查询，不扩展授权原始 SQL

**Decision**
人员授权来源仍复用 `Authorizer` 的既有授权计算结果。UI-03 新增的临时角色和业务主体绑定
有效期由 core 通过 `core/query.Bind` 读取；静态组、岗位和角色部门范围由 organization 模块通过
`organization/query.Bind` 读取，再经 core 提供的窄连接契约装配。时间窗用同一个 UTC 毫秒
`EvaluatedAt` 在 Go 中判断。不得为展示字段扩展 `effectiveRolesCTE` 或递归主体授权 SQL，也不得
复制授权优先级或让 core 直接导入 organization 的 model/query 包。

**Reason**
授权器中的 CTE 是历史核心计算路径；把仅供界面展示的 `expires_at` 加入不同分支的 `UNION` 和
递归列会扩大原始 SQL 契约，并把数据库日期类型推导差异带入 MySQL、PostgreSQL、SQLite。
生成查询对表、列和结果模型提供编译期约束，同时保持来源是否生效仍由唯一授权器决定。

**Tradeoffs**
每次授权来源快照固定增加四次有界查询，但不产生按来源逐行查询。修订号前后检查覆盖这些读取，
并发变更仍会使快照重试。历史授权器原始 SQL 不在 UI-03 范围内重写。

**Scope / Status**
`internal/iam/core/member_authorization.go`、organization 授权来源读取适配器及三方言针对性测试。
SQLite、PostgreSQL 16 无前缀/带前缀及真实 MySQL 8.4 浏览器验收均已通过；最终 `make ci`
通过，总覆盖率 90.1%，core 90.2%，organization 90.1%。

## D32 — 出站 SCIM 未知结果使用不自动过期的操作占用

**Decision / Reason**
以target持久operation token覆盖push、deprovision、配置替换及删除；首次远端PUT前提交live mapping intent。
自动同步取得claim后重新检查mapping状态，显式人工重新provisioning仍允许。客户端超时、HTTP202、
进程死亡与本地提交失败均不证明远端请求停止，因此不能依靠会过期的租约允许后续DELETE越过旧PUT。

**Tradeoffs / Scope**
未知结果会阻塞整个target，必须取得provider完成/取消证据后按精确token修复，不能自动清锁。
provisioning v6升级和回退均要求停止全部writer/worker并排空远端请求；COUNT guard不是在线屏障。
Redis失效时限流open/closed仍保留D24/D26的业务选择，不受此决策替代。

**Status**
本轮评审后修复授权范围内已实施；SQLite、真实PostgreSQL和MySQL操作并发及有数据回退保护通过。
gateway504先响应、provider迟到PUT/DELETE的双HTTP回归及变异通过；408/所有5xx/未知响应均保持claim。
真实MySQL完整门禁EXIT0，v6已纳入207文件迁移锁；生产停writer/排空请求与provider恢复取证仍须部署演练。

## D33 — OAuth撤销必须覆盖已签发的应用访问令牌

**Decision / Evidence**
真实MySQL/浏览器联测证实：个人consent撤销成功并从列表消失后，access token仍可introspect为active。
原实现只撤refresh/code，实际principal bearer校验未查询应用授权；现有“撤销结束已授访问”契约未兑现。
修复为应用授权独立grant incarnation，经consent→code/refresh→JWT传递，并在真实Bearer认证链校验。

**Alternatives / Tradeoffs**
仅查询consent是否存在会在重新同意时复活旧JWT；仅改introspection遗漏直接资源访问；全局提升
principal credential_version会误撤其他应用。采用独立随机grant代号，不依赖时间精度或同毫秒先后。
旧无grant OAuth令牌须重新授权，普通浏览器session及其他应用隔离语义保留。升级禁止新旧writer混跑；
仅删数据库grant并不能保证旧binary拒绝已签JWT，不能据此宣称安全降级。

**Scope / Status**
oauth/authn/pkg claims契约、模块内查询/三方言迁移和真实前后端验收；属于用户已授权的评审后必要修复，
不是代用户选择新的账号注销或组织归属模型。方案已二次复审并实施；三方言旧数据升级/回退保留状态、
签名后撤销交错、同毫秒regrant与其他app隔离通过，真实MySQL UAT浏览器10/10通过。
Introspect仅明确无效凭证返回inactive，基础设施/取消/未知故障保留500；HTTP回归与变异通过。
外部离线JWT消费方的即时撤销、生产升级/降级流程仍需部署验收；本地通过不代表生产已就绪。

## D34 — 应用就绪范围与Redis拓扑支持

**Decision / Reason**
用户明确本轮一次完成应用功能与代码验收，生产部署、灾备与运维演练暂不纳入；Redis集群所需
配置/客户端/错误处理属于应用代码，必须纳入。沿D26成熟方案授权补standalone/cluster/sentinel、
TLS/mTLS及ACL，保留单机URL配置，严格拒绝混用和半配置。未知网络结果不重放，保留明确重定向。

**Tradeoffs / Status**
全局Lua限流要求同slot，支持Cluster拓扑不表示认证负载跨槽分片；异步复制可能丢失消费/计数，
不附加数据库消费账本，也不宣称切主后严格exactly-once。Redis故障open/closed由启用者显式配置，
不替用户制定生产策略。真实plain/全mTLS拓扑、重定向、切主和三项变异已验证；最终门禁证据见发布检查点第2.2节。

## D35 — 个人令牌范围与本人账户操作

**Decision / Reason**
个人PAT限定一个当前有效租户、本人已有非platform接口权限及显式到期时间。现有权限/数据/字段
检查始终保留，PAT是其额外上限。opaque令牌仅存摘要，创建成功只返回一次；创建参数规范化后
绑定共享reauth目的/目标/会话/主体/credential version。消费proof与写入、审计同事务。
普通authentication-only、OAuth换发和平台入口不接受PAT，真实权限路由通过可信上下文提供
实际tenant/permission（entity adapter使用其query selector），防止scope在后续换发中丢失。
个人管理接口采用统一session-only注册，规范和运行时都拒绝Bearer。

**Alternatives / Tradeoffs**
不复用服务账号密钥，不默认无限期或制定业务最长有效期；数据库使用支持9999年的时间列，
用户显式填写期限。丢失创建响应不能再次披露secret，可从列表撤销后重新创建。
个人权限选择复用当前注册表及CheckBulk，不要求role_view管理权限，不另建权限目录。

**Scope / Status**
沿现有租户归属展示UI04；分别提供退出当前租户、申请关闭全局账号，不新建独立主体组织模型。
退出保留全局会话和其他租户，撤销所属租户OAuth/PAT并保留last-admin保护。关闭仅申请、可取消、
平台可追踪/驳回，申请本身不自动停用或选择保留期限。平台显式pending→processing处理入口
按version并发控制并原子停用/撤销凭据，覆盖零租户账号，保留全部租户last-admin及平台权限保护；
后续处理沿既有生命周期/保留/法律冻结/外部义务，完成态由墓碑派生。OAuth新code及待同意请求
与退租/关闭共用principal锁，并重验成员和credential version，防迟到请求恢复旧授权。
个人闭环服务/HTTP及失败回滚通过，真实PostgreSQL/MySQL组合与并发通过；最新后端真实浏览器
个人安全12/12、生命周期6/6、全业务矩阵792/792通过。最终独立门禁与交付状态见发布检查点和task-state。

## D36 — 查询门禁：禁手写 SQL 与禁拼表名，用只减不增的冻结基线

**Decision / Reason**
用户要求"严禁 raw SQL、全部走 gorm/gen 类型化写法"。实测：生产代码 18 个 `.Raw(`/`.Exec(` 调用点、23 条语句，
但**真正的战场是 72 条 `.Joins(` 生产语句与约 170 处绕过 `Bind` 的拼表名**。
新增 `tests/architecture/query_gate_test.go`（5 条 AST 规则）+ `testdata/query_gate_ledger.json` 冻结基线：
`handwritten-sql` 18、`concatenated-table-name` 146、`naked-table-literal` 715，预算 **879**（347 条目）。
门禁放在 `tests/architecture` 而非 `tools/*`，因为它随 `make test-integration` 在 `make ci` 内，每个提交必跑。

**Alternatives / Tradeoffs**
不用 `forbidigo`：它只能按标识符名/正则匹配，表达不了"ADD 出现在某方法实参里""字面量在某上下文里"，
而 `prefix` 这个名字在 URL、S3 key、env 变量三处合法存在，会大量误伤。
`naked-table-literal` 只匹配 `^(iam_|goose_)`，这三类前缀永远不会命中，实测零误报。
`gorm-expr-dynamic` 初版曾把 `gorm.Expr(clock+" + ?")`（列名 + 运算符，正是该 API 的用途）误判为违规，
已收窄为"动态**且**文本含 SQL 关键字或表名"，真实仓库归零。
预算只能降不能升：`QUERY_GATE_UPDATE=1` 在结果超过既有预算时直接失败；**确需升预算**（例如规则补漏后
合法浮出更多既有违规）必须设 `QUERY_GATE_REBASELINE=<原因>`，原因会写进账本与 diff，是一次留痕的、
可评审的动作，而不是靠删账本绕过。三条执行路径都做了变异验证
（漏一条→报 new violation；多一条→报 not found；预算调低→报 above its budget）。

**变异测试在冻结前抓到并修掉了两个真实漏洞**（这正是"规则要能被证伪"的价值）：
① 裸表名规则原本只看实参/RHS/复合字面量/常量声明，**不看 `return`**——
   `func helper(...) string { return prefix + "iam_roles" }` 只要不叫 `table` 就能绕过。
   补上 `return` 上下文后预算从 836 升到 879（多浮出 55 处既有违规，其中大数是此前完全没被统计的）。
② 补 `return` 时一开始把 `audit/anchor.go:151` 的 S3 对象键与 `authn/returnurl.go:129` 的 URL 前缀
   误报为拼表名（它们同样由名为 `prefix` 的变量拼成）。已收紧为"必须同时出现表名字面量或
   `.TableName()`"，两处误报消除；夹具里也各留了一个正例与一个反例把这条边界钉住。

**Scope / Status**
P0 已完成并实证（含夹具证明 5 条规则均非空断言、白名单可断言）。后续按阶段推进，**基线集合只减不增**；
清理时必须同步下调 `budget`。**不得为了让门禁变绿而抬高预算或删除条目。**

## D37 — 跨模块取表名走窄接口注入，不放宽架构测试、不放宽依赖全序

**Decision / Reason**
128 处 `table("字面量")` 中只有 34 处是本模块自有表，94 处是别人的表。
用户初选"允许跨模块 import query"，但实测 `pkg/module/registry.go:126` 的 `Activate` 要求
"模块必须注册在其全部依赖之后"——**依赖图必须是严格全序，环即启动失败**；
而 organization/identity/authn/oauth/provisioning 全部已依赖 core，
**core 反向 import 它们的 query 在启动期就是硬失败**，与风格无关。
最终确认走窄接口/注入（照抄既有 `DirectoryAuthorizationSourceConnector`、`RoleGrantStore` 两个先例），
未环方向的跨模块引用也不放开，保持 `cross-module-storage-import` 门禁与 `Activate` 一字不改。

**Alternatives / Tradeoffs**
改 `Activate` 允许成环会破坏关停严格逆序的根基，影响全仓 19 个模块的生命周期与迁移顺序，是降低架构强度而非提升，
且选窄接口后它无事可做。代价是需新建 6–9 个契约与连接器，工作量更大。

**Scope / Status**
P2/P4b/P5 落地；**具体形状由 D40 定稿**（跨界结构体声明在被对方 import 的那一侧）。
授权引擎的语句保持单条（`administrator.go` 的 last-admin 守卫由 `Protect` 在调用方事务里
做前后两次测量，拆分会让两次测量落在不同快照上）；`relationshipTraversalExpressions` 的三方言 switch
原样搬迁，性能基线 28 个指纹必须零变化以证明"只搬家不改语义"。

## D38 — authn 的 EnsureBootstrapPrincipal 与两条越界写删除，平台管理员初始化只保留 create-only 的那份

**Decision / Reason**
`authn/web.go` 的 `EnsureBootstrapPrincipal`（及 `BootstrapPrincipal`）与它专用的
`Repository.createPrincipal` / `updateBootstrapPrincipal`，连同无调用方的 `getPrincipal`，一并删除。
生产路径已由 `internal/app/platformadmin.go` 承担：`identity.BootstrapPrincipalStore` 建 principal、
`authn.BootstrapCredentialStore` 建 credential、`core.PlatformAdministratorStore` 写管理员行，
三个能力在同一事务里由组合根编排。authn 那份是重复实现，并且是 authn 写 identity 拥有的
`iam_principals` 的三条越界写中的全部来源。

**证据（实测，不是判断）**
① `grep -rn "EnsureBootstrapPrincipal" --include=*.go` 的全部命中在 `_test.go` 与定义处；
② **panic 探针**：在函数首行插 `panic(...)` 后，`go test` 全仓（排除 `internal/iam/authn` 自身）
   与 `tests/` 六个集成套件（architecture / goose / testdb / lifecycle / migration / performance）
   **全部通过、无 panic**；对 `getPrincipal` 同法同结果；
③ `internal/app/platformadmin.go:36-44` 自己写明 bootstrap 是 create-only，
   "password rotation belongs to a separate audited reset flow"。

**语义差异（必须记下来，否则以后会被当成回归）**
被删的那份会**重置密码并吊销会话**（reconcile 语义）；生产路径是 create-only，
重复执行返回 `PlatformAdminAlreadyBootstrapped`。密码轮换由 `internal/app/platformadminreset.go`
经 `authn.ResetBootstrapCredentialOn` 承担——**删它不丢任何能力**，只是去掉了第二条语义不同的路径。
`resetBootstrapCredential` 本身保留（重置流程在用）。

**Alternatives / Tradeoffs**
保留并把三条越界写改成调用 identity 的能力：代价是仓里长期留着两份做同一件事、语义还不同的实现。
本次不做、只记为技术债：同样留着那份重复实现。均已否决。

**覆盖率实测与预估相反**
事前预估"删掉一段覆盖率高于包均值的代码会把 authn 压低"，实测 **authn 90.6% → 90.7%**（反而升）——
被删的那段含若干未覆盖的错误分支。预估错了，以实测为准。

**Scope / Status**
S1 已完成：净 **-196 行**（8 文件，+74/-270）。`go build`、全量单测、`make lint`、
`tests/architecture`（含查询门禁账本）全绿；总覆盖率 90.1%。
测试侧改动：`bootstrapLocalPrincipal` 与 4 个 harness 改用新的包内 `seedLocalPrincipal`
（直接写 principal + credential 两行，并按 `normalizeLogin` 归一化登录名——否则 fixture 用自己的
登录名都查不到自己）；读 principal 的 6 处测试改用 harness 的 `principal()` 直读，
不再让生产代码为测试保留一个读方法。

## D39 — authn 与 identity 的双向越界改为互相委派；不可委派的只剩一个 JOIN 形状

**Decision / Reason**
authn 与 identity 互相往对方拥有的表里读写（18 条语句，双向）。修法不是「发布表名」，而是**委派**：
拥有者在调用方的事务里执行自己的语句。样板是仓内已有的 `internal/app/principallifecycle.go`
与 `identity.PrincipalStateStore`。

- **authn → identity 13 条**：3 条死码删除（D38）；7 条委派给 `identity.PrincipalStateStore`
  新增的 6 个 reader；3 条**不可委派**（每条都同时读 authn 自己的表），保留在 authn，表名改走
  identity 发布的集合。
- **identity → authn 5 条**：全部委派给新增的 `identity.PrincipalCredentialStore`（authn 提供）。

结果：脚本普查「模块用自己的前缀拼别人家表名」**65 处 → 0 处**。

**接线形状：两处方向反转，这是被迫的**
`authn` 激活在 `identity` **之前**，于是两种常规形状都走不通：
authn 去 `Resolve` identity 的类型 → 架构门禁 `module-dependency` 要求声明 identity →
`Activate` 的注册序校验失败；identity 去 `Resolve` authn 的类型 → 必须 import authn →
与已有的 authn→identity 成 **Go 包环**。
合法形状只有：**类型声明在 identity，消费方提供钩子，identity 解析自己包的类型后推过去**
（`inspectCall` 对「实参所属包 == 自己」不记依赖）。据此新增
`identity.PrincipalTableSetReceiver`（推表名）与 `identity.PrincipalStateReceiver`（推能力）。
命名刻意用 `Receiver` 而非 `Connector`，以区别于方向相反的 `core.PrincipalTableSetConnector`。

> **D40 起类型已改名，形状不变**：`identity.PrincipalTableSetReceiver` →
> `identity.PrincipalTableNamesReceiver`（载荷改为 identity 自己的 `PrincipalTableNames`）；
> `core.PrincipalTableSetConnector` → `core.PrincipalTableNamesConnector`。
> 本条的**结论与理由全部仍然成立**，只是不具名集合 `gormx.TableSet` 已整体删除。

**`LockActiveRow` 不是 `LockActive` 加个返回值——这是安全差异**
`lockPrincipal` 的 7 个调用点里 6 个只取 error，依赖「principal 不存在或已停用 → 报错 → 事务回滚」。
`LockActive` 是一条 UPDATE，**不报告是否匹配到行**。照「复用 LockActive」的最初设想改，
会让**停用账号走通找回密码 / MFA 注册 / 邮箱验证**。故新增会报告 `found` 的 `LockActiveRow`，
并有专门断言钉住（`TestSecurityStateWritesRefuseADisabledPrincipal` 5 个子用例）。

**SQL 逐字节未变，是实测而非声称**
先在 authn 侧冻结 9 条语句的完整文本（DryRun + 真实 PostgreSQL 方言，含 gorm 单独发出的
NOT EXISTS 子查询），再在 identity 侧**独立写死同一批字面量**断言新 reader 产出相同文本，
搬完再跑一次。三次都相等。这层断言的必要性在于：性能基线的查询计划指纹需要外部数据库，
**`make ci` 里没有**，没有它等于在 CI 看不见的地方改冻结路径。

**关户路径的 sessions 被写两次，刻意保留**
identity 请 authn 用 **identity 的数据库时钟**吊销；authn 自己的凭据吊销又用**服务时钟**跑一次。
两条都带 `revoked_at IS NULL`，identity 在前，**落库的时间戳是 identity 的**，第二条是真 no-op。
删掉任一条都会静默改变审计时间戳来源，而任何「终态」断言都看不见。
故保留，并在 `internal/app` 的组合测试里钉住「恰好 2 条 sessions UPDATE」；
该断言做过变异验证（改成 3 即失败，实测值 2）。要改时间戳来源需单独 ADR。

**Alternatives / Tradeoffs**
- 放宽 `isStableWiringCapability` 白名单或调换注册序：前者是用户明令禁止改 `tests/architecture`，
  后者影响全仓 19 个模块的生命周期与关停逆序，风险远大于收益。均否决。
- 加宽 `identity.ServiceAccountValidator` 以接走 authn 的四表 JOIN：它的 SQL 是冻结的
  （性能基线 `scim_bulk` 路径，`count(*)`），改返回字段即破指纹。否决，该 JOIN 留在 authn。
- 把两条 `iam_principals ⋈ iam_credentials` 的 JOIN 拆成两次调用：会改 `session_lookup` 指纹，
  且多一次往返。否决，保留 JOIN、表名走发布集。

**测试侧的一处不对称（如实记录）**
identity 的测试**不能**使用 authn 的真实能力——authn import identity，所以 identity 的测试也不能
import authn，这正是当初需要这个能力的同一个方向约束。identity 侧用替身（写同样的行），
真实闭包的行为在 `internal/iam/authn/credentialstore_test.go` 断言。替身文件顶部写明了这一点。

**Scope / Status**
阶段一（S0–S8）完成，7 个提交 `9e89c5e..61f2b35`。门禁账本 **385 → 372**，四次均手动下调，
全程未用 `QUERY_GATE_REBASELINE`。

## D40 — 跨模块表名一律走类型化结构体；声明在被对方 import 的那一侧；不具名集合整体删除

**Decision / Reason**
阶段二把「拥有者发布一袋按字符串查的表名」（`gormx.TableSet` + `XTables{Published}`）
全部换成类型化结构体，全仓一种形态。用户要求：强类型约束写法、全仓统一、0 遗憾。
消费方拿到的一律是结构体字段，**没有任何一处再用字符串查表**；问一张拥有者没有的表
从「运行期 error」变成「编译不过」。

**唯一的一条规则（由 Go 的 import 方向决定，不是偏好）**
> 跨界的类型化结构体，声明在**被对方 import 的那一侧**。

- **消费方 import 拥有者**（拥有者激活在前）→ 拥有者导出 `PublishedXTableNames(prefix)` 与
  `XTableNames`，`module.Provide[XTableNames]`，消费方 `Resolve` 后直接持有。
  适用 65 处：authn/identity/organization/oauth/provisioning/governance/federation ← core 等。
- **拥有者 import 消费方**（拥有者激活在后）→ **消费方声明它需要的字段**，拥有者在连接器里填满。
  适用 6 对：core←organization(12 字段)、core←identity(1)、authn←organization(1)、
  authn←oauth(2)、identity←organization(1)、identity←oauth(1)。
- **两条都走不通的一对**（authn↔identity，见 D39）→ identity 推自己的 `PrincipalTableNames`
  给 authn 提供的 `PrincipalTableNamesReceiver`。

连接器随载荷改名：`XTableSetConnector` → `XTableNamesConnector`。
`TableSet` 这个词在仓里已不存在，名字里留着它就是假话。

**「消费方加字段 → 拥有者编译不过」并不成立，别这么写文档**
计划里曾这么描述 B 类的强度。实际上 Go 的**带键结构体字面量**在结构体新增字段时照样编译，
新字段静默为零值。真正的强制手段是运行期 + 启动期：`core.joinedTables.bindDirectoryTables`
逐字段检查 `IsZero()` 并**报出字段名**，authn/identity 的 bind 同理；每个模块的 `Initialize`
再查一次 `foreign.bound()`，任一槽位未到即**启动失败**。这一条已对 7 个模块做过变异验证
（删掉检查 → 对应用例立刻失败）。

**「是否已绑定」从每处一条收敛到每模块一条**
原先每个调用点都有 `x, err := r.foreign.accessTable("iam_x"); if err != nil {...}` 三行。
现在是一行字段读取，检查搬到 `Initialize`（Registry 相位保证那时 Provide/Bind 全部完成）。
**代价，如实记录**：在模块外手工构造 repository 而忘记 bind 的调用方（只有测试夹具），
过去得到一个 error，现在得到空表名。模块自身的路径由启动检查覆盖。

**顺带删除的东西（全部是本工作流自己引入、现已无调用者）**
`gormx.TableSet`/`NewTableSet`/`Lookup`/`Names`/`Len`；13 个模块的 `TableSet(prefix)`；
4 个 `XTables{Published}` 能力；`query.BindDirectoryTables`/`BindPrincipalTable`；
authn/identity `Repository` 上的 `tenantTable()`/`memberTable()`/`principalTable()`/
`serviceAccountTable()`/`oauthTable()` 中间取名方法；8 个无人消费的 `tables.go` + 其 `tables_test.go`；
`internal/app/bootstrap_iam_tables_test.go`（它枚举的 13 个 `TableSet` 已不存在）。

**`query.TableNames.Map()` 也删了——先前记为「有意保留」，后来推翻**
最初的保留理由是「13 条前缀断言靠它枚举」。这个理由在看到 `backend-module` SKILL.md 的模板后不成立：
文档会教**每个新模块**都写一个无人调用的 `Map()`。13 条断言改成反射枚举 `BindNames(prefix)` 的字段后
**比原来更强**——`Map()` 同样是手写的，它漏掉的字段原测试根本看不见，而反射走的是结构体每一个字段。
已变异验证（去掉某个名字的前缀 → 用例立刻红）。federation 那条 `BindNames vs Bind` 漂移断言
也一并改成反射，语义不变。

**Alternatives / Tradeoffs**
- **让 organization 直接填 `core/query.DirectoryTables`**（省掉 core 根包再声明一份 12 字段）：
  被架构门禁 `cross-module-storage-import` 禁止（跨模块 import 他人 `query`/`model`/`dbgen`）。
  用 root 包里 `type X = query.Y` 的**类型别名**能绕过（源码里不出现该 import），
  但那是绕规则不是守规则。故 core 根包声明一份，`joined_tables.go` 里逐字段转换——
  多 12 行，换来的是转换本身受编译器检查。
- **不删 8 个无人消费的 `TableSet`**：它们是 P1 为这套机制铺的，机制没了就是死代码。
  代价是丢掉 `TestEveryModulePublishesPrefixedTableNames` 这条「13 个模块都得发布」的跨模块门禁；
  其「前缀正确」的保证在每个模块自己的 `query/table_test.go` 里仍在
  （`TestBindAppliesPrefixToEveryXTable`），丢掉的只是「新模块也必须发布一份」这层提醒。

**Scope / Status**
阶段二。SQL 文本逐字节未变——施工上靠「保留局部变量、只删错误分支、语句行不进 diff」，
验证上靠既有的 9+8 条冻结断言与 MySQL 查询计划指纹。

---

## D41 — Logical Table ≠ Physical Table（表名唯一权威 = Resolver）

**Date** 2026-09-13

**Decision**
数据库访问层严格区分三件事并保持解耦：**Logical Table Name**（业务 / Repository / Query Builder
可见的唯一表名）、**Physical Table Name**（运行时由部署 namespace 解析出的真实表名，如
`sl_iam_roles` / `uat_iam_roles`）、**Table Resolver**（唯一把 Logical 解析为 Physical 的
基础设施边界）。业务代码不得自行推导、拼接、缓存或传播物理表名。

**判定指标不是「0 个 `runtime.TablePrefix()`」，而是「0 个未经授权的 physical-table-name
决策」**：resolver 内部的 TablePrefix() 合法；硬编码 `"uat_iam_users"` 即使不用 TablePrefix
也违规。

**Invariants**
1. **逻辑名是业务可见的唯一表标识**：业务层 / Repository / Service / Query 只能见逻辑名、
   发布的 typed 引用或 resolver 生成的名称；不知道部署 namespace。
2. **`runtime.TablePrefix()` 是基础设施状态**：只允许出现在 resolver（`query/table.go` 的
   BindNames/Bind）、module 表名发布/绑定、DB 基础设施注入、明确登记的 B3 例外；禁止出现在
   业务查询、Repository 实现、Service/Domain 代码。
3. **前缀拼接只能发生在 resolver 边界内**：`prefix + table`、`runtime.TablePrefix() + table`、
   `fmt.Sprintf("... FROM %s ...", prefix+table)` 及其等价物在数据库访问代码中禁止。
4. **Raw SQL 不能绕过**：Raw / CTE / JOIN / recursive 中使用的表名必须来自 resolver /
   published names，不得自行拼 runtime prefix。
5. **跨模块访问保持所有权**：消费方只经 owner 发布的 typed 名/引用读表，不复制 owner 的
   物理命名逻辑。
6. **方言与 namespace 独立**：MySQL/PG/SQLite 的 dialect 差异不得改变 Logical→Physical 的
   规则；dialect 只负责 SQL 行为，resolver 只负责物理表名。

**Allowed**：`BindNames(...)`、`Bind(db, prefix)`、`PublishedXXXTableNames(...)`、typed 表引用、
resolver 生成名、明确的基础设施注入、登记的 B3 例外。
**Forbidden**：Repository/业务代码里出现 `runtime.TablePrefix()`；`prefix()` 访问器在
Repository 实现中传播；`prefix + table`；`fmt.Sprintf(... FROM %s ...)` 拼物理表名；模块自行
构造他模块物理表名；Raw SQL 独立重建物理表名。

**Non-goals**：不要求消灭 Raw SQL / 动态 SQL / gen 表达式；不强制三库同一 namespace 实现；
不把模块 schema 所有权与部署 namespace 混同。

**Enforcement 顺序（先清理、后机械化——避免「测试把现状定义成架构」）**：D41 入册 → SKILL
执行规则 → 明确 allowlist/边界 → 清理现存违规（9 `prefix()` + 4 `table()` + `NewRevisionStore(prefix)`）
→ 确认剩余合法点 → 账本 19→15→14 → **最后**才把规则机械化进 architecture tests / CI。
**清理完成前不改 `tests/architecture`。**

**Reason**
HEAD 0e33cd7 实测 40 处 `runtime.TablePrefix()`：约 24 处是合法 resolver/发布/绑定
（`PublishedXXXTableNames(prefix)`、`query.Bind`、infra 注入、dlock/wuid `WithTablePrefix`）；
剩余违规面集中为 **9 个 `prefix()` 访问器 + 4 个 `table(name) { return r.prefix() + name }`
帮手 + `core.NewRevisionStore(prefix)`（4 模块把部署前缀当业务参数传入 core 构造函数）**——
它们是同一缺口的不同表现：物理表名正从 Resolver 向上游泄漏。goose 版本表
（`principallifecycle.go:47`）是 B3 特殊项（框架拥有表、goosex 无访问器)；`audit/anchor.go`、
`authn/returnurl.go` 的 `prefix + "..."` 是 S3 对象键/URL 拼接，非 DB 表名，不误伤。

注：`NewRevisionStore(prefix)` 的严重度是 resolver 边界泄漏（前缀作为业务参数运输），不是
跨 namespace 混淆——全部模块共享同一个部署 `table_prefix`，不存在「上层模块知道 core 私有的
另一套命名」；修法仍是归位：core 的 revision 存储自己经 resolver/bound 名取表，调用方不再传前缀。

**Alternatives Considered**
1. `schema.NamingStrategy{TablePrefix}`（连接级）——gorm 只对 model 派生路径生效，
   `Table()`/Raw/JOIN 不经过 naming（已核 v1.31.2 源码），会造出两套前缀系统；且前缀是运行期
   部署配置，不该埋进数据源层。
2. 按库 namespace（PG schema / MySQL database 分部署）——SQLite 无 schema namespace，三库统一失败。
3. 新建中央 `Table(module, logicalName)` resolver——等价物已存在（每模块 `query/table.go` 的
   BindNames + 发布名），再建一层是过度设计；缺的是 enforcement 不是 abstraction。

**Tradeoffs**
每模块 resolver 各自持有（而非中央函数）符合「表名有主人」；代价是「不该碰表名的地方」要靠
规则钉死。D41 对 sqlc 的隐含裁决：多方言 + 运行期前缀 + 跨模块 schema + 动态查询四个补丁会
吃光 sqlc 的简单性，故 sqlc 不作目标（Bun 同理：迁移成本换不回编译期列校验）。

**Scope / Status**
全仓。Accepted / Architecture Invariant。enforcement 未机械化；现存合法点保持明确 allowlist。

---

## D42 — 列表端点默认有界,客户端需要全集时自己翻页

**Date** 2026-09-16

**Decision**
所有随租户数据增长的列表端点,分页默认**有界**:`minimum:"1" maximum:"200" default:"50"`
(沿用 `core/http.go` 里 `listTenantMembersInput` 的既有先例)。仓库侧新增 `XxxPage` 变体,
**与既有非分页方法并存**,内部调用方不受影响;handler 由 `respx.OK` 改为 `respx.List`。
前端由 `iam-api.ts` 的 `requestAllPages` 统一翻页,**需要完整集合的消费点行为完全不变**。

**Reason**
此前分页是 opt-in:`core/pagination.go` 的 `normalizePage` 把 `limit=0` 当"不限",而多数 input 的
`Limit` 是 `default:"0"`,于是**不传 limit 的调用方拿到全表**。契约审计实测:53 个返回数组的 GET
无分页参数,其中 31 个随租户数据增长,`/iam/tenants` 更是平台级。已追到实现层:
`core.ListMembers` 以 `.Find()` 结束,无 `.Limit()`,而全仓另有 317 处 `.Limit()` ——
**是既有模式没被用上,不是缺少模式。**

**Alternatives / Tradeoffs**
- *只加 opt-in 参数*:零行为变化,但不解决无界,否决。
- *服务端硬上限(恒不超过 200)*:前端零改动,但超限静默截断;虽可由 `meta.page.total` 检测,
  却把"发现被截断"的责任推给每个客户端。作为备选保留,未采用。
- 采用方案的代价是前端必须配合。**代价不在"加翻页控件",而在判断每个消费点要"一页"还是"全集"** ——
  角色编辑器用成员列表给复选框打勾,拿到部分集合会把**已分配成员渲染成未分配**,
  这是错误的界面而不是短的界面。这个判断无法机械化,是本次唯一不可并行的部分。

**Scope / Status**
样板 `aabee75`(`/iam/roles/{role_id}/members`)确证:`respx.OK → respx.List` **不改变响应 schema**
(两者返回同一个 `Envelope[T]`,`List` 只填 `meta.page`),故契约变更是纯增量的两个 query 参数,
前端类型无需重新生成。其余模块按同一模式铺开。

## D43 - File operation preparation shares the execution walker

**Date** 2026-09-18

**Decision / Reason**
Use the existing policy walker with a bounded, read-only prospective tree overlay
for operation preparation. Persist cumulative node/closure costs and expected
result counts in the files-owned migration8 plan_json column. Recheck the actual
cost at commit and roll back mismatches. Transient negative identities remain
strictly inside the overlay; they are never database keys, IAM inputs or API IDs.
New-node IAM checks remain mandatory on actual persisted identities at execution.

**Alternatives / Tradeoffs**
A second conflict algorithm would diverge on recursive batch collisions. Running
real tree writes and rolling them back during preparation would violate the
read-only preparation boundary. The lazy overlay instead bounds distinct cached
nodes by twice the configured operation node budget plus the maximum ancestor
path. It may reject excessive inspection rather than load an unlimited space.
This is NOT yet per-conflict identity/decision persistence or a timing guarantee.

**Scope / Status**
Implemented and fresh SQLite unit/race/fault tested. Migration roundtrip verified
in SQLite, embedded PostgreSQL16 and in-process MySQL, with/without prefixes.
Real MySQL8 operation runtime and full product acceptance remain pending.

## D44 - Local storage platform durability is explicit

**Date** 2026-09-18

**Decision / Reason**
Retain file data sync, root confinement and immutable hard-link publication.
Unix directory sync errors remain fatal. On Windows close the directory handle
without attempting unsupported directory FlushFileBuffers; do not advertise
Unix-equivalent metadata power-loss durability. Windows native functionality is
verified, but this platform durability criterion is NOT production accepted.
Use an actual junction to test Windows root escape without symlink privilege.

**Alternatives / Tradeoffs**
Ignoring arbitrary Access denied errors would hide real file/storage failures.
Windows has no equivalent supported per-directory fsync here; replacing
publication with absolute-path operations would weaken root confinement.
Preserve the current storage boundary and document the precise limitation.

**Scope / Status**
Local driver compatibility only, implemented and tested. Production durability
still requires target-platform crash/resource acceptance under the approved plan.

## D45 - File browser presentation does not invent a live API

**Date** 2026-09-18

**Decision / Reason**
Implement the user-requested Web page in the existing authenticated shell with
shared UI/theme/i18n. Presentation snapshots contain only authorized fields; they
are not a hand-written public API contract. The /files page remains explicitly
unavailable until files-owned IAM/audit HTTP and generated OpenAPI are ready.
Effective menus own discoverability; no unconditional tenant/platform link or
guessed permission code. Fixtures live outside the production route/module graph.

**Alternatives Considered / Tradeoffs**
Fake tenant files/localStorage mutations would obscure missing persistence and
authorization. Calling provisional endpoints would freeze an unapproved contract.
The component can be browser-tested now, but real file operations and UI/backend
acceptance remain pending. Bound pages to100, label search/selection page scope,
and clear stale selections/metadata when location/page/access state changes.

**Scope / Status**
Web browsing presentation checkpoint; no claims of working upload/download,
mutation, lazy whole-space tree, conflict decisions or real file-service E2E.

## D46 - Copy and move are real-time, conflicts are transient

**Date** 2026-09-18

**Decision / Reason**
User explicitly corrects the product contract: ordinary copy/move are synchronous
requests, not persisted awaiting-decision workflows. Detect and return conflicts
without conflict/decision tables or pending user tasks. Confirmation resubmits
explicit strategies and observed versions; reauthorize and revalidate before one
atomic publication. No open transaction waits for a user, no new conflict cleanup,
and no implicit background queue. Transfer/upload lifecycle remains separate.

**Alternatives Considered / Tradeoffs**
The prior persisted conflict frontier and submission records add inappropriate
lifecycle/schema/cleanup complexity. Removed the newly authored model/migration9/
query drafts before implementation or original DB migration. Real-time semantics
do not remove transaction rollback, source/target revision, IAM, bounded traversal,
audit or idempotency requirements. Existing operation engine/schema were earlier
work and need scoped runner review; prior migrations/data are not auto-deleted.

**Scope / Status**
Confirmed explicit user instruction, superseding asynchronous conflict/task
sections and historical next actions. Design updated; draft removal regenerated.
Files race regression suite, full backend build and diff check PASS after removal.
Synchronous runner/HTTP/real Web mutation and production acceptance remain pending.

## D49 - Workspace affiliations are directory anchors, ACL edits share file fences

**Date** 2026-09-18

**Decision / Reason**
Implement approved hierarchy within existing account/tenant namespaces through
directory workspace kind/entity/department/principal markers. Children resolve
their nearest workspace via verified ancestor path, not copied IDs or a second
organization mirror. Workspace roots are independent ACL inheritance boundaries;
private binding initializes unbound directories only, never silently reassigns.
Durable NodeACL stores one subject/action/effect/applicability row; node and space
revisions fence the complete direct rule set and synchronous mutations together.

**Alternatives Considered / Tradeoffs**
No workspace table or per-entity physical driver is needed. Per-child ownership
copies introduce subtree rewrites and drift on moves. Private persistence loaders
must not be treated as IAM authorization: actual organization/subject validation,
delegation and management-read still need owner capabilities and services.
Bounded256-rule node budgets prevent partial/truncated authorization; exceeding
the limit fails closed. Old nodes stay unbound, without inferred tenant-wide access.

**Scope / Status**
Migration9/query/private storage and isolated tests implemented. Six engine/prefix
upgrade/down/up combinations PASS (MySQL dialect engine, not real MySQL8). Files
race PASS at86.3% below90% gate; lint/vet/build/architecture PASS. No original DB,
existing service, conflict/task tables, commit/push or public-route changes.
Next: transaction-bound live IAM subjects/affiliations and service+actual audit,
then generated HTTP and real Web ACL/sharing. Full product not accepted.

## D48 - Hierarchical file ACL and sharing require design confirmation

**Date** 2026-09-18

**Decision / Reason**
User explicitly requires design before implementation. Scope now includes platform,
tenant, entity and department management visibility; fine-grained directory/file
access for different accounts; temporary/permanent password/token sharing.
Existing design section21 records the proposal. After delivery user says continue,
authorizing execution of the three recommended choices: management-read bypass of
ordinary child ACL without write/share authority, distinct account-private versus
tenant-personal ownership, anonymous read/download-only sharing.
Previous no-public-sharing exclusion is superseded by the sharing requirement;
the delivered anonymous read/download recommendation is now the implementation target.

**Alternatives Considered / Tradeoffs**
Do not infer full write/share authority from upper-level visibility or org position.
Propose separate management-read capability and ordinary IAM+ACL evaluation,
reusing existing role/group identities and policy fences rather than parallel IAM.
Propose distinct account-private and tenant-personal workspaces to avoid ambiguous
multi-tenant ownership. Durable ACL/share configuration is required across requests
and does not authorize conflict tables, pending copy/move tasks or plan lifecycle.

**Scope / Status**
Confirmed design-first, requested features and subsequent continue authorization.
No original DB migrations or existing service changes are authorized by continuation.
Next: isolated transaction IAM/actual audit tests,
workspace ownership+ACL, generated HTTP/real Web and sharing, with existing gates.

## D47 - Synchronous engine records only final success

**Date** 2026-09-18

**Decision / Reason**
Implement D46 through one caller-owned transaction and the shared bounded policy
walker. Conflicts and decisions live only in request memory; the first unresolved
frontier is returned without persistent writes. Confirmation checks the observed
space revision and exact snapshot keys. A merge can reveal a deeper frontier; no
intermediate tree is committed. Only final success uses the existing Operation
journal for actor/space/key-scoped replay. No OperationItem pins, leases, pending
states, new schema or conflict cleanup are created by this entrypoint.

**Alternatives Considered / Tradeoffs**
Reimplementing a second conflict walker would diverge on prospective batch
collisions. Simulation and actual execution instead share policies and verify
cost/revision parity. Copied identities use created-from aliases and immutable
effective content origins; generated or negative temporary IDs never enter
conflict responses/keys. Changing an earlier overwrite invalidates later choices
whose observed content/revision changed. Whole moves reauthorize old trash too.

**Scope / Status**
Internal engine implemented; 11 focused top-level SQLite tests, files race,
vet, scoped pinned lint, full build and architecture tests PASS. Files coverage
85.3% does not meet90% gate. Real transaction-bound IAM/audit, HTTP/OpenAPI,
live Web operations, other database runtime and production acceptance pending.

## D50 - File ACL subjects come from the caller transaction

**Date** 2026-09-18

**Decision / Reason**
Resolve a file request's principal, current active department, active static and
dynamic groups, and effective IAM roles through the same transaction that reads
and mutates the file tree. Lock the tenant policy revision before those reads and
reject a changed/stale snapshot. The resulting identities are ACL match inputs,
not action grants; an action-specific IAM constraint remains mandatory.

**Alternatives Considered / Tradeoffs**
Request claims do not carry current groups, roles, or department state and would
remain valid after revocation. Resolving each subject outside the file transaction
would permit IAM/file time-of-check races. Files must not query IAM-owned tables
or implement role expansion itself. The narrow core function adds one capability
but reuses the existing authorizer, foreign-table wiring, revision fence and role
resolution rather than creating a second policy engine.

**Scope / Status**
Implemented and focused core tests pass, including rollback-visible revocation,
expired group membership, inactive inputs and module wiring. Files integration,
workspace-reference validation, management-read, HTTP and Web remain pending.

## D51 - IAM action ceilings and file ACL are separate gates

**Date** 2026-09-18

**Decision / Reason**
Every file action first needs its transaction-bound `file_<action>` IAM data
constraint and then a matching effective node ACL. Copy uses source `read` plus
destination `create`; move retains its independent action. The separate
`file_read_all` permission may bypass ordinary ACL only for list, traverse, read
and download inside its verified management scope. Workspace entity, department
and personal-owner references are revalidated before either route can succeed.

**Alternatives Considered / Tradeoffs**
Treating an IAM entity grant as a node grant would expose every file in scope and
erase per-directory differences. Letting ACL grant an action absent from IAM would
let a file owner manufacture organization authority. Using one broad file_view
check for every mutation would make create/delete/share indistinguishable. The
two gates cost additional bounded reads but preserve explicit revocation and the
approved distinction between visibility scope and access mode.

**Scope / Status**
Implemented for the synchronous operation service and tested under race. The
permissions are registered in the shared catalog; no public file HTTP endpoint
is exposed yet. Platform/account entry, ACL editing/delegation, sharing and Web
live data remain pending.

## D52 - Public file routes expose files and directories, never nodes

**Date** 2026-09-18

**Decision / Reason**
`Node` remains the private persistence abstraction shared by files and directories.
The public HTTP contract uses `/files/{file_id}` and
`/directories/{directory_id}` so the resource type and accepted identifier are
unambiguous. Original bytes are the explicit `raw` representation at
`GET /files/{file_id}/raw`, following the useful convention in the archived
Spring Boot file API. Roots are independent resources at `/file-roots` and the
current principal's idempotent singleton is `/personal-file-root`.

**Alternatives Considered / Tradeoffs**
Keeping a public node collection leaks the storage model and lets one route claim
two resource types. Using `GET /files/{file_id}` directly for bytes leaves no
unambiguous metadata representation, so `/raw` is retained. Separate file and
directory mutation routes add small handler duplication but allow atomic kind
validation and precise OpenAPI parameters.

**Scope / Status**
Implemented in backend HTTP, generated OpenAPI/client, Web calls and contract
tests. Internal database columns, ACL evaluation and tree algorithms keep node
terminology. Global removal of `/iam` and `/authn` remains a separate proposed
routing migration.

## D53 - Files and directories share one public file resource

**Date** 2026-09-18

**Decision / Reason**
The user rejected D52's separate `/directories`, `/file-roots`, and
`/personal-file-root` resources because a directory is also a file entry. The
public contract is now one `/files` collection: `kind` distinguishes file from
directory and `scope` identifies a root's ownership boundary. Metadata, update,
delete, child listing and raw content are representations or operations on that
same file identity. A GitHub-style root-relative lookup is exposed as
`/files/{root_file_id}/-/{path}`; the explicit root is required because one caller
can see several organizational and personal roots.

**Alternatives Considered / Tradeoffs**
Separate directory URLs duplicate one underlying identity and force the Web
client to dispatch by kind. Bare `/files/-/{path}` cannot identify which visible
root anchors the path. The router uses a catch-all internally for multi-segment
matching while OpenAPI documents a normal `path` parameter; neither router syntax
nor the persistence `Node` name is public.

**Scope / Status**
D53 supersedes D52's route split. Implemented in backend HTTP and authorization,
OpenAPI, generated TypeScript, Web calls, route audits and focused tests. Internal
tree persistence remains unchanged. Global `/iam` and `/authn` migration is still
design-only.

## D57 - Account and organization files share URIs but never authorization context

**Date** 2026-09-19

**Decision / Reason**
Use the same `/files` resources for account-private and organization files. A
request with both `X-Tenant-Id` and `X-Entity-Id` enters the organization path;
a request with neither enters the authenticated Principal's account space. One
header without the other is invalid. The server derives ownership from verified
context, never from a body/query owner selector. GET remains read-only; the account
root is provisioned idempotently by `POST /files` with `scope=account`.

**Alternatives Considered / Tradeoffs**
A second account API would duplicate file semantics and drift. Reusing the current
selected tenant for account files would let organization state influence private
ownership. Account authorization therefore uses a strict owner ACL path without
tenant IAM, while global file IDs are still checked against the current space.
Account audit events use the existing tenant-zero chain and retain Principal ID;
this avoids inventing a second audit subsystem but leaves account events in one
platform chain rather than per-tenant chains.

**Scope / Status**
Implemented in middleware, service, HTTP/OpenAPI, generated Web client and
`/account/files`. Focused lifecycle, cross-space, incomplete-header, race and
desktop/mobile browser route tests pass. ACL management, sharing, organization
root recovery, root-level concurrency fences and real MySQL/PostgreSQL acceptance
remain out of scope for this decision and prevent production-readiness claims.
# Recycle Bin Restore Location Disclosure And Rename - 2026-09-19

- Decision: return the original restore location as optional structured path
  segments only when the caller can traverse the full active path from its file
  root to the original parent. Omit it on any missing, inactive, malformed, or
  unauthorized segment.
- Reason: trash read access must not reveal denied organization directory names;
  structured segments also preserve names containing separators and let Web
  localize known root labels.
- Decision: distinguish destination-name collision from stale revision/tree
  conflicts. A collision is retryable by supplying a new name on the existing
  file update resource; no restore-command route or persistent conflict record.
- Scope: recycle-bin list and restore only. Existing delete semantics, ACL rules,
  historical trash placement retention, and copy/move behavior are unchanged.
- Status: implemented and verified.

## D58 - File preview and editing remain separate versioned capabilities

**Date** 2026-09-19

**Decision / Reason**
Propose one MIME-first file presentation classifier for icons and preview/editor
eligibility. Isolate pre-1.0 `open-file-viewer` behind a dynamically loaded local
adapter and pass it only an already-authorized Blob. Preview uses the existing
download permission because receiving preview bytes is not meaningfully weaker
than downloading them. Text/code editing is a separate lazy Monaco mode. Each
save stages a new immutable Object, appends the existing Version model, and CAS
advances the file head; no history or editor-session table is introduced.

**Alternatives Considered / Tradeoffs**
Passing the raw URL cannot include required tenant/entity headers and invites PDF
iframe fallback into an authenticated URL. Loading every viewer plugin exposes
immature parsers: 0.1.47 has an open DOCX injection report and archive expansion
has no resource budgets. Phase 1 therefore enables only reviewed common formats.
CodeMirror remains appropriate for compact form fields; Monaco is proposed only
for the distinct IDE-like full-file editor and is loaded on demand. Blob preview
is simple and authorized but requires a size limit until range delivery exists.

**Scope / Status**
Design proposed in `apps/frontend-web-admin/docs/file-preview-and-editing-design.md`.
Awaiting user confirmation. No application dependency, runtime route, schema, or
source behavior has changed.

## D59 - Image rows use bounded authorized thumbnails

**Date** 2026-09-19

**Decision / Reason**
Browser-renderable images in active file listings load as thumbnails through the
existing authorization-aware raw client. Loading is near-viewport, cancellable,
limited to 8 MiB before buffering, and falls back to the category icon on any
load, limit, or decode failure. Account and organization headers remain derived
from request context.

**Alternatives Considered / Tradeoffs**
Direct raw URLs cannot carry mandatory tenant/entity headers. Eagerly buffering
every image in a 100-item page is wasteful. A generated thumbnail API would give
stronger decoded-pixel and bandwidth bounds, but adds backend conversion, cache,
storage, invalidation, and format policy outside this requested slice.

**Scope / Status**
Approved by the user and in implementation. Server-generated thumbnails remain a
future scale hardening item.

## D60 - Office editor candidates require format and licensing gates

**Date** 2026-09-19

**Decision / Reason**
Do not directly embed PPTist: it is an AGPL-3.0 complete Vue application rather
than a packaged renderer, and advertises roughly 85%+ PPTX import fidelity.
Univer's OSS core is Apache-2.0 and embeddable, but its current capability matrix
places Office import/export and mature slide support in commercial Pro. Either
candidate therefore requires an explicit licensing decision and real document
round-trip acceptance before it can save versions in this product.

**Alternatives Considered / Tradeoffs**
PPTist may still be viable with commercial permission or a separately operated
AGPL-compliant service. Univer Pro is architecturally closer to this React app
and spans sheets/docs/slides, but introduces commercial licensing and a large
office runtime. Neither changes the safe fallback in Phase 1.

**Scope / Status**
Univer remains excluded until Pro licensing is approved. FortuneSheet is approved
for implementation only after bounded ZIP preflight and termination controls.
This decision was superseded by D61 after the user required one solution to cover
all common Office families.

## D61 - External Office viewers replace partial open-source Office editors

**Date** 2026-09-19

**Decision / Reason**
Do not integrate PPTist, Univer OSS, or FortuneSheet. None reliably covers all of
PPT/PPTX, DOC/DOCX, and XLS/XLSX. Use Microsoft Office Viewer as the default
Office handler and Google Viewer as the alternate `Open with` choice.

**Alternatives Considered / Tradeoffs**
PPTist is presentation-only and AGPL-3.0; Univer's needed Office capabilities are
commercial; FortuneSheet is spreadsheet-only. External viewers provide broader
compatibility but disclose file bytes to a third party and require public HTTPS.
They may receive only an explicit short-lived, read-only, version-bound,
revocable and audited URL from the shared file gateway. Tenant policy may disable
the transfer; authenticated raw URLs and localhost URLs are never forwarded.

**Scope / Status**
Approved. Design updated; public token gateway and handler UI remain to be
implemented after the active route-contract migration.

## D62 - Public routes and Swagger groups follow resource prefixes

**Date** 2026-09-19

**Decision / Reason**
Remove `/iam`, `/authn`, and other implementation-domain prefixes from the
pre-production public API without compatibility aliases. Public paths use
resource nouns. Every application OpenAPI operation has exactly one lowercase
kebab-case tag equal to its first path segment, so the same resource prefix is
always grouped together.

**Alternatives Considered / Tradeoffs**
An IAM prefix can be a legitimate gateway namespace for an independently hosted
service, but this application exposes one API and IAM is not a resource. Deeply
nesting globally identified Tenant/Entity/Department resources would duplicate
ownership and enlarge authorization paths, so they remain top-level resources;
mandatory headers keep the request authorization boundary explicit.

**Scope / Status**
Implemented. Includes route declarations, action subresources, avatar resources,
menus, Web clients, test manifests, generated OpenAPI and a repository-wide
composed-contract gate. Core migration 16 upgrades existing built-in menu routes
from the retired `/iam/...` locations without rewriting custom labels or
translations.

## D63 - All durable uploaded content uses governed physical and logical file layers

**Date** 2026-09-19

**Decision / Reason**
All durable runtime uploads, including file-manager content, avatars, commerce
images and business attachments, use the shared physical Object and logical
Node/Version layers, validation, authorization, audit, retention and reference
safety. Source assets, deployment configuration, keys, backups and logs remain
in their proper infrastructure. Physical objects carry no business ownership;
logical roots carry the lowest verified ownership scope.

Scopes are system, tenant, entity, department, tenant-personal and account. The
server derives the narrowest scope from the resource; callers cannot choose a
broader owner for convenience. User-managed files may exist without a domain
binding. Resource-managed files require an atomic registered-purpose binding and
are hidden from ordinary directory listings by default while remaining visible
in their authorized domain UI and lifecycle reconciliation.

Platform-wide logical/physical file viewing is deferred. The current phase adds no
global file API, Web page, or placeholder platform permission. A platform
administrator's ordinary account `/files` request never changes into a global
query. If global viewing is requested later, metadata inventory and actual content
reading require a new design and separate permissions and auditing.

**Alternatives Considered / Tradeoffs**
Putting every business asset into visible directories makes the file manager noisy
and permits moves/deletes to break business resources. Separate avatar, commerce
and attachment stores duplicate security and cleanup. Object ownership is also
rejected because one immutable object can back multiple logical versions/scopes.
A registered binding ledger adds schema and reconciliation cost, but is necessary
to prove resource-managed lifecycle and prevent long-lived unowned files.

**Scope / Status**
Confirmed product direction; detailed proposal is section 23 of
`apps/backend-api/docs/file-management-design.md`. The binding and system-scope
foundation is implemented locally. Existing and new avatar uploads now use
logical resource-managed files; startup migration adopts legacy bytes without a
copy and is idempotent. Global file management is explicitly outside the active
implementation scope.

## D64 - Avatars are account-owned resource files

**Date** 2026-09-19

**Decision / Reason**
Store current and candidate avatars as account-root File Nodes and Versions with
registered `principal.avatar` or `principal.avatar-candidate` bindings. Reads
authorize the active binding and verified account owner rather than trusting a
storage key. Candidate promotion rebinds the same logical file; account erasure
revokes every active binding before the existing object collector reclaims bytes.

**Alternatives Considered / Tradeoffs**
Keeping direct avatar storage writes would leave durable bytes outside the shared
file lifecycle. Copying legacy bytes would duplicate storage and complicate
rollback. Legacy objects are therefore checksummed and adopted in place. New
accounts have no stored default avatar until one is explicitly set; presentation
uses the existing empty-avatar fallback instead of creating ungoverned bytes.

**Scope / Status**
Implemented and verified across the Files and Identity transaction tests, race
tests, three-dialect migration matrix and full backend regression. Commerce media,
general attachments and a platform-wide inventory remain future consumers of the
same registered-purpose contract, not part of this change.

## D74 - Current identity is discovery, not a parallel resource tree

**Date** 2026-09-23

**Decision / Reason**
`GET /me` is the only current-principal alias. It lets a newly authenticated
client discover its principal ID when login has not supplied one. Once known,
self-service and authorized administrators use the same
`/principals/{principal_id}` profile and child-resource contracts. Tenant,
entity, and department contexts never own `me`; target identity is selected by
the principal ID and access is decided by authorization, field filtering,
fresh-authentication requirements, and audit.

Credential-bound authentication transactions that do not yet have a principal
ID remain top-level transaction resources. This exception does not authorize a
second `/me/*` account tree or `/user/current/*` aliases.

**Alternatives Considered / Tradeoffs**
A parallel `/me/*` tree is convenient for self-service clients but duplicates
handlers, schemas, tests, and security behavior and prevents one uniform admin
contract. Putting `me` below tenant/entity paths falsely models ownership and
uses URL shape as a security mechanism. Requiring a principal ID adds one
post-login discovery read for clients that do not already receive the ID, but
keeps every later resource canonical.

**Scope / Status**
Confirmed design rule. This supersedes D65's `/me/*` hierarchy and D66's nested
`/.../me/...` projections. The complete OpenAPI migration remains proposed and
no runtime route is changed by this decision record.

## D75 - Public routes require a resource semantic contract first

**Date** 2026-09-23

**Decision / Reason**
No application URI is approved from an existing handler, table name, foreign
key, screen, or current path alone. The resource must first define its meaning,
direct owner, identity, lifecycle, canonical representation, and authorization
boundary. Route conclusions are labeled `MODEL_CONFIRMED`,
`CONTRACT_CONFIRMED`, or `PRODUCT_DECISION_REQUIRED`; an unresolved field blocks
the canonical route recommendation.

Path parameters identify the resource named by their collection. Database
composite keys may support a relationship identity but do not automatically
define the public API. Atomic workflows may affect several resources but must
name and return those resources honestly. Optional context does not establish
ownership.

A globally identified resource may deliberately expose both its short item URI
and a direct-owner scoped item URI. Both addresses are one resource contract:
identical schema, version, authorization, mutation, audit effect, and service
implementation. The scoped form additionally verifies the parent relationship.
This is not permission for compatibility aliases, transitive ancestor chains,
or owner-specific behavior behind the same noun.

**Alternatives Considered / Tradeoffs**
Designing from existing URLs is faster but preserves semantic collisions such
as tenant principal routes that list through membership and mutate global
identity. Designing directly from tables leaks storage choices. The semantic
gate adds review work, but makes incorrect assumptions visible before they
become public compatibility obligations.

**Scope / Status**
Confirmed review rule. This supersedes D66's unverified department-owner
example because the current model requires `tenant_id` and makes `entity_id`
optional, and supersedes D62's first-segment Swagger grouping rule. Documented in
`apps/backend-api/docs/http-resource-routing-design.md`. Runtime route changes
remain pending explicit approval of the revised OpenAPI proposal.

## D76 - Files and menus are resources with mount points, not a hierarchy to merge

**Date** 2026-09-24

**Decision / Reason**
The organization has levels -- tenant, entity, department -- and a person
beside them. Files and menus are not properties of those levels; they are
resources that can be mounted at any of them. A mount point is addressed by its
own collection, and a view reads exactly one: the tenant view lists the
tenant's files, the entity view the entity's, the department view the
department's, and a person's own view lists files that belong to no tenant at
all.

Seniority does not mean one collection answers with the levels beneath it. It
means an administrator may open the entity and department views as well, which
authorization already decides. Merging the levels into one answer puts a
navigation decision inside a resource and makes the collection mean something
its URI does not say.

A person is not a level of the organization. The tenant-independent personal
drive (`account` scope) therefore belongs to no tenant view.

**Alternatives Considered / Tradeoffs**
Containment -- a tenant collection answering with its entities' and
departments' roots -- was built and reverted. It is convenient for one screen
that wants every reachable root in a single request, and it reads plausibly
because a department's files really are stored inside the tenant's space. What
it costs is that every collection then means "everything under here that you
may see", which is a query result rather than a resource, and the caller can no
longer ask for one mount point without a filter. The earlier `context` scope
from `d35da67` was that same merge, and it also swept members' per-tenant
personal directories into an answer any tenant member could request.

The cost of mount points is that a screen showing several levels issues one
request per level. That is a client concern and is visible, rather than a
server-side merge whose membership rule nobody can see from the URI.

**Scope / Status**
Confirmed design rule for files, and for menus when their contract is revised.
`/entities/{entity_id}/files` listing only that entity's roots (`346a809`)
follows from it. The remaining work is on the client: each view reads its own
mount point, which needs a department view the router does not have yet.

## D77 - One canonical URI, and a natural key unless it is ambiguous

**Date** 2026-09-24

**Decision / Reason**
A resource has one address. Aliases cost a second operation id, a second set
of tests and a second authorization assertion, and they make every reader ask
which one to use. The only aliases are discovery entries for a client that
cannot know an id yet: `GET /me` and `GET /sessions/current`. That is the
rule, rather than a list of exceptions -- a browser holding only a session
cookie genuinely cannot name its session.

An item is addressed by its natural key under its collection. The test is
whether a client can build the address from what it already holds and whether
the result names exactly one resource. Segment count is not the test:
`(resource, field)` is two segments and is still a natural key. An id is
minted only when no natural key passes that test.

Tenant membership keeps `(tenant_id, principal_id)`, so its item is
`/tenants/{tenant_id}/memberships/{principal_id}`. Authorization relationship
gets an id, because `subject_relation` and `entity_id` may be empty and an
empty value is a legitimate part of the identity, so no address a client
constructs is unambiguous -- which is why today's eight-selector DELETE
silently reports no change when one is omitted.

**Alternatives Considered / Tradeoffs**
Minting an id everywhere is uniform and removes the judgement, at the cost of
a primary-key rebuild on a populated table and an indirection over an
identity that already exists; the manifest already rejected that for field
policy. Keeping tuples everywhere avoids migrations but leaves relationships
addressed by query selectors, which is a filtered delete wearing an item's
clothes. Dual addressing was offered as navigation convenience; it is
declined because the generated document has only the short form today, so
this is a decision not to build rather than a removal.

**Scope / Status**
Confirmed design rule. It supersedes D75's paragraph permitting a resource to
expose both a short and an owner-scoped item URI, and it replaces D74's
wording that `GET /me` is the only current-principal alias. Recorded in
`apps/backend-api/docs/proposed-api-manifest.md`. The relationship id needs a
migration plan for two tables before it is implemented; no runtime route
changes with this record.

## D78 - The web client must take its URLs from the contract before routes move

**Date** 2026-09-24

**Decision / Reason**
`apps/frontend-web-admin` builds request paths as strings and checks them
against nothing. The generated types are imported for `components` only; the
`paths` type, which is where a moved route shows up, is imported by no file.
Regenerating the types after a route migration that had already shipped moved
14,826 lines and broke no build, which is the evidence: typecheck catches a
renamed field and says nothing about a renamed route.

The proposed contract moves roughly 114 operations. Slicing that work by
domain and calling each slice verified because `bun run typecheck` passes
would be claiming a check that does not exist. The client must therefore take
its URLs from `paths` — request helpers that accept a path template and its
parameters, the shape `openapi-fetch` uses — before any route moves.

**Alternatives Considered / Tradeoffs**
A static inventory of the client's path strings was measured and rejected:
paths are composed at runtime (`/tenants/${id}` plus a suffix the caller
passes), so no regex recovers a whole path, and a checker that silently
skipped what it could not parse would be worse than none.

A runtime assertion in the shared request layer would catch only the calls a
test happens to exercise. Partial coverage presented as a gate is the failure
this record exists to avoid.

Converting is the real cost: 442 path strings across a 2,749-line client, and
every call site changes shape because a template plus parameters replaces an
interpolated string. It is mechanical and large, and it is cheaper before the
routes move than after, because afterwards every site is wrong in two ways at
once.

**Scope / Status**
Confirmed prerequisite for the route migration, not yet implemented.
`bun run check:api-types` (`755f533`) keeps the generated types honest about
the document meanwhile; it does not make the client safe.

**Implementation - 2026-09-25**
Implemented with `apiRoute`/`pagedRoute` importing generated `paths`. Every IAM
transport accepts only a branded address carrying its supported method;
template, required path/query parameters, parameter types and query enums are
checked at construction. Mounts are explicit, including files and audit scopes;
offset pagination admits only GET operations declaring offset/limit. Existing
fetch, error/envelope handling, uploads and download verification are reused.
Audit filters now use generated query objects. URLSearchParams standardizes
query serialization (spaces become +); identifiers still use encodeURIComponent,
and root-relative file paths preserve their slash separators.

Compiler diagnostics exposed existing archive aliases and POST anonymization;
the client now uses published attempts/verifications/content and PUT. The file
collection contract omitted inherited query fields because its embedded Go input
was unexported. Exporting FilePageInput repairs Huma discovery; regenerated
documents include after/limit/state for all collections. This is a necessary
contract repair, not a new route migration. Twelve negative compile assertions
replace the old permissive literal scan. All runtime tests remain covered.

Status: D78 implemented and verified. Public API migration has not started.

## D79 - Execute evidence-backed API optimization, not the whole URI inventory

**Date** 2026-09-25

**Decision / Reason**
User authorized an optimized implementation plan, second review, implementation
and real E2E. The full manifest failed its implementation review. Execute the
bounded specification in `apps/backend-api/docs/api-optimization-implementation-2026-09-25.md`:
identity reads use unchanged GET /me; real BrowserSession discovery uses
Cookie-only GET /sessions/current; global /sessions/{session_id} reads/revokes
only the authenticated owner's active item. Current logout and existing step-up
move under /sessions/current without aliases. Reuse ListSessions/RevokeSession,
logout transactions, profile projection, CSRF and existing security services.
Correct the already-invalid personal multipart scope and remove its unused
client entry. Preserve account drive and historical data.

**Alternatives Considered / Tradeoffs**
Whole principal unification, two-request onboarding, permission catalog removal,
generic authentication transactions, response/If-Match rewriting, destination
file-operation migration and relationship ID backfill are rejected for this
optimization: no required capability or demonstrated defect justifies their
behavior/data costs. Existing self-only /me/sessions collection is retained;
adding a principal selector provides no needed capability. These are decisions
against implementation here, not an automatic next-task list. D74/D76/D77 design
principles remain recorded; D77's relationship-ambiguity rationale is corrected
by the review, while its ID design direction is preserved.

**Scope / Status**
Implemented and accepted. Real SQLite/Chromium E2E 14/14, browser fixture
regression 74/74, archive desktop/mobile PASS, backend authn/files suites PASS,
frontend gates and generated drift checks PASS. 206 paths / 296 operations;
the added operation is session item GET. No database migration. Evidence and
limits are in the execution plan. Historical documents explicitly defer to it.

## D80 - Swagger groups belong to the final path prefix

**Date** 2026-09-26

**Decision / Reason**
The user explicitly requires first-path-segment groups, regardless of the
suffix or workflow. Implement through Huma OnAddOperation in the shared builder
after mount expansion. Tenant files belong to tenants, principal files to
principals, and globally addressed file items to files. Exactly one tag.

**Alternatives Considered / Tradeoffs**
Workflow grouping and suffix grouping violate the latest instruction. A central
framework hook covers typed routes, expanded mounts and adapter registrations.
Small prefix groups and a large tenants group are accepted consequences of the
specified ownership rule; no artificial grouping prefixes are introduced.

**Scope / Status**
Supersedes D75's exception to D62's prefix-grouping rule. Prefix grouping is
implemented; full export and live browser assertions pass. Complete explicit
principal migration was subsequently authorized and implemented under D81.

## D81 - Complete explicit principal addressing

**Date** 2026-09-26

**Decision / Reason**
User authorized "继续，搞到位": migrate all 41 global `/me/*` operations and four
nested tenant/entity projections to `/principals/{principal_id}/...`. Keep only
`GET /me` identity discovery and the existing `/sessions/current` resource.
Explicit self registration validates the verified actor before any handler:
invalid canonical ID 422, foreign ID 404. No cross-account privilege is added.

**Alternatives Considered / Tradeoffs**
Keeping aliases or migrating files alone leaves the requested unified resource
tree incomplete. Applying a self-only guard to every principal URL would break
the existing authorized cross-account avatar reader. Therefore scope is declared
explicitly, and avatar bytes move to `/avatar/content` to avoid colliding with
settings GET. Historical stored references are normalized when returned.
Restricted MFA enrollment keeps its original verifier and error behavior; the
verified password login result provides `subject` for its explicit address.

**Scope / Status**
Implemented. 296 operations retained; exactly 46 paths change (45 principal
migrations plus avatar bytes). Methods, request bodies and credential schemes
preserved. No database/file migration. Web page `/me/*` navigation is unchanged.
Evidence: docs/api-routing-inspection-2026-09-26.md; real SQLite E2E15/15,
browser regression74/74, live demo5/5 and generated inventory/drift checks PASS.
This supersedes D79's choice to retain `/me/*`; no optional API redesign follows
automatically from this decision.

## D82 - Qualified Public Resource Names And Unified Workflow Roots

**Decision / Reason**
User required a complete inspection after identifying ambiguous groups/positions
and split email/invitation Swagger groups. Names follow actual domain meaning:
principal-groups, job-positions, membership-invitations, role-access-requests,
role-access-reviews, authorization-relationships and authorization-scope-model.
Invitation acceptance is /membership-invitations/acceptances; email issuance is
/email-verifications/requests and consumption remains /email-verifications.
Tenant collections and principal projections use the same canonical names.

**Scope / Tradeoffs**
The change intentionally replaces public paths; retired URLs answer404. It does
not change database/domain names, SCIM protocol Groups, permission discriminators,
operation IDs, payloads, security or UI navigation. Polymorphic company/merchant/
store entities keep their established domain name. Final URI first-prefix tags
remain mandatory, so tenant-contained collections belong to tenants.

**Evidence / Status**
Implemented; docs/api-resource-naming-audit-2026-09-26.md records the exhaustive
resource dispositions. 296 operations preserved, exactly37 addresses changed;
all component schemas and every operation field except tags compare equal.
Real SQLite/Chrome E2E18/18, fixture UI74/74, live selectable demo5/5 and actual
Swagger309 prefix sections PASS. Final independent integration suite and generated
contract drift gates PASS; self-review and requirement acceptance PASS.

## D83 - Remove Independent Positions And Preserve Existing Grants

**Decision / Reason**
User approved removing job positions after reviewing the overlap with departments
and roles. Remove the independent feature from runtime APIs, Web routes/forms,
role assignment selectors, relationship subjects, authorization sources, access
reviews and the permission catalog. Existing principal groups remain available.
This supersedes D82's retention of the job-positions resource.

**Implementation Plan / Review**
Trace every consumer first; retire the runtime feature; add module-owned data
migrations; regenerate public contracts; verify upgrades and authorization with
real databases and browsers; restart the demo environment; review and deliver.
The plan uses existing groups and module migration milestones. No new domain
concept, database reset, dependency or automatic group-management privilege.

Organization v11 converts existing positions into static groups, retaining IDs,
tenant, status, order, version, timestamps, membership windows and role bindings.
Names use "Group <id>: <old name>" to avoid ordinary group-name collisions.
An ID conflict stops the upgrade without overwriting either record. Core v17
changes relationship subjects, detaches custom children before deleting retired
menus, and removes the five obsolete position permissions. Governance v5 changes
existing review items to group grants. Core/governance depend on organization v11.
Rollback retains the group representation; it never resurrects duplicate grants.
Historical SQL/models and empty legacy tables support upgrade/schema checks only.

**Verification Evidence**
TestIAMPositionRetirementUpgrade checks both deployment prefixes, ID conflicts,
active/disabled state, membership windows, role bindings, both relationship tables,
pending review items, custom child menus, privilege retirement and repeat upgrades.
SQLite, embedded PostgreSQL, MySQL dialect engine and real MySQL 8 passed. The
MySQL nullable-parent predicate uses COALESCE and a materialized derived table;
this avoids the dialect engine's faulty self-join update and MySQL error 1093.
Reverse-DAG rollback tests first remove core v17's organization dependency.

Chrome/SQLite E2E: 18/18, including independent group authority, last-administrator
protection, relationship/review workflows, retired position routes returning 404,
position assignees/subjects returning 422, and no position schemas/catalog/menus.
Artifact: apps/backend-api/.temp/sqlite-lifecycle-VZjgyl/report.json.
Fixture Web regression: 72/72 (the two retired position-page cases were removed).
Selectable demo accounts: 5/5; live/rendered Swagger: 301 prefix-grouped operations.
Published OpenAPI: 288 operations, eight position operations removed.
Backend coverage: 91.4% overall; core 91.2%, organization 90.4%, governance 92.0%.
Frontend workspace typecheck/lint/tests/build and generated contract drift passed.
Full backend unit/integration suites, lint, formatting, coverage and OpenAPI
drift gates passed. Migration-status acceptance includes upgrade/down/up across
all three engines and both prefixes. Direct execution; no delegation.
Self-review and final requirement acceptance: PASS. No optional follow-on change.

## D84 - Consolidate Role Permission Reads; Retain GUID HTTP APIs (2026-09-26)

Decision: User explicitly retained GET /guid and GET /guid/{count} and approved
the other confirmed review optimization: remove GET /roles/{role_id}/permissions.
Keep GET /roles/{role_id}/permission-grants, permission writes/conditions, and
internal ListPermissions used by authorization. No database migration.
Reason: the removed read merely projected permission_code from full grants;
the Web role page already consumes grants including conditions.
Alternative: maintain two read contracts. Rejected as redundant for current clients.
Tradeoff: external callers of the removed read must consume permission-grants
and extract permission_code. No compatibility alias that preserves duplication.
Scope: one public operation, its unused Web wrapper/generated contracts, exact
route manifests and browser-script consumers. GUID remains an intentional API.
Evidence: published operations 288 -> 287; retained operation definitions identical;
backend full unit tests, frontend typecheck/lint/build and 46 client tests PASS;
OpenAPI/client drift PASS; real SQLite/Chrome lifecycle E2E 18/18 PASS in
.temp/sqlite-lifecycle-QXgwdq/report.json. Live retired GET 404, GUID reads 200.
Status: accepted; self-review and requirement acceptance PASS. No further deletion.

## D87 - Navigation Scope And Source Consistency (2026-09-26)

Decision: user authorized continuation of the proposed correction. Add platform
and personal entries to the existing workspace switcher. Choose the home route
from global permissions: tenant_view -> platform tenants; platform_administer ->
platform audit; otherwise selected tenant/personal. Explicit login return URLs
are preserved. Platform shell reads global permissions and omits tenant menu
reads/context labels. Render tenant catalog hierarchy directly; retain configured
access-request placement and supplement missing self-service links once.
Reason: tenant menu management did not reflect hardcoded sidebar wrappers or
fallback placement; platform authority was obscured by tenant-first navigation.
Alternatives considered: new platform menu persistence/API or role-name routing.
Rejected because existing fixed routes and global permission APIs suffice.
Tradeoffs: platform/entity/personal and tenant supplemental entries remain shared
fixed catalogs. A lower read-only manager list was added, then removed following
explicit user authorization because it was not actionable menu management.
Tenant administrators cannot mutate platform navigation. Existing server guards
remain authoritative; discoverability changes do not grant extra permissions.
Scope: current sidebar, workspace switcher, home routing, menu manager boundary
and actual role browser regression. Existing closure tracking page gets an entry
under its verified platform_administer guard; no new business page/API/schema.
Status: IMPLEMENTED and acceptance/self-review PASS. Frontend gates PASS; actual
20-case Chrome audit plus four final Chinese platform-switcher cases PASS. The
platform switcher omits tenant entity loading until tenant scope is entered.
Existing closure page copy corrected to its administrative review purpose.
Reopened: user requires full sidebar/manager correspondence including built-ins.
Built-in editability choice pending; do not treat the removal as completion of
that requirement. Future mutation E2E must be isolated from user acceptance data.

## D88 - Business-Neutral IAM Ownership (2026-09-26)

Decision: global principals, tenant memberships and tenant/entity role assignments
are distinct. Tenant is the isolation boundary; entity is a business unit within
one tenant. Platform administrator authority is not an editable tenant role.
Reason: user requires the same IAM foundation for multiple business domains,
including merchant/stores and headquarters/companies, without implementing
these domain modules now.
Alternatives considered: account types per tenant/business; one-tenant-per-account
uniqueness; representing platform administrators as ordinary tenant roles.
Tradeoffs: tenant creation quotas belong to the eventual business admission policy,
separately from joining tenants. Registration is not an implicit creation grant.
Current tenant routes require active membership even for a platform administrator;
do not silently change that admission boundary while correcting UI scope labels.
Scope: generic identity/authorization invariants and workspace ownership; no new
business module, creation quota or per-business database schema is authorized.
Workspace requirements: personal views manage the global account; platform views
manage platform resources; tenant views manage the selected tenant; entity views
manage a selected entity within that tenant. Menus reflect those resource scopes
and effective server permissions, never an account-wide tenant-admin label.
Entity is not a department or role: departments describe organization structure,
roles describe authority, and entities own business resources. A tenant grant
must not implicitly become an all-entity grant without an explicit policy.
Creation policy may vary by business domain (one or many tenants per account);
it must remain separate from membership and from generic IAM identity constraints.
Status: CONFIRMED BY USER and IMPLEMENTED. Generic account/tenant/entity
cardinality preserved; distinct workspaces and scoped navigation verified.

## D90 - Persisted System Navigation and System Audit (2026-09-27)

Decision: system administration includes menu management, audit events and audit
governance. Rename system administrator assignment to System access grants to
distinguish global authorization from account management. Persist system menus
through the existing menu engine; actual sidebar reads their effective tree.
Reason: user acceptance identified missing foundational functions and misleading
labels in the system workspace. Static navigation cannot reflect menu edits.
Alternatives considered: another static catalog or a separate menu/audit engine.
Tradeoffs: an internal reserved __system__ organization root has a generated ID,
no tenant membership and no tenant role. Atomic seeding runs at module Start after
GUID startup, preserves existing records and uses the existing unique slug.
Canonical page permissions remain mandatory even after menu binding edits.
System audit uses only partition0, with separate view/export permissions and
actual operator attribution during dedicated account views.
Scope: explicit /system/menus, menu-catalog/navigation and system audit adapters,
shared management pages, navigation and regression/E2E acceptance.
Status: IMPLEMENTED and ACCEPTED. Full browser38/38, final focused17/17,
backend/frontend/contract gates and real MySQL8 PASS. Root self-review and final
requirement acceptance PASS; retained demo rebuilt and visually verified.
This supersedes D87's static system-navigation alternative for the system scope.

## D89 - Operating Platforms and Dedicated Account Views (2026-09-26)

Decision: the same deployment may contain separately operated platform scopes,
each with different menus/views and department/role authorization. Separate system
administration from operating-platform and tenant administration. Principals remain
global; scoped grants do not turn into permanent account types.
Reason: user explicitly clarified the operating-platform requirement and reaffirmed
dedicated view tokens with real operator attribution and target-account invisibility.
Implementation interpretation: match the selected account's logged-in workspace
using its current permissions and data scope. The dedicated token carries the
operator separately; it does not silently add operator grants to the target.
System-level management is a separate authority and workflow.
Alternatives considered: one global platform label; ordinary target browser login;
overwriting target cookies; silently applying system privileges to a target view.
Tradeoffs: default views are read-only, explicit writable selection uses a new
immutable record, and actual mutations remain visible in business data. No target
login session/notification is created. View records belong to system management,
not the target's personal/session/token catalog.
Scope: original administration/account-view goal; no business module or quota.
Status: IMPLEMENTED; engineering acceptance PASS. System/operating grants,
dedicated token lifecycle, frontend scopes and target views are active. Real
MySQL8 full gate, PostgreSQL16 runtime and frozen performance budgets, migrations,
35-flow lifecycle and14-flow desktop/mobile views PASS. Ready for user acceptance.

### D89 Implementation Review Contract

Decision: reuse existing owner partitions with immutable organization-root
scope_kind tenant/platform/system. Existing rows remain tenants; shared physical
tenant_id keys preserve the existing directory/RBAC engine. Separate explicit
system and selected-platform checkers/routes from ordinary tenant admission.
Current global platform administrator storage represents legacy system authority;
never fan those grants out into operating platforms.
Reason: independent architecture/security review confirmed global CheckPlatform
and tenant authority expansion cannot implement operating-platform isolation.
Alternatives considered: duplicate organization engines; representing platforms
as customer tenants; imposing a single platform parent for every business tenant.
Tradeoffs: migration preserves IDs and storage relationships, but every admission,
catalog and role write must verify the owner kind. No business tenant/platform
association or quota is introduced by this change.
View boundary: scope is immutable and checked against resolved resource owners.
Tenant views can reach their own entity descendants; entity views cannot reach
parent management; system views cannot bypass tenant authority; personal views
bind the target. Operator assurance is never copied into target grants.
Status: review contract implemented; architecture/security and final acceptance
reviews PASS. Scope admission and actual actor attribution gates are verified.
Evidence: administration-account-view-plan-2026-09-26.md. Token issuance active.

## D86 - Hierarchical Menu Management (2026-09-26)

Decision: extend existing menu PATCH with structured atomic placement; retain
parent_id, existing tenant policy lock, audit runner and menu_update authority.
Left directory tree omits leaves. Root selection initially lists every hierarchy;
branch selection initially lists immediate children. Right-hand rows expand and
collapse descendants in place without changing the selected left directory.
Drag supports before/inside/after/root for leaves and complete subtrees. Up/down
enters an adjacent branch at its last/first child and exits before/after its
parent at the sibling boundary. Root boundaries are disabled.
Reason: requested cross-level movement must preserve descendants and commit
parent/order together; independent metadata writes can produce partial movement.
Alternatives considered: extra movement endpoint/permission or target dialog.
Tradeoffs: sibling orders are renumbered under one transaction; arrow movement
follows tree adjacency. Existing metadata edits remain a separate PATCH mode.
Scope: management UI/API, grouped role-view navigation and real role browser E2E.
View-context groups reuse sidebar components; persisted menu hierarchy remains
authoritative. No schema migration or authorization expansion. Four retained
demo groups organize existing IDs/routes/grants only in the local demo tenant.
Status: IMPLEMENTED; final self-review and acceptance review PASS. Five personas
on desktop/mobile, platform/entity/personal contexts, movement persistence and
actual denied mutations verified. Evidence: menu-tree acceptance record.

## D85 - External Identity Ownership Remediation (2026-09-26)

Decision: ADOPTED and implemented following user authorization. Preserve global principals and
tenant integration ownership; prohibit email-based reuse of an existing principal;
require independently verified binding and security-source continuity; reserve
the shared SAML signing-key rotation for platform authority. SCIM activation follows
both global and membership status. Tenant contact/display fields remain local;
global profile edits require self-service or platform authority, including human
tenant administration. Global edits do not mirror into local fields.
Reason: confirmed F1-F3 evidence and the human/SCIM shared-profile authority gap.
Alternatives considered: new tenant account model, per-entity providers, per-tenant
signing keys and SCIM-only profile restriction. They either add unrelated concepts
or leave the same authority gap reachable through another existing API.
Tradeoffs: replace the old SAML route; make SCIM username immutable after creation;
change tenant-admin profile-edit authority; quarantine legacy untrusted bindings
and invalidate affected credentials. Independent recovery evidence is required;
unresolved legacy ownership blocks deployment instead of receiving unsafe trust.
Scope: remediation plan W1-W4 and acceptance criteria; implementation authorized
by the user after the final three-role plan review.
Status: ADOPTED, IMPLEMENTED and ACCEPTED. Independent cross-review findings
repaired; final CI, real MySQL gate, 24-flow signed-browser E2E, tenant token
isolation, relevant race checks and five demo-persona menu audits PASS. SQL plan
fixtures reflect approved queries; existing performance budgets remain unchanged.
Evidence: apps/backend-api/docs/external-identity-scope-review-2026-09-26.md and
apps/backend-api/docs/external-identity-remediation-plan-2026-09-26.md.
# System Role Administration Correction - 2026-09-27

Decision: Replace restricted system direct grants with direct system role
assignments on the reserved real system root. Present full superadmin through a
protected role while retaining immutable full authority in administrator records.
Departments organize membership and never automatically grant roles. System role
permissions initially have no conditions or group/directory inheritance.
Reason: user requires comprehensible account, department and role administration;
moving the old grant editor does not supply those relationships or revocation.
Alternatives considered: navigation-only changes, permanent union with direct
grants, tenant-derived system roles. These leave conflicting authority sources.
Tradeoffs: preserve existing restricted grants through transactional conversion;
ordinary new roles cannot grant the legacy platform_administer tenant override.
System authority blocks suspension/erasure until explicitly revoked, including
disabled memberships. Account categories overlap over global human identities.
System shared files have independent singleton ownership and global file actions.
Scope: approved system administration correction, preserving customized menus,
scoped identities and account-view operator attribution. No business-specific
tenant quotas or permanent principal account types.
Status: IMPLEMENTED and VERIFIED. Independent architecture/security reviews and
engineering acceptance PASS. Real browser lifecycle45/45 and focused24/24 PASS
with zero mocked business responses or page errors; frontend234 tests and all
four workspace gates PASS. Normal MySQL/PostgreSQL gates preserve original
performance budgets and246 published migrations. Retained demo refreshed; user
manual acceptance is available. Delivery requires verified commit/push.

## Account View Tab Isolation - 2026-09-27

Decision: every UI account view opens a fresh independent tab with a fresh scoped
credential; only child module memory stores it. Default selection is an admitted
nonpersonal workspace, matching management scope/owner when available. Personal
is a fallback only when no admitted workspace exists. Original tab stays operator.
Reason: user requires concurrent target views without shared tokens or identity
collision, and explicitly prohibits credential persistence. URL propagation is
allowed but unnecessary for the chosen one-shot verified window handoff.
Alternatives considered: shared storage/cookies, same-tab replacement, credential
URL transport, persistence to survive reload. Shared/persistent authorization
violates the requirement; same-tab replacement does not provide parallel views.
Tradeoffs: reload/duplicate/back-forward loses memory authority and fails closed;
noncredential tab markers identify that state. Router and API transport both guard
against operator-cookie fallback. Every view retains live backend permission,
original operator-session, scope, expiry and audited attribution checks.
Scope: frontend handoff/startup/dialog and real browser acceptance; existing
backend issue/revoke contract retained. No credential in storage or URL/history.
Status: IMPLEMENTED and VERIFIED. Architecture/security review PASS, including
independent reload detection when storage reads fail. Final frontend237 tests and
all four workspace gates PASS. Real focused26/26 and full47/47 browser flows PASS
with zero mocked business responses or page errors. Ordinary/storage-disabled
refresh, duplicate/copied handoff, concurrent read/write scopes and individual
exit/revocation verified. Root self-review and requirement acceptance PASS.

## Quiet Windows Execution - 2026-09-27

Decision: use ConPTY (`exec_command tty:true/login:false`) for agent commands and
explicit hidden Windows child-process creation for test/build helpers. Preserve
headless browsers and hidden background services. The user requires no flashing
command windows while using other desktop applications.
Reason: hidden script children alone did not address the execution host console;
ordinary piped shell calls continued to flash. Virtual-terminal mode is available
in the existing tool and does not require a new launcher or dependency.
Scope: repository execution rules and touched test launcher options; no product
behavior changes. Status: APPLIED; all subsequent commands use virtual terminals.

## Unified Access View Objects - 2026-09-27

Decision: reuse /account-views for account, operating-platform and tenant targets.
Account views use the selected account's authority. Organization views retain the
actual visitor identity and scope the token to the selected organization. Dedicated
tokens stay only in each new tab's memory; readonly is the default and writable
requires explicit selection. No owner impersonation or membership creation.
Reason: user confirmed visitor plus typed access object for all three entries.
Admission requires account_view_create and current organization membership, or
protected full system-administrator authority. Creation permission alone does not
grant nonmember access. Only verified exact organization views gain that scoped
system authority; ordinary cookies and account views keep existing admission.
All uses recheck the original session, credential versions, organization activity
and live admission. Audit records actual visitor, typed view object, scope and mode.
Compatibility: legacy requests and stored tokens default to account. Extend existing
token lifecycle, immutable DB record, guards and handoff rather than add a session
system. Refresh loses memory credentials and fails closed without cookie fallback.
Status: IMPLEMENTED and VERIFIED. Full real E2E50/50, affected backend tests,
MySQL8/PostgreSQL16 integration and frontend workspace gates PASS. Plan/review:
apps/backend-api/docs/access-view-implementation-plan-2026-09-27.md.

## Separate Authentication Entrances - 2026-09-27

Decision: expose /login/system, /login/platform, /login/tenant and /login/user.
System/platform expose login only; tenant/user expose registration and password
recovery. Reuse one global principal and existing password/MFA/passkey/session
flows. Selecting an entrance never grants administrator authority or organization
membership. Resolve live permissions/memberships; choose among multiple admitted
organizations, enter a single admitted workspace, deny unassigned privileged entry.

Tenant registration first verifies the personal email, then creates a caller-owned
tenant through existing atomic bootstrap (membership, roles, menus, files, audit).
Deployment iam.tenant_registration_enabled defaults false. No client-selected
owner; all access-view tokens are refused, including writable views. Existing
invitations handle joining; business-specific tenant-count policies are not added.
The user entrance opens the personal file workspace. Public recovery resets the
same global identity and revokes its sessions; it does not manufacture a different
account or administrative identity. Public entrance query values only preserve UI
continuation in verification/recovery messages and accept tenant/user values.

Development enables these flows and captures delivered messages in the ignored
local .temp/dev/mailbox.jsonl; it does not send external email. Production retains
its existing configured delivery and explicit registration policy.

Status: IMPLEMENTED and VERIFIED. Direct architecture/security self-review and
requirement acceptance PASS. Affected backend suites and app composition PASS;
OpenAPI generation/drift and frontend types/lint/237 tests/build PASS. Full real
E2E55/55 (.temp/sqlite-lifecycle-pZ98rn) and final entrance6/6
(.temp/sqlite-lifecycle-yiHcTz) PASS, no mocked business responses/page errors.
Chinese retained demo selector, registration policy and desktop/mobile smoke PASS.
No database migrations or dependencies introduced. No further implementation
or product decision is pending for these entrances.

## System Account Views Never Exceed The Operator - 2026-09-27

Decision: a system-scope account view is admitted only when the operator's own
system authority covers the target's: a full administrator is viewable only by a
full administrator, and every platform permission the target holds must be held
by the operator. Tenant and platform account views keep "target authority" as
decided in Unified Access View Objects.
Reason: review of 346a809..HEAD proved account_view_create alone admitted a
read_write system view of a full administrator, i.e. full authority.
Alternatives: requiring full administrators for all views (rejected: changes the
confirmed support workflow for tenant/platform views).
Scope: core authorizeAccountView (issuance and every use). Status: IMPLEMENTED,
TestSystemAccountViewNeverExceedsOperatorAuthority, mutation-verified.

## Standalone System Permission Reads Take No Fence - 2026-09-27

Decision: CheckPlatform outside a caller transaction reads without the policy
revision fence or FOR UPDATE; inside a mutation transaction it keeps the fence.
One reader (systemPermissions) serves both. Extends D55 to system authority.
Status: IMPLEMENTED; zero-write and fenced/plain agreement tests.

## System Routes Name Their Root owner_id - 2026-09-27

Decision: routes under /system/ that select a tenant or operating platform use
path owner_id, never tenant_id, because tenant_id reads as tenant-gated admission.
The composed-spec test enforces it for every module, plus lowercase OperationIDs.
Organization's two system department routes were renamed accordingly.
Status: IMPLEMENTED.

## Audit Remediation Is A Closed List - 2026-09-28

Decision: the 2026-09-28 scoped IAM audit is remediated as a fixed list (task-state
"IAM Audit Remediation" items S/G/C/N/D/F/P). Completion = every listed item fixed,
each fixed class guarded by a set-wide invariant test that is mutation-verified,
make ci + release-check + full real E2E green, then ONE verification review limited
to the changed code and the listed classes. New issues found outside the list are
recorded as backlog and do not reopen or block this remediation. "Record but don't
change" items of the audit stay unchanged by decision.
Delegated decisions (user: "全部修掉", accepting the audit recommendations):
- Tenant-route principal suspension is refused when the target holds system or
  operating-platform authority (platformAccessGuard) or is an active member of
  another organization root; tenant admins remove membership instead.
- Secret-keyed ephemeral tables may use hash primary keys (exemption to the
  snowflake rule); sessions and passkeys expose a snowflake public ID in the API.
- Create operations return 201 everywhere; every list response uses respx.List.
- JWT audience comes from configuration with neutral default "api".
- pkg/interpreter and its four interpreters are removed (no binary uses them).
- react-leaflet license is a legal question: no code change, reported to user.
Reason: user requires a finite remediation, not open-ended fix/review cycles.
Status: ACCEPTED.

## Delegated OAuth Tokens Carry No Administrative Authority - 2026-09-28

Decision: any token with a client_id (authorization-code, refresh or
client_credentials) is refused with 403 on every Registrar-guarded operation,
in every tenant including the client's own. The only scopes with meaning are
OIDC openid/profile/email, served by /oauth/userinfo; no route declares OAuth
scope support, so no opt-in exists. Account-holder bearer helpers follow the
same rule. Reason: audit S1 proved a tenant-B client token had the user's full
authority in tenant A and on /system. Status: IMPLEMENTED (composed invariant
over 322 operations).

## Per-Operation SCIM Reauthorization Is Kept - 2026-09-28

Decision: SCIM bulk keeps re-checking credential and service account inside
every operation transaction (c7a02b1), so a revocation committed mid-bulk stops
the remaining operations. The per-operation cost is accepted. After the
allocation fix (5932893) the enforced gate passes unchanged on a quiet machine
(scim_bulk_25 median 5.2-5.8 ms vs 8 ms, 2026-09-28), so no re-baseline was
made; the latency gate judges the median.
Rejected: one check per bulk (weakens revocation), a cross-module combined query
(D-ServiceAccountValidator rejection). Known limit: 1 ms rounding lets a 2x
regression of sub-millisecond paths pass; D13 margin unchanged.

## Self-Registration Activation Proves Both Mailbox And Password - 2026-09-28

Decision: re-registering a pending self-registered address replaces its
credential and voids earlier links; completing email verification also requires
the password of the registration being confirmed. Directory/invitation-provisioned
pending principals are never re-credentialed by public registration.
Reason: audit S8 residual - the owner could click an attacker-triggered link.

## List Parameters - 2026-09-28

Decision: an offset-paged collection declares `limit` (default 50, minimum 1,
maximum 200) and `offset` (default 0, minimum 0), answers through respx.List
(items as data, paging in meta.page), and names its free-text filter `search`.
Cursor-paged file collections keep `after` and their own bound (limit 50, max
100) by design: they page a live directory tree whose rows move, not a counted
table. Navigation trees (tenant menus, effective menus) are read whole and
declare no paging parameters; a page of a tree is a tree with missing branches.
Alignment (audit C13): personal tokens 20/100 -> 50/200; todo q -> search,
20/100 -> 50/200, offset default 0; the two menu routes drop limit/offset
(tenant menus ignored them; effective menus defaulted to "all").
Guard: composed-spec invariant TestComposedOpenAPIPagesListsOneWay.
Status: IMPLEMENTED.

## Business Error Codes - 2026-09-28

Decision: a failure the client must tell apart from another with the same HTTP
status carries a business code in the existing envelope `code` (100000+), via
respx.ErrStatus (respx.Err with an explicit status and optional data). The
message is always a namespaced, localized key; the client branches on the code,
never on message text. Each module owns a block: authn 101000-101999 (101001
captcha_invalid), files 102000-102999 (102001 restore name conflict, 102002
state changed, 102003 operation conflict, data = conflict frontier). The web
client mirrors the codes in `businessCode` (iam-api.ts). The error-key gate has
no allowlist any more (audit C15).
Status: IMPLEMENTED.

## Secret-Keyed Tables Keep Hash Primary Keys - 2026-09-28

Decision: exemption to the snowflake primary-key rule. A table whose row is
looked up by a presented secret (cookie, token, code, challenge) keys it by the
secret's hash, because that hash is the only lookup the protocol offers and a
second key would add an index without adding an access path. Exempt tables:
iam_sessions, iam_passkeys (credential-ID hash), iam_mfa_challenges,
iam_passkey_challenges, iam_stepup_challenges, iam_reauth_challenges,
iam_recovery_codes (principal_id + code hash), iam_password_recovery_tokens,
iam_email_verification_tokens (token HMAC), iam_identity_link_transactions,
iam_oauth_codes, iam_oauth_consent_requests (challenge hash),
iam_refresh_tokens. None of these hashes is ever an API identifier.
Sessions and passkeys are listed and addressed by users, so they additionally
carry a snowflake `public_id` (authn migration 18, unique, three dialects) that
is their only API identity (/sessions/{session_id},
/principals/{principal_id}/passkeys/{passkey_id}). New rows are inserted with
it; rows created before the column get one from the authn module's start-up
backfill. The column stays NULL-able only for that window.
Status: IMPLEMENTED (audit D2).

## Login Presents a Captcha on Every Attempt - 2026-09-30

Decision: the password login form presents a captcha unconditionally, the same
way registration already does, rather than only after N failed attempts or only
for an IP the limiter has flagged.

Reason: a progressive captcha needs a pre-authentication per-IP failure counter,
and the only counter this service has is the Redis-backed one in
`sharedSecurity`, which `newSharedSecurity` returns nil for when Redis is not
configured. A progressive challenge would therefore be silently absent in
exactly the deployments that run without Redis, while an unconditional one
degrades to nothing worse than itself. Per-principal `failed_attempts` cannot
serve either, because it is only reachable once the login name resolves.

Alternatives Considered: captcha after N password failures (rejected: fails open
without Redis, and needs a new contract on the login error path for the client
to learn a challenge is now required); captcha only for an IP over the limiter
threshold (rejected: same Redis dependency, hardest of the three to test,
because a test must forge both the address and the counter state).

Tradeoffs: every legitimate login types a challenge. Accepted deliberately for
an identity console. The existing `captcha.test_answer` escape is what keeps
automated logins possible, and it is configured, never a constant.

Scope: password login on all four entrances (one shared `PasswordForm`). Does
not change MFA, passkey, OAuth or federated sign-in.
Status: ACCEPTED, NOT IMPLEMENTED.

## Development Rate Limiting Runs an In-Process miniredis - 2026-09-30

Decision: the authentication limiter is exercised in development by a debug-only
flag that starts miniredis inside the serving process and points the module's
Redis client at it. The flag MUST refuse to start the service outside debug mode.

Reason: the limiter, its middleware and the per-operation `RateLimitMetadata`
tags are already complete; the only reason nothing is limited locally is that
`newSharedSecurity` returns nil without Redis, which short-circuits the
middleware. miniredis is already a module dependency, speaks real RESP over a
real socket, and therefore exercises the same go-redis path production uses,
which an in-process limiter would not. `make dev` keeps its zero external
dependencies.

Alternatives Considered: a Docker Redis in `make dev` (rejected: makes Docker a
prerequisite of starting the app, which today needs only Go, Node and bun); an
in-process limiter fallback when Redis is absent (rejected: it changes the
production default from "no limiting" to "per-replica limiting", which multiplies
every configured threshold by the replica count and silently loses the global
bound the config's `global` field promises).

Tradeoffs: a test double is compiled into the release binary. Contained by the
debug-mode refusal, which must be covered by a test that fails if the guard is
removed.

Scope: development and the local UAT environment only. No production default
changes; a real deployment still configures `shared_security` against its own
Redis, and gets no limiting if it does not.
Status: ACCEPTED, NOT IMPLEMENTED.

## The Development Limiter Store May Coexist With A Captcha Test Answer - 2026-09-30

Decision: `NewWebService` refuses a configured `captcha.test_answer` whenever
shared security is active, with one exemption: `shared_security.development_server`.

Reason: the two guards were written for different worlds and collide in the only
environment that needs both. Shared security means "a deployment serving real
people", which is why a bypass alongside it is refused. The development server
means the opposite -- an in-process store, reachable by nothing else, dying with
the process, and refused outside debug mode. Without the exemption `make dev`
cannot have both a working limiter and a scriptable login, so one of the two
things the user asked for would have to be off by default.

Alternatives Considered: leave the guard absolute and keep the limiter opt-in in
development (rejected: the limiter is then off in the environment it was asked
for, and nobody enables a flag they have to remember); drop the guard entirely
(rejected: it is the right refusal for every configuration but this one).

Tradeoffs: a bypass and shared security can now coexist, in debug mode only. The
whole safety rests on the debug refusal in `validateRuntimeConfig`, which is
covered by a test that fails if the guard is removed.

Scope: authn web service construction. No change to what a real deployment may
configure: without debug mode the development server is refused before the
service is built, and the original guard applies unchanged.
Status: IMPLEMENTED.

## The System View Keeps Its Workspace Switcher - 2026-09-30

Decision: the system administration view continues to offer the workspace
switcher, listing only the tenants the caller belongs to.

Reason: what it offers is a workspace the account actually holds, and it is the
only way back to that workspace from system administration. `system/layout.tsx`
has always fed it `myTenants()`, so it never had the boundary defect the tenant
half of the main shell had. A platform administrator who belongs to no tenant
sees an empty switcher, which is the ordinary case in a real deployment; the
development fixture makes the super administrator the owner of the Development
tenant because the browser audits need an administrator who can operate inside
one, and that is what makes a system account appear to have a tenant at all.

Alternatives Considered: remove the switcher from the system view so every
tenant, including the caller's own, is reached through the account view
(rejected by the user: it strands a legitimate membership behind a scoped,
time-boxed mechanism meant for tenants one does not belong to); change the
development fixture so the super administrator holds no membership (rejected as
out of scope here: the audits depend on it, and it would need a second
administrator persona built for them).

Scope: navigation only. It does not widen what the switcher may list -- that
stays membership, per the decision recorded for the main shell.
Status: ACCEPTED, NO CHANGE REQUIRED.

## The Development Limiter Store Stays Behind A Runtime Refusal, Not A Build Tag - 2026-09-30

Decision: `shared_security_development.go` is compiled into every build, and the
only thing keeping its in-process Redis out of a deployment is the debug-mode
refusal in `validateRuntimeConfig`. It is deliberately not moved behind a
`//go:build dev` tag.

Reason: the two tests that make the refusal meaningful -- that the flag is
rejected without debug mode, and that the limiter actually refuses and then
recovers -- would move behind the tag with the code. Unless a second tagged run
were added to ci, the default gate would lose both, which trades "two packages
absent from the shipped binary" for "two security assertions absent from every
build". Both are mutation-verified today: removing the guard or the clock turns
them red. The repository also has no build-mode split, so a tag would oblige
`make dev`, `iam-uat.mjs` and any developer build to carry `-tags dev`.

Cost accepted: the binary carries miniredis and, through it, the gopher-lua
interpreter miniredis uses for Redis scripts. That is binary size rather than
attack surface while nothing else instantiates either: both are reachable only
through a flag the application refuses to start with outside debug mode.

What would change this answer: a deployment target where debug mode could be
turned on by configuration an attacker influences, another importer of
gopher-lua that makes it reachable without the flag, or binary size becoming a
constraint. Any of those makes the tag worth its cost, together with the tagged
ci run that must come with it.

Alternatives Considered: `//go:build dev` (rejected for the reasons above);
dropping the development server and requiring Docker Redis locally (rejected
when the limiter work was planned -- see the miniredis decision above).
Status: IMPLEMENTED (as the absence of a build tag).

## The Captcha Provider Is Chosen, And Turnstile Is Off By Default Locally - 2026-09-30

Decision: `authn.captcha.provider` selects `image` or `turnstile` explicitly.
Turnstile is not inferred from the presence of keys, and `make dev` keeps the
drawn challenge unless asked for the other with `DEV_CAPTCHA=turnstile`.

Reason: inferring the provider from configuration would let a half-filled
Turnstile section serve drawn challenges while an operator believed they had
turned it on -- a silent downgrade of the thing they were configuring. And
validating a Turnstile token means reaching Cloudflare, so making it the local
default would put the internet between a developer and their own login form.
The switch exists so the widget can be worked on; it uses Cloudflare's own
dummy keys, which pass on any hostname.

Alternatives Considered: infer from keys (rejected above); make Turnstile the
local default with dummy keys (rejected: `make dev` has needed nothing but Go,
Node and bun, and a login that fails when the network does is a poor trade for
exercising a widget most changes never touch).

The client renders what `/login/options` names, which also removes an older
assumption: the console used to ask for challenge kinds until it got a drawn
one and give up after five tries, so a deployment offering only the interactive
kinds had a sign-in page that could never be answered.

Verification fails closed, per the rule the shared security configuration
already states. A refusal from Cloudflare and an unreachable Cloudflare are
told apart: both refuse the attempt, but only the first reports the business
code for a wrong answer, because telling somebody who solved the widget that
they got it wrong sends them round a loop they cannot win.

The site key is published by `/login/options` and exempt from the secret-tag
gate. It has to reach the browser for the widget to render, and it is the
secret key beside it -- resolved through secretx, never leaving the process --
that makes a token worth anything.
Status: IMPLEMENTED.

## Principal Erasure Owners Publish Their Own Schema Requirement - 2026-09-30

Decision: each IAM owner of principal data exports `MigrationSource()` as a
package function and `PrincipalErasureVersion` beside `ErasePrincipalData`.
`internal/principallifecycle` composes them and restates nothing. It reads each
stream with `goosex.CurrentVersion` inside the erasure transaction: version 0
(never installed) is skipped, an installed owner below its version fails with
`identity.ErrUnavailable` before anything is written, and a failed read is
returned as itself. `goosex.VersionApplied` is removed; it had no other caller.

Reason: the anonymizer carried each owner's version table name and a minimum
version by hand. Two had gone stale (authn 13 while erasure touches tables from
14-16; federation 5 while it touches 00006), and nothing compared them. And
because a disabled module is never migrated and cannot be selected with
`migrate --module`, the default deployment (federation off) had no
`goose_federation` table, so every anonymization returned 503. The composed
suite hid this by migrating federation by hand. A stream that was never
installed cannot hold records, and federation tables only ever come from its
own migrations, so skipping it loses nothing; an owner that was installed and
then disabled is still erased, as the anonymizer contract requires.

Alternatives Considered: reuse the `principal-erasure` milestone (rejected: it
means "guard triggers installed", `tests/migration` relies on that meaning, and
milestones must be strictly increasing so it cannot move onto another
milestone's version); require every owner to be fully migrated (rejected: a
disabled owner is never migrated, so any unrelated later migration would block
erasure indefinitely); check the version inside each `ErasePrincipalData`
(rejected: seven copies of the skip/behind rule, and skipping is the composer's
semantics). This supersedes D71's literal version table names: a package
function is not the `cross-module-concrete-type` reference D71 was avoiding.

Tradeoffs: `CurrentVersion` probes with `Migrator().GetTables()` rather than
`HasTable`, because `HasTable` reports a failed lookup as a missing table,
which here would silently skip an owner. The drift gate is
`TestAnonymizePrincipalDataHoldsEachOwnerToItsErasureVersion`: each owner at
exactly its version must erase, one below must refuse. It migrates through
goosex rather than the milestone planner, because the planner cannot hold one
owner below a version its neighbours' later milestones depend on.

Scope: principal erasure only. The OAuth service's collapse of repository
errors into `ErrInvalidRequest` (`oauth/service.go` 396/414/594/818/827/872/886)
is the same class of defect and is to be fixed as its own task.
Status: IMPLEMENTED.

## A Failed Read Is Not A Refusal - 2026-09-30

Decision: a read that fails is returned as the error it is. Only a row that
is genuinely absent (gorm.ErrRecordNotFound, or a repository's own not-found
sentinel) becomes the domain refusal -- invalid_grant, a wrong MFA code, a
bad invitation link, a missing file. Applied across oauth, authn, federation,
organization and files (dc82574..HEAD).

Reason: folding every error into the refusal turned outages into client
errors. The sharpest case was OAuth refresh: a database hiccup answered
invalid_grant, and a conforming client discards its refresh token. Elsewhere
users were told their code or link was wrong and retried against a dead
store, and nothing reached the 5xx alerts.

This reverses what ef2bef6 pinned for files: that commit described
AccountResourceFiles.Open masking space/node/version/object read failures
as ErrNotFound as "authorization semantics". It was a test-only coverage
commit recording existing behaviour, with no decision behind it, and the
masking bought nothing: returning the raw error still serves no file, a
storage fault is not something a caller can provoke to learn whether a file
exists, and the binding read in the same function already propagated its
failures. The user delegated the call; the four pinned expectations in
TestAccountResourceOpenPropagatesLookupAndStorageFailures now expect the
injected fault.

Alternatives Considered: leaving the uniform refusal where it hides which
check failed (authn). Kept as it was -- the refusal for a missing row is
unchanged; only a failed read now escapes it, and a caller cannot cause one.

Scope: the 29 read sites listed in the task state plus loadPasskeyUser.
FinishPasskeyLogin's propagation mirrors reauthentication.go and cannot be
unit-tested (webauthnx's protocol is private and needs a real assertion).
Status: IMPLEMENTED.

## D104 - The Platform Answer Is Read Only When It Could Change The Verdict (2026-10-05)

Decision: `CheckBulk` no longer asks whether the subject is a platform
administrator before it knows the answer matters. A permission the tenant's own
grants already allow is allowed whoever asks, so the platform answer -- four
statements of its own: the system tenant by slug, the principal's status, the
platform administrator table, and that tenant's membership -- cannot change the
verdict and is not read. One authorization check went from **7 statements to 3**
in the common case, on the hottest path in the service, run once per request.

`platformAnswerDecides` is written as the exact negation of the loop that
decides each permission, and says so, because the two drifting apart is the one
way this becomes a security bug: a new branch below that consults the platform
answer, not admitted above, would see `false` and silently refuse. A system-scoped
permission is decided by nothing else and always asks; an operating-platform or
unknown code is refused outright; a tenant-scoped one asks only when neither the
permission itself nor `tenant_administer` is granted.

Both halves are mutation-verified: forcing "always ask" turns the saved-statements
test red, forcing "never ask" turns the platform administrator's verdicts red.

`CheckPlatform` is read-only -- no audit, no writes -- so not calling it has no
effect beyond the statements it would have sent.

**The frozen performance baseline is deliberately NOT regenerated for this.**
The `authorization` path's recorded plan set now names 4 statements the code no
longer sends, so `TestIAMPerformanceExternalDatabase` will report a plan-signature
mismatch until it is regenerated -- and that regeneration must happen on a quiet
reference machine, never on a loaded one. The reason is in the generator:

    benchmark.ThresholdNSPerOp = roundedIAMPerformanceThreshold(ordered[len(ordered)-1] * 3)

The threshold is the slowest sample times three. Regenerating on the machine this
was written on (XProtect pegging a core for eight hours, Docker's MySQL on a
virtualised filesystem, the gate taking three hours where it takes fifteen
minutes) would have written authorization's threshold as 42ms where the committed
value is 7ms -- a gate weakened six-fold and frozen into the repository. That is
weakening a test to make it pass, so it was not done. Regenerate on the reference
machine, and take the `plans` block from that run; the `benchmarks` block already
in the file was captured on a quiet machine and is the one worth keeping.
Status: IMPLEMENTED, baseline refresh OUTSTANDING.

## D105 - Principal Deletion Stays Logical; Files Get No Erasure Path (2026-10-11)

Decision: deleting a principal is the existing logical delete (suspension: status
disabled, everything retained and restorable). Personal files follow it as-is: they are
kept with the principal and are not trashed or purged by any erasure step. No file
erasure is implemented. The existing anonymization feature is left untouched.
Reason: user — "keep it simple: logical delete only, no erasure"; files must be
consistent with personal data, and personal data is retained under logical delete.
Supersedes the earlier same-day draft that trashed personal files on erasure.
Status: accepted.

## D106 - The PAT Ceiling Is Enforced On The Action, Not Only The Route (2026-10-11)

Decision: a personal access token's permission list caps the file action the service
layer authorizes (file_<action>), in addition to the route guard code. Route guards and
the published contract are unchanged.
Reason: PATCH /files/{file_id} is guarded by file_view but renames/restores, and
file-operations is guarded by file_create but moves; a view-only PAT could change data
(D35 says a PAT is an additional ceiling). Enforcing at the action covers the whole class.
Alternatives: split routes by action (rejected: breaking API change); raise route guards
(rejected: blocks principals holding rename/restore without update).
Refinement: list, traverse and read are exempt from the ceiling because every file route
performs them before its main action and the route guard the token must hold already
bounds them; every other action requires file_<action> in the token. Copy therefore needs
file_create (plus update/delete when it overwrites). Enforced once in
nodeAuthorizer.accessMany.
Status: accepted (user: fix all audit findings).

## D107 - GUID Generation Requires An Authenticated Caller (2026-10-11)

Decision: GET /guid and GET /guid/{count} remain (D84) but are no longer anonymous; the
batch maximum drops from 10000 to 100. Supersedes the anonymous part of D84.
Reason: anonymous callers could monopolise the shared ID generator every write uses.
User chose "not public". Mechanism: pkg/extension/authz imports internal/infra/guid, so
guid cannot import authz; guid declares a narrow RouteAuthenticator that core provides
from its *authz.Registrar ((*Registrar).Authenticate, extracted from
RegisterAuthenticated without behavior change). Without it the routes answer 503
(fail closed). gRPC GuidService is out of scope. Status: accepted.

## D108 - Map Rendering Uses Leaflet Directly (2026-10-11)

Decision: replace react-leaflet / @react-leaflet/core (Hippocratic-2.1, not OSI) with
leaflet's own API (BSD-2) in packages/ui. Reason: license; one consumer file. Status: accepted.

## D109 - Myrmidon Pending Product Questions Adopt The Recommendations (2026-10-11)

Decision: every open product question in the Myrmidon design docs is decided as its
recommendation column says. That covers PRD §18 Q1–Q11 (working name Myrmidon with
the display name from deployment config; first ICP = AI software studio; MVP does
not expose a DAG engine, templates only generate work-item trees; storage follows the
framework's three dialects with PostgreSQL recommended; machines enrol through OAuth
device code; delegated merge only for non-self-produced, all-verified, LOW-risk items;
client portal shows AI participation by mode, configurable; ad-hoc sessions must link a
work item before a claim; no Matrix/A2A in MVP; lead dispatch MANUAL in hybrid and AUTO
in autonomous; knowledge K1 full-text first, K2 pgvector later) and the open questions
in section 12 of specs 01–09.

Reason: the product owner answered "该有的就有" when asked to settle them.

Scope: docs/myrmidon only; no code. Implementation has not started — the owner will
review the prototype first. Reversing any item means editing PRD §18 and this record.

Status: accepted.

## D110 - Supervisor Agents May Create Agents Inside A Human-Set Spawn Grant (2026-10-11)

Decision: a supervisor (LEAD) agent may create sub-agents, but only inside a spawn
grant that the team owner sets (non-delegable, step-up): projects, concurrent head
count (default 3), daily creations (5), total and per-agent budget ($50 / $15), allowed
roles (executor, coordinator), machines and models, max effort, lifetime (7 d) and idle
retirement (48 h), and approval mode (per-request human approval by default; automatic
only for autonomous projects). Sub-agents cannot be supervisors or spawn (depth 1), stay
within the supervisor's permissions, skills and MCP, cannot run full-auto, and cannot be
delegated decision authority until a human adopts them. When the supervisor retires or the
grant ends they become orphans; a human adopts or retires them (auto-retire after 7 d).
Every agent records its origin (HUMAN_CREATED / AGENT_SPAWNED, plus adopted / orphan), and
the UI marks and filters by it and groups sub-agents under their supervisor.

Reason: the product owner wants the team to grow itself where it makes sense, while humans
keep control of permissions, head count and money, and can always tell who created whom.

Scope: docs/myrmidon (PRD §10.8, specs 00 v1.8, 03, 05, 08, prototype). No code.

Status: accepted.

## D112 - Adopt The Research Upgrade A-G (2026-10-11)

Decision: the product owner accepted all seven recommendations of
docs/myrmidon/research/2026-10-collaboration-research.md: (A) every work item has an
accountable human, agents only execute; (B) the readiness gate is hard for agent dispatch and
advisory for humans; (C) the software template follows Alibaba Yunxiao's work item types,
defect model and transition conditions; (D) a HUMAN_ONLY team mode where agents only assist;
(E) first domain templates software / video / novel / team tasks, model-scored thresholds
marked as needing calibration; (F) Yunxiao import and Codeup / Flow evidence in v1.1, MCP
read/write in v2; (G) the ten P0 reliability mechanisms enter the MVP.

Scope: specs 00 v2.0 §15, 02, 03, 05, 07, 08, 10, new 11 and 12, PRD §10.10, prototype.

Status: accepted.

## D113 - Internal Quality Gates; Internal Issues Never Reach Humans Or The Client (2026-10-11)

Decision: after an executor claims completion, the platform re-runs deterministic checks, then a
tester agent and a product acceptance agent check the work; all three must pass (L2 "internal
acceptance") before any human sees it. Any internal failure returns to the executor with
structured feedback without notifying humans. Humans are involved only when internal rounds
(default 5) are exhausted, a requirement conflict is found, the line is stopped, a tolerance
exception or budget threshold occurs, or the template mandates a human gate. The client sees
only delivery packages of L3-accepted items. Issues found by humans or the client after internal
QA passed are recorded as escapes and fed back into the QA agents' memory and the template eval
set; the escape rate is a core quality metric. The acceptance agent judges the real delivered
form (run, screenshots, files) and must use a different model family from the executor.

Reason: the product owner wants humans to focus on acceptance; whatever the agent team can fix
internally must not leak to humans, let alone the client. The 2048 MVP showed automated tests
alone miss visual defects, which a screenshot-reviewing acceptance agent should catch.

Status: accepted.

## D114 - Escape Events Have Several Publishers But One Learning Executor (2026-10-12)

Decision: `quality.escaped` is published by 02 (HUMAN_REVIEW, at T15 / T26), 07
(CLIENT_ACCEPTANCE, item-level client rejection) and 12 Part A (defects carrying
`escaped_from_stage`; Part A lives in the delivery module). All publishers use one
idempotency key `escape_key = (work_item_id, claim_id or acceptance_id)`; a key is published
once, and a later publisher (for example a defect created from a rejection that 07 already
published) only references the existing escape. 03 never publishes. 02
`delivery.escape_learning` is the only learning executor: it consumes every `quality.escaped`,
upserts `delivery_escapes` by `escape_key`, proposes EXPERIENCE memory drafts through 06
`MemoryWriter.Propose` and calls 11 `EvalSetWriter.ProposeCase` (11's existing semantics).
08 keeps its record table (idempotent on event id), notifications, the "quality escapes" page
and the human confirm / dismiss step; RECORDED activates the drafts 02 proposed, DISMISSED
needs a reason and withdraws them (02 acts on `quality.escape_learning_decided`). 08 no longer
calls MemoryWriter or submits eval cases.

Reason: the specs contradicted each other: 00 named 02 the sole publisher while 07 and 12 also
published, 05 consumed the event "from 03", and 08 wrote memory and eval drafts itself,
duplicating 02's claim to be the only learning executor.

Alternatives Considered: 02 as the sole publisher with 07 / 12 calling into 02 (rejected:
07 and 12 own the client-acceptance and defect facts and already share the delivery module);
08 as the learning executor (rejected: learning would run outside the delivery transaction
that knows the internal QA verdicts and template).

Tradeoffs: consumers must tolerate several publishers and dedupe by `escape_key`; activation
and withdrawal of drafts become an extra 02 consumption of an 08 event.

Scope: specs 00 §7.1 / §14 / §15, 02 BR-02-58 / §3.24 / §7.4, 05 BR-05-113, 07, 08 BR-08-63 /
AC-08-85, 11, 12 BR-12-14 / BR-12-36. No code.

Follow-ups (main agent, 2026-10-12): after the 08 human confirm/dismiss, 08 publishes
`quality.escape_learning_decided` and 02 escape_learning activates or withdraws the drafts (accepted). 02 also
publishes `quality.escape_learning_proposed{escape_key, memory_ids[], eval_case_ref}` so 08 can fill its read-only
columns. 12 adds `worklog.returned` / `worklog.withdrawn` so the WORKLOG_APPROVAL inbox item can close.

Status: Accepted.

## D115 - Keep WorkItemTransitioner.SetSystemBlock (2026-10-12)

Decision: keep 02 `WorkItemTransitioner.SetSystemBlock` (BR-02-61).

Reason: technically necessary so 11 can set and clear the system block reasons
TOOL_UNAVAILABLE / PROVIDER_UNAVAILABLE; these reasons must not be set or cleared by hand.

Scope: specs 00 §8, 02 BR-02-61, 11. No code.

Status: Accepted.


## D116 - Calibrate Deterministic Capture Tools Per Machine (2026-10-12)

Decision (user: 「工具校准可以，加上吧」): deterministic capture / detection verifiers (screenshot, DOM layout,
a11y, media probes, text statistics) are calibrated per machine against TOOL_PROBE cases (known-good and
known-bad sample pages with fixed expected verdicts), reusing spec 11's calibration table, eval cases and
J2/J3 jobs (`method=PROBE`, `machine_id`). All probes matching = CALIBRATED automatically; any mismatch =
UNCALIBRATED/DRIFTED + quality owner notified. Recalibrate on first capability, browser variant/major change,
every 30 days, or on demand. 03 BR-03-103 runs VISUAL / INTERACTION only on CALIBRATED machines; with none, the
AUTOMATED run waits (PENDING, TOOL_UNCALIBRATED): no FAILED, no internal round, no human notice; 24 h -> escalate.

Reason: in the 2048 run a headless-browser crop below ~500px produced a stable false positive that burned two
internal rounds and wrongly escalated to a human (research §5.1). Quality gates that bypass humans must not trust
their own tools blindly.

Alternatives Considered: (1) trust deterministic tools (status quo) - rejected, the false positive was real;
(2) human confirmation for every tool calibration - rejected, probe results are deterministic; (3) fall back to
running on an uncalibrated machine and marking criteria unverified - rejected, an unverified criterion blocks
AGENT_ACCEPTANCE (BR-03-98) and would still burn rounds; (4) 02 SetSystemBlock - rejected, it is a pre-start
readiness block, not a mid-QA wait.

Tradeoffs: one more background job per machine; a UI item can wait up to 24 h when no machine is calibrated.
Out of scope for now: letting a reviewer mark an internal finding as a tool false positive to auto-create a probe.

Scope: specs 11 §3.5 / §3.6 / §4.2 / §5.6b / §5.7 / §7 / §8 / AC-11-35..38, 03 §3.19 / §3.23 / BR-03-103 /
AC-03-86..88, 00 §7.1 / §8 / §14, PRD §10.10, prototype template detail (verifier tab). No code.

Status: Accepted.

## D117 - Client Change Requests Through the Portal (2026-10-12)

Decision (user: 「可以 更新完我看下」 on the proposal, incl. "beyond-tolerance changes need client agreement"):
- 01: IntakeKind += `CHANGE` (portal 〔申请变更〕 on a CONVERTED intake, API `kind=CHANGE`; email v1.1).
  intake_records += `change_of_intake_id`, `target_requirement_id` (both required for CHANGE), `client_approval` JSON.
  Triage card += `change_impact` {base_version_no, criteria_diff[{op ADD|RETIRE|REPLACE, criterion_ref?, text}],
  affected_work_items[{work_item_id, effect STALE|NEW|CANCELLED}], schedule_delta_days, new_expected_date,
  internal_cost_delta_micros (team only), client_price_delta? {amount, currency, note} (human-entered, client-visible,
  no billing model), tolerance_check {breached[TIME|COST|SCOPE], snapshot}, requires_client_approval = breached≠∅}.
  Agents may draft the impact; affected items come from 02 `ChangeImpactPreviewer`, breach from 07 `ToleranceReader.WouldBreach`.
- IntakeStatus (00) += `CLIENT_APPROVAL`. TRIAGED→CLIENT_APPROVAL: human with `scope_change` submits (kind=CHANGE,
  requires_client_approval). CLIENT_APPROVAL→TRIAGED on: client APPROVED / REJECTED(comment) in the portal; team
  withdraws; team records offline approval (human, evidence file required, shown 「由团队记录」). No new card revision
  while CLIENT_APPROVAL. Reminders: client 7 d, team 14 d; never auto-decide.
- Conversion of a CHANGE: default APPEND_TO_REQUIREMENT to target_requirement_id (NEW_REQUIREMENT allowed); needs
  change_impact, base_version_no = current, and if requires_client_approval an APPROVED decision for the same card
  revision. 02 `AppendFromIntake` applies diff + impacts (BR-02-32) and, when approved, 07
  `ToleranceAdjuster.ApplyApprovedChange` in the same tx (baseline_no+1; refused while an exception is open).
  Within tolerance: no client approval, client is told the result and impact. DECLINE of a CHANGE requires client_message.
- Portal: 〔补充说明〕 only while RECEIVED/TRIAGED/CLARIFYING; CONVERTED shows 〔申请变更〕. Client sees change status
  评估中 / 待你确认 / 已接受（需求 vN） / 未接受（理由）, criteria diff in plain words, schedule delta, new date, price delta if
  set; never internal cost, work item ids or internal QA.

Reason: requirements were changeable only implicitly (new intake + team triage); the client had no entry, no visibility
and no say on schedule/price impact. Baseline integrity (no in-place edits) is kept.

Alternatives Considered: client edits the requirement in place - rejected (breaks the acceptance baseline);
client approval for every change - rejected (slow for changes absorbed within tolerance); computing a client price -
rejected (no billing model; price delta is entered by a human).

Scope: specs 00, 01, 02, 07 (+08 only if a registration is needed), PRD, prototype portal + team change intake page.

Status: Accepted.

## D118 - Top-Level Domains, Contextual Side Nav and a Slack-Style Conversation Space (2026-10-12)

Decision (user: 顶部菜单放 工作台/会话/项目/协作/治理，会话做成 Slack 布局，协作含 目标/需求/任务/测试/缺陷/流水线；
「如果更好就按照这个来」，其余菜单与页面结构由我重新设计):
- Top bar: brand · domain tabs 工作台 · 会话 · 协作 · 项目 · 团队 · 治理 · ⌘K search · 裁决徽标 · 通知 · 语言/主题 · 用户.
  The left side shows only the current domain's sub-nav; 会话 is full-bleed with its own panes.
- 工作台 (personal): 总览 · 我的裁决 · 我的任务 (new: work assigned to me) · 我的计划.
- 会话 (Slack 3-pane): left = 提及与线程 · 频道 (grouped by project) · 私信 (人↔人, 人↔Agent; Agent↔Agent read-only for
  owners) · 执行会话 (running agent sessions, pending permission requests); centre = messages with structured cards
  (裁决卡、完成声明、停线、派单提案) that act on the real objects; right = thread / details / members.
  Chat is never the source of truth: a work request to an Agent in a DM becomes a dispatch proposal, as in channels.
- 协作 (project filter at the top of the side nav, default 全部项目): 规划 目标 · 报告 / 需求 来件 · 需求 (new list) /
  开发 任务 · 评审台 / 测试 用例与测试计划 · 缺陷 (new list) · 穿透记录 / 流水线 流水线 (new: per work item stage runs
  平台重跑→隐藏验收→测试 Agent→产品验收 Agent→人工评审→交付, plus external Flow runs) · 执行 / 工时.
- 项目: 项目组合 · 交付 · 领域工作台 (视频 / 小说 / 团队任务).
- 团队: 成员与 Agent · 机器 · 知识库 · 模板库.
- 治理: 策略 授权与策略 · 流转配置 / 连接 集成 · 需求开口 · API 网关 / 记录 审计.
- Mobile: domains + sub-nav in the drawer; bottom bar 工作台 · 会话 · 协作 · 裁决.
- Framework: 09 `MenuPage` gains `Domain` (top-level key) next to `Group`; 08 §8.6 shell spec rewritten accordingly.

Reason: 20+ sidebar items in 6 groups were growing (quality, templates, Yunxiao pages were reachable only via buttons);
domain tabs + contextual side nav scale, match 云效 (Projex / Testhub / Flow) habits, and give conversation the
full-width layout chat needs.

Alternatives Considered: keep the single sidebar (rejected: does not scale); 项目 as the only container with all work
inside it (rejected: cross-project views for leads are primary; a project filter covers per-project use);
naming the work domain 交付 (kept 协作 per the user).

Tradeoffs: one more click for cross-domain jumps (mitigated by ⌘K and the inbox badge); 人↔人 / 人↔Agent DMs extend
spec 05's channel kinds.

Scope: prototype shell + new pages (chat, my-work, requirements, defects, pipelines); PRD §12; specs 08 §8.6, 09 C6,
05 §8.8 (+DM kinds). No code.

Follow-ups (spec writing, 2026-10-12): conversations live under the team-level `/t/:tenantID/chat[/:channelID]`
(`?thread=` opens the right pane); old channel routes redirect there. Member DMs are `ChannelKind.DIRECT`, team-scoped
(no project), unique per pair; a human↔human DM is readable only by its two participants (no owner / audit access);
a human↔Agent DM is also readable, read-only, by the Agent's accountable owner and `audit_read` holders.
Revision (user: 「可以 you call」, 2026-10-12) - the project is the main container (云效 Projex style); 协作 is removed:
- Top bar: 工作台 · 会话 · 项目 · 团队 · 治理.
- 工作台: 总览 · 我的裁决 · 我的任务 · 我的计划.
- 项目, outside a project: 项目组合 · 来件 · 交付总览 / 跨项目: 需求 · 任务 · 缺陷 · 流水线 (team-level lists with a project filter).
- 项目, inside project P (the URL carries P; side nav top = project switcher + 返回项目组合): 概览 · 项目频道 /
  规划 目标 / 需求 需求 / 开发 任务 · 评审台 / 测试 用例与测试计划 · 缺陷 · 穿透记录 / 流水线 流水线 · 执行 /
  交付 交付 · 工时 / 领域工作台 when the project uses a VIDEO / NOVEL / TEAM_TASKS template / 设置 项目设置.
  Detail pages (任务、缺陷、执行、评审单、内部验收…) render inside the project space of their project.
- 团队: 成员与 Agent · 机器 · 知识库 · 模板库 · 目标树 · 报告中心 (team-level goals and reports).
- 治理: unchanged. 会话: unchanged (team-level; project channels grouped by project).
- Mobile bottom bar: 工作台 · 会话 · 项目 · 裁决. 09 `MenuDomain`: HOME / CHAT / PROJECTS / TEAM / GOVERNANCE plus a
  menu `Scope` of TEAM or PROJECT for items that render inside the project space.
Reason: the project is the data boundary (members and Agent bindings, permission scope, budget and tolerance,
constitution, template, client and delivery); daily work happens inside one project; cross-project views are kept
for leads under 项目 › 跨项目 and in 工作台.

Status: Accepted.

## D119 - Remote Preview, Terminal and Files Through the Runner Tunnel; Server and k8s Runners (2026-10-12)

Decision (user: 「可以 you call」 on the proposal, all recommendations taken):
- Transport: the runner's existing outbound connection (WebSocket, multiplexed streams) carries preview traffic,
  file reads and terminal I/O. Runners never open inbound ports; works for workstations, servers and k8s pods.
- Two preview classes. (A) Running services (web, API, dashboards): a preview gateway on the server proxies
  `https://<random-label>.<preview-domain>` to 127.0.0.1:<port> on the runner. HTTP/1.1, WebSocket (HMR), SSE and range
  requests. (B) Artefacts (video, novel chapters, documents, images, PDFs, reports): no service is started; the runner
  serves read-only file streams from the execution workspace and the console renders them with built-in viewers
  (device-framed web view, OpenAPI console, video player with an ffmpeg low-res proxy when needed, chapter reader with
  diffs, document / image / PDF viewers). File-change notifications push refreshes.
- Preview domain: a separate registrable domain from the console, from deployment configuration (never hard-coded);
  one unguessable label per preview for origin isolation (agent-written content is untrusted); access still requires
  login and execution read permission on the project, exchanged for a preview-domain-scoped short-lived session.
  Without wildcard DNS, a path-mode fallback is allowed and labelled "some apps may not work".
- Who starts a service preview: by default the platform, from the project constitution / template preview config
  {kind, start argv, port, health path, env}; an Agent may also offer a port, but only ports opened by its own
  execution's process tree — never arbitrary machine ports. Reviews preview the exact claimed commit, restarted in an
  isolated copy. Idle 30 min auto-stop; max lifetime; stopped with the execution.
- Client share links: allowed only with human approval, an expiry, read-only, revocable, audited; never expose
  internal records. Preview is progress, not delivery.
- Remote terminal: read-only watch of the Agent session by default; interactive shell is a separate permission,
  human-only, non-delegable, step-up, fully recorded to audit, idle timeout, cwd limited to the workspace. Per-machine
  switch 「允许远程终端」, off by default on personal workstations.
- Remote files: read-only browse / diff / download of the workspace (paths limited to the workspace, secret files such
  as `.env` masked); edit / upload only after the execution is paused or taken over by a human, recorded as a human commit.
- Machine types: WORKSTATION / SERVER (headless daemon, service-account auth) / K8S_POOL (one ephemeral pod per
  execution, autoscaled).
- Prototype covers every phase (user: 「开发可以分期分版本，但是原型要全部做掉我才开工」); each v1.1 feature carries a
  「v1.1」 label in the prototype. Development phasing:
  MVP = web / API service preview, artefact preview for documents / images / reports, read-only terminal,
  read-only file manager, SERVER runner. v1.1 = interactive terminal, file editing, video and novel previews, K8S_POOL.

Reason: humans work from the web or app while Agents run elsewhere; the specs had only after-the-fact evidence and a
manually entered demo URL. frp-style reverse tunnelling is borrowed but embedded in the runner, because preview access
must follow project permissions, per-execution isolation, audit, quotas and revocation.

Alternatives Considered: running frp / ngrok (rejected: public ports, shared-secret auth, unaware of executions);
path-based previews on the console origin (rejected: breaks apps and leaks the console session to untrusted code);
forwarding arbitrary machine ports (rejected: exposes other services on the worker).

Scope: specs 03 (preview / terminal / file sessions per execution, share links), 04 (tunnel, runner capabilities,
machine types, switches, k8s pool), 08 (permissions, step-up, audit, approval inbox), 00, PRD; prototype remote
workspace page and machine types. No code.

Follow-ups (spec writing, decided under the user's 「you call」, 2026-10-12): client share links are v1.1; the runner
WebSocket channel moves to MVP (long polling stays as control fallback) because MVP previews need multiplexed streams;
SERVER / K8S_POOL default the terminal and file-edit switches on (tenant-adjustable), workstations stay off; the menu
field is `MenuPage.Space` (TEAM / PROJECT) because `Scope` already exists; preview config is a `preview` block in the
project constitution, falling back to template `params.preview`.

Status: Accepted.

## D120 - Adopt Raft-Inspired Agent Protocol, Operations Manifest, Anchored Comments, Lifecycle and Loop Contracts (2026-10-12)

Decision (user: 「you call」 / 「jixu」 after the Raft comparison; scope = my recommendation P0 ×3 + P1 ×2 from
docs/myrmidon/research/2026-10-raft-comparison.md §5):
1. Agent inbox protocol (03 / 04 / 05): every Agent has a durable inbox as the single source of truth; wake hints carry no
   content (poll, hint stream with heartbeat, or signed webhook); acknowledgement by cursor (`since`); a write (claim,
   message, evidence, status) made while the conversation / work item already has newer events returns
   `interrupted` with the newer events, and the Agent must re-read and retry with the same idempotency key.
2. Agent operations manifest (05, with 08 for authorization): a machine-readable list of every operation an Agent may
   call — input schema, side effects, idempotency, required capability / permission, delegable or not — and every
   result returns `next` (suggested next operations). Spawn quotas and authority checks are enforced per operation;
   `CAPABILITY_NOT_AUTHORIZED` maps to 08 reason codes.
3. Anchored comments (03 artefact viewers, 02 review, 12 defects, 07 client acceptance): comments anchored to a
   code line range, a table row, an HTML region (selector + rect at a viewport), a video timestamp / range, a text
   paragraph (novel chapter); a comment can become structured review feedback, a defect, or a client rejection item,
   and keeps the anchor and the artefact version it was made on.
4. Agent lifecycle (05): states online / thinking / working / error / offline reported by the runner; three resets —
   restart (keep session), session reset (clear context, keep workspace), full reset (clear both) — human-only for
   full reset; workspace and memory viewer limited to the Agent's accountable owner and admins.
5. Loop contracts (10, with 05 reminders): recurring Agent work (daily / weekly reports, periodic checks) declares
   cadence, self-checks, budget, escalation conditions and output format; driven by reminders anchored to a thread.

6. Team tool catalog: managed MCP servers + skills (user: 「团队级mcp， skill？」, 2026-10-12; Raft has a
   workspace MCP catalog, observed logged in): the team keeps one catalog of (a) MCP servers — transport
   (Streamable HTTP / stdio packaged), provider connection, credentials sealed by the platform (09 key envelope),
   recommended presets — and (b) skills — versioned, content-hashed packages (like this repo's `.agents/skills` +
   `skills-lock.json`). Grants: which projects / roles / Agents may use each entry (default none); tool calls are
   audited; credentials never reach the machine in clear (the runner gets a scoped, short-lived token or a local
   proxy, as Raft's computer does). Distribution: the runner syncs granted skills into the runtime's skills folder
   and injects granted MCP servers into the session config at execution start; the execution records the catalog
   versions it ran with (evidence). Machine-local skills / MCP detected by 04 stay visible but are flagged
   「本机自带，未纳入团队目录」. Owners: 05 catalog + grants, 04 distribution, 09 sealing, 08 permissions / audit.

Not adopted now: client joint conversation, P2 items, pricing, relationship graph (left for a later round).
User decisions 2026-10-12: finish the D120 specs (「1 继续写完」); the page reader extension stays click-free (「2 不加」).

Reason: Raft's agent-runtime layer is stronger than ours (stale-context writes, model-readable operations, precise
comments); these fill gaps without changing our delivery-governance core.

Scope: specs 02, 03, 04, 05, 07, 08, 10, 12, 00, PRD; prototype (artefact viewers with anchored comments, agent
lifecycle in agent detail, operations manifest page, loop contract in report / reminder config). No code.

Follow-ups (spec writing, 2026-10-12; defaults chosen under the user's delegation, reversible before implementation):
catalog skills mirror 06 skills (a skill loads only when mounted in 06 and granted in 05; existing mounts migrate to
Agent-level grants); MCP credentials stay sealed server-side, the machine gets a 15-minute token bound to the execution
lease and calls go through the runner's local proxy to the platform relay; stdio MCP servers needing credentials are
v1.1; `tool_catalog_manage` is held by the team owner only; machine-local tools keep working but are flagged (teams
cannot block them yet); disabling a catalog entry cuts off running executions; a loop contract over its monthly cost
cap pauses immediately. 01 portal endpoints for client anchored comments added (01 BR-01-88..91, CR-13 merged into
07 / 03 / 00); signatory contacts only may comment; a contact sees all client threads on a delivery they can see.

Status: Accepted (decided under the user's delegation; reversible before implementation starts).

## D121 - Client Engagement Is Conversation-First (2026-10-12)

Decision (user: 「ok 主要是对接优化，内部反正都是动态的灵活的，按具体需求业务场景来的。更新原型给我看下」 on the proposal):
- The client side becomes one 「对接会话」 per client account, with a thread per requirement topic, linked to the project
  after conversion. Waterfall (SOW / requirement document with phases and milestones), checklist (list / spreadsheet)
  and conversational styles all live in it; an intake Agent turns each into a structured requirement draft.
- The source of truth is the 「需求确认单」 (items, acceptance criteria, priority, milestones, out of scope), signed off by
  the client as the baseline; the conversation is not. Waterfall and checklist need sign-off before work starts; small
  conversational requests may take a fast lane judged by tolerance, with a confirmation notice to the client.
- The 乙方 assigns an 「对接组」: an accountable human 对接人 + an intake Agent (+ domain Agents). Agents draft, summarise,
  ask and report; scope, dates, price and commitments are human only. Outgoing Agent messages need human approval by
  default; a team may allow Agents to answer factual / progress questions on their own. The team has an internal side
  thread the client never sees.
- Clarification, change (D117), milestone progress (drafted by an Agent, approved by a human; ties to D111 reports) and
  delivery / acceptance (with D120 anchored comments) appear as structured cards in the conversation.
- After conversion the internal flow (requirement → goals / KR drafted by an Agent and confirmed by a human → plan →
  tasks → execution → quality gates → acceptance → delivery) stays flexible per project and scenario; the client sees it
  at milestone level only. Channels: portal, email (thread = conversation thread), API; IM connectors v2.

Reason: clients work in different styles; a single conversational container absorbs them and keeps one traceable
baseline, while commitments stay with an accountable human.

Scope: prototype first (client portal + team engagement workspace); specs 01 (major), 02 / 05 / 07 / 08 / 10, PRD after
the user reviews the prototype.

Status: Accepted.

## D122 - Rich Media, Voice and Web Search in Conversations (2026-10-12)

Decision (user: 「会话要支持各种图片视频 word pdf 文件 markdown 语音等等等，还有联网搜索等等？」; design proposed and adopted
under the user's standing delegation, reversible before implementation):
- Applies to the client portal conversation (D121), the team engagement workspace (D121) and the team 会话 space (D118).
- Messages carry attachments with inline viewers: images (gallery, zoom), video (player, seek), PDF (paged), Word / Excel /
  PowerPoint (server-side converted preview, original downloadable), Markdown (rendered), code blocks (highlighted),
  other files as file cards. Drag-and-drop, paste, multi-upload with progress; malware scan and size limits on upload.
- Voice messages: hold to record; transcribed to editable text (machine tool such as whisperx per 04, or a platform
  transcription service); clients may speak requirements in the portal.
- Agents read attachments (text extraction, OCR, transcription) to build requirement drafts; anchored comments (D120)
  work on attachments.
- Web search is an Agent tool from the D120 team tool catalog (granted per project / role, default off). Results appear as
  a search-result card; every claim carries source links. Queries exclude client-confidential content (project / client
  names, internal data) unless a human explicitly allows it; every search is audited. Clients do not search in the
  portal; Agent answers may cite search results with sources, and commitments still follow D121 (human only).

Scope: prototype (shared media components used by portal, engagement workspace and 会话), then specs 01 / 05 / 03 / 09 /
08 / 00 and PRD with the D121 spec pass.

Status: Accepted.

## D123 - 协作 Is Always One Project; Project Selector Next to the Logo; Previews Have a Home (2026-10-12)

Decision (user, 2026-10-12): 「顶部的项目菜单改为协作，项目切换器单独一个选择器放到左上角 logo 右侧下拉查看和切换，协作里面
不展示我的项目列表菜单」; 「先不做项目组合和全部项目视图吧」; and, asking where live previews are, the answer adopted here:
- Top bar: 工作台 · 会话 · 协作 · 团队 · 治理 (协作 replaces 项目). A project selector sits right of the logo; it always shows
  one project (the last used, default the first) and lists projects plus 〔新建项目〕. No 「全部项目」 option.
- 协作 is always the selected project's space; the portfolio, cross-project lists and 交付总览 are dropped for now. Old
  team-level routes (projects, work, requirements, defects, pipelines, deliveries, engagements, intakes) open the
  current project's page. 甲方对接 moves into the project space top group. Team goals stay under 团队 › 目标树.
- Work-item detail pages open in the item's own project, regardless of the selected one.
- Project space › 交付 gains 「预览与产物」 (`previews/<project>`): running local services (web / API) and deliverables by
  task (web, API, video, documents, source, files, novel), each opening the remote workspace (D119) viewer.
Supersedes the corresponding parts of D118 (project-first revision). Specs (PRD §12, 08 §8.6, 09 MenuDomain label) to be
updated with the D121 spec pass.

Status: Accepted.

## D124 - No Separate Client Portal: Clients Are External Members; Remove Redundancy (2026-10-12)

Decision (user: 「其实不需要单独的甲方门户，正常的项目视图都是甲方可以看的，会话频道就是甲方沟通的地方」, then 「冗余的东西都去掉」
on the proposal, taking its three recommendations):
- Clients are external members of the same app with real accounts (email invite; Google / email login), not portal
  access links. Their top bar: 待办 (things awaiting their sign-off, acceptance or change confirmation) · 会话 (shared
  channels and DMs with their 对接人 only) · 协作 (their projects only).
- In a project a client sees only: 概览 (milestone level), 需求确认单 (versions, sign-off), 变更 (confirm per D117),
  交付与验收 (with D120 anchored comments), 预览与产物 (client-facing items only), 问题反馈 (raise issues; triaged by the team
  into internal defects). Never: tasks, review desk, pipelines, executions, internal QA, internal defects, internal
  channels, Agent internals, goals / plans.
- Client communication happens in a shared channel in 会话, explicitly marked 「甲方在此频道」 and created by a human;
  channels are internal by default. D121's rules carry over: requirement drafts, 需求确认单, change and acceptance cards
  live there; Agent messages to the client need human approval by default; scope / dates / price / commitments are
  human only; the team's 内部旁注 is never visible to the client.
- Same routes, two views: a page renders the client view when the viewer is a client (server-side projection in the
  product; preview-as-client mode for the team). An automated leak check over every client-visible page stays.
- Removed as redundant: the separate client portal (all portal pages and layout), the 甲方对接 list and engagement
  workspace (merged into the shared channel and the 需求确认单 page), the old intake list and intake detail (intake
  channel configuration stays), and all legacy alias / redirect routes (links point to canonical routes).
Supersedes the client-portal parts of D117 / D120 / D121 / D122 / D123 (their rules carry over into the shared channel
and the client view). Specs (01 major, 02 / 05 / 07 / 08 / 10, PRD) to be rewritten after the prototype review.

Status: Accepted.

## D125–D128 — 全量需求补齐获准与 React Flow（2026-10-12）

Decision: 用户阅读恢复记录后说「继续，接受建议；可视化工作流用 https://reactflow.dev/？」；采纳缺口清单 E1–E4。
- D125：02 拆解模板与 11 领域模板统一 WorkflowDef JSON；React Flow（@xyflow/react）负责编辑画布，不能充当执行引擎或业务数据唯一真相。模板库提供编辑、Agent 草稿人工审核发布；项目流水线提供只读实例图。原型全部补齐，正式开发 v1.1；trigger v2。
- D126：MVP 收敛为软件交付核心闭环与内部质量门，重排八周计划；富媒体、语音、搜索、编制授权、非软件模板、远程终端 v1.1。原型仍完整展示分期能力。
- D127：有裁决权的人可以覆盖 FAILED / INCONCLUSIVE 自动结论；必须写理由、重新认证、审计留痕、交付包标注，不篡改原自动检查结果或外部红线。原型认证仅明确的模拟步骤。
- D128：A3/A4/A6–A9、B2–B8、C1–C4 按已提出建议直接补齐，不逐项再询问；仍处于规划和原型阶段，不开工产品后端/前端。
Reason: 恢复此前遗漏并使 PRD、规格、原型一致，同时让 MVP 规模可执行。
Alternatives Considered: 自绘画布；使用画布内部数据为执行模型；继续扩大 MVP。均不采用。
Tradeoffs: React Flow 增加局部 React 依赖；采用独立本地构建编辑器以复用现有静态原型，避免全站迁移。
Scope: docs/myrmidon/ 与本文件、MEMORY.md；不修改 apps/ 或旧 MyrmidonAI 项目。
Status: Accepted.

## D129 — 左侧菜单不跨顶级区域（2026-10-12）

Decision: 用户指出协作二级菜单中的频道跳往会话区不合理。删除团队/甲方协作侧栏的项目频道、甲方共享频道、共享频道项；所有频道从顶部「会话」进入。检查其余侧栏项同类跨区问题。
Reason: 左侧菜单应导航当前顶级区域，不重复其他顶级入口。
Alternatives Considered: 保留快捷跳转；不采用。
Tradeoffs: 进入频道经统一会话入口，保持导航位置可预期。
Scope: 原型导航和规格菜单同步。
Status: Accepted.

## D130 — 全 UI 以易用性和规模为验收依据（2026-10-12）
Decision: 用户要求重新评审全部 UI，删除冗余和不实用操作，优先易用、操作闭环、性能和大数据量。直接修复本轮原型相关问题；生产实现继续等待原型评审。
Scope: docs/myrmidon 规划、原型与验收记录，不触及 apps/。
Status: Accepted.

## D131 — 根目录持久状态文件（2026-10-12）
Decision: 按用户要求，将 .claude/decisions.md 移到根目录 ADR.md，将 .claude/task-state.md 移到根目录 MEMORY.md。CLAUDE.md 与现行文档引用同步更新。已发布历史 SQL 迁移中的注释保留原路径，不改迁移指纹；对应记录已完整迁入本文件。
Status: Accepted.

## D132 — 通用 AI 工作与交付平台 PRD 重整（2026-10-13）

Decision: 按用户要求优化 `docs/PRD.md`，从仅乙方软件交付定位扩展为面向 OPC、多人团队与多模型 Agent 的通用工作平台；补全协作会话、频道、线程、文件资产、知识、记忆、上下文与专业工作包。
Reason: 用户明确指出原架构不够全面，尤其缺少协作会话频道；新方向需要区分通用工作语义和专业流程，以及讨论记录和业务事实。
Alternatives Considered: 只增加知识库小节；为每种行业建设独立系统；把所有沟通和执行合成单一 Session。均不足以表达本轮需求。
Tradeoffs: 保留 PRD 原 §0–20 主题编号与现有治理约束，新增 §21–23 专题；引入通用成果版本、Review、个人作用域与 API 执行器的产品语义，会产生需后续同步的规格差异。
Scope: `docs/PRD.md`、本文件和 `MEMORY.md`；不修改 apps、规格或原型，不恢复用户已删除的旧 PRD / docx，不提交推送。
Status: 用户已请求通用方向与 PRD 优化；2.0 为规划稿。新增 P0 范围、具体权限政策、对象归属和排期建议尚不视作工程实施批准。PRD §18.2–18.3 列出需冻结选择与规格同步范围；旧工程契约未变更部分继续有效，冲突部分不得混用开发。

## D133 — 以用户、工作流程和协作责任重写 PRD（2026-10-13）

Decision: 用户否定上一版“在堆 UI，不是在做产品”，继而要求清楚说明用户与不同视角、通用流程与业务适配、人和 Agent 内部协作、会话 / 执行 / 实时反馈预览，以及减少页面和路由冗余但保持功能完整。按这组问题将 docs/PRD.md 重写为 3.0 产品设计提案。
Reason: 能力清单、页面数量和文档结构校验不能证明产品设计成立；必须说明一件工作的责任、推进、例外、交接、价值与实际使用路径。
Alternatives Considered: 在 2.0 后继续追加功能；只压缩菜单；将全部能力塞进单页。均无法回答用户的新要求。
Tradeoffs: 放弃沿用旧 PRD 的 §0–20 编排，主文按用户与工作展开，工程约束和规格索引移到附录；既有文档引用须按本版标题定位。提出工作台视图合并、规范对象地址、复用查看器与旧 UI 路由退休映射；这是产品方案，尚未改动路由实现。
Scope: docs/PRD.md、ADR.md、MEMORY.md；复用现有规格保留高级专业、目标报告、主管编排与治理能力，不修改原型、apps 或既有删除，不提交推送。
Status: 用户的纠正与需求已明确；3.0 已据此编写供评审，不能标为用户已接受。D132 的通用定位继续保留，其 2.0 章节结构与引用由本版取代；新增个人归属、共享政策与路由合并等提案需在实施前冻结，见 PRD 附录 B.3。

## D134 — 完整需求清单与环节小团队交付主流程（2026-10-13）

Decision: 用户要求结合原材料生成完整需求清单，明确 1–22 项后追加 23 服务器/服务健康群告警与 24 完整工作流程。新增 docs/REQUIREMENTS.md，PRD 顶部链接并声明本轮冲突优先级。
- 主流程：Human 与 Agent 会话协商 → 生成并确认计划任务 → 共同生成并确认 SOP Flow → 各环节 AI 执行、Human 负责的小团队 → 内部验收通过 → 下游接收或有依据退回上游重做 → 全部必需环节完成 → Human 负责正式交付。
- 明确纳入组织树、OKR 审核、计划跟进、跨项目全局工作台、永久消息与全局可追溯搜索、自动记忆知识、分布式 daemon、frp 式随机域名预览/文件/PTY终端、Token 自动调节、定时任务、自动周期报告、监督 Agent、服务器与服务告警、React Flow 无限画布、Tauri Web/桌面/移动、多 runtime/API 与远程模型配置、轻量无 JVM 新核心栈。
- 覆盖旧“不提供跨项目汇总”和“移动端仅响应式/桌面可选”限制；消息默认永久保存。交付必须内部必需门禁通过，旧有理由覆盖不能直接绕过失败验收交付，误检应保留原记录并复核。
Reason: 需要完整、可追踪的需求基线，逐项对应用户原始编号；上下游交接与退回必须具备版本、责任和失效传播，不能只有画布和群聊。
Alternatives Considered: 所有材料进万能记录表或知识库；消息仅依赖总线历史；直接堆叠 Matrix/Centrifugo/NATS。建议采用领域事实+对象原件+审计事件+统一引用+可重建搜索+有来源知识记忆，并按职责选消息组件。
Tradeoffs: 范围明显大于旧 MVP，不继承原八周估算；永久存储、实时基础设施、跨平台和多 runtime 需要实际资源/版本验证。Matrix 互联和加密策略、具体中间件、发行包名、性能预算、保留例外仍为待验证或实施前冻结项。
Scope: docs/REQUIREMENTS.md、docs/PRD.md 的最新入口、本文件、MEMORY.md；无产品实现、无原型或规格批量改动、无提交推送，保留既有删除状态。
Status: 用户明确的需求范围与主流程已记录；清单中的技术建议和具体实现方案不视作用户已批准或已验证兼容。清单为当前 24 组需求的权威入口。


## Workspace project initialization — 2026-10-13
- Decision: rename apps/frontend-web-admin to apps/frontend-web; initialize apps/frontend-app using the official Tauri 2 React/TypeScript/Bun template; initialize apps/runner as an independent Go module.
- Reason: Web, native client and execution node have distinct build/runtime boundaries. Runner includes future CLI/daemon and agent/model/executor responsibilities, so its directory is runner rather than runner-cli.
- Alternatives: runner-cli would describe only the command-line surface; a nested Git repository would disrupt the existing monorepo.
- Tradeoffs: independent package manifests and build commands; no shared client package extraction at this stage.
- Scope / Status: user explicitly requested initialization only; no model adapters, task execution, daemon, node registration or client integration implemented. Formal application identifier remains a release-time setting.

- Refinement: keep the frontend monorepo with one apps/web application (renamed from apps/iam); sys/ops/ai remain future in-app domain boundaries. frontend-app is packaging only and embeds that application's build; duplicate React template and greet demo removed. Go remains runner's host; Node.js adapters are added only when an SDK requires them. No upgrade/keepalive implementation in this initialization task.


## Slack-like collaboration transport — 2026-10-13
- Decision: use Centrifugo as the client realtime transport, with existing Go collab business logic, relational message storage and file storage. Do not add Matrix or NATS/JetStream for this scope.
- Reason: user asked to select only the necessary stack. Collaboration must share platform identities, project/object authorization, task references, approvals and search; no federation or Matrix-client interoperability is required.
- Alternatives: Matrix provides a broader chat protocol but introduces room/identity/permission integration; NATS is infrastructure messaging rather than the required end-user collaboration layer. Implementing an additional custom chat WebSocket transport would duplicate Centrifugo responsibilities.
- Design: transactionally persist messages and outbox before publishing; retry delivery and deduplicate/reconcile on clients. Centrifugo history is bounded recovery cache, never the permanent archive. Backend authorization governs subscriptions and permission revocation. Multi-instance Centrifugo uses Redis; a single instance can start with the memory engine and database catch-up. Mobile OS push remains separate.
- Tradeoffs: threads, read positions, mentions, membership, search and audit remain platform responsibilities; an additional realtime service needs monitoring. Deployment capacity and failure recovery require later validation.
- Scope / Status: selected architecture, documentation only; no dependency installation, service deployment or chat implementation. Separate node dispatch does not inherit a NATS requirement.
- Sources: https://centrifugal.dev/docs/getting-started/introduction ; https://centrifugal.dev/docs/server/history_and_recovery ; https://docs.nats.io/nats-concepts/jetstream


## External chat connectors and Centrifugo NATS clarification — 2026-10-13
- Requirement: support future WhatsApp, Telegram, WeChat/WeCom, Slack and other chat integrations through authorized connectors. Keep per-platform capability declarations, identity/channel mappings, message provenance, idempotency, loop prevention and delivery status. Sending notifications alone does not imply bidirectional chat or complete historical sync.
- Clarification: Centrifugo supports Core NATS as a broker (no publication history/recovery) and JetStream as an async consumer (a distinct integration). The earlier decision excludes NATS from the current deployment, not from future compatibility.
- Decision: retain Go collab + database + Centrifugo; external connectors use official platform APIs independently. Revisit JetStream only when a concrete backend messaging requirement exists; external chat integration alone is insufficient reason to deploy it.
- Scope / Status: documentation only, no external accounts connected and no messages sent. Platform-specific account eligibility, scopes and feature parity remain implementation-time verification requirements.
- Sources: https://centrifugal.dev/docs/server/engines ; https://centrifugal.dev/docs/server/consumers ; platform sources in docs/REQUIREMENTS.md §8.3.


## 2026-10-13 — Consolidated final PRD authority
- Decision: docs/PRD_FINAL.md is the integrated product entry; retain original PRD.md and REQUIREMENTS.md as unchanged source baselines. Preserve all 291 existing requirement IDs, namespace PRD acceptance as JNY, add explicit gap and operational criteria.
- Reason: user requested a complete, more detailed PRD with no silent loss and two reviews. Conflicts are reconciled inline and tracked in T2, rather than leaving contradictory normative copies.
- Alternatives considered: shorten the PRD into a checklist or only append references; rejected because professional behavior and journey details would be lost.
- Tradeoffs: narrative and acceptance layers overlap intentionally; one implementation per domain fact. Unconfirmed policy/technical parameters remain explicit in S8.
- Scope/status: documentation complete and reviewed twice; no schema/API/product implementation implied. Current next goal is a new complete mock prototype for review.


## Dynamic API field rules — reviewed recommendation (2026-10-13)
- Decision: runtime per-path OPTIONS derived from final Huma input schemas, compiled by frontend into shared field rules; preserve server validation and add structured validation issues. Full contract and impact: apps/backend-api/docs/dynamic-field-rules-design-2026-10-13.md.
- Reason: user requires runtime reuse of backend constraints and messages rather than duplicated per-page rules; existing AutoForm supports rules but actual Web forms use a separate handwritten path.
- Alternatives considered: build-time validation generation (does not satisfy runtime goal); separately maintained OPTIONS rules (duplicates source); arbitrary downloaded scripts/full custom validation DSL (unnecessary and unsafe semantics); wholesale form rewrite (out of scope).
- Tradeoffs: unknown rules/metadata failures explicitly delegate to existing server validation with visible notice; tri-state composite semantics; same OpenAPI exposure switch; application-lifetime cache per open form, not HTTP OPTIONS cache. Standard constraints stay in Huma; only needed custom rules share Describe/Validate.
- Scope: design only; no API/frontend implementation, password-policy change, DB migration, dependency addition, commit or push.
- Status: two independent review rounds integrated and primary-agent acceptance complete; recommended implementation plan, NOT user implementation approval. Password tag/runtime divergence is recorded as an identity-form migration prerequisite, not silently fixed.


## Dynamic field rules — user constraints revision 2 (2026-10-13)
- Decision: enforce four user requirements: shared UI auto-binding handles input limits/validation/errors; backend tagged module-owned declarations are the single field-rule source; messages cover zh-CN/en-US/ms-MY; all affected paths and discovered rule/conversion inconsistencies must be resolved before implementation acceptance.
- Reason: initial staged plan could leave handwritten forms, duplicate service checks and known drift unfinished.
- Alternatives / Tradeoffs: no per-page rule assembly; no schema-only publication over divergent business validation. Shared form boundary and field adapters preserve layout and original behavior outside the boundary. Custom tag handlers derive metadata and validation together; business-state checks remain server-side with localized structured errors.
- Scope: updates reviewed design and acceptance only, not production implementation. Password character/byte constraints require compatibility-preserving consolidation, not silent policy strengthening; amount rounding/date conversions are necessary affected-path fixes.
- Status: user requirements confirmed; supersedes earlier notes deferring affected password/price issues or treating unsupported existing rules as acceptable completion. Implementation cannot be declared complete at pilot stage.


## Dynamic field rules — exhaustive impact closure, revision 3 (2026-10-13)
- Decision: refine the approved design direction using actual registration provenance: Huma OperationDocumenter identifies typed operations, Adapter.Handle records runtime paths, final schema joins by operationId; do not derive execution behavior from final OpenAPI alone. Extensions compile before Huma precompute; service validation uses write-to-server semantics and preserves normalization/profile ordering.
- Evidence: base398 operation tuples exactly equal live composition; optional diagnostics yield411 documented operations. Actual registrations372 typed +26 direct adapter;13 optional raw docs; one file wildcard path rewrite; OAuth5 form bodies documented after registration require protocol execution classification.
- UI decisions frozen: target/buildRequest/bindings descriptor; per-operation issue_messages from backend error/i18n definitions; preserve existing401 retry, best-effort account-view exit and synchronous popup; preserve current amount/time wire semantics; typed multi-issue/focus/version policy.
- Scope: 4 attached matrices under apps/backend-api/docs covering411 operations,20 HTTP modules/357 scanned production files,54 UI entries/48forms/262 referenced API members,31 FieldTypes. Metadata/UI automatic validation implementation still not performed. No unresolved identified design decision remains; all listed fixes are required implementation scope.
- Alternatives: final-Paths-only registration, treating docs-only body as executed schema, limiting migration to forms/AutoForm, blocking logout on OPTIONS, or adding legacy shared-library business endpoints were rejected on concrete evidence.
- Status: user-requested impact audit complete within explicitly enumerated source/runtime sets. Does not claim every semantic branch proven or future implementation accepted; production tests/browser tests listed in matrices remain required. Preserve all unrelated workspace changes; no commit/push.


## Final prototype navigation and interaction decisions — 2026-10-13
- Decision: tenant switch/create/join beside Logo; tenant owns multiple project instances with tenant-scoped rollup. Only tenant workbench lists all projects; instance workbench is the selected instance overview. Collaboration contains operational features, not duplicate overviews. Sidebar/channel selection changes local panels without URL/history/top-area transitions.
- Reason: explicitly confirmed user corrections during prototype acceptance. Keep object deep links for deliberate detail navigation.
- Decision: exactly reuse six original theme palettes and existing three dark palettes; extend the remaining dark palettes explicitly. Shared styled controls/validation/scrollbars. Workflow UI uses that name, n8n-style node library + inspector; calendar and multi-tab task preview share existing domain state.
- Alternatives rejected: duplicate project list below selector, project-as-tenant identity, cross-area sidebar shortcuts, approximate theme colors, native browser confirmation/validation popups, separate calendar task records, one hardcoded task preview.
- Scope/status: implemented and reviewed in docs/prototype mock only; captured in PRD_FINAL S9. Real tenant authorization, execution engine, previews and resource lifecycle still require backend/native implementation and separate acceptance.


## 2026-10-13 — Internal handoff acceptance authority
- Decision: the designated downstream Human receives the fixed internal handoff package and that action fulfills L3 authorized acceptance. Do not add a mandatory separate upstream Human approval for every internal handoff. External publication retains separate authorization.
- Reason: user explicitly selected this option when the PRD ambiguity was surfaced; avoids redundant approvals while keeping accountable Human receipt and immutable evidence.
- Scope/status: confirmed for prototype and PRD design; implementation underway. Package version, applicable L2 checks and recipient identity must still be validated. Does not waive existing external publication permissions.

## 2026-10-13 — Conversation-first work and management flow refinement
- Decision: keep New Work as a shortcut into a conversation draft; explicit Human confirmation creates work and links its confirmed plan. Tenant membership inbox stays beside the Logo; organization page manages outgoing scoped invitations and members.
- Decision: manual and scheduled report generation share one logical identity per tenant/subject/cadence/calendar period/timezone. Regeneration preserves Human edits and submitted snapshots; corrections are revisions of the same report.
- Decision: Human estimate, suggested duration, calibrated duration and confirmed actual effort are distinct; Agent wall-clock, tool/model duration, tokens and costs are separate read-only telemetry dimensions. No inference of actual Human hours from chat presence. Existing submitted historical logs retain their original interpretation without inventing confirmation metadata.
- Decision: Agent configuration includes role, responsibilities, system prompt, task/user template, runtime/model version and selected Skills/MCP. Tool selection does not grant permission. The navigation label is Workflow Templates; broader professional work packages retain their existing product responsibilities.
- Reason: latest user identified seven incomplete/awkward flows and asked for coherent design and a members-page redesign. These refinements reuse current work, report, organization and workflow facts rather than parallel records.
- Alternatives/tradeoffs: generic CRUD work initiation and manually entered Agent telemetry rejected; separate duplicate reports per trigger rejected; task-specific and Human confirmation flows require explicit states. Real email, scheduler, runtime, usage ingestion and authorization remain production work, not local prototype claims.
- Scope/status: PRD_FINAL v4.2 S10 and local prototype changes; integration tests and review tracked separately in docs/prototype/REVIEW.md.


## 2026-10-13 — Human organization versus Agent roster and task execution team
- Decision: organization/members includes Human only. Agent identities live in their own roster, with separate Human-created and Agent-created lists and immutable exact creator attribution. Task execution teams (usually Agent pools, optionally Human) live under tasks/execution and reference those identity sources.
- Decision: editing a task team propagates to current assignment views; it does not edit formal organization or historical artifact authors. Dynamic/temporary creation and parent retirement do not auto-delete Agent identities. Lifecycle actions preserve records and follow existing authorization.
- Reason: explicit latest user correction: placement, hierarchy and views must be unambiguous and linked. Supersedes the earlier mixed organization roster and members-page task-team tab.
- Migration: legacy mixed organization links are archived and removed from current Human membership; Agent records and work references survive. Missing historical creator identity remains explicitly unknown rather than guessed.
- Scope/status: PRD_FINAL S10 and prototype; task-team relocation and integrated scoped review passed (130 unit tests; 21 browser suites at desktop and mobile). Runtime processes remain mock-only.

## QA 批次固定与独立门禁（2026-10-13）
- **Decision:** 测试计划固定用例修订、条款映射、目标构建和环境；批次固定工作版本与返工声明链。每批每项只允许记录一次结果，需要重验必须先结束旧批次，再启动新批次；测试报告保存不可变计划与结果快照。
- **Reason:** 防止覆盖失败来伪造绿色门禁或绕过失败轮次，避免陈旧弹窗改写已记录结果。通过、失败、跳过、阻塞、未执行分别统计；缺失条款映射不推断为全覆盖。
- **Alternatives Considered:** 同一批次反复覆盖当前结果并只保留最近结论；拒绝，因其不能明确界定失败轮次和复核证据。
- **Tradeoffs:** 复验多一步结束／新建批次，换取明确历史和计数边界。原型使用明确标记的模拟证据，仍执行真实状态约束。
- **Scope:** 原型 QA 计划、批次、报告和工作必需质量门。主体按 Human／Agent 稳定 ID 判断独立性；当前有效固定批次有失败才计内部失败轮，多项失败同批次仅一次，旧版本或旧声明链报告不污染当前计数。本决策不改变既定失败阈值。
- **Status:** 依据既有 PRD 与工程规格，由主代理在已授权原型评审修复范围内确定实施细节；实现及整体验证进行中，不代表新增用户产品审批。


## 2026-10-13 — Task preview width and focused content
- Decision: desktop execution preview has an accessible draggable/keyboard separator with bounded width and local width preference; compact layouts retain the full-width dialog. Remove the two footer links to artifact comments and acceptance from TaskPreviews.
- Reason: explicit latest user instruction; preview panel is for preview, task/artifact actions already have their own entry points.
- Scope/status: local prototype implemented; actual mouse344→584px, keyboard bounds/persistence and375px preview tests passed. The broader full-PRD audit remains active.


## 2026-10-13 — Delivery-driven preview resources and browser chrome
- Decision: task resource manifests determine tabs; no manual add/close and no fixed tab set based solely on discipline. One material/service per tab; repository uses tree+code view. Web/API/Swagger use service-specific views; document/media/config formats use suitable viewers. FRP is transport, not a mandatory rendering mechanism.
- Reason: explicit user corrections. Content must fill available height, desktop width adjustable and expandable, mobile fullscreen. Do not put acceptance/work actions in preview.
- Decision: Web toolbars provide per-tab back/forward/refresh/address and planned authorized new-window access. Cross-origin internal navigation needs a verified preview bridge; normal iframe DOM/history cannot be read. File viewers use format-specific reading controls instead of irrelevant navigation.
- Tradeoffs/status: mock toolbar and resource-driven tab selection implemented; actual iframe gateway/new-window resource URLs, media/Office rendering and service lifecycle remain production integration. Spec and prototype never claim those connections exist.


## 2026-10-13 — Prototype audit timestamps and project identity repair

New audit events and chat messages retain ISO UTC occurrence timestamps; Intl formatting belongs only in display. Existing HH:mm records remain date-unknown rather than being assigned today's date. This repairs traceability without fabricating historical facts.

New project creation references a Human ID from the active tenant, stores the display name alongside it, and rejects stale-tenant or unknown/Agent identities at the domain command. Owner choices come from the tenant roster rather than a fixed name list. Existing project records are not silently reattributed. These are requirement repairs, not new product policy.


## 2026-10-14 — Report period identity and immutable historical scope
- Decision: report uniqueness uses tenant + subject + cadence + periodStart; timezone is retained metadata, not a second-report key. Reopening a generated calendar period uses its original timezone and boundaries; direct identity/timezone rewriting remains rejected.
- Reason: spec10 §3.8 report_key and BR-10-43 explicitly require one report per subject/calendar period and preserve generated boundaries after timezone changes.
- Decision: acceptance-based report completion uses immutable event dates/subjectKeys and exact versioned event IDs; distinct work counts dedupe workId, not acceptance versions. Historical Plan reading is explicit and preserves default live Plan behavior. New Human effort captures fixed scope at creation; records for dates other than today do not infer historical project/team membership from today's organization.
- Alternatives considered: counting today's completed status, rereading today's work/organization assignments, inferring saved legacy authors by display name, adding timezone to uniqueness, and reconstructing old content from current work were rejected because each fabricates or rewrites historical facts.
- Tradeoffs: missing legacy scope/time/history is surfaced as unknown and excluded from certified scoped aggregates. Stable personal owner IDs remain valid evidence of individual ownership. Known pristine demo fixtures remain distinguishable from saved legacy data.
- Scope/status: parent-authorized local prototype report fact correction implemented and scoped tests/browser/mutations passed. Does not certify production runtime, full report content sections or the entire PRD.


## 2026-10-14 — Report top-level authority and immutable submission identity
- Decision: SYSTEM:team_top applies to tenant report only, not arbitrary organization node; keep independent Human authority for node/project reports, prohibit self-review absent explicitly supported policy. Missing submitted confirmer cannot be retroactively approved from current settings.
- Reason: spec10 BR-10-20/24 and reproduced node/null-confirmer bypasses; original author/subject cannot change merely because a legacy calendar key is missing.
- Alternatives: treating every team node as tenant TEAM or granting generic owner-role bypass rejected; these widen the documented exception.
- Scope/tradeoff: local prototype report domain only; old unresolved submissions may be returned and resubmitted, full organization-supervisor policy and production IAM not implemented.
- Status: bounded domain and1440/375 browser verification PASS, full report and global acceptance not achieved.


## 2026-10-14 — Goal / Plan / report authority boundaries
- Decision: one目标管理 entry/object expresses O/KR; period Plan owns confirmed commitments; period report owns fixed observed facts/reflection. Reuse references rather than duplicating quarterly goals per week or creating report-only next plans.
- Reason: user's redundancy concern, existing core object boundaries and independently reproduced missing nextPlan/report structure.
- Alternatives/tradeoff: collapsing all three objects loses separate commitment/observation/version responsibilities; keeping distinct objects requires bidirectional drilldown and factual generation to avoid repeated input.
- Scope/status: wording and menu deduplication implemented and1440/375 navigation/collab/goalsPASS; complete linkage/report content still required. Routine service-only Plan not supported by current mandatorygoal_id contract and needs explicit model rather than fake catchall goals.


## 2026-10-14 — Fixed report confirmation and personal execution
Decision: apply existing BR-10-40 through explicit Human REOPEN, preserving confirmed snapshots and stable authorship even when current author is absent. Personal completed-work reports require immutable executor identity; owner/acceptance actor alone does not prove execution.
Reason: both direct edit of confirmed reports and actor/executor conflation were reproduced. Alternatives rejected: automatic draft conversion, inferring executors from current assignment, deleting historical confirmation.
Tradeoffs/scope: local prototype only, no backend or runtime claims. Separate decision contribution reader and structured report content remain incomplete. Status: scoped implemented; integrated full acceptance pending.


## 2026-10-14 — Child report evidence preserves every scope and fixed period
Decision: aggregate fixed submitted child bodies and fixed fact leaves, filtering each intermediate scope/time period; source links use one shared eligibility rule across editor/save/check. Cross-zone day-only effort overlap is unknown rather than split. Duplicate child paths within a parent are visited once.
Reason: independent probes demonstrated scope leakage, rewriting/dropping historical facts, false zero, and exponential traversal. Alternatives rejected: same-tenant personal pooling, current organization backfill, current-source substitution, proportional day splitting.
Scope/tradeoffs/status: local reporting prototype implemented and bounded gates pending completion; missing historical membership, full denominators and four report sections remain explicitly incomplete. No backend/API expansion.


## 2026-10-14 — Reports adopt real next-period member Plan revisions
- Decision: report.nextPlanBinding captures real tenant/member/next-calendar-period Plan ID, revision, Plan snapshot, confirmed baseline and actual adoption-time checks. It is part of the fixed report submission fingerprint; absent/null binding preserves the previous ten-key legacy fingerprint. No independent report-only editable next-plan list.
- Drafts must explicitly re-adopt after Plan revision/body/dependency changes; fixed submissions preserve original content/checks and warn on current changes, deleted dependencies or duplicate live Plans. Human report acceptance does not approve or rewrite the Plan. Original existing Plan timezone is retained and disclosed rather than creating another Plan.
- Real member Plan creation/opening is an explicit action through the existing Plan workflow. Tenant filtering and duplicate conflict checks apply to shared openPeriodPlan. Project/team next-plan projection is still unavailable; do not create a fictional member Plan for aggregates.
- Browser reproduction found ordinary shared Button/IconButton defaulted to native submit inside forms. Defaults now type=button, with explicit submit overrides retained; report navigation and adoption are no longer accidental saves. This is an actual business-flow correction, not style refactoring.
- Scoped status: 273 tests / 2546 assertions and six related browser suites at1440/375 PASS; broad shared-component browser regression still running. Report four sections/carry/outcomes and global acceptance remain incomplete. No backend/real runtime claims, no commit/push.

NextPlan decision final bounded validation: unit273/2546 and buildPASS, all36browser suites each1440/375PASS/sourcehashes matched, seven semantic mutants + controlled defaultButton browser negative detected. Remaining structured-report gaps independently re-proven; globalgoalstillACTIVE/NOTREADY.


## 2026-10-14 — Structured report facts, author confirmations and safe historical rendering
- Decision: persist four sections as typed content and include it in fixed submission/approval fingerprints; absent legacy content retains its original fingerprint. A completed WORK_ITEM is tied to actual fixed acceptance event/version/scope/executor; unsupported readers cannot authenticate completion. Downstream Plan chapter is derived from the real adopted Plan snapshot.
- Decision: author detail/reflection acknowledgment is generated by trusted store actions, bound to content fingerprint and stable author; Human-triggered Agent acknowledgment is explicitly local simulation with recordedBy. Historical display uses stored author identity, not today's roster.
- Decision: fixed structured submissions require an intact factSnapshot. Missing/corrupt evidence blocks with B1, never switches to live sources; valid fixed history remains readable/confirmable after current sources disappear. Malformed content renders an error without deleting raw data or inventing replacement chapters. Ordinary save refuses malformed references/review metadata atomically.
- Scope/tradeoff: local prototype, no production provenance claims. Explicit-only content initialization is interim, not the final requirement; mandatory new-report content/migration/currentPlan/carry/outcomes and aggregate projections remain unfinished. Empty-section none policy needs explicit resolution; do not synthesize tasks or accept silent empty chapters. Scoped gates underway, not full acceptance.

## 2026-10-14 — Corrupt fixed Plan display and projection
- Decision: the existing reportNextPlanItems projection rejects malformed adopted snapshot structure and non-scalar directly rendered metadata/IDs. Report detail and editor reuse this check and display an explicit diagnostic, preserving the raw adopted object.
- Reason: personally reproduced missing/null/non-array items TypeErrors and object-valued goalId/revision React child crashes; independent SSR reproduced the latter.
- Alternatives/tradeoff: no fallback to current live Plan, no serialization of corrupt objects into plausible IDs, no automatic empty replacement. Absent binding and valid empty historical lists retain their original meaning. Shape validation is not authority or Plan acceptance.
- Scope/status: local report prototype repair; sixteen malformed-shape unit/SSR regressions, browser verification pending in checkpoint. No product rule or production API change.

Final bounded verification: `/tmp/report-plan-corruption-final-browser-{1440,375}.json` each8suitesPASS/sourceUnchanged=true, including all16corrupt adoptedPlan shapes through actual detail→edit→rejected save→cancel with storage unchanged. All106frozen src/test/dist files match worktree; browser runner syntax and git diff --checkPASS. Independent scoped reviewPASS; owned servers/browser sessions closed, user4173untouched. This closes the corruption class only; global goalACTIVE/NOTREADY, consecutive full clean rounds0.

## 2026-10-14 — New report structure and explicit legacy migration
- Decision: new generator and direct-save creation require empty typed four-section drafts; unavailable stats remain null, no completion/uncompleted/next commitment invented. A trusted contentRequired marker is immutable through public save and included in fixed submission fingerprints.
- Legacy: old fixed bodies retain their ten-key fingerprints. Generate/open keeps old body unchanged; explicit migrateLegacyContent saves a new draft under the same ID, preserves original revision and any original submission in existing history, and records trusted Human actor/time/sourceRevision. No automatic reinterpretation of old prose or approvals.
- Tradeoff/scope: missing/incomplete new chapters block rather than inheriting legacy compatibility. Aggregate nextPlan projection remains unsupported, not hidden by legacy fixture passes; unresolved empty-section policy is not invented. Local prototype only.
- Verification: full295tests/3063assertions+buildPASS; four semantic mutants killed; final frozen browser regressions pending in latest MEMORY checkpoint. Initial list-projection browser crash reproduced and repaired, failures retained.


Final bounded required-report verification (2026-10-14): 295 tests/3063 assertions and build PASS; frozen `/tmp/report-required-verified-20261014` all107 src/test/dist hashes independently rechecked against worktree. Both `/tmp/report-required-verified-browser-{1440,375}.json` contain38 suites, pass=true, sourceUnchanged=true, and sourceHashes matching freeze. Four semantic mutants detected; independent scoped reviews passed. Initial React list crash and subsequent modal-test misdiagnosis evidence retained; corrected modal test verifies actual dirty-discard resolution, not background DOM clicks. Owned servers/debug/reviewer browsers closed per terminal checkpoint; user4173 untouched. This is bounded verification only: global ACTIVE/NOT READY, consecutive full clean rounds0; missing currentPlan/carry/aggregate projection and wider requirements remain.


## 2026-10-14 — Confirmed Plan baselines fail explicitly without live substitution
- Decision: validate tenant, fixed subject/calendar/timezone, baseline revision, item identities/type/title/completion criteria/positive finite weights before projecting confirmed Plan facts. Invalid baselines return diagnostics and null progress/counts, retaining originals; never substitute current draft or quietly normalize stored evidence.
- Reuse: one planConfirmedSnapshotProblem guard serves Plan reading, all dedicated Plan commands, report next-Plan adoption and fixed binding checks. Free-task completion history uses the same shape predicate in reader and writer; a matching entry cannot hide corrupted surrounding history. Report generation discloses unavailable baselines and N2 retains this as a nonblocking issue under existing spec.
- History: legitimate fixed report bindings stay readable if current Plan changes or becomes corrupt; corruption of the adopted fixed baseline itself is rejected. Read-only original baseline/history remains accessible on the diagnostic view; no silent reset or invented approval.
- Alternatives rejected: throwing React/TypeError, reporting zero/negative progress, pooling foreign-tenant Plan records, taking a conflicting live subject/timezone, and certifying an adopted corrupt baseline through the report bypass. Unprovable old metadata is preserved and shown unavailable.
- Scope/status: authorized prototype repair only, not production IAM or full report/currentPlan/carry/aggregate implementation.300 tests/3585assertions+buildPASS; final39browser×2 pending. Full productACTIVE/NOT READY, consecutive clean rounds0.


Final bounded verification 2026-10-14: full300 tests/3585 assertions+buildPASS; `/tmp/plan-baseline-adoption-20261014`108src/test/dist hashes match worktree,39suites each1440/375PASS with sourceUnchanged=true and both source/scriptHashes matched (`/tmp/plan-baseline-adoption-browser-{1440,375}.json`). Seven corrected semantic mutants detected only after unmutated controlPASS; initial missing-React mutant results explicitly INVALID and retained. Independent domain/UI reviews and parent keyboard/raw-history checks passed within stated scope; gradient contrast still incomplete. All this turn owned servers/browser sessions closed, user4173 untouched. GlobalACTIVE/NOT READY, consecutive complete clean rounds0; no requirement-wide acceptance upgrade, no commit/push/backend changes.


## 2026-10-14 — Exact current-period Plan diagnostics and fixed-history retention
- Decision: member report current Plan selection is same tenant/subject/cadence/calendar start and end. DAY Plans are not a WEEK baseline; duplicate same-period Plans are an explicit unresolved conflict, never merged or chosen arbitrarily. Confirmed replans continue using original confirmedSnapshot with pending-reconfirmation N2; unconfirmed drafts have no confirmed progress.
- Reason: root + independent read-only probes reproduced mixed cadence/duplicates, missing draft/reconfirmation N2, and loss of original N2 after fixed submission. Existing BR-10-17/30/48 governs; no new blanket submission block or extra approval added.
- Compatibility: reportEvidence stores planIssues alongside existing factSnapshot. Fixed reports restore those original strings; older warning-only snapshots preserve original N2 without live Plan substitution. Corrupt planIssues yields explicit error, not historical repair.
- Scope: selection/diagnostics only. This does not implement report currentPlanBinding, baseline items/outcome snapshot, current-item reference checks, carryover or full report generation. Prototype/local simulation; no backend/runtime changes.


Final current-Plan diagnostic checkpoint 2026-10-14: full302tests/3610assertions+buildPASS; controlled5semantic mutants all detected, unmodified controlPASS. `/tmp/report-current-plan-freeze-20261014`107src/test/dist hashes match final worktree. `/tmp/report-current-plan-all-browser-{1440,375}.json` each39suitesPASS/sourceUnchanged=true; all source/scriptHashes match freeze, actual served4223 dist bytes match worktree (`/tmp/report-current-plan-final-hash-audit.json`). Independent product/UI checks and root visual checks passed within diagnostic scope; scoped375light/dark ReportFacts axe0violations/0incomplete. Browser jobs76376/30095 and scoped jobs13484/92172 exited0; runner closes named browsers; independentUI browser closed. Owned4223 preview15663 stopped; user4173untouched. No commit/push/backend changes.
Global goalACTIVE/NOTREADY, consecutive full clean rounds0. This repairs exact-period selection/N2/fixed diagnosis only; fixed current-Plan baseline identity/items/outcome evidence and report auto-generated unfinished items/typed references/carryover/provenance/outcome history are still next, followed by aggregate/other fact readers/OKR lifecycle/archive/scoped libraries/professional/org/platform/large-data/fullguideline/taste requirements. No reduction of350REQ/70scenario denominator.


## 2026-10-14 — Member report current-period fixed Plan reference
- Decision: optional report.currentPlanBinding captures genuine same-tenant/member/cadence/calendar Plan identity, adoption sourceRevision, baseline revision/status, confirmed flag and exact snapshot. ConfirmedSnapshot is the commitment source even when a later draft is pending; no baseline means explicitly unconfirmed draft, not inferred approval. Report fingerprint conditionally includes binding; null/absent retains exact old compatibility.
- Reuse: reportCurrentPlan is extracted from existing exact-period selection and reused by diagnostics/adoption/check; structural guard reuses planConfirmedSnapshotProblem. Existing ReportPlanSnapshot/Section/Notice/Link/Button + modal dirty-navigation reused; no independent report Plan list or editor.
- Changes: confirmed notes/real estimate updates do not force re-adoption because commitment snapshot stays identical; a new confirmed baseline or changed unconfirmed draft requires explicit adoption for draft save/submission. Incoming forged metadata/snapshots denied; original retained binding may keep historical sourceRevision/status; no implicit removal.
- History: fixed report binding remains readable after live source deletion/conflict/corruption or roster loss; current drift is disclosed as warning, never substituted. Malformed embedded fixed identity/calendar/items rejects; report acceptance fingerprints binding and never approves Plan. Missing unbound current Plan still N2, no new mandatory adoption policy.
- Scope/status: local prototype binding/reference only. Not current-item validation, auto-uncompleted/outcome snapshots, carryover/provenance, aggregates or real Agent runtime. Build passes but main minified chunk504.03kB/gzip159.30kB triggers Vite500kB warning; performance gate remains open, threshold not raised.


## 2026-10-14 — Explicit Plan correspondence in report entries
- Decision: an explicitly referenced Plan item is verified against the report's adopted fixed snapshot, never today's live Plan. An empty reference remains an off-plan entry subject to its own fact/author checks. Optional adoption remains optional when no explicit Plan item is claimed; explicit claims without a verifiable binding receive a diagnostic, not guessed history.
- UI: show original stable IDs/titles, offer only compatible fixed work for accepted completion, permit author-declared off-plan incomplete entries, invalidate reason/disposition confirmation on link changes. Invalid old IDs remain visible until explicit correction; report linking never approves/edits Plan.
- Shared control: open Select focuses enabled selected option or first enabled option, retains existing Escape/focus restoration behavior. No alternate native selector introduced.
- Scope/status: prototype only; local simulations and incomplete fact readers explicit. Raw old histories remain preserved. No global acceptance;41suite regression currently running.

## 2026-10-14 — Typed FREE_TASK report evidence without invented work acceptance
- Decision: a confirmed adopted current Plan supplies FREE_TASK_EVIDENCE with the original item/event/commitment, subject and actor identity, nonempty evidence, Plan and report timezone periods, and as-of cutoff. Work and FREE events have typed dedup identities; completed_count includes both, accepted_work_items and legacy work totals count only Work IDs.
- History/reuse: retain existing reportEvidence.structuredFacts in submission.factSnapshot; fixed reports read only selected frozen FREE facts with original submission cutoff, never current Plan fallback. Missing/corrupt historical facts remain explicit B1; no migration or inferred historical owner. Shared planFreeTaskFactValid now rejects wrong kind/actor or missing Agent recorder.
- UI: genuine evidence picker inserts the exact fixed Plan item, displays evidence/time/actor and explicitly local Agent simulation/recording Human; no work ref or fake version for FREE tasks. Existing component/theme/layout reused.
- Scope: reader and manual adoption only. Automatic report chapter generation, fixed Plan outcomes, carryover, DECISION/DELIVERY readers, aggregates/metrics and global350REQ/70scenario acceptance remain open.321unit tests/4010assertions+buildPASS; six corrected semantic mutants detected; dedicated browser flow1440/375PASS. Full42browser suites each width currently running; terminal evidence pending in MEMORY. First cutoff mutant survived because the test only checked an out-of-period future date; added same-period as-of assertions detects it. Initial UI suite used aria-label for a name-based textarea; corrected test selector, no product fix.

FREE_TASK decision terminal validation2026-10-14: exact-source321tests/4010assertions+buildPASS;42browser suites each1440/375PASS,114freeze+8served bytes+allsource/script hashes matched. Independent bounded domain and UI reviews complete; root genuine acceptance and history probesPASS. No global acceptance upgrade; new-report automatic chapter fill remains a directly reproduced requirement gap.


## 2026-10-14 — Immutable delivery receipt and shared PageHead containment
- Decision/reason: spec07 BR-07-34 requires one fixed receipt per delivery. Preserve existing same-content retry before rejecting changes to accepted/partial/rejected receipts. New outcomes require explicit Human-authorized rework and a new versioned fixed package; do not overwrite rejected evidence or relabel the original package.
- Reuse/scope: existing receipt item validation/transaction/rework/role/offline controls, UI Button/Notice; no new receipt schema, authenticated client identity, signatures, backend or transport. Domain + actual4stateUI checks and semantic mutation verify only this local prototype boundary.
- Layout decision: shared PageHead firsttextchild min-width0/overflow-wrapanywhere resolves directly rendered unbroken-title overflow while preserving full title/ID and genuine destination; no clipping/truncation/hidden-overflow. Extend actual generatedPlan→WorkDetail browsergate with fulltext/Range bounds boththemes.
- Status: integrated; own386unit4407assertions/buildPASS, scopedactualflowsPASS; newcurrentfull48×2 regression in flight. Broader acceptance still NOTREADY/completecleanrounds0.

### Required next external acceptance report contract — design assessed, not implemented
Persist one immutable ACCEPTANCE fact on actual first local receipt, fixed tenant/project/delivery/work/version/time/completeitems/result/originalpackagesnapshot. Client signatory and any internal Human recorder distinct; unknown real contact identity remains unknown. Explicit package acknowledgment binds exact fixed contents, not only versionlabel. LOCAL_SIMULATION remains in UI/report/fixedfacts; a local contentfingerprint is not a signature or verifiedclientidentity. No default sender/owner/executor/recorderpersonalcredit; scopeproject/tenant and onlyprovableteam. Dedicated typedreader/reference/title/checker/frozenstructuredFacts/N4 reuse currentfactspipeline; externalreceipt never adds acceptedWork count or completes Plan a secondtime. Partial/rejected outcomes remain explicit, not promoted to fullacceptance; legacy history not backfilled. Actual form input and reader implementation/testing remain outstanding.


## 2026-10-14 — Uniform local receipt recorder authority
Decision: all three delivery receipt outcomes use the existing local simulator executor authorization gate; UI explains and disables unauthorized actions consistently. Reason: actual partial/rejected commands bypassed the full-accept guard and persisted history/rework/state. Alternatives rejected: banning production occupational executors (explicit capability may authorize a Human), permitting rejection without recorder permission, or hiding authorization failures only in UI. Scope/status: isolated prototype repair, domain and two-width affected browser PASS; independent actual UI and main integration pending. Existing immutable terminal receipts and owner/external retries remain unchanged. This does not implement production authentication or typed ACCEPTANCE reporting.


## 2026-10-14 — Knowledge export authenticity and rejected-action focus
Decision: knowledge export accepts only a current authorized saved object with unchanged JSON; serializes canonical originals, never caller payload/history. Complete saved history retains same nonempty record ID; strict status-event history compatible, historical private scope still separately authorized. Reason: forged public history identity/scope and relabeled secret body bypassed scope-only checks. Existing sole UI caller passes saved record/list; no arbitrary historical export requirement or caller exists. Scope: isolated local prototype repair, not production IAM/shared ACL/project scope/archive.
Decision: shared ActionForm focuses and scrolls the inline business error summary on every rejected attempt, including identical errors, preserving modal focus restoration and form data. Reason: root native375 reproduction showed Save remained focused outside dialog after error insertion. Reuse existing themed role=alert; no new modal/footer layout. Status: domain tests/mutations passed; full stage/unit/build and actual browser verification pending, not integrated.


## 2026-10-14 — Prototype acceptance boundary correction
- Decision: deliver a complete interactive prototype covering existing requirements and interactions; do not implement real backend, distributed execution, persistence services or external integrations in this task.
- Reason: this was the user's original scope; the redundant prototype-vs-production question incorrectly reopened an established decision.
- Alternatives considered: production implementation is outside the authorized scope.
- Tradeoffs: local simulations demonstrate product flows, with real-service boundaries visibly disclosed; they cannot certify production behavior.
- Scope / status: confirmed; two complete clean review rounds assess the frozen prototype and its required designs, not production readiness.


## 2026-10-14 — 工作流画布与绩效入口

- Decision：协作与 Git CI/CD 工作流继续复用现有 `@xyflow/react`；按类型定义节点参数，面板划分配置、输入、输出、运行记录、错误策略。绩效按周期进入，租户/项目只汇总，人/Agent独立评分；个人工作按项目分组，不新增人×项目绩效菜单。
- Reason：需求核心是流程编辑、端口与质量门，已有 React Flow 画布和版本生命周期可复用。relation-graph 官方定位主要为关系数据展示，也支持自定义交互画布；本轮没有为替换成熟入口引入第二套组件的必要。
- Alternatives：relation-graph；为每个场景独立造画布/菜单；保留独立工时录入。
- Tradeoffs：继续复用降低两套图组件维护成本，但节点配置不等于执行引擎。真实CI、Runner、遥测和定时器未实现；旧历史无证据保持未知。Human校准可选，不把墙钟或消息推算成实际投入。
- Scope / Status：可交互原型；用户确认方向已实现并进行限定验收。参考 https://github.com/xyflow/xyflow 与 https://github.com/relation-graph/relation-graph 官方README，未进行两者性能基准比较。


## 2026-10-14：探索统一到工作流；知识与文件分开
- Decision：用户明确探索由统一工作项/工作流消化，可为单节点、阶段或独立工作流；取消用户新建独立ADHOC入口，保留旧记录兼容。
- Reason：避免第二套目标、执行和验收逻辑。私信用于沟通，确认工作后再执行。
- Alternatives Considered：独立临时探索执行入口（旧03规范）；用户明确弃用新建入口。
- Tradeoffs：保留ADHOC历史读取和关联，不能删历史或猜归属。
- Scope / Status：原型入口与文案；已确认用户决定。
- Decision：知识库归治理，租户/项目两层；文件与素材归协作独立页面，知识可引用文件和事实。
- Reason：原件与可复用知识不同生命周期，权限和来源需保持。
- Alternatives Considered：混合“资料与知识”页或都放协作；用户最终提出治理知识库。
- Tradeoffs：共享存储和查看能力允许复用，导航和维护意图不可混淆；私人知识权限保留。
- Scope / Status：文档已登记，页面拆分尚待完成，不计已验收。

## 2026-10-14 — 复用 cc-switch 配置底座
- Decision：本地配置复用 cc-switch/cc-switch-cli，只扩展云端授权版本下发和回执。
- Reason：用户要求保持成熟工具效果，避免重复引擎。
- Alternatives Considered：自建类似功能；不采用。
- Tradeoffs：需核实上游版本能力；WebDAV不代替平台授权下发。Go调用独立可执行程序，无新增Node必需依赖。
- Scope / Status：文档与原型边界已登记；未安装或真实集成。


## 2026-10-15 — 多业务边界先行，迁移执行获准
- Decision：用户批准开始执行。先冻结 X00/X01 当前协作边界，再按 M00–M20 分批推进；当前一期复用 IAM + 协作，同一 Web 壳体，后端沿既有模块单体扩展。
- Reason：避免迁移把产品、组织和身份混为一层，同时不为未定义商城提前建设基础设施。
- Alternatives Considered：先做全部空页面、先拆微服务、先实现商城准入；均不作为当前前置条件。
- Tradeoffs：未来第二产品的消费者准入/audience/权益/退出门禁仍按 X02–X11 触发；当前组件通过不等于业务服务通过。
- Scope / Status：执行已批准；不替换 IAM 默认落点，不把 operating platform 当产品目录，不建立多余产品注册表，不预设买家属于商家租户。商城商业模型仍待正式需求。


## 2026-10-16 — Governance seeding reuses IAM role ownership semantics
- Decision: core publishes transaction-bound RoleSeeder.EnsureRoleOn / GrantPermissionOn. EnsureRoleOn creates only when the caller's governance ledger has no owned role; an existing normalized display name returns ErrRoleNameConflict and is never adopted. nameKey must match IAM's existing lowercase/trimmed display-name key. Governance role_key and translation key remain separate from IAM name_key.
- Reason: existing IAM CreateRole/UpdateRole maintain display-name uniqueness; replacing name_key with a business key would break that invariant. Core cannot infer governance ownership from a matching name.
- Alternatives considered: claim arbitrary same-name roles or overload IAM name_key; rejected because both alter existing IAM semantics and violate spec08's nonowned-name conflict rule.
- Tradeoffs: governance must consult and persist its role ledger in the same transaction, and use applied_codes to avoid restoring manually revoked permissions. The core capability alone does not implement reconciliation or drift handling.
- Scope / Status: internal provider capability implemented; reuse existing role validation, grants, auditing and revision transactions. Governance consumer and project creation remain incomplete. No HTTP behavior changed.


## 2026-10-16 — Isolate business governance from IAM governance registration
- Decision: spec08 package internal/governance uses lifecycle ID business_governance and migration version table goose_business_governance. Existing internal/iam/governance keeps governance/goose_governance. Business tables remain governance_* as specified.
- Reason: the existing IAM module already owns both identifiers. Reusing either would collide in registry composition or interpret business migrations as IAM migration versions.
- Alternatives considered: rename the published IAM module/stream; rejected as an unnecessary compatibility change. Merge business rules into IAM governance; rejected because spec08 explicitly separates business governance and IAM access governance.
- Tradeoffs / Scope: internal registration identity differs from the short conceptual module label; no HTTP path, permission code or business table changed. Status: applied to new schema module; production registration pending.
- Conflict recording: RoleSeeder returns the existing role plus ErrRoleNameConflict on a read-detected collision, without writing or adopting it. This lets governance record the required nonnull conflicting role_id. Zero-role errors still require full rollback, including races during insert.


## 2026-10-16 — Fence tenant seeding with the shared lease row
- Decision: dlock publishes TransactionLease through module services; governance acquires governance:seed:{tenant} for 60 seconds, starts one SQL transaction, calls Lock.FenceOn before protected work, runs the full manifest, then releases the lease after transaction completion.
- Reason: checking a timer or refreshing a lease alone cannot prevent another worker from taking over while an existing SQL transaction is still writing. FenceOn validates the current unexpired token and holds the row with FOR UPDATE on PostgreSQL/MySQL. SQLite transaction locking rejects competing writes or stale snapshot write upgrades.
- Alternatives considered: separate automatic renewal during the SQL transaction; rejected for this entry because renewal contends with the fenced row and single-writer connection pools. Existing general lock APIs are unchanged.
- Tradeoffs: protected work holds the lock row until commit/rollback; callers must fence before any protected reads/writes. Request contention remains ErrNotAcquired rather than a false successful seed; scheduler may skip that pass. Cleanup runs after transaction completion even when the request context was canceled.
- Scope / Status: provider and private seed entry implemented, SQLite/race verified. Real PostgreSQL concurrent takeover test pending final execution after recovering from system-disk cache exhaustion. Production governance composition still pending.


## 2026-10-16 — Conditional permissions must stay conditional during role seeding
- Decision: RoleSeeder offers GrantConditionalPermissionOn alongside existing unconditional grants. Governance manifest entries carry explicit per-code conditions and use that capability whenever a condition key exists; missing capability fails closed. Empty/null conditions are invalid.
- Reason: spec08 §3.3b limits engineer plan_approve to LOW/MEDIUM risk. Granting it as a plain permission would widen authority. The existing IAM policyx condition and SetPermissionCondition path already own validation, audits and revisions.
- Alternatives considered: unconditional grant plus a later update outside the transaction; rejected because it exposes a broad grant and cannot roll back atomically. Overwriting existing administrator conditions on every seed; rejected because reconciliation must preserve manual changes.
- Tradeoffs / Scope: a newly inserted grant receives its condition in the same caller transaction; an already existing grant remains untouched. Historical applied_codes prevent later regrant/condition overwrite. Status: capability and algorithm integrated, targeted tests pass; fixed production manifest and governance lifecycle still pending.


## 2026-10-16 — Initial owner membership is a bounded IAM snapshot
- Decision: only the first creation of a non-conflicting team_owner seed copies active, unexpired direct members of the IAM Administrator role through the original AddMember transaction path. Preserve source assignment expiration; never synchronize membership on subsequent seed runs.
- Reason: spec08 §3.3 requires a one-time Administrator snapshot. Copying expired/disabled members or converting limited grants to permanent grants would widen authority. IAM explicitly permits editing/renaming Administrator, so the source must still possess unconditional tenant_administer; a display name alone cannot authorize promotion. Renamed/missing sources are not guessed from arbitrary role names.
- Alternatives considered: resynchronize on every reconciliation or flatten expiry; rejected because either resurrects revoked access. Copy all effective administrators/group grants; rejected because the specification names direct role membership and does not authorize that expansion.
- Scope / Status: core capability and first-creation seed hook implemented and tested; full production initialization not yet enabled.

## 2026-10-16 — Periodic job cron parsing
- Decision: use pinned github.com/robfig/cron/v3 v3.0.1 for the standard five-field parser only. Reject embedded timezone prefixes and descriptors; Schedule.Timezone is the single IANA timezone source.
- Reason: 09 §6.2 requires standard cron; the repository has no existing parser. Reusing a parser avoids implementing cron grammar and calendar edge cases locally.
- Alternatives considered: handwritten parser; package-owned in-memory scheduler. Neither is needed: execution leadership, durable progress and misfire handling belong to jobs per spec.
- Tradeoffs: one Go dependency, recorded in the main and integration-test module locks. No change to database-backed execution semantics.
- Scope/status: declaration validation implemented; durable scheduler and task execution still pending.

## 2026-10-16 — Queue admission transaction guard
- Decision: serialize each tenant's admission using a persistent dlock-owned transaction mutex row, acquired in the caller transaction and released by commit/rollback. Batch tenant guards are acquired in increasing tenant ID order.
- Reason: count-then-insert can exceed the specified pending cap under concurrency; an external renewable lease acquired from inside an existing SQLite writer transaction can deadlock.
- Alternatives considered: process-local mutex (does not protect multiple replicas), a separate quota counter table (extra storage and reconciliation), unfenced count (racy).
- Tradeoffs: admission is serialized per tenant. Protected job reads use row locking on PG/MySQL to see current state even with a pre-existing MySQL repeatable-read snapshot; SQLite uses its transaction writer lock. The bounded pending-ID read has a tenant/state index. Callers must roll back on any queue error and maintain consistent lock ordering.
- Scope/status: queue admission/cancel implemented; worker transitions back to PENDING must synchronize with this guard when implemented; retries retain the specified task lifecycle rather than being discarded when new admissions fill the queue. Real MySQL8 concurrency remains unverified until the daemon is available.

## 2026-10-16 — Bounded job concurrency lock names
- Decision: retain `jobs:ck:<kind>:<key>` when its UTF-8 byte length fits dlock's 191-byte bound. Longer tuples map to `jobs:ck-sha256:<digest>` using a length-delimited kind/key tuple.
- Reason: each declared field may be 128 characters; the combined lock name can exceed the provider limit. Truncation would make unrelated tasks contend on the same lock.
- Alternatives considered: reject valid queue keys, enlarge dlock storage, or truncate; rejected because they respectively narrow the job contract, change a shared provider unnecessarily, or merge identities.
- Tradeoffs / Scope: long lock names are opaque but deterministic; normal names retain the spec format. No new dependency or storage change. Implemented in the private execution path, not yet production activated.

## 2026-10-16 — Worker cancellation and settlement boundaries
- Decision: Start creates lifecycle-owned claim and execution contexts. Quiesce stops claims first, drains for the configured duration, then cancels handlers and settles owned attempts through existing finish CAS within a bounded cleanup budget. A handler ignoring cancellation retains its process-local slot until actual return; timeout plus 30s settles TIMEOUT and exposes a queue-labeled leaked-worker gauge.
- Reason: canceling a startup/root context must not skip graceful drain, while waiting for uncooperative code must not block shutdown indefinitely or permit unlimited replacements. Fenced settlement avoids duplicate attempt refunds.
- Alternatives considered: release slots at deadline or wait indefinitely; rejected by spec09 BR-09-07/12. Force-killing goroutines is not a Go capability.
- Tradeoffs: uncooperative handler goroutines can remain until process exit; Go cannot revoke their external effects. Handlers still require idempotency. A quiesced module instance is not restarted; a new lifecycle creates a new instance.
- Scope/status: worker lifecycle and bounded settlement implemented and SQLite/race + PostgreSQL two-instance tested. Production activation requires the real same-transaction deadletter connector, scheduler and application configuration, which remain unfinished.

## 2026-10-16 — Job deadletters use the real transactional outbox
- Decision: application wiring binds jobs.DeadLetterConnector to events.Publisher and declares job.dead_lettered before catalog sealing. Publish on the finish transaction, with SYSTEM actor, background_job subject/current version, realtime NONE and safe kind/attempts payload.
- Reason: spec09 requires DEAD and its event to commit atomically; a jobs-to-events lifecycle dependency would conflict with event dispatchers consuming jobs. Provider capabilities remain module-owned and the composition root joins them.
- Alternatives considered: direct cross-module storage writes, post-commit publication, or reverse module dependencies; rejected for ownership, atomicity and lifecycle-cycle reasons.
- Platform semantics: spec09 §3 and CR-09-06 explicitly permit tenant_id0 for infrastructure records. Publisher accepts0 (still rejects negative IDs) and uses explicit decimal tenant topics, including tenant/0. No fabricated tenant or implicit tenant reassignment.
- Scope/status: wiring and actual SQLite worker/outbox commit + injected post-insert rollback tests implemented for tenant0 and tenant1. Production jobs registration remains pending scheduler/config completion. This does not implement outbox delivery or notifications.

## 2026-10-16 — Schedule definition reconciliation preserves operator state
- Decision: compare a deterministic hash of the normalized code declaration and effective queue. Identical definitions preserve all stored progress; changed definitions recompute the next occurrence from database time while preserving enabled, creation time and firing history. Removed declarations are disabled and logged as orphaned, not deleted.
- Reason: spec09 stores runtime progress durably and allows operators to pause plans. Restarting or upgrading code must not silently reset progress or undo a pause. Calendar evaluation uses the declared IANA timezone through the existing cron parser.
- Alternatives considered: unconditional upsert/reset on startup or deletion of removed definitions; both lose specified runtime/operator state. Re-enabling a reintroduced row cannot distinguish an operator pause from orphan disable with the current schema, so disabled remains disabled.
- Tradeoffs: reintroduced plans retain their prior disabled state until explicitly enabled. A syntactically valid cron with no future occurrence fails reconciliation atomically instead of writing an unusable next_run_at.
- Scope/status: reconciliation and calendar calculations implemented and SQLite/race verified. Leader lifecycle, transactional firing, overrides and tenant fanout remain pending; no complete scheduler claim.

## 2026-10-16 — Scheduled firing transaction and missed-slot boundary
- Decision: enqueue and schedule progress update share a transaction, with queue admission guard before schedule guard. Match the local declaration hash to the stored definition so an old leader cannot fire a newer plan with stale semantics. Long schedule idempotency keys use SHA256 over the full specified logical key when it exceeds the queue's128-byte bound.
- Reason: failures after enqueue must not leave progress and work disagreeing; maximum128-character schedule names otherwise exceed the queue key limit after adding prefix and timestamp. Truncation would merge distinct occurrences.
- Misfire interpretation: COALESCE enqueues one due occurrence and computes next from DB now. SKIP considers the whole occurrence slot missed when the following occurrence is also due; sub-slot polling lateness still fires. This makes SKIP usable with a polling scheduler without inventing an arbitrary wall-clock grace threshold.
- Alternatives considered: separate progress/enqueue commits, truncation/narrowing valid names, or skipping any nonzero lateness. Rejected for atomicity, identity loss and starving virtually all polled SKIP plans.
- Scope/status: PLATFORM transaction primitive and SQLite tests implemented. PER_TENANT is not silently coerced to PLATFORM; fanout and leader lifecycle integration remain unfinished.

## 2026-10-16 — Tenant fanout snapshot shares the durable job row
- Decision: preserve the target JSON directly in jobs.fan_out's existing payload column; freeze its target kind and logical firing time in two nullable metadata columns, and reuse concurrency_key for the schedule name. Ordinary jobs keep metadata NULL. Use organization-owned ActiveTenantEnumerator for ascending active tenant pages of at most500; preserve governance's existing suspended-inclusive100-row enumerator.
- Reason: wrapping a valid64KiB payload with dispatch metadata would exceed the same queue limit. Looking up a mutable schedule at execution time could silently change queued work after deployment. Existing tenant reconciliation has deliberately different inclusion semantics.
- Alternatives considered: reducing the public payload limit, increasing all job payload limits, compressing payloads, or adding a separate snapshot table. Rejected as incompatible, not reliably bounded, or unnecessary extra storage lifecycle. Two typed nullable fields retain the snapshot in the job's existing retention/transaction boundary.
- Tradeoffs: additive three-dialect migration00002 and corresponding generated query fields; metadata constraints enforce platform-only/nonrecursive/complete references. Down migration removes dispatch metadata (and therefore cannot preserve future execution of such dispatches), while retaining existing payload rows; rollback is not a mechanism for keeping fanout operational.
- Scope/status: storage, active-tenant capability, frozen-snapshot handler and root connector implemented with database/race tests; leader activation remains pending. No change to governance membership semantics, ordinary enqueue limits, or production startup inventory.

## 2026-10-16 — Independent scheduler and execution roles
- Decision: honor separate workers_enabled and scheduler_enabled switches (both defaulttrue per spec09). Scheduler uses existing renewable Singleton leadership, reconciles on acquisition and fires at poll_interval. Quiesce cancels scheduling with new claims before draining existing handlers; bothdisabled starts no background lifecycle.
- Reason: an enqueue/scheduler-only replica must not execute handlers, and an execution-only replica must not mutate periodic progress. Reusing existing lifecycle and lease primitives keeps stop ordering and failover behavior consistent.
- Tradeoffs/status: graceful takeover verified on SQLite and three public-lifecycle PostgreSQL instances, including actual periodic handler execution. Process-kill lease expiry remains unverified until production configuration/composition permits process-level testing; production registration is still pending.

## 2026-10-16 — Schedule interval override normalization
- Decision: normalize deployment interval overrides into the existing schedule catalog during Initialize, before sealing; reject unknown names/invalid durations atomically. Copy caller-owned configuration. Explicit interval replaces cron.
- Reason: reconciliation and firing must hash and execute the same effective definition; unchanged effective timing must not reset persistent progress.
- Alternatives: applying overrides only on database write would leave local firing hashes stale; accepting invalid overrides silently would hide configuration errors.
- Tradeoffs: changing effective timing recalculates the next due instant through existing reconciliation while preserving operator pause and execution history. Enabled override precedence is not implemented by this change.
- Scope: internal jobs configuration only; production AppConfig mapping remains pending.
- Status: implemented and tested; full production/business acceptance pending.

## 2026-10-16 — Production jobs composition
- Decision: register jobs after audit in the single composition inventory; map app-owned settings through moduleconfig.go. Keep existing metrics/telemetry/audit middleware order and event registration unchanged.
- Reason: job execution requires audit/guid/dlock; existing root connectors bind dead-letter publication and active-tenant enumeration before initialization.
- Alternatives: separate background registry would duplicate lifecycle/migration ownership and is rejected.
- Tradeoffs: jobs migrations are now included even in IAM-disabled deployments. Worker drain must fit the configured graceful shutdown budget. Per-schedule enabled precedence awaits explicit clarification.
- Scope/status: production composition/config implemented with app race/build/architecture verification; running dev process not restarted and full business acceptance pending.

## 2026-10-16 — Job contribution discovery
- Decision: composition root collects HandlerProvider and ScheduleProvider declarations from the activated registry after Wire; jobs.Initialize validates all references and seals the catalog.
- Reason: follows spec09 catalog contract, excludes disabled modules and allows plans to reference handlers contributed later in registration order.
- Alternatives: module-local self-registration would duplicate composition behavior and make declaration discovery inconsistent.
- Tradeoffs: invalid contributions fail preparation; partial catalog state is not used after failed startup. Owners remain lifecycle module names until an explicit logical namespace exception is needed by a real contributor.
- Scope/status: root discovery implemented; actual worker/outbox execution and rollback tests pass through discovery. Governance provider and full business acceptance remain pending.

## 2026-10-16 — Governance job logical namespace
- Decision: business governance declares governance.seed_reconcile as an hourly PLATFORM job; the root maps its concrete module type to JobNamespace governance while lifecycle identity stays business_governance.
- Reason: spec08 freezes job identity and the existing IAM governance module already occupies the lifecycle name. The job owns tenant pagination, including suspended tenants according to existing seeding semantics.
- Alternatives: renaming IAM, changing published job identity, or globally allowing arbitrary module prefixes would break ownership or compatibility.
- Tradeoffs: one explicit composition-root alias; startup sweep and production governance registration remain pending real SYSTEM/first-access authorization.
- Scope/status: declarations, invocation and actual root composition tested; no claim of live production role initialization.

## 2026-10-16 — Internal role seed authority without erasing caller claims
- Decision: reuse the IAM transaction-bound Service copy pattern already used by EntityProvisioner. RoleSeeder alone marks its private copy for system manifest grant/condition operations and verified Administrator-member copying; ordinary HTTP Service instances remain unmarked.
- Reason: first-access role initialization must not depend on the initiating user's grant authority, and erasing authn claims would hide attribution. Manifest ownership stays with the internal governance consumer; this capability must never accept user-defined manifests or be exposed as a role editing endpoint.
- Preserved constraints: catalog/root/role reference validation, canonical policy conditions, last-administrator protection, active-member/expiry checks, caller transaction, audit append and policy revision updates. Source permission checks apply to ordinary user grants as before. Privileged writes retain initiating principal/account-view context and add operation_source=role_seed.
- Alternatives: removing caller claims (rejected: loses provenance); duplicating IAM writes in governance (rejected: breaks ownership/audit/revision); global bypass context token (rejected: broader than the existing internal capability).
- Tradeoffs: code holding the existing RoleSeeder is trusted to submit only its code-owned manifest. The private flag is never placed on the shared HTTP service or exported through configuration/context.
- Status: targeted and complete core race regression pass using real IAM repository/authorizer; production governance integration remains pending.

## 2026-10-16 — Governance production registration and startup sweep
- Decision: register business_governance with the IAM activation flag after its IAM/jobs dependencies; Start persists a platform governance.seed_reconcile job through the existing jobs.Queue. Startup and hourly schedule share the same concurrency key/handler.
- Reason: initialization work must be durable/retryable and use existing worker/audit lifecycle. A queue error fails startup rather than silently omitting the sweep. Synchronous EnsureTenant remains available to first-access consumers.
- Alternatives: a new goroutine/timer (rejected: duplicates jobs); running all tenant writes synchronously during Start (rejected: blocks startup for the whole fleet); permanently fixed startup idempotency key (rejected: suppresses later required startup sweeps).
- Tradeoffs: each starting replica can enqueue a sweep; shared concurrency and per-tenant seed ledger serialize execution and prevent duplicate state. Worker-disabled replicas can enqueue for other workers. No new startup schedule identity is invented.
- Verification: real composed SQLite IAM/governance integration covers14roles, real audit chain, initiator attribution, persisted revoked grant preservation and full transaction rollback after injected final audit failure. Production registration/startup app race and final governance race pass; existing dev process not restarted.

## 2026-10-16 — Delivery transaction core
- Decision: introduce a private projectService.createOn operating strictly inside the caller's transaction. It delegates entity/root provisioning, role bindings, channel creation, IAM audit and outbox publication through existing provider-owned capabilities; only delivery tables use delivery generated queries.
- Reason: spec07 BR-07-01 requires one atomic aggregate, including failures after publication. Caller command owns authorization, full request validation, optional client creation, role-key resolution and seed preparation; those are not inferred by storage assembly.
- Tradeoffs: the core takes private validated draft rows and does not expose persistence models through public APIs. It checks transaction/capability/tenant/actor/member structural boundaries and copies child rows before replacing project IDs. API, command authorization and real cross-module integration remain pending.
- Alternatives: separate commits per module or compensating deletion rejected because they violate required creation atomicity.
- Status: real SQLite delivery migrations with provider transaction probes prove success and rollback at every step; not yet a production create endpoint.

## 2026-10-16 — Reserved-entity business binding authority
- Decision: EntityRoleBindingStore marks only its transaction-local Service copy as a business binding writer. PutEntityRoleBinding skips caller role-grant escalation only after loading a reserved-type entity under the tenant policy revision lock. Unreserved entities still run the original grantability checks; shared HTTP Service remains unmarked. The internal path first acquires the existing empty-code authorization fences in system-authorization then tenant-policy order, and uses the existing current-read helper when checking the reserved entity type.
- Reason: spec07 mandates automatic creator delivery_lead binding after authorized project creation, even if the initiator does not personally hold all role permissions. The business owner must authorize the command and validate role/subject mapping before invoking this internal capability.
- Preserved checks: tenant-qualified entity and role ownership, active tenant membership, binding shape/effect/expiry, transaction, policy revision, audit. Caller authn context is retained; business binding audit adds operation_source=entity_provisioning.
- Alternatives: dropping caller claims, globally changing role-grant authorization, or granting tenant-wide creator rights rejected. Direct writes from delivery would duplicate IAM policy/audit behavior.
- Tradeoffs: capability trusts the internal business owner, and requires declared reserved entity types. Delivery must publish its project reservation before this path becomes active.
- Status: targeted and final full core race regression pass, with build/lint/architecture and existing IAM performance regression. New binding path PG/MySQL concurrency acceptance remains pending. Project command/API still pending.

## 2026-10-16 — Delivery ownership, required security and optional provisioning
- Decision: register delivery under IAM activation after governance/collab. It contributes project EntityTypeReservations, project_read/manage/stage_change/mode_change actions, and SIGNAL project.created schema1. project_create remains a non-delegable pseudo-action and is not registered in IAM.
- Root wiring: delivery publishes ProjectProvisioningConnector; internal/app/delivery.go resolves existing entity, binding and channel capabilities and binds them before Initialize. Missing optional capabilities remain nil, generate named startup warnings and keep createOn unavailable. Binding cannot repeat or occur after initialization.
- Reason: required IAM security/audit/guid/governance/events are lifecycle dependencies; optional provisioning must not be silently substituted or force successful project creation without required resources. Existing root-connector pattern avoids a second DI mechanism and an unconditional collab lifecycle dependency.
- Tradeoffs: the project reservation becomes effective when delivery activates; public project commands/routes are still pending and this code has not been deployed/restarted. Disabled IAM removes delivery with its other dependent modules.
- Validation: module tests cover permission/event declarations, pseudo-action absence, reservation snapshot, missing required dependencies and optional capability binding lifecycle; real composed app verifies migration and provider registration. Full project HTTP/API behavior remains unimplemented.

## 2026-10-16 — Fleet identity facts in the project transaction
- Decision: fleet publishes a narrow AgentDirectory.ByPrincipalOn capability using the caller transaction and an explicit tenant boundary. It returns a detached non-secret identity/role view; delivery retains IAM membership, command authority and role-binding responsibility.
- Reason: service accounts are not necessarily Agents; spec07 BR-07-02 requires real fleet classification and the spec05 role mapping. Revoked roles must disappear in the same transaction.
- Alternatives: service-account prefix/name inference or trusting incoming role keys rejected. Cross-module table reads rejected by module ownership rules.
- Tradeoffs: this is the identity portion of AgentDirectory, not its planned runtime/load/statistics operations. Lifecycle state is returned as fact; binding validation follows spec07 active tenant membership and compatible roles, without adding an unrequested ACTIVE-only project membership rule.
- Scope/status: directory, app activation and delivery member resolution implemented; full command, fleet Agent lifecycle and runtime operations remain pending acceptance.

## 2026-10-16 — Project creation command and authority capability
- Decision: publish only governance.ProjectCreationAuthority, delegating to the existing project_create Check branch. Delivery owns command orchestration; no full AuthorityChecker claim until remaining actions/ListEligible exist.
- Reason: provider-owned capability enables real command DI without duplicating policy evaluation or cross-module storage access.
- Transaction order: preclassify HUMAN on writer and seed roles before the business transaction; lock sorted principal IDs, acquire existing IAM system/tenant authorization fences, revalidate human, check tenant project count, validate references/members, assemble and persist the aggregate. AuthoritySource always comes from the server decision.
- Alternatives: independently committing aggregate components rejected by BR-07-01; checking count outside fenced transaction rejected because concurrent creators can exceed500; trusting incoming actor/source rejected.
- Tradeoffs/status: internal H1 command takes already Huma-validated/defaulted input and explicit writer. HTTP mapping/idempotency, full composed integration and actual MySQL concurrency acceptance remain pending. New intake-conversion creation must use the same tenant fence before counting.

## 2026-10-16 — Shared transactional idempotency prerequisite
- Decision: add provider-owned infra/idempotency Store.Do and platform_idempotency_keys per spec09 C7; do not reuse files-operation/job dedup tables. Unique key is tenant/subject kind/subject/operation/key. Insert-on-conflict plus a current row lock serializes duplicate commands through caller commit.
- Error semantics: a checked savepoint rolls back callback effects for non-success responses; deterministic4xx can be stored without partial business writes. 5xx/429/version409/interrupted discard the key; Go errors require caller rollback. Large responses retain the mandated resource reference for caller reread.
- Hashing: preserve typed struct encoding order and exact numbers, omit only top-level seen_seq. HashRequest returns an error on failed encoding; spec09 signature clarified because a silent zero/fallback hash can replay an unrelated request. No existing callers to break.
- Tradeoffs/scope: strong transactional mode is implemented first for H1. Middleware takeover, configurable limits, purge, metrics and HTTP i18n remain pending, not silently replaced by this mode. Project H1 must use Do inside its actual business transaction after independent role seeding.

## 2026-10-16 — Server-owned creation facts in tenant route guards
- Decision: extend existing authz.Guard with optional CreationAttributes for tenant POST creation. The declaration is copied at registration; resource Type comes from permission resource and Owner from authenticated principal. Request body/header/query cannot select these facts; resource ID is absent before creation.
- Reason: H1 must use authz.Register(project_manage), whose permission check precedes the business handler. Existing conditional grants on project type/owner/DEFAULT/HYBRID otherwise fail before the domain checker can provide those facts.
- Alternatives: authenticated-only route would drop the declared permission guard; weakening IAM condition evaluation would authorize the wrong context; trusting client-provided resource attributes would permit spoofing. All rejected.
- Scope/tradeoff: only tenant Register accepts creation facts, only POST; other scope helpers reject them. Existing guards and permission/error behavior are preserved. Real IAM HTTP test proves no-facts denial, declared-facts allowance, then revocation denial. H1 must reauthorize transactionally; these facts are never an independent grant.

## 2026-10-16 — Opt-in operation request contracts over OPTIONS (M02 in progress)
- Decision: shared authz.RegisterRequestContract(api, path) registers OPTIONS after the resource operations; method query selects GET/POST/PUT/PATCH/DELETE. Project creation is the first consumer. Payload projects registered Huma parameters/requestBody plus only reachable schema definitions; SHA-256 versions the canonical payload. No duplicate field constraints or executable rules.
- Reason: user requested API-derived form rules; migration §5.2 explicitly calls for method-level metadata. Reuse the selected operation middleware chain, with its operation and HTTP method for method-sensitive authorization; restore OPTIONS context for response. Never invoke its business handler. Fail registration for non-public operations without an active middleware chain.
- Alternatives considered: unauthenticated global schema dump, per-form handwritten rules, invoking mutation handlers for validation. Rejected due to permission mismatch, drift, or side effects.
- Tradeoffs: opt-in after all resource operations registered; existing resources are not silently changed. Response currently private/no-store to avoid stale authorization caching. CORS preflight carrying Access-Control-Request-Method is rejected by this metadata route; it does not grant cross-origin access or replace deployment CORS policy. Locale-aware rule messages, client validator compilation, unsupported-rule disclosure and wider endpoint adoption remain pending M02 scope.
- Scope/status: implementing M02/S00 transport; production project endpoint wired and HTTP-tested, not complete dynamic-form acceptance. Full business migration remains NOT READY.

## 2026-10-16 — AutoForm contract interpretation and explicit unsupported rules
- Decision: reuse existing Zod validation and AutoForm lifecycle; optional contract prop compiles the registered request schema, derives required/numeric input hints, validates on blur/submit, and parses numeric edit strings only at submission. No global input coercion/default mutation or dependency added. Null contract means loading/unavailable, undefined retains existing forms.
- Reason: schema-driven validation must preserve Unicode code-point lengths, explicit null/omission, exact numeric safety and Go/JS regex semantics. Inspected installed Zod fromJSONSchema: UTF-16 length checks and ignored custom formats do not meet these requirements unchanged. Its semi-experimental converter was not used.
- Alternatives: hand-coded per-page business constraints (drift); blindly accepting all JS regexes (dialect mismatch); adding a separate validator runtime (extra dependency/runtime behavior). Current adapter uses existing Zod primitives with explicit supported vocabulary.
- Tradeoffs: unsupported constraints block submitting a contract-backed form and return diagnostics. Full-int64 wire serialization, decimal multipleOf, remaining formats/regexes, server-only validator disclosure and permissions are pending; never claim all schemas are supported. Messages use shared form.contract keys in en-US/zh-CN/ms-MY.
- Scope/status: M02 partial implementation; existing forms unchanged unless opted in. Production project pages and live OPTIONS fetching not yet wired; full acceptance NOT READY.

## 2026-10-16 — Project detail reuses Entity ownership and snapshot reads
- Decision: H3 uses core.EntityOwnerResolver through existing authz.RegisterOwned with project_read. Delivery reads its own tenant-scoped project row and delegates Entity/client reads to core.EntityProvisioner.GetOn.
- Reason: reuse IAM identity/authorization and avoid cross-module storage imports; name/client and version must come from one snapshot for the editor ETag.
- Alternatives: separate project identity or direct IAM table reads rejected; independent read-committed queries risk mixing concurrent rename/client/version state.
- Tradeoff: short read-only repeatable-read transaction on writer; actual MySQL compatibility validation remains blocked by local Docker availability. No lifecycle mutation, provisioning or new data model introduced.
- Scope/status: H3 implemented; composed HTTP, delivery/app race, architecture, lint/build checks passed. Full project list/edit/UI and M00–M20 remain incomplete.

## 2026-10-16 — Project list record query reuses IAM joins and data constraints
- Decision: H2 record query uses the existing provider-owned core.AccessTableNames for Entity/client JOINs and core.TenantConstraint for project_read. All scope/denial/filter predicates precede count and pagination; total/page share a read-only repeatable-read transaction. No new identity table or query DSL.
- Reason: name filtering/sorting must occur before pagination; per-row GetEntity after paging cannot supply correct name-filtered totals. This is the existing D39 exception for a real cross-owner JOIN, not arbitrary cross-module storage access.
- Alternatives: duplicate project names, in-memory post-page filtering, default-zero activity counts rejected. Department-only scope cannot grant all rows when project has no department attribute; eligible Entity grants are carried in ResourceIDs.
- Tradeoff/status: private record portion implemented and tested; H2 HTTP response is deliberately not exposed until actual work-item/review/delivery counts exist. Required delivery_work_items and delivery_deliveries persistence remains pending; no narrowed H2 acceptance claim. Name contains uses existing pkg/query like operator.

## 2026-10-16 — Requirements storage precedes work-item statistics
- Decision: implement spec02 project counters, requirements, acceptance criteria and requirement-version storage within delivery, using additive migration00002 in each dialect and generated model/query/dbgen artifacts. Project IDs continue to be existing Entity IDs.
- Reason: H2 open/review counts require real work items; work items require real requirements. A separate simplified counter table or fabricated zero values would diverge from the accepted business schema.
- Integrity: same-module foreign keys include tenant/project/requirement as appropriate; supersession cannot cross requirements. Unique project numbers and requirement version numbers remain DB constraints. Migration00001 and published files are not rewritten.
- Tradeoffs/status: this is storage only. Requirement commands, baseline immutability, allocation/limits and future change-record foreign key remain mandatory before feature acceptance. Cross-module IDs retain spec00 reference-only semantics. Work-item/delivery rows and H2/H4/UI are still pending.


## 2026-10-16 — 资源框架先行与既有页面统一
- Decision: 以既有资源、契约、权限、AutoForm/AutoTable/CRUD 层为落地基础；先规范、全量清册、框架补缺，再逐批迁移。既有 IAM 纳入相同标准。
- Reason: 用户明确要求复用框架而非页面各自实现，并要求先评审排查再修改。
- Alternatives considered: 直接修角色页样式、逐页复制校验、另建 CRUD 框架均不采用。
- Tradeoffs: 前置核对成本增加，但统一缺口在责任层关闭；专业交互保留并记录原因，资源按业务声明支持的操作，不自动开放全部写权限。
- Scope/status: 流程与原则已确认；详见迁移方案 §10、清单 §11。具体全量影响范围和实现验收待完成，非架构已交付声明。


## 2026-10-16 — 显式路由工作区布局
- Decision: React Router handle.layout=workspace 由共享useWorkspaceLayout读取，应用壳提供桌面剩余高度；页面内部各面板承担滚动。首批仅三个角色路由启用，窄屏维持堆叠文档流。
- Reason: 已实际复现外层滚动带走短角色列表；修复责任包含壳高度链，不能各页猜测viewport减值。
- Alternatives considered: 全站强制overflow-hidden会截断未迁移页面；按路径字符串硬编码角色判断不可复用；均不采用。
- Scope/status: 角色三个作用域运行验证已通过；其他页面需要明确声明并验证后接入，不能仅加handle就签收。角色API/权限语义保持原样。

## 2026-10-16 — 结构化请求错误的兼容落地
- Decision: 执行既有动态字段规则方案§5；respx增加具名ValidationIssue/Data，422使用保留业务分支的联合schema，普通错误message与四字段封装不变；生成文档和前端类型同步。
- Reason: 页面不得拆译文定位字段，也不能覆盖已有业务冲突data。
- Alternatives considered: 按点拆Huma location会混淆字面点号字段与嵌套字段；替换全部错误schema会丢业务类型，均不采用。
- Tradeoffs: 利用现有操作Schema确认唯一JSON Pointer，歧义/动态位置保留全局反馈；仅发布受控token白名单参数，不暴露Value。前端重译资源与专业嵌套控件仍须后续闭环。
- Scope/status: 公共响应/类型链已实现并通过HTTP三语、生成一致性、race与全主模块测试；不表示所有资源表单或业务已验收。

## 2026-10-16 — 角色原始与规范化字段规则共源
- Decision: core拥有roleFields tag声明；Huma SchemaTransformer在预计算前将同一声明投影到创建/PATCH/platform请求，服务复用fieldrules编译结果。首批只提供实际需要的trim-space与UTF8字节上下界。
- Reason: 原始字符数与规范化后字节数的接受范围不同，直接用原始schema替换service验证会改变兼容性；平台1024字符覆盖须保留。
- Alternatives considered: 全改字符数、让内部调用也承受HTTP raw限制、前端另写长度常量，均不采用。
- Tradeoffs: 共用纯框架库但业务声明仍模块自治；PATCH未提供name时只校验旧值、不额外规范化。领域保留名/授权/状态限制继续留在原领域层。
- Scope/status: 后端tag→Schema/服务桥接与三语字节错误已实现，兼容性/race/生成一致性测试通过；前端扩展执行/元数据消息、OPTIONS和页面接线仍未完成。


## 2026-10-16 — 角色工作区滚动边界落到Tab内容
- Decision: 延续显式workspace布局；桌面角色右栏仅TabPanel滚动，资源header与TabList留在外部flex列中；窄屏保留自然堆叠。
- Reason: 用户报告并实际复现section滚700导致Tab从y279到-421；原左右独立测试不能证明Tab导航可见。
- Alternatives considered: 给各页叠加猜测高度/多层sticky未采用；利用既有壳高度链和Tabs组件即可，不新增布局框架。
- Tradeoffs: 内容可用高度扣除真实标题/导航，不扣固定viewport常量。原测试增加header/Tab稳定及各tab可达断言。
- Scope/status: 同一角色组件影响租户/平台/系统；与AutoForm/AutoTable迁移状态分开验收，详见MEMORY与清单§12。本决策不宣称整个页面CRUD迁移完成。


## 2026-10-18 - H2 list amount transport remains exact without changing detail API
- Decision: use a dedicated list record projection in existing delivery/project_list.go. Budget cap/spent are decimal integer strings in USD micros; cap and display-only pct are null without a cap. pct is a floating-point display ratio, never used to reconstruct amounts or alter budget state.
- Reason: int64 micros can exceed JavaScript safe integers. Embedding existing ProjectView would also expose detail-only configuration fields and retain unsafe numeric amount transport.
- Alternatives considered: change existing detail ProjectView (wire compatibility impact); stringify after converting to float64 (already loses precision); expose entire detail record (unnecessary fields).
- Tradeoffs: old detail amount transport remains unchanged and is not claimed fixed; client must format list amounts from decimal strings. No HTTP list exposed until real summaries are implemented. No rounding/clamping or new budget rules introduced.
- Scope/status: private H2 record projection implemented; seven DB-to-JSON boundary/NULL cases and unchanged detail cap/spent contract pass. Full H2 and count semantics still pending.


## 2026-10-18 - Approved H2 project-wide activity counts
- Decision: user explicitly approved open work items as all nonarchived rows outside ACCEPTED/CANCELLED/SKIPPED, including summary items; review as nonarchived NEEDS_HUMAN_REVIEW; waiting deliveries as nonarchived SENT. Counts are project-wide, not current-approver queues.
- Reason: project list summarizes persisted project activity; approval resolved unspecified collection semantics.
- Alternatives considered: leaf-only open counts, current-approver review counts. Not selected.
- Tradeoffs/scope/status: grouped per-page tenant-scoped queries share the existing repeatable-read transaction; no workflow or authorization changes. Implemented and verified against real isolated HTTP data and independent browser fixture.

## 2026-10-18 - Project-list pagination contract
- Decision: use default 50 and maximum 100 for project.list. Frontend explicitly requests 20 initially.
- Reason: specification 07 API #2 and section 8.1 explicitly cap this collection at 100; default 50 follows the common API convention.
- Alternatives considered: raising maximum to the generic collection limit of 200 would violate the project specification.
- Tradeoffs: the generic OpenAPI pagination gate retains every assertion and precisely expects 100 only for project.list; all other collections retain 200.
- Scope/status: project list only; focused composed HTTP/OpenAPI tests pass.
