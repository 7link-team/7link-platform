# 动态 API 字段校验：方案评审与代码影响范围

日期：2026-10-13，修订版 3（全量结构清单、实际路由对账及交叉复核）。范围：设计调查与评审，不实施业务代码；当前目录以 apps/frontend-web 为准。
状态：方案评审完成，主评审已整合两轮独立评审并确认推荐方案可进入实施；不代表新功能已实现或用户已批准实施。

## 1. 结论与目标

采用「Huma 已注册输入契约 → 每路径 OPTIONS → 前端编译为字段 rules → 服务端结构化错误回填」。
后端已有 tag / Schema 是标准规则的唯一声明来源；前端维护通用规则执行器，页面只维护字段布局、请求构造及字段绑定。
不另写一份 OPTIONS 规则配置，不重新反射 DTO，不把任意后端函数序列化成 JavaScript，不为本需求改造全部页面为 AutoForm。
本方案解决动态复用声明式字段校验；数据库状态、权限等业务裁决仍由服务端执行，但也必须接入可定位错误闭环。

用户确认的四项硬性要求（优先于初版的分期建议）：
1. UI 组件统一绑定规则，自动处理可安全实施的输入限制、字段/表单校验和错误呈现；页面不手写编译/逐条执行规则。
2. 后端字段规则单一声明来源，采用模块拥有的带 tag 输入/值类型；元数据、校验、提示参数从该声明派生。
3. message 支持 zh-CN / en-US / ms-MY，并覆盖运行时切换语言与服务端错误回填。
4. 所有受影响路径有实现和验收闭环；试点只是实施顺序，不能用“后续处理”替代本次完整实施范围。
当前仍处于方案确认阶段，这四项不被解释为已经实现。

评审结论：方向可行，满足运行时动态化目标；必须同时建设契约一致性、字段错误、现有手写页面适配。
仅新增 OPTIONS handler 或仅修改共享 AutoForm 均不满足用户目标。

## 2. 核查范围与证据

### 2.1 全量契约清点

对当前 docs/openapi.json 全量结构化扫描：OpenAPI 3.1.0，275 路径、398 操作。
GET 178 / POST 106 / DELETE 54 / PATCH 25 / PUT 35 / OPTIONS 0。
operationId 缺失/重复 0，路径模板与 path 参数名称不一致 0。
请求媒体声明：application/json 126、multipart/form-data 6、application/octet-stream 6、
application/x-www-form-urlencoded 8、application/scim+json 7。数字是媒体声明次数，不是互斥操作分类。
全规范（包含响应）出现 date-time、int64、uri、email、double、int32、binary；有 oneOf、nullable 类型数组、
additionalProperties、枚举、默认值、数值范围、字符串/数组长度、正则。不能仅处理平面 JSON 对象。
既有 make openapi-check 会实际生成规范并运行已发布接口覆盖和规范合法性检查；执行结果见 §10。
既有路由覆盖测试按映射清单检查存在性，并非所有 raw routes 与运行时路由的完整双向集合证明；本轮不宣称补齐了该证明。

### 2.2 直接代码证据（路径相对于仓库根）

| 范围 | 证据 | 对方案的影响 |
|---|---|---|
| HTTP 装配 | apps/backend-api/internal/app/http.go:48、117 | 生产启动、测试装配两处都需调用统一注册函数 |
| 规范导出 | apps/backend-api/internal/app/openapi.go:100 | 导出也必须运行同一元数据注册流程，不能只改运行时 |
| Huma 设置 | apps/backend-api/pkg/extension/huma/huma.go:68、72 | 已有 OnAddOperation 与 OpenAPI 暴露配置 |
| 标准规则 | apps/backend-api/internal/iam/organization/http.go:152 | 邀请邮箱/有效期限制已由 tag 声明 |
| 明确漂移 | apps/backend-api/internal/iam/authn/http.go:91；registration.go:56 | 注册密码 tag ≥8 字符，业务 ≥12 UTF-8 字节；不能宣称 tag 已覆盖全部实际规则 |
| 跳过校验 | apps/backend-api/internal/iam/oauth/http.go:344 等 | SkipValidateParams 操作不能标记为自动校验完全覆盖 |
| 错误详情 | apps/backend-api/pkg/extension/huma/respx/error.go:22、transform.go:31 | 私有 details 被翻译拼进 message，前端不能可靠定位字段 |
| 已有动态列表契约 | apps/backend-api/pkg/extension/huma/respx/envelope.go:29 | Meta.Capabilities 是过滤/排序/投影，不等于写入参数规则；保留原用途 |
| AutoForm 规则能力 | apps/frontend-web/packages/ui/src/components/auto-form/types.ts:95 | 已支持 rules(value, values) 与异步错误信息 |
| AutoForm 执行 | 同目录 use-auto-form.ts:102、183 | 验证后另行过滤输出；需针对最终 payload 校验，外部错误回填待扩展 |
| CRUD 变换 | apps/frontend-web/packages/ui/src/components/crud/crud-form.tsx:92 | toServerData 会改变载荷，验证必须在它之后执行 |
| 实际业务表单 | apps/frontend-web/apps/web/src/app/invitations/page.tsx:94、280 | 手写 form、trim/Number/undefined 转换、独立 role_ids 状态，不能只检查 DOM |
| 前端客户端 | apps/frontend-web/apps/web/src/lib/iam-api.ts:1151、1224、1375 | ApiError 保留 data；OPTIONS 需避免触发菜单变更通知 |
| 路由类型 | apps/frontend-web/apps/web/src/lib/api-route.ts:3 | Method 暂无 OPTIONS |
| 第二客户端 | apps/frontend-web/packages/ui/src/lib/api-client.ts:138 | 只保留单字段错误，需支持统一 issues；不新增第三个客户端 |
| 数值控件 | apps/frontend-web/packages/ui/src/components/auto-form/field-renderers-rich.tsx:70 | price 当前做 Number/Math.round 最小单位转换，不能当精确 decimal 字符串输入直接复用 |

全量 AST 清点：Web 96 个非测试/非生成 TS/TSX，48 个原生 form / 33 个文件；另含按钮、弹窗、上传、
回调和直接请求，共登记 54 个页面/组件，262 个实际 iamApi 方法/辅助成员映射，未解析成员 0；其定义中264个去重method/path字面量均与规范相符。
Web 业务源码没有 AutoForm/CrudForm/useAutoForm/createApiClient 调用；共享库 31 种 FieldType 已逐项覆盖。
后端 20 个 HTTP 模块、27 组共 357 个生产 Go 文件完成枚举/规则候选扫描，输入引用闭包 122 个具名组件。
实际装配基线 398 操作与提交的规范集合逐项一致；含全部可选诊断配置为 411 文档操作，详见 §12。
这些是全量结构/来源盘点及证据化语义风险核查，不等于所有业务分支逐行审计或新功能运行时验证。
数据库状态/第三方协议/真实部署行为仍由已登记实施验收证明，不声称可静态证明“绝无未知缺陷”。

### 2.3 独立评审

后端子评审：注册时机、公开暴露、CORS/CSRF/账号视图、Huma 实际行为、错误模型。
前端子评审：两客户端、共享组件与手写页面、最终载荷、日期金额、缓存、错误显示。
主评审：全量规范盘点、两侧语义整合、改动界限、验证与最终接受。
依赖升级、SQL、存储架构、视觉设计与全仓安全审计不属于本次范围。

## 3. 冻结的推荐设计

### 3.1 单一来源与注册位置

字段规则唯一来源：模块拥有的 Go 输入/可复用值类型上的 tag。标准约束沿用 Huma 原生 tag；
补充约束（例如 UTF-8 字节长度、精确小数、字段比较）使用明确的扩展 tag，错误消息 tag 只引用 namespace 限定的 i18n key。
统一适配层编译这些声明：标准规则继续由 Huma 执行，扩展规则注册的固定处理器执行；输出 Schema/x-field-rules/messages。
SchemaProvider/SchemaTransformer 是声明的投影适配点，不是让开发者另写一份范围、正则或必填参数的第二来源。
服务层仍需字段校验时复用同一声明编译出的校验入口；不保留 len/正则/范围硬编码与 tag 并行维护。
复用字段/值类型由业务模块拥有，不建立中央业务 DTO 包；不同操作确有不同语义时使用明确的创建/更新声明组合。
业务存在性/权限/余额等查询留在领域服务，它们不伪装成静态 tag，但必须返回统一字段/表单错误。
扩展 tag 的精确语法在第一阶段冻结并以编译/启动校验及测试约束，不执行任意表达式。
新增 pkg/extension/huma/request_metadata.go：复用 Huma 原生 OperationDocumenter 在 typed 注册时记录来源，
API/Adapter 装饰器记录实际 Handle 的 operationId/method/runtimePath，模块注册完后以 operationId 连接最终公开 schema。
不能只遍历最终 Paths：文件 catch-all 的文档路径已被改写，26 个协议操作直接 Adapter.Handle，另有诊断 raw 路由。
最终为符合条件的真实业务路径注册一个 OPTIONS operation，响应按目标方法区分；公开 path_template 用文档路径。
OperationDocumenter 保留原 !Hidden/AddOperation 语义，既有 group/路径修饰继续生效；不使用调用栈判断生产来源。
此接线在临时 overlay 装配测试中已证明可区分 372 typed 与 26 direct adapter，详见全操作清单。
统一接入 startHTTPServer、App.RegisterHTTP、ExportOpenAPI；框架层只操作 huma.API，不把 chi 引入业务模块。
每个 OPTIONS 使用独立稳定 operationId，例如 request-metadata-<稳定路径标识>；冲突启动报错。
先快照再注册，不遍历同时修改 map；OPTIONS 不描述自己，显式已有 OPTIONS 不覆盖，隐藏操作与 raw 协议不自动包装。
只引用当前启用模块，不从提交的静态 JSON 作为运行时来源。导出命令依旧固定模块集生成可复现文档。

规则执行时机冻结：扩展 tag 编译和 schema 投影必须在 Huma processInputType/预编译之前，通过已有 SchemaProvider/
Transformer 扩展机制提供；模块请求/服务入口调用同一扩展 Validate。DocumentOperation 仅记来源，注册后的快照不修改规则。
直接服务校验复用 huma.Validate 时使用 ModeWriteToServer 与每请求独立的 PathBuffer/ValidateResult；
不可直接用采用 ModeReadFromServer 的 ModelValidator。编译产物启动时冻结，不能共享可变错误缓存。
现有 raw 字段检查→trim/NFC 等规范化→规范化后规则→领域状态检查的顺序保持，分别用明确 profile 描述；
HTTP 与内部调用原先不同的接受集合不得因“统一来源”被无意合并。

validation_coverage 不从最终 RequestBody 存在与 SkipValidateBody=false 猜测：OAuth 五个 form body 在注册后
由 documentOAuthForm 补写，实际由协议解析器执行。注册时记录原始声明，协议操作显式标注执行器/声明来源；
无法证明 Huma 执行的 body 不能标 schema。RawHTTPRoutes 单独登记，诊断/静态 UI 不自动包装成业务表单。
现有 398 个业务操作均登记：typed 同源检查、协议参数描述与专属执行分别处理；可选 13 个诊断文档操作明确保留原始协议，
不为其第三方嵌入 UI 创造平台表单规则，也不覆盖现有 wildcard OPTIONS。

### 3.2 OPTIONS 返回什么

复用 {code,message,meta,data}，data 包含 protocol_version、revision、path_template、operations、components、messages。
operations 按方法记录 operation_id、parameters、request_body、validation_coverage。
parameters 保留 in/name/required/style/explode/schema；request_body 保留 required 和不同 media type 的 schema/encoding。
components 仅包含这些输入 schema 可达的本地引用闭包，保留 $ref，处理递归引用；不内联无限展开。
不附带输出 schema、示例中的真实值、数据库状态、当前权限或凭据值；自定义 secret 默认值与示例不能被带出。
required、null、empty、readOnly/writeOnly、additionalProperties、嵌套/数组/组合结构不得扁平化丢失。

标准 schema 是公共事实；前端把它编译成用户描述的 rules 数组。
不要为了传输规则数组再维护一份与 schema 平行的标准约束。
仅标准 schema 不能表达的规则使用同一 schema 节点上的 x-field-rules，示意：

```json
{
  "type": "string",
  "x-field-rules": [
    {"rule": "decimal", "args": {"scale": 2, "min": "0.01"}, "message_key": "validation.amount"}
  ]
}
```

这不是现有字段的默认金额政策，也不把所有金额自动改成字符串；每个业务继续遵守其真实存储/传输契约。
自定义规则处理器提供 Describe 与服务端 Validate；字段参数只在 tag 声明，Schema 扩展和实际校验同时消费这份声明。
首批只实现实际需要的规则，不创建通用表达式语言、远端脚本解释器或任意 async URL 执行器。

### 3.3 字段文案

OPTIONS 根据现有 Accept-Language / respx locale 输出已本地化的规则提示模板及稳定 code/message_key。
所有标准和扩展校验消息必须有 zh-CN / en-US / ms-MY 三份翻译，使用现有模块 namespace 与 respx 消息目录。
前端不另维护同一字段规则的三语副本；通用断网/兼容性/加载提示继续使用前端已有 i18n。
切换语言：重取对应语言元数据，对当前值重新校验；保留输入，不回填旧语种异步结果。
已有服务端错误必须有可重新本地化的 code/message_key/安全 params：对应三语消息资源随元数据提供，切语言重渲染；
不为翻译重放业务写请求。不能映射字段的错误也要使用新语种表单级消息，不能长期残留旧语种。
标准规则复用后端现有验证消息目录和参数；专用正则可在 schema 扩展中指定易懂的字段提示。
规则执行返回结构化 issue；再适配 AutoForm 的 string | undefined 返回值。
参数只允许契约定义的结构化值，前端作为文本渲染，不能执行模板代码或插入 HTML。
字段显示名称、布局、帮助说明仍由页面维护，不要求后端决定 UI。

文案协议冻结为 data.messages[ruleId] = {code, message_key, template}，ruleId 为标准关键词
（required/minLength/minimum 等）、format:<name> 或 custom:<name>。
节点级 x-field-messages 使用相同 ruleId 和结构覆盖通用文案，不重复写范围/长度等约束值。
模板仅支持 {paramName} 的字面替换；不支持表达式、HTML、嵌套格式代码。params 从实际执行的 schema 关键词生成：
required 的 property 来自父对象 required 列表，minLength 的 min 来自该节点 minLength，范围值来自对应 minimum/maximum，
enum 的 allowed 是 schema.enum 数组（展示值按固定文本序列化），不从提示文案反向解析约束。
未知 template 参数或缺文案时显示当前语言通用无效值提示并记录适配问题，不能显示未替换模板或因此放过已确认的错误。
后端现有 printf/i18n token 必须经显式消息适配表变为这套模板，不把内部格式符直接交给前端执行。

最小完整响应示意（标准 rules 由前端从 schema 编译）：

```json
{
  "code": 0,
  "message": "ok",
  "meta": {"request_at": "2026-10-13T00:00:00Z", "elapsed_ms": 0},
  "data": {
    "protocol_version": 1,
    "revision": "sha256:<canonical-contract-and-messages>",
    "path_template": "/example",
    "operations": {
      "POST": {
        "operation_id": "create-example",
        "parameters": [],
        "request_body": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["name"],
                "properties": {"name": {"type": "string", "minLength": 1}}
              }
            }
          }
        },
        "validation_coverage": {"params": "schema", "body": "schema", "business": "server_only"}
      }
    },
    "components": {"schemas": {}},
    "messages": {
      "required": {"code": "required", "message_key": "validation.required", "template": "缺少字段 {property}"},
      "minLength": {"code": "min_length", "message_key": "validation.min_length", "template": "至少输入 {min} 个字符"}
    }
  }
}
```

示意中的 key 为公开协议设计，不声称这些翻译 key 已存在。实施时适配已有 respx 消息目录。
validation_coverage 中 schema 只表示有注册期证据证明该部分由 Huma Schema 校验，不表示全部业务规则或全部前端规则已支持；
跳过、注册后仅补文档或原始协议为 protocol/server_only，并记录执行来源。前端另行计算每个子树的执行支持度。

服务端业务 issue 的翻译资源冻结：operations[method].issue_messages[message_key] = {code, template}，语言由 Accept-Language 决定。
标准规则依次查节点 x-field-messages → data.messages[ruleId]；业务字段/表单 issue 用当前操作 issue_messages 精确 key。
code→message_key→三语模板由模块既有错误映射与 i18n 目录统一提供，错误构造和元数据复用该映射；
不另维护前端业务翻译副本，不发布实际用户值。缺失模板阻断已接入操作的验收；旧服务端无 key 仍按兼容策略提示。
不要求所有业务错误都成为 tag：静态字段规则来自 tag，状态/权限错误来自领域错误契约，这是不同职责。

### 3.4 公开策略、预检和缓存

元数据为静态接口说明，沿用已有 API.OpenAPI.Enabled：关闭则不发布 OPTIONS 元数据。
新 OPTIONS 为独立公开操作，不复制目标鉴权/限流元数据与中间件，业务请求仍按原鉴权执行。
因此 OPTIONS 成功不是有权操作的证明，Allow/operations 也不是用户权限清单。
本轮发现 Huma 默认 `/schemas/{schema}` 在 OpenAPI.Enabled=false 时仍注册；原有 schema 路由行为保留，
这里的开关保证“不额外发布 OPTIONS”，不把它描述成原系统所有 schema 都不可访问的安全保证。
该选择保持已有文档暴露边界；本轮不额外新增独立公开开关。将来确需关闭规范但提供元数据时再明确独立配置。
App.RegisterHTTP 与 ExportOpenAPI 要显式传递暴露/导出意图，不能继续依赖 humax.Config 零值碰巧决定是否注册。

仓内未发现完整 CORS 预检处理；有 CSRF 跳过 OPTIONS 与 telemetry 暴露头，并不等于支持 CORS。
Origin + Access-Control-Request-Method 表示预检：元数据处理不得将其当作页面取规则，也不得擅自允许任意来源。
保持现有部署代理的 CORS 策略；没有获准跨域策略的请求不得因新增 OPTIONS 得到允许跨域头。
普通 OPTIONS 不带 Access-Control-Request-Method，前端仍通过现有客户端、凭据和语言处理发起。

OPTIONS 响应按 HTTP 语义不可缓存；不设计基于 304/浏览器 HTTP 缓存的 OPTIONS 协议，也不把 CORS Max-Age 当元数据缓存。
前端仅作应用内去重：base URL + 具体路径 + method + content type + locale + 当前会话/工作区/账号视图代次。
同一表单打开时取得一次，关闭后释放该使用周期的缓存；同周期并发请求合并，重开表单重新获取。
语言/身份/工作区切换立即失效，取消或忽略旧请求返回，避免错语种及旧上下文回填。
revision 是确定性输入契约与文案版本散列，不含 request_at/elapsed_ms；仅用于版本识别，不承诺锁住业务提交时的规则。

### 3.5 不可用与不支持策略

推荐采用兼容现状的服务端最终校验模式：OPTIONS 暂不可用、被部署关闭、出现不支持规则时，保留用户输入，
给出明确的表单级提示（本地部分校验不可用，提交后由服务器校验），允许按原流程提交；不把 unsupported 当 PASS。
服务端错误必须能回填并显示，失败时不得清空表单或由新增校验/元数据逻辑自动重放写请求。
既有 UI 客户端 401 刷新后一次重试保持原授权传输行为；本方案不顺带取消或扩展重试政策。
401/403/已关闭账号视图优先执行原身份处理，不能用 metadata fallback 吞掉鉴权。
这属于本方案明确推荐的策略，不是静默 fallback；如果未来要求所有提交必须先完成本地校验，应单独变更此政策。
纳入完整实施范围的已有字段规则必须获得执行支持或有明确不可本地执行的业务原因，并闭环服务端校验及错误呈现。
server_only 只用于真实的服务端状态依赖/原始协议内容，不可把待实现的正则、金额、日期等常见字段规则挂在其下宣布完成。
unknown 是灰度/未来契约兼容策略，不是当前实现遗漏的验收豁免。纳入范围的声明漂移与未完成适配由 CI 阻止发布。
本地执行结果使用 valid / invalid / unknown 三态；缺支持不能被当成 valid 或 invalid。
组合规则必须传播 unknown：anyOf 中一个已知分支失败、另一个未知时整体 unknown；
oneOf 不能因一个分支已成功就忽略未知分支可能也成功，not/条件规则同理。
只在本地能证明整体失败时拦截；不从不确定分支里抽出字段错误直接阻止提交。
与未知子树无关且可独立证明的失败仍可拦截。该语义必须由组合/嵌套共同样例锁定。

## 4. 语义规则与边界

| 问题 | 统一要求 |
|---|---|
| required | JSON 字段存在性与非空分别处理；不直接等同 AutoForm trim 后为空、HTML required；空数组、false、0 均按真实 schema 判断 |
| PATCH | 缺省、null、空串不同；只检查实际提交的请求对象，不给缺省字段填 UI 默认值后误发 |
| 文本长度 | 对标 Huma Unicode code points，不用 JS UTF-16 length；若服务层有字节约束需显式声明并分别执行 |
| 正则 | 只支持已验证 Go/JS 语义一致的子集；不擅自加锚点或 flags；当前 7 种 pattern 纳入共享样例，含空白与非 ASCII 字符 |
| 邮箱/URI | 不能直接复用现有简化 email 正则宣称等同 Huma；共同样例验证，尚未证明一致则该 format 为 server_only |
| integer/int64 | JS 不安全整数不能被静默 Number 转换；沿用 ID 字符串契约；number 超安全范围明确不可本地精确判定 |
| 金额 | 精度/范围/符号遵守各字段契约；decimal 字符串用整数化/BigInt 精确比较，不用浮点取模；不改币种或舍入政策 |
| date | 严格合法公历日期与闰年，不依赖宽松 Date.parse |
| date-time | 对标现有 RFC3339 接受范围，显式时区；本地日期时间不是时间点；不默认浏览器时区来补后端未知语义 |
| 跨字段 | validate(value, requestValues) 支持明确字段引用；依赖字段变化重验；有条件必填/oneOf 保留对象级校验，不能拆成互不相关字段规则 |
| 隐藏字段 | UI 隐藏不代表 API 不校验；最终请求有字段就按请求规则检查；无法映射的错误显示在表单层 |
| 上传/原始协议 | 描述媒体类型与可公开参数，首批不实现文件内容/SCIM/OAuth 自定义规则的完整客户端执行 |
| 默认值 | 不把 schema default 全局自动写入 PATCH；后端默认行为与页面初始化分离 |

注册密码 8 字符 / 12 字节的差异属于必须解决的实施项，不再列为悬挂的后续前置条件。
兼容修复方案：先冻结现有 HTTP 实际接受集合，再将其字符数约束和 UTF-8 字节数约束分别声明、组合执行，
去掉服务层对应硬编码并改为复用模块拥有的声明校验入口。不能直接把 8 改成 12 字符而改变既有密码接受政策。
同时盘点注册/登录/改密/重置/邀请接受及内部调用；不同入口有意不同的规则明确组合，不强迫登录采用新密码强度要求。
对每个入口的成功/失败边界和多字节字符做前后对照；存在无法兼容的政策冲突须在该阶段明确决策并解决，未解决不得验收。

## 5. 服务端错误闭环

保留原 code/message/meta/data envelope。普通请求校验失败改为 data.validation_errors 数组；
已有业务冲突 data 不覆盖，需字段错误的业务类型显式增加可选 validation_errors，旧客户端仍可读 message。
新增具名 ValidationIssue / ValidationErrorData，并在相应 422 响应显式发布字段错误 Schema；
对本来有业务 422 的操作保留其业务响应类型，与字段错误类型使用明确的兼容联合，不覆盖原业务 data。
不能只往 errorEnvelope.Data(any) 填对象就声称 OpenAPI 自动获得结构化类型。
需要通过 Huma Schema/响应描述扩展把具名组件与相应响应连接，并验证生成 TS 能引用它们。
建议 issue 结构：{in, path?, location?, code, message_key?, params?, message}；已接入错误须有可重译的消息映射。
path 是可可靠确定时的 RFC6901 JSON Pointer；in 区分 body/query/path/header/cookie。
location 保留 Huma 的安全字段位置，未知路径不得猜测为 JSON Pointer；无法绑定的错误展示全局提示。
嵌套对象/数组、属性名含 . / ~ 与数字键都要测试，不能简单 split('.') 转路径。
不回传 rejected value（尤其密码/token）。

Huma ErrorDetail 没有原生稳定规则 code。复用 respx 已有未本地化 token 白名单映射 code，
只解析本系统控制的内部 token，不从中文/英文文案反推规则；仅发布 allowlist 的安全参数。
unknown/custom 错误输出 invalid_value 与安全 message；不能伪造精确 keyword。
首次支持的规则必须能给出准确字段绑定或明确表单级 fallback。

两套现有客户端把服务端 issue 规范化到同一个纯数据类型；另有 account-view.ts 两个直接 fetch，是第三条既有传输路径，
须接相同 issue normalizer 与 Accept-Language；不因此新建第三套 HTTP 客户端。
account-view-dialog 的 catch 必须保留 cause 并展示可见字段/全局错误。退出账号视图仍先清本地状态、best-effort DELETE，
不得增加阻止退出的 OPTIONS 等待、必填校验或把原本抑制的撤销失败变成阻断提示。
openAccountViewTab 保持首个 await 前同步 window.open；在 dialog 打开时预取元数据，不能因为异步校验触发浏览器弹窗阻止。
无法在同步阶段完成的预检在既有开窗/握手流程内处理并保留原失败撤销，不重放发行请求。
AutoForm 增加外部字段错误注入能力；CRUD catch 继续显示原有总体提示并把字段详情交回表单。
手写页面用同一校验 hook/适配器呈现字段错误；字段变化清除对应旧错误，异步结果须核对输入版本。

## 6. 前端接入方式

### 6.1 统一 UI 绑定层（本轮新增硬要求）

采用共享的 API 表单上下文 + 字段绑定能力，而不是页面逐条绑定 rules。
表单边界一次指定 method、请求地址、载荷转换和必要字段映射；字段通过 name / 显式 requestPath 注册。
共享层负责 OPTIONS 生命周期、规则编译、控件 props、输入反馈、跨字段重验、submit、服务端 issue 回填。
AutoForm/CRUD 自动接该上下文；现有手写表单接同一个边界，Input/Textarea/Select/日期金额等适配组件按字段名读取约束。
基础 Input 不独立请求 OPTIONS，不在组件中硬编码业务接口；没有表单上下文时保持原组件行为。
页面只保留布局、标签、请求构造、纯 UI 交互；不得重新写一套 required/maxLength/rules/message。
绑定契约固定为 {target, buildRequest, bindings}：target 明确有效 method/url/contentType，buildRequest 生成一次最终请求快照，
bindings 将 UI 字段映射到一个或多个 {in,path/name} 以及真实 focus handle；不得反射任意 commit 函数或猜 DOM 载荷。
无表单的用户动作也复用请求执行入口与全局 issue 摘要；无可编辑输入的自动清理/退出保留原不中断语义。
同一字段保留 issue 数组及来源/请求代次：优先当前可证明的本地错误，再当前服务端错误；其余可展开显示。
按当前 DOM 注册顺序聚焦首个可操作字段，未知位置聚焦摘要；数组重排/字段修改清除对应旧快照错误并重验依赖。

输入限制与校验分工：
- enum 约束选项，日期上下界控制可选日期，数组上限控制追加入口，适用时自动设置 inputMode/min/max/step 等。
- 可证明安全的长度/字符限制由共享输入适配器处理，兼容粘贴、撤销、选区替换、移动端和中文 IME composition。
- 正则、金额、日期不按“每个中间输入都必须最终合法”拦截；允许金额 1.、负号等中间态，blur/submit 做最终验证。
- 不静默截断字符串、不自动四舍五入金额、不擅自 clamp 数值；超限操作给明确反馈。
- Unicode 长度按后端单位，不盲目使用原生 maxlength；日期和数字控件限制不能与实际校验产生两套语义。
- 统一处理 dirty/touched、错误定位、首个错误聚焦、ARIA 和复杂组件服务端错误，不要求每页实现。

### 6.2 共享执行器与请求语义

共享纯规则编译器建议位于 packages/ui/src/lib/request-validation.ts，职责独立于 React 和网络。
输入 OPTIONS 内的操作契约，输出：字段规则、对象级校验、支持度；不建设完整自研 JSON Schema 引擎。
优先覆盖当前实际出现且能证明一致的类型/约束；$ref/嵌套/数组/对象规则不能漏判。
未支持组合/format 在开发阶段显式记录 unknown，完成现有范围的支持后才能验收；真实业务条件按既定 server_only 策略闭环。Zod 已存在，可复用已有类型/错误适配，但不假定 Zod 的 email/number 默认语义等同 Huma。
暂不增加 Ajv、表单库或 decimal 库；需要完整 JSON Schema 支持时再评估标准校验器，避免不完整重实现冒充完整支持。

页面仍需声明：目标 method + URL、content type、最终 payload 构造、UI 字段 ↔ 请求位置映射。
这些是页面与接口的绑定，不是重复维护约束。路径生成复用 api-route / CRUD resource 已有机制。
提交顺序：保留原 UI 转换 → 构造最终请求 → 应用公共契约校验 → 发原业务请求 → 回填服务端 issue。
同一次提交只构造一次最终请求表示；校验与发送消费同一快照，不重复调用有状态的 toServerData。
JSON 校验针对实际序列化语义（对象 undefined 被省略、数组 undefined 和非有限 number 会成为 null）；
不能先校验原始对象，再发送语义不同的 JSON。原始二进制/表单编码使用各自请求表示，不做 JSON 往返转换。
blur/change 可检查单字段，submit 必须检查最终 payload 及对象级规则。
原生 form 迁移时统一选择自定义校验展示（noValidate），避免浏览器另一套 required/length 抢先拦截；
HTML 属性只在语义一致时作为体验辅助，尤其不能让 UTF-16 maxlength 比后端更严格。
AutoForm 复用 rules 与错误渲染，不将全部业务页面迁移到 AutoForm。
远端 required 存在性校验由协议执行器负责；不能直接设 field.required=true 然后触发现有 trim/空数组非空检查。
对启用远端契约的字段分离可视必填标记和校验来源，显式跳过被替代的旧 API required/rules；纯交互规则继续执行。
未启用远端契约的表单保持原有 required 行为。
确认密码、验证码入口状态等纯交互规则可保留，不能因为删除重复 API 校验而误删它们。

## 7. 代码影响清单

### 必改（基础设施）

- apps/backend-api/pkg/extension/huma/request_metadata.go、request_metadata_test.go（新增）：快照、引用闭包、注册与契约。
- apps/backend-api/internal/app/http.go、openapi.go 及对应测试：三个装配/导出路径一致。
- apps/backend-api/pkg/extension/huma/respx/error.go、transform.go、对应测试及 locales/*.json：结构化详情与复用消息。
- apps/backend-api/docs/openapi.json：由 make openapi 生成，禁止手改。
- apps/frontend-web/packages/ui/src/lib/request-validation.ts 及测试（新增）：共享中立契约、规则编译与 issues。
- apps/frontend-web/packages/ui/src/components/auto-form/types.ts、use-auto-form.ts：动态规则与外部错误接入。
- apps/frontend-web/packages/ui/src/components/crud/crud-form.tsx / resource.ts：最终载荷、方法路径、错误传递。
- apps/frontend-web/packages/ui/src/lib/api-client.ts 与 apps/web/src/lib/iam-api.ts：OPTIONS 与错误适配；保持既有两客户端边界。
- apps/frontend-web/apps/web/src/lib/account-view.ts、components/account-view-dialog.tsx：第三条既有传输路径、语言/错误与同步 popup 兼容。
- apps/frontend-web/packages/ui/src/i18n/intl-provider.tsx 及消息目录：切语言加载代次保护，缺失 percent 三语补齐；规则消息仍来自后端。
- apps/frontend-web/apps/web/src/lib/api-route.ts：OPTIONS 方法支持；iam-api.ts 排除 OPTIONS 的 mutation 通知。
- 必要时新增共享 React hook（与纯编译器分工清楚）：生命周期、缓存、取消、错误绑定；两客户端注入请求函数。
- apps/frontend-web/apps/web/src/lib/api-schema.ts、api-routes.ts：从源规范重新生成。

### 统一 UI 组件改动（不能遗漏）

- packages/ui/src/components/input.tsx、textarea.tsx、select.tsx 及 checkbox/radio/多选等对应绑定适配。
- packages/ui/src/components/pickers/date-picker.tsx、date-time-picker.tsx、time-picker.tsx 与 auto-form/field-renderers*.tsx：
  统一日期边界、数值/金额输入表示、精度与中间态；不继续在 renderer 中保留会先舍入再校验的路径。
- 共享表单边界/字段绑定组件及其测试：统一 props、状态、错误、注册与销毁；具体文件按组件责任归并，避免每个字段新建框架。
- 后端相应模块的声明、字段校验调用、三语 locales 与边界测试：处理漂移及服务层重复，非只增加元数据出口。

### 逐表单修改（共享接入后统一自动处理）

首批邀请成员：apps/web/src/app/invitations/page.tsx；覆盖已存在 email/整数范围/optional/数组值及错误回填。
随后挑一个 PATCH 编辑表单（例如 departments/page.tsx），验证缺省/null/编辑语义；共享 AutoForm/CRUD 另有集成用例。
推广到 33 个含 form 文件时逐个确认是否调用目标业务 API，搜索表单不机械纳入。
权限、安全、注册、文件等受影响表单全部纳入核对与迁移清单；实际规则及序列化语义问题在该实施范围内解决。
自定义 decimal/date 用共同契约用例验证能力，不为展示能力新增虚构业务金额字段或改已有接口类型。
共享库遗留 /sys/files、/sys/configs/file-storage/limits、/regions、/regions/{id} 不在当前后端规范，当前 Web 未使用该 CRUD 层；
不为“闭环”新建这些业务接口、不暗换成现有文件 API。用合成契约验证组件能力，真实接入通过显式 target descriptor。
_leaflet-map 第三方 Nominatim 不属于平台 API，use-auto-table 的 fetch(query) 是注入回调，不能误登记为新 HTTP 客户端。

### 不改

数据库、迁移、表、业务模块边界、鉴权决策、权限枚举、业务 API 路径与现有写方法、runner、Tauri 包装、UI 布局。
注册密码声明漂移、价格控件校验前舍入和受影响日期语义属于本需求必要兼容修复，纳入实施并验收；不修改无关业务政策。
无需所有模块批量添加新 tag；已有标准约束直接复用，自定义声明按真实需求追加。

## 8. 实施顺序与验收

1. **契约与一致性基线**：按本版已冻结协议与全量清单逐项建实施状态，盘点对应真实字段规则；建立 Go/TS 共用输入样例。
2. **后端元数据与结构化错误**：三个注册入口一致，实际 OPTIONS 请求与已有 POST/PATCH 请求回归，生成规范。
3. **前端共享执行与适配**：两客户端、AutoForm/CRUD、手写页面共用规则；不新增重复 HTTP 层。
4. **首批闭环**：邀请成员 + PATCH 编辑 + 共享组件；在线更改后端约束重启后，前端不重建、重开表单可取得新规则。
5. **逐表单推广**：按表单清单移除重复 API 规则；每个已迁移表单必须有明确覆盖状态，不把未支持规则算作完成。

首批通过仅表示阶段完成，不是任务完成。所有受影响表单/接口/组件已在本版四份附件登记；实施第一阶段逐项建立完成状态，
每项记录规则来源、UI 绑定、输入转换、三语消息、服务端字段错误、测试与处理结果。
排除项必须有实际依据（例如无 API 参数的纯本地搜索），不能用“后续做”排除困难项目。
最终验收要求清单未处理项为 0、同源规则漂移为 0、重复手写 API 限制清理完成、三语缺失为 0；
有政策冲突必须在交付前解决，未解决则任务 NOT READY。未知规则兼容分支不能用于掩盖当前支持缺口。

必须验收：
- 开/关模块与规范暴露开关、unknown path、显式 OPTIONS 冲突、所有目标方法、嵌套路由参数与同路径多方法。
- 路由注册/导出元数据集合一致，不递归描述 OPTIONS；关闭 OpenAPI 后不新发布 OPTIONS 元数据（现有 /schemas 路由保持既有行为）。
- 预检不执行元数据/业务副作用；不绕过原鉴权，账号视图读规则不误按写操作拦截，不消耗目标登录限流预算。
- missing/null/empty/0/false、PATCH、Unicode、Go/JS 正则、数字精度、非法日期/闰年/时区、跨字段依赖。
- 共享样例同时驱动真实 Huma 请求和前端规则，不是只检查序列化后的 tag 非空。
- 断网/unknown rule/部署禁用/语言切换/会话切换/过期响应；用户输入保留，不由新增校验/元数据逻辑自动重放写操作；保留既有401刷新重试。
- body/query/path/header 错误、数组/嵌套、复杂字段名、未知错误全局显示、密码/token 不出现在响应或日志新增内容。
- 元数据规则变更无需前端构建；新规则算法仍需要发布支持该算法的前端，不能声称无限动态。
- 原生 form 与 AutoForm/CRUD 各自真实提交成功/失败；OPTIONS 不触发菜单 mutation 广播。
- Input/多行/选择/日期/金额/数组组件统一输入限制；键盘、粘贴、IME、blur/submit、字段依赖与服务端错误一致。
- zh-CN/en-US/ms-MY：每条规则、业务字段错误及语言切换均验证，不以 key 回显替代翻译。
- 全影响清单零悬挂；既有 tag/服务层差异和金额/日期转换问题均有兼容修复与回归证据。

实施完成需跑 backend make ci；没有 SQL 改动，不要求为该改动跑数据库性能门禁。
frontend 从 apps/frontend-web 执行 bun run typecheck、bun run lint、bun run test、bun run build、bun run check:api-types；
首批页面浏览器闭环与共享组件集成用例不可由纯规则单测代替。

## 9. 反向风险审查

A1 暂存/离线：不新增离线队列；必须验证元数据/提交失败保留当前表单值。
A2 大请求：已有上传协议触及，但首批不解释文件内容；保留服务端体积限制和原错误路径，不把 Schema 长度当网关限制。
A3 状态机：不改变业务流转；规则请求失败不能导致页面无法退出/输入丢失。
A4 隔离：静态元数据公开边界跟随 OpenAPI；动态权限和租户数据不下发，业务鉴权保持原状。
A5 精度：decimal 支持触及，必须精确解析和共同边界用例；不新增金额运算、计费和舍入政策。
A6 幂等：OPTIONS 无副作用；不新增校验/元数据驱动的写重试，保留既有401刷新重试，不改变原业务幂等设计。
A7 兼容：旧客户端继续读 message；旧规则下记录不因 UI 新默认被自动重写；未知规则与灰度版本明确降级。
A8 异步作业：不引入任务/队列，不适用；仅表单异步校验须处理过期返回。

依赖范围：后端沿用 Huma v2.39.1 与现有 Go 依赖；前端沿用 React / Zod（manifest ^4.4.3）及现有组件。
无依赖新增/升级建议；本轮未做依赖最新版本、维护状态或许可证全仓审计。

## 10. 实际验证与限制

- 全量 OpenAPI 结构统计与 path 参数集合检查：PASS（见 §2）。
- go test ./pkg/extension/huma/...：PASS。
- 后端独立评审执行 go test ./pkg/extension/huma ./pkg/extension/huma/respx ./pkg/extension/authz：PASS。
- bun test packages/ui/src/components/auto-form/validation.test.ts apps/web/src/lib/iam-api.test.ts：69 PASS，0 FAIL，432 assertions。
- make openapi-check：PASS；实际重新生成规范，TestPublishedOpenAPI 检查通过，输出与 docs/openapi.json 一致。
- 文档 JSON 示例解析、主要证据路径、反向评审修订覆盖、LF/空白检查及限定文件 git diff --check：PASS。
- 新 OPTIONS、新动态校验、浏览器交互未实施，因此没有该功能的运行时 PASS。
- 未跑全仓 CI/前端 build：本轮交付文档，未改应用代码；相关基线测试不证明未来实现已经正确。
- 现有 workspace 有大量用户改动和 Web 目录迁移；本轮不提交、不清理这些文件。

## 11. 最终复核

第一轮：后端/前端独立调查；主代理对照源码与完整规范确认证据。
第二轮：两名评审只读检查完整方案；提出标准文案 wire 格式、组合规则 unknown 传播、具名错误 Schema、
AutoForm required 分离与最终载荷单次构造。已全部纳入 §3/5/6，主代理逐项复核，无未处理的设计阻断。
自审：范围、复用、架构、复杂度、既有行为保护、状态持久化 PASS（设计交付口径）。
最终接受：动态规则方案、当前代码影响清单、复杂类型与错误语义、分阶段落地及验证门禁均已给出，PASS。
修订版 2 将用户四项要求纳入硬验收；注册密码等既有漂移属于必须兼容修复的实施范围，不再悬挂。
本轮 v3 全量补查采用 Hybrid：三个独立清单、主线实际路由对账与交叉复核；生产代码仍未实施。
下一步：用户批准实施后执行 §8 第1阶段，再按阶段推进；本轮评审工作无剩余项。


## 12. 全量影响闭合与最终设计决定（修订版 3）

本节取代早期“代表性采样已足够”的完成口径。用户误发的项目菜单消息已明确撤回，本轮未修改 UI 布局。

### 12.1 四份证据附件

| 附件 | 可核对覆盖集合 | 实施/验收编号 |
|---|---|---|
| [全操作与实际路由](dynamic-field-rules-operation-inventory-2026-10-13.md) | 398 基线 + 13 可选诊断 = 411 operation；122 输入 Schema 引用；runtime/document path及 raw 路由 | OP001–OP411；RT01/RT02/IN01/JS01/MP01/PR01 |
| [后端模块与规则来源](dynamic-field-rules-backend-impact-2026-10-13.md) | 20 HTTP 模块；27 组357生产文件；声明/状态/协议/非输入分类 | BE01–BE22 |
| [Web 页面与非表单动作](dynamic-field-rules-frontend-impact-2026-10-13.md) | 96非测试非生成源文件；48 form/33文件；54 UI入口；262 API成员及额外账号视图直接请求 | FE-001–FE-054、FORM-001–FORM-048、UI-A–UI-H |
| [共享 UI 与传输/语言](dynamic-field-rules-ui-impact-2026-10-13.md) | 31/31 FieldType、两客户端及第三条既有直接传输路径、三语和Tauri复用边界 | A-UI-01–10、T-UI-01–15 |

各项“已登记”不等于“已实施”；生产实现状态仍为 NOT IMPLEMENTED，新功能验收 NOT RUN。
编译产物/model/query/dbgen/proto 不作为规则声明源重写；保留其持久化/生成责任。排除条件与文件全集在附件明确给出。

### 12.2 UI 评审决定全部收敛（D-UI-01–06）

| 决定 | 本版冻结的推荐实现 | 状态 |
|---|---|---|
| D-UI-01 消息资源 | §3.3 operation.issue_messages 与标准 messages 分离索引；模块现有错误映射+i18n同源，支持三语重译 | 已纳入，不再待选择 |
| D-UI-02 绑定接口 | §6.1 target/buildRequest/bindings；多路径映射和真实focus，元数据transport由现有路径注入 | 已纳入 |
| D-UI-03 数值日期转换 | 保留真实调用wire与时区，草稿精确解析不先舍入/clamp；没有调用的共享类型以合成契约验证，不新增业务政策 | 已纳入 |
| D-UI-04 既有重试 | 不新增metadata驱动写重放，保留现有401一次刷新重试；不改退出best-effort | 已纳入 |
| D-UI-05 元数据状态 | loading/unavailable/unknown提示后走原服务端最终校验；明确invalid才拦截，原401/403处理优先 | 已纳入 |
| D-UI-06 多错误与定位 | issue数组、请求代次、本地/服务端优先级、DOM focus与未知位置摘要；数组重排保护 | 已纳入 |

这六项是基于用户要求与既有行为的实现细化，不需要新增产品语义决定。各附件若保留“待主设计冻结”的历史描述，以本表为准。

### 12.3 本轮新增证据与复核

- 实际 Go overlay 装配测试：基线 adapter398 / document398 / router399；全诊断 adapter398 / document411 / router425，PASS。
- 基线规范与实际装配的 operationId+method+path 集合全相等；唯一业务路径改写已明确映射。
- OperationDocumenter 与真实注册分类逐项验证：372 typed、26 protocol，PASS；不在生产使用审计调用栈。
- 全操作矩阵 411 行与运行时记录经独立后端复核逐项相等；6 OAuth SkipParams及5后置form body文档声明已明确处理。
- 前端6个既有测试文件：105 PASS / 0 FAIL（24997 assertions）；另账号视图与组织路由2文件：9 PASS / 0 FAIL（42 assertions）。
- 后端 policyx/query 既有测试 PASS；文件名/todo标题与状态/注册配置4个相关测试在3包运行 PASS。
- 没有新OPTIONS、UI自动绑定、三语动态错误的运行时实现，因此不声称这些验收已经通过。
- 最终文档验收：5份文档、369个证据/附件链接、JSON示例、411操作集合、31 FieldType集合、OpenAPI指纹与LF/空白检查 PASS。
- 本轮无需跑未改代码的全量build/CI；后续实施仍必须通过§8门禁、全部模块和UI清单中的真实请求/浏览器用例。

### 12.4 完成边界

设计及影响核查交付：覆盖集合、登记动作、明确保留/排除、验收用例和阻断条件已闭合。
没有把已发现的密码/文件名/Unicode/金额/日期/动态组DSL/原始协议/账号视图等问题悬挂给“以后再说”。
这不等于数学证明所有语义缺陷已发现，更不等于实施完成；实现期间新增发现若属于影响范围，必须同步账本并处理后再验收。
下步进入实现时从全量声明基线开始，先基础设施、再分批接入、最后全清单归零；试点通过不得单独结项。
