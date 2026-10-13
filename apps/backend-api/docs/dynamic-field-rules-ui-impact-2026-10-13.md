# 动态字段规则：共享 UI、客户端与语言链路全量影响清单

日期：2026-10-13。依据：`dynamic-field-rules-design-2026-10-13.md` 修订版 3（含 §12.2 已冻结决定）与本工作树源码。本文仅调查/设计，生产代码未修改。文中 A-UI/T-UI 是实施/验收任务编号，均不是已实现标记。文件路径相对仓库根；下文 UI 指 `apps/frontend-web/packages/ui/src`，Web 指 `apps/frontend-web/apps/web/src`。

## 1. 范围与结论

逐一核查 `auto-form/types.ts` 的 **31 个 FieldType** 及 registry、实际 renderer；核查 2 套客户端及账号视图第三条既有直接传输路径、3 种内置语言、AutoForm/CRUD、原生基础组件、日期/时间/文件选择器、Tauri Web 复用配置。31 项都有具体输入/输出及实施动作，不能把“没有当前业务调用”视为共享组件支持已经完成。业务逐页面/接口清单由配套清单负责；本表覆盖组件能力，不推断存在虚构金额或地理业务接口。

统一绑定可行，但目前没有现成 API 表单上下文可直接开启。复用现有 `ApiProvider/useApi`、`useAutoForm`、FieldRegistry、crudFactory、apiRoute 和两套客户端，并适配 account-view 既有直接 transport；增加一个共享纯契约执行器与一个 React 表单/字段绑定层足够。无需第三套 HTTP 客户端、完整表单框架、新 decimal 库或逐字段独立网络请求。

仅给 AutoForm 赋 `required/rules` 不满足需求：现状先验证 UI values，再过滤字段，再由 CRUD 转换发送；存在 null 丢失、金额提前舍入、日期宽松解析、数组/对象子路径错误无法回填等链路断点。所有 API 规则必须针对最终请求快照，控件只消费同一编译结果的安全体验提示。

## 2. 单一规则来源与共享绑定职责

| 动作 ID | 所有者/文件 | 必须实现的边界 | 对应验收 |
|---|---|---|---|
| A-UI-01 | UI `lib/request-validation.ts`（建议新增） | 只解释 OPTIONS Schema/$ref/x-field-rules/messages，输出结构化 issue、三态结果、输入提示和依赖图；不含 React、fetch、页面业务规则。稳定 issue 类型由两客户端及 account-view 直接 transport 共同引用 | T-UI-01 |
| A-UI-02 | UI 共享 form context/hook（具体归并到一个职责文件） | 一次声明目标 method/URL/content-type、构造请求及字段映射；管理会话期元数据、注册/注销、touched/dirty、输入版本、依赖重验、submit 和错误分发。基础控件不 fetch | T-UI-02/03/04 |
| A-UI-03 | UI `auto-form/types.ts`、`use-auto-form.ts`、`auto-form.tsx` | 分离 API required 存在性与 UI 必填标记；远端模式跳过被替代的 API rules，保留纯交互规则；内部保存 issues，渲染时翻译；提供外部 issues、全局错误及 focus 注册入口 | T-UI-03/04/12 |
| A-UI-04 | UI `crud/types.ts`、`crud/resource.ts`、`crud/crud-form.tsx` | FormFactory 明确目标请求描述，不能从 commit 函数猜 URL；资源沿用 createApiPath、POST create、PUT edit，不为迁移改成 PATCH。toServerData 只调用一次；已构造快照交执行器和原 transport | T-UI-05 |
| A-UI-05 | UI `input.tsx`、`textarea.tsx`、`select.tsx`、`checkbox.tsx`、`radio-group.tsx`、`switch.tsx`、`tabs.tsx`及 renderer | name/requestPath 注册；基础组件无 context 时保持行为；选择器/组合控件绑定真实可聚焦触发器，避免给无 name 的所有 Input 隐式套业务规则 | T-UI-06/12 |
| A-UI-06 | UI pickers 与 rich renderers | 表中 31 项的精确转换、边界、数组/对象错误绑定；无上下文行为与远端模式隔离，必要兼容修复需冻结旧输入/输出 | T-UI-07/08/09/10 |
| A-UI-07 | UI `lib/api-client.ts`、Web `lib/iam-api.ts`、`lib/account-view.ts`、`components/account-view-dialog.tsx` | 两客户端与第三条既有直接传输路径共用元数据/issue 适配；保留各自鉴权与业务 data；账号视图增加 locale、保留 catch cause，保持同步开窗与 best-effort 退出；普通 OPTIONS 与预检区分 | T-UI-11/13 |
| A-UI-08 | Web `lib/api-route.ts` 与生成器产物 `api-schema.ts`/`api-routes.ts` | method 增加 OPTIONS，由规范生成后获得路径参数类型；路径生成/组织映射复用原逻辑，不字符串猜路由模板 | T-UI-11 |
| A-UI-09 | UI i18n provider/消息目录与 Web i18n loader | 原控件操作提示/元数据不可用提示三语；规则消息只从后端同源资源取得。修复需要的切语言竞态与 issue 重译，不复制每字段规则翻译 | T-UI-14 |
| A-UI-10 | 配套契约/组件/客户端测试 | 本文每项 T-UI 均登记实现/证据；31 registry 项及替换 renderer/custom 分支有穷尽门禁 | T-UI-15 |

字段绑定标识必须是 `{in, path}` 或 `{in, name}`，不能仅用 DOM name 字符串；body 用 RFC6901 指针，query/path/header/cookie 明确位置。UI name 可与请求位置不同，一对多转换由表单显式映射。相同字段多处控件共享状态但保留焦点优先级。重复/模糊映射不能猜测，开发诊断并把服务端 issue 放全局；字段卸载需移除焦点句柄，但最终载荷中仍存在的隐藏字段继续校验。数组删除/重排后按当前快照指针绑定，过期响应不能误绑新行。

表单边界分三层：UI 草稿（允许 `-`、`1.`、不完整日期）→ 页面既有转换生成一次最终 Request 表示 → 契约执行器验证该表示后交原客户端。JSON 应检查实际 JSON 语义：undefined 对象成员省略、数组洞/undefined 和非有限数变 null；保留 null/false/0。编码异常必须作为提交失败呈现，不发送不同载荷。FormData/File/URLSearchParams/二进制不经 JSON 往返；参数 style/explode、重复 query 键也须与真正编码后的请求一致。控件 change/blur 的投影校验不能代替 submit 全对象校验。

## 3. 31 种字段穷尽矩阵

证据位置：基础 renderer 在 UI `components/auto-form/field-renderers.tsx`；富组件在 `field-renderers-rich.tsx`；其他文件逐行列出。表中“动作”指实施要求，不是现状已有能力。每一行都需共用 T-UI-04（服务端错误/语言切换）和 T-UI-12（可聚焦/ARIA），另有专用测试。

| # / FieldType | 当前输入与输出、实际转换 | 绑定/输入限制/兼容动作 | 专用测试 |
|---|---|---|---|
| 01 text | TextFieldRenderer：string/number 显示，DOM change 输出 string；config.type 默认 text | 字符数按 code point、字节长度独立；IME/粘贴/撤销保持；禁止静默截断 | T-UI-06 |
| 02 textarea | 字符串；maxLenField 取其他值 Number，计数用 JS `.length` | 计数与契约单位统一；现有 maxLenField 若纯 UI 联动保留，不作为 API 限制第二来源 | T-UI-06 |
| 03 number | registry 仍用 TextFieldRenderer；FieldRow 注入 type=number（baseConfig 可覆盖）；输出仍 string | 页面数字转换保留并在最终 payload 检查；超安全整数不得 Number；空串/指数/负号中间态不提前 coercion；min/max/step 仅可证明一致时下发 | T-UI-07 |
| 04 password | registry 用 TextFieldRenderer，FieldRow 不自动注入 password 类型，需 config.type | 迁移不能以 FieldType 假设 DOM 已正确；明确密码渲染配置，字节/字符规则来自目标入口；不记录输入或把 rejected value 放 issue | T-UI-06/15 |
| 05 email | registry 同 text，不自动注入 email 类型；输出原 string | 不复用现有简化 email regex 宣称等同 Huma；noValidate 统一呈现；请求原有 trim 仅由原转换决定 | T-UI-01/06 |
| 06 code | registry 实际 TextAreaFieldRenderer，非 CodeMirror | 验证真实字符串，不因为标签“code”自动 JSON.parse；支持大文本长度与多行 | T-UI-06 |
| 07 select | normalizeOptions 值为 string；SimpleSelect null 回调成空串 | enum 按最终值类型匹配，UI string→number 等沿用显式映射；保留服务端旧值供查看，不能默认替换成首项 | T-UI-09 |
| 08 multi-select | 非数组真值变 `[String(value)]`，选择输出 string[]；目前不渲染 error | 消除 API 模式隐式类型救正，maxItems 禁新增仍可删除；补组级/项级错误及描述关系 | T-UI-09/12 |
| 09 checkbox | checked=Boolean(value)，输出 checked===true | required 不等于必须勾选，false 合法性由 schema；nullable/缺省与 false 的显示/提交分离；目前无 error 文案 | T-UI-09/12 |
| 10 checkbox-group | registry 与 multi-select 完全相同 | 同 08，独立 registry 门禁，不能漏别名 | T-UI-09 |
| 11 switch | Boolean(value)，输出 boolean；目前无 error 文案 | 同 09，缺省不自动向 PATCH 写 false | T-UI-09/12 |
| 12 radio | string 枚举；有 label，无 error 渲染 | typed enum 和空/缺省保留；组级 issue、真实选项 focus | T-UI-09/12 |
| 13 date | parseDateValue 将字符串拆年月日并 `new Date(y,m-1,d)`；输出本地 YYYY-MM-DD 或空串 | 拒绝滚动纠错（如 2 月 30 日）及 0–99 年映射；日期非 UTC 时间点；from/to 与实际 schema 同源；不通过 Date.parse 猜合法性 | T-UI-08 |
| 14 dateRange | `{start,end}`；normalizeRangeValue 非字符串→空串；end fromDate 使用 start，可能覆盖 config 下界 | 子路径绑定；不得把 start≤end 当所有 API 通用政策；存在该关系时由声明驱动，边界取有效交集且不自动改值 | T-UI-08 |
| 15 datetime | DateTimePicker 字符串 YYYY-MM-DDTHH:mm[:ss]，无时区 | 本地墙上时间不能直接当 RFC3339；原 API 转换明确时区，date 与 time 边界同时落实；不得丢掉 offset/秒精度再验证 | T-UI-08 |
| 16 datetimeRange | 两组 DatePicker+TimePicker；split 时间 slice(0,8)，join 仅日期时间完整才输出；对象 start/end | 必须保存子控件未完成草稿，验证最终双时间点；不推断时区，不把 partial join 空串当缺省 | T-UI-08 |
| 17 time | TimePicker HH / HH:mm / HH:mm:ss，formatValue 可任意重写 | 时间/时长格式由目标声明区分；不能把 parse clamp 后值当原值有效证据；子输入版本及 blur 注册 | T-UI-08 |
| 18 timeRange | `{start,end}`，分别输出 TimePicker 字符串 | 各子字段与对象规则，跨午夜政策不能自创；formatValue 转换前后共同样例 | T-UI-08 |
| 19 tabs | 默认首项显示，effect 在 value undefined 时写入第一项；未消费 disabled/error | API/PATCH 缺省不得因 mount 写默认；纯 UI tabs 不迁移为请求字段；确为字段时 enum/disabled/错误与 focus 接入 | T-UI-09/03 |
| 20 title | 仅 label；初始化/校验/getValues 跳过 | 明确非请求字段，零元数据请求、零 issue 注册；标题变化不触发字段校验 | T-UI-15 |
| 21 price | 输入 Number(raw) 后 Math.round(n*10**decimals)，显示 Number(value)/factor；默认 decimals=2 | 保留最小单位整数旧传输，不全局改 decimal string；草稿精确解析后拒绝超精度，禁止先舍入；允许 `1.`/负号草稿；安全范围检查再转 number，字符串金额另按声明 | T-UI-07 |
| 22 rich-text | tiptap-field.tsx：输入 HTML string，onUpdate getHTML()；空文档可能 `<p></p>` | API string 长度检查实际 HTML，不自行 stripHTML；“编辑器空”是显式 UI 规则；动态消息只当文本，绑定 editor focus | T-UI-10 |
| 23 code-editor | codemirror-field.tsx：string 原样输出，lang=json 只加载语法扩展 | string 与 JSON 对象契约由原序列化决定；不得因为 lang=json 自动 parse；补编辑器 ARIA/焦点接口 | T-UI-10 |
| 24 image | image/media 共用 uploader；单值对象/null，多值对象[]；元素 imageFileId/imageFileUrl/args | 两阶段请求：上传规则与父表单附件引用规则不同；父 maxItems 不能冒充文件大小。现有 slice 选择数量需 API 模式明确超限反馈，保留已选值 | T-UI-10 |
| 25 media | 同 image，accept 未配置仍默认 image/*；视频通过 args.type/扩展名识别 | 不因类型 media 偷改 accept；按实际媒体声明绑定。失败/部分成功不自动重放上传；枚举文件引用和对象子字段错误 | T-UI-10 |
| 26 remote-select | useRemoteOptions GET list limit=999；value String；失败置空 options | 远端候选项不是静态 enum/权限证明；只和真实 schema enum 相交，不因列表空而抹去值；依赖变化与规则变化分开 | T-UI-09 |
| 27 remote-multi-select | 已选数组 map(String)，GET 同上；输出 string[] | maxItems/uniqueItems/item enum；处理依赖刷新后旧选项和过期响应；保留字符串 ID | T-UI-09 |
| 28 relation-list | 输入对象数组按 valueKey/countKey 映射，id String、count Number(...??1)；编辑 Number(raw)||0；新增空 id/count=1 | count 保留草稿，防空/非法→0；默认值由既有交互明确，不来自 PATCH schema default；数组上限、每行子路径错误与重排版本 | T-UI-07/09 |
| 29 bool-select | true/"true" 显示 true，其余（包括 undefined/null）显示 false；onChange boolean | 显示缺省与提交缺省分离；不得将 string "true" 当真实 boolean 自动通过；枚举/nullable 与控件可表示值核对 | T-UI-09 |
| 30 location | location-field.tsx：对象；键入 lat/lng 为 string，地图回调 number；regionId Number(v)||0，地址 string | 每个子字段有映射和最终转换；坐标类型/范围按 schema，region ID 不能盲目 Number；地图仅选择工具，不是额外业务校验源 | T-UI-07/10 |
| 31 custom | AutoForm 特殊分支直接渲染 component，默认 CustomFieldRenderer 永远不达；未传 onBlur | component 使用同一 field binding，约定值/子路径/focus/blur；覆写 registry 也需同契约；不能把 custom 作为漏校验豁免 | T-UI-10/12/15 |

`dynamicType` 只换 renderer，不改 declared type/custom 分支；请求规则必须绑定 requestPath，不随视觉类型丢失。只读渲染 `readonly-value.tsx` 不发送请求、不校验编辑草稿；detail 不应取得写操作元数据或触发提交。字段可隐藏不等于最终 payload 中不存在。`readonly-value.tsx` 的 price 显示固定除以 100，再按 config.decimals 做 toFixed，与编辑端 10**decimals 并非完全一致；该路径不得拿来生成请求或契约校验输入。若本次绑定需要覆盖非 2 位精度，显示一致性也纳入 T-UI-07 回归，不修改币种政策。

## 4. 基础控件、选择器及上传额外边界

| 文件/能力 | 已核查事实 | 必须纳入动作/测试 |
|---|---|---|
| Input / Textarea | 原生 props 透传；Textarea forwardRef，Input 是普通函数；没有 context/内建错误状态 | A-UI-05：合并用户事件而非覆盖；React 19 ref/焦点传递实际验证。API 模式禁原生 maxlength 比 code points 更严；composition 结束再校验。T-UI-06/12 |
| SimpleSelect / Select | SimpleSelect 无 name/onBlur/aria-invalid/ref 公共参数；只支持 label/description；底层 base-ui Select 使用 portal | A-UI-05：新增绑定入口/触发器焦点，不寄希望于 HTML FormData 自动得到值；复合控件内部移焦不应反复误判 touched。T-UI-09/12 |
| Checkbox / Switch / RadioGroup / Tabs | renderer 许多只传 label；不同组件事件是 checked/value，不是原生 change | A-UI-05：统一值适配但保留 boolean/string 类型；组选中状态与错误状态分离；未绑定纯导航 Tabs 不变。T-UI-09/12 |
| DatePicker / Calendar | Date|null；Calendar buildDisabledMatcher 处理 before/after；DatePicker display 用 useLocale，calendar 可 localeCode override；选择器缺少 name、error/ref 绑定口 | A-UI-06/09：最小/最大边界同源；焦点在 trigger；语言切换不改变日期语义；本地日期必须逐年月日验证。T-UI-08/12/14 |
| DateTimePicker | 宽松 new Date；split rawTime.slice(0,8)，Clock 只时/分；点击 Clock 可补今天，showSeconds 时修改会产生 :00 | A-UI-06：先冻结真实调用需要的时区/秒语义；禁止将 ISO offset 输入无声截断，禁止不完整草稿自动补日期来通过校验；对原有纯 UI 行为不无关重构。T-UI-08 |
| TimePicker | parseTime clamp 至 23/59；NumberInput 去非数字并 slice(-2)、clamp；键盘递增回绕；默认标签英文 | A-UI-06：远端输入不得把 99 改成 23 或静默删字符；合法时钟选择与非法键入处理分离。绑定模式保留草稿、反馈超限；通用操作文案三语。T-UI-08/14 |
| FileInput | FileList/File[]；未控制模式按名字去重累加，回调只发当前 incoming；删除构造 DataTransfer；无 maxFiles/name/error/ref 口 | A-UI-06：校验受控最终集合/实际 FormData，不能看内部列表推断发送集合；accept 是选择提示不是 MIME 安全校验；数量/公共参数可绑定，内容校验留服务器。T-UI-10/12 |
| ImageFieldRenderer | 除父表单外自行 GET `/sys/configs/file-storage/limits`，POST `/sys/files` multipart；文件限制失败用 10MiB/1GiB 默认 | A-UI-06：已核查这些遗留端点不在当前 OpenAPI，当前 Web 未用该 CRUD 层；保留既有路径与行为，不新建后端接口或暗换 Web 文件 API。组件绑定能力用合成契约验证，真实接入用显式 target；不将旧默认作为新规则第二来源。T-UI-10 |
| MapPicker / GoogleMapPicker | location wrapper 以 Number 构造地图值，回调坐标与地址 | 不新增地理业务规则；绑定对象子字段，地图 provider/瓦片/视觉不改。T-UI-10 |

## 5. AutoForm/CRUD 当前断点与兼容验收

1. `buildInitialValues`/`setValues` 用 `initialData[key] ?? field.value`，把显式 null 换成默认；远端/PATCH 模式需保留成员存在性。不是全局把所有表单改成 dirty-only：某操作原本完整 PUT 就维持完整提交，PATCH 是否省略由原构造器决定。T-UI-03。
2. `validateOne` 跳过隐藏字段并把 required 视为 trim 空/空数组；协议 required 是成员存在性。远端字段必须跳过被替代的检查，不把 schema.required 直接赋 field.required。现有未接入表单仍原行为。T-UI-03。
3. `validate` 的 Zod 路径 flatten 只取首消息，表单级和嵌套信息可能丢失；新 issue 状态不能靠 flatten 存储。原 Zod 若提供 UI 规则仍执行，但迁移后 API 规则不得双重保留。T-UI-01/04。
4. `validateField`/`validate` 异步完成直接 setErrors，无输入/locale/operation 版本核对；字段编辑清除旧错误后，旧 promise 仍可写回。新增字段和 submit 快照版本，跨字段依赖也重验。T-UI-02/04。
5. `getValues` 在验证后才剔除 title/show=false/submit=false；CRUD 的 toServerData 又变载荷。执行器必须消费发送快照。非法隐藏请求成员显示表单级，不强行把隐藏控件显示出来。T-UI-05。
6. `CrudForm.handleCommit` catch 只调用 callback，没有把错误交回 AutoForm；需注入 issues 但不重复 toast，不调用成功回调。自定义 FormFactory 是 opaque commit 函数，必须增加明确目标描述或标记未绑定，不能猜 method。T-UI-05。
7. `crudFactory` 创建 POST 可覆盖地址，编辑固定 PUT；`CrudForm` 判断 mode===add 才 create，copy 模式现有分支需真实测试（不能因展示 copy 就猜实际是 create）。该既有行为单列回归/核实，不借本需求未经确认改业务提交政策。绑定契约必须与实际所调用 commit 方法一致。T-UI-05。
8. AutoForm 已 noValidate，手写原生 form 迁移也统一 noValidate。custom 分支没有普通 FieldRow 的 blur 容器；多选/boolean/radio/tabs 不展示 error，日期组错误仅标在 span/div 而非交互 trigger；共享层必须统一渲染并防重复错误文案。T-UI-12。

## 6. 两客户端、第三条既有直接传输路径、路由与缓存矩阵

| 链路 | 现状 | 兼容要求 / 动作 |
|---|---|---|
| UI createApiClient | request(method:string) 已能发 OPTIONS；JSON.stringify 在 fetchOnce；所有请求加 JSON Content-Type，Bearer，credentials omit；getLocale/getHeaders 每次执行 | 扩展现有 request options 支持 signal/明确 locale/元数据静默错误策略与必要 cache 指示；不新增客户端。普通 OPTIONS 无 body，不手工发送 Access-Control-Request-Method。T-UI-11 |
| UI ApiError | code:number、status、field；丢弃 envelope data；onError 每次失败自动提示 | 保留旧构造调用兼容，追加 data/issues；旧 field 只作为 legacy fallback，不能从 message 猜 path。元数据失败用表单统一提示避免各输入 toast 风暴。T-UI-13 |
| UI 401 refresh | onUnauthorized 成功后重放一次所有 request；现有 auth test 冻结该行为 | 主方案 v3 已冻结保留既有 401 一次刷新重试，不新增校验/元数据驱动的写重放；元数据 retry 不重复提交。T-UI-11/13 |
| Web iam-api | requestEnvelopeWithBase 通过 organizationRouteURL 最终拼地址；使用 accountViewRequest、session/view revision；Accept-Language 来自 document.lang；init 支持 signal | 元数据必须复用同映射和身份，显式 locale 覆盖解决 provider commit 与 document effect 时序；cache key 按最终有效 URL/身份，而非仅模板。T-UI-11/14 |
| Web ApiError | status/code:string/message/data/businessCode；apiError code 可能退到 localized message | 从新增 validation_errors 结构取稳定规则 code；不把旧 ApiError.code 当规则 code。保留 captchaInvalid 101001、fileOperationConflict 102003 等 businessCode/data 读取及 conflict guards。T-UI-13 |
| Web mutation 广播 | shouldNotifyMenuCatalogChange 排除 GET/HEAD，目前 OPTIONS 会被当 mutation | 排除 OPTIONS，写成功通知维持原状。T-UI-11 |
| account-view | `account-view.ts` 已允许 GET/HEAD/OPTIONS，使用视图 token/credentials omit，并有 revision 保护 | 复用已支持分支，不新增以目标 POST 判断权限的请求；元数据失败不能绕过关闭视图检查。T-UI-11 |
| account-view 直接 POST/DELETE | `account-view.ts:146/161` 直接 fetch，cookies include、Origin；POST 非 2xx 丢弃 body 并抛通用 Error，dialog catch 不接 cause；不经过 iam-api | 第三条既有 transport 接同一 normalizer/metadata adapter 和 Accept-Language，保留 cookie/Origin 与原身份边界，不创建第三客户端。dialog 保留 cause 并绑定字段/全局错误。T-UI-11/13/14 |
| api-route | Method 只有 get/post/put/patch/delete；模板替换一次编码；文件 path 特殊保留斜杠；query undefined 省略，0/false 保留 | OPTIONS 类型+规范生成；具体嵌套路径、编码后的 ID、文件路径、不同方法同路径均测试；禁把用户值拼成新模板。T-UI-11 |
| list/query | UI buildListQuery 支持 simple/RSQL；Web apiRoute 将 query 值 String 后 URLSearchParams | 不将列表 capabilities 当写规则；用于 API 查询表单时，按真正 style/explode/序列化校验，不能自行改过滤方言。T-UI-01/11 |

账号视图特殊生命周期：`openAccountViewTab` 保持首个 await 前同步 `window.open`，dialog 打开时预取元数据；不得在点击处理到开窗之间插入 metadata/异步校验等待而触发 popup blocker。异步检查留在既有开窗/握手流程内，失败保持 popup close/已发行视图 revoke 回滚，不重放发行。`exitAccountView` 先清本地状态，撤销请求 best-effort 且抑制失败；不增加阻止退出的 OPTIONS 等待、必填检查或强制显示已抑制撤销错误。退出 UI 只呈现原本实际向上传播的错误。

元数据缓存只有应用内表单生命周期，不使用 OPTIONS HTTP 缓存/304/CORS Max-Age。键为 **有效 base URL + 具体路径 + 目标 method + content type + locale + session/workspace/account-view generation**；schema revision 是版本识别，不含请求时间，也不锁业务写请求版本。Form open 时读取；同周期同键 in-flight 合并；close 释放使用者与缓存；重新打开重取。多表单共享 promise 必须引用计数，关闭一表单不能 abort 另一个的请求；身份/语言改变则整体丢弃旧代次。成功缓存、失败状态不跨新打开周期复用；不是增加长期 TTL 或后台轮询。

降级状态机（A-UI-02/07，T-UI-02/11）：

| 结果 | 表单行为 | 不能发生 |
|---|---|---|
| loading | 保留草稿；提交策略沿设计“服务端最终校验”并显示部分本地不可用状态 | 永久禁用退出/清空字段 |
| 合法当前契约 | 执行可证明规则；invalid 阻止相应请求；valid 正常提交 | 用旧 UI rules 再维护同一范围 |
| 404/405/部署关闭/网络超时/5xx | 当前语言明确提示本地校验不可用；按原客户端业务请求提交，由服务端决定 | 自动换 URL/绕过鉴权/重放写请求 |
| 401/403 或视图已关闭 | 先保持原客户端身份/权限处理；本地元数据不可用不授予权限 | 把“可降级”解释为吞掉鉴权或允许已关闭视图操作 |
| 未知协议版本/缺目标 method/$ref 不完整/未知规则 | unknown，保留可独立证明的 invalid；不能报告本地全 PASS；记录不含用户值的支持缺口 | anyOf/oneOf/not 中从未知分支抽错误误拦截 |
| response 晚于关闭/切语言/切工作区/新输入 | 丢弃旧结果；输入和当前 issues 不被旧代次覆盖 | 恢复旧语种/旧用户字段错误 |

## 7. 三语、动态消息与旧错误兼容

现有 UI `i18n/config.ts` 内置 en-US/zh-CN/ms-MY，默认 en-US；IntlAppProvider 通过 loadMessages 动态加载，切语言 replaceState 而非 remount，可保留表单。Web `lib/i18n.ts` 加载 shared + iam + files，规则消息不可塞进静态页面翻译形成第二份字段规则。IntlProvider 默认 timeZone=UTC 不代表 DatePicker/new Date/DOM datetime-local 自动使用 UTC。

实测消息盘点：三个 UI locale 的 `form.validation` 均 **8 个键**；`autoFormRuleMessageKeys` 声明 `percent`，三语目录均缺该键。现有很多验证消息仅“Invalid input/输入无效/Input tidak sah”，静态 createAutoFormRules 默认英文。远端 API 规则迁移应由 metadata 三语参数模板替代这些 preset；纯 UI 仍引用的 preset 翻译必须补齐，不可直接删全局兼容规则。消息键一致不等于具体错误具有可读原因。

| 场景 | 精确设计/缺口闭环 | 验收 |
|---|---|---|
| 本地 issue | 存 code/message_key/params/path，不存不可重译的唯一 string；调用 data.messages 或节点 x-field-messages（仅 `{param}` 文本替换）渲染，再兼容 AutoForm string 展示 | T-UI-14 |
| 服务端字段 issue | 两客户端及账号视图直接 transport 共用 normalizer 读取 data.validation_errors，不覆盖业务 data；in/path 严格核验，未知路径/location 原样安全保存并显示全局；不 split('.') | T-UI-04/13 |
| 切语言已有 errors | 新 locale 元数据刷新，当前输入重验；保留服务端 issue，再用新词典重渲染；依赖的通用 fallback 来自前端三语；不重放业务请求求翻译 | T-UI-14 |
| 业务 message_key 未出现在规则词典 | 主方案 v3 已冻结 `operations[method].issue_messages[message_key] = {code, template}`；业务 issue 精确按 key 取当前语言模板，标准规则查节点 x-field-messages → data.messages[ruleId]；同用模块既有错误映射/i18n。缺失模板阻断已接入操作验收，不从旧 message 猜翻译 | D-UI-01 / T-UI-14 |
| 旧服务端/旧客户端 | 新客户端无 validation_errors 时保留旧 message 初始展示；切语种无法重译的 legacy 字符串展示当前语言通用表单错误并保留可安全的 code/status 信息；已接入路径缺 key 必须 CI 阻断，而非把 fallback 当覆盖 | T-UI-13/14 |
| 快速 en→zh→ms | IntlAppProvider 当前没有加载 generation 检查，先请求可能后完成覆盖后选择；绑定层 generation 不足以修复 provider 最终 locale 错误，需同作用域竞态修复 | A-UI-09 / T-UI-14 |
| 时间/数字显示 | useLocale/Intl 只处理显示；不把本地化小数分隔符、日期顺序、AM/PM 字符串当 API wire；DatePicker localeCode 与显示 locale 差异须统一 | T-UI-07/08/14 |

消息参数仅允许安全 schema 参数，不能回传 rejected value（密码/token/上传内容）；动态文案 React 文本渲染，不能 dangerouslySetInnerHTML。模板未知参数显示当前语言 generic invalid 并记录协议缺口，不能显示原 key/未替换花括号或把已证明 invalid 变 valid。

## 8. 主方案 v3 已采纳的决定（D-UI-01–06）

下列调查发现已由主方案 v3 §12.2 全部收敛，状态均为“已采纳，实施未开始”。负责人仍为对应后端模块与前端共享层；不能把设计决定已采纳混同为运行验收通过。

| ID | 原调查依据 | v3 已采纳决定 | 对应验收 |
|---|---|---|---|
| D-UI-01 | 业务 issue message_key 不一定是标准 ruleId；早期设计仅 messages[ruleId] | `operations[method].issue_messages[message_key] = {code, template}`；标准规则查节点 x-field-messages → data.messages[ruleId]，业务 issue 精确 key；复用模块映射/i18n，不复制前端规则翻译 | T-UI-14 |
| D-UI-02 | 单一 scalar field 可转换为多个 API 字段；factory opaque，custom 无通用 focus/blur | 采用最小 `{target, buildRequest, bindings}` contract；bindings 明确多路径和 focus handle。元数据 transport 由现有客户端注入；不要反射 commit 函数或 DOM 推断载荷 | T-UI-03/05/12 |
| D-UI-03 | price 现存最小单位整数；DateTimePicker 墙上时间；TimePicker 有 clamp；location ID Number | 按真实业务调用冻结每种转换前后样例；金额草稿精确解析但保留既有 wire，时间只有接口已知时区才转换。没有调用的组件用合成契约测试能力，不虚构业务接口/政策 | T-UI-07/08/10 |
| D-UI-04 | 原设计“无自动重放”与 UI 客户端已有 401 once retry | 不新增校验/元数据驱动的写重放，保留既有 401 一次刷新重试；退出仍 best-effort，不新增阻断等待 | T-UI-11/13 |
| D-UI-05 | 同步 loading 时能否 submit、非法 schema 如何反馈尚无 hook API | 沿已推荐 server-final 策略：loading/unavailable/unknown 允许原提交并明确提示；invalid 才阻止；401/403 原鉴权优先。禁止另造等待超时后偷偷变策略 | T-UI-02/11 |
| D-UI-06 | 同字段服务端/本地多个 issue、数组重排/字段删除和焦点优先级 | 保留 issue 数组与来源/请求快照；优先当前可证明本地错误，再当前服务端错误，余项可展开/全局；按注册 DOM 顺序聚焦第一个可操作字段，未知位置全局提示聚焦摘要。字段修改清该字段旧 server issue，依赖错误重验 | T-UI-04/12 |

以上均已明确采用，不再是待选菜单；实现必须遵守主方案 v3 的字段来源、既有 wire/时区与身份/重试边界。新功能仍需逐项测试后才能验收。

## 9. 测试与验收任务（新增功能全部 NOT RUN）

| ID | 必须执行的证明；建议落点 |
|---|---|
| T-UI-01 | request-validation 单测 + Go/TS 共享样例：missing/null/empty/0/false、Unicode code points/bytes、现有全部 regex、format、safe integer、decimal、递归引用、嵌套/数组、组合 unknown、readOnly/writeOnly/additionalProperties；真实 Huma 请求结果对照 |
| T-UI-02 | form lifecycle 集成：每周期去重/关闭释放/重开重取、多个 consumer 取消、locale/session/workspace/view 切换、离线/慢响应/未知版本、后端规则更新不重建前端 |
| T-UI-03 | AutoForm add/edit/copy/detail、初始 null、submit=false/show=false、动态字段、纯 UI 规则保留、PATCH 省略不注入默认、untouched/touched/dirty 的值保存 |
| T-UI-04 | data.validation_errors→字段/表单：body/query/path/header/cookie、`/a~1b/~0key/0`、点号属性、数字键、嵌套数组、未知 location、多 issue；编辑/切换后旧 promise 不回填；错误不得含值 |
| T-UI-05 | CRUD toServerData spy=一次；校验快照与实际 HTTP bytes 一致；POST createApiPath/PUT edit/copy 实际分支；失败保留值不 success/callback 重复；自定义 factory 明确目标 |
| T-UI-06 | DOM/浏览器输入：IME composition、emoji/组合字符、粘贴、撤销、选区替换、移动键盘、blur/submit；无原生 maxlength 抢先阻拦；密码类型及不泄露 |
| T-UI-07 | number/price/relation/location：`''`、`-`、`1.`、超精度、边界两侧、指数、负数、9007199254740993、0/null、decimal 精确比较、非有限数 JSON null；禁止舍入/clamp 后假通过 |
| T-UI-08 | 日期/时间：2024-02-29/2023-02-29/2024-02-30、年 0099、日期上下界、offset/RFC3339、DST 重复/不存在时间（接口需要时）、保留秒、range 子路径、跨午夜，非法输入不滚动修正 |
| T-UI-09 | enum typed mapping、空候选/旧选项、remote 依赖慢响应、array maxItems/uniqueItems 与删除、boolean false/null、tabs mount 不误写 PATCH、relation 增删重排 |
| T-UI-10 | 富文本 HTML/空编辑器、CodeMirror JSON 字符串、custom 覆写、文件选择数量/类型/上传失败/父引用、multipart 实际边界与 DataTransfer、location 地图/手输类型；不重放上传 |
| T-UI-11 | 两客户端及账号视图直接 transport OPTIONS 真请求：正确有效 URL/路径编码/Accept-Language/鉴权、无 mutation 通知、account-view 只读可取；404/405/401/403/503/空响应/非法 schema、真正 CORS 预检和普通 OPTIONS 分离；普通写请求行为回归；account-view.test.ts 加强同步开窗/握手失败回滚/退出无 OPTIONS 等待，并做浏览器 popup smoke |
| T-UI-12 | 每个 registry 分支和 custom 的 error可见、aria-invalid/describedby/required、触发器 focus、无重复错误、portal blur、禁用/只读；未绑定 Input/搜索 UI 无业务元数据请求 |
| T-UI-13 | 两 ApiError legacy constructor/message/field/data/businessCode 保持；新 issues union 不覆盖文件 conflict/captcha data；旧服务端结构、未知字段、JSON 失败、401 refresh 原行为；账号视图 POST 保留 body/cause，退出抑制撤销失败不变 |
| T-UI-14 | 三语言每种标准/扩展/业务 issue 模板实际插参；快速切语言逆序响应、document.lang 时序；不 remount 草稿、不重放写请求、不残留 key/旧语种；缺翻译 CI 失败 |
| T-UI-15 | 脚本校验 FieldType=registry=矩阵 31 项，替换 registry/custom 不绕开绑定；纯 title/detail 明确排除；现有业务 API 限制源扫描零重复，新增声明需对应执行/消息样例 |

必须补 DOM/浏览器交互测试；现有 `auto-form/validation.test.ts` 只验证 preset 函数，不能证明 useAutoForm、控件、外部错误或 IME。实施完整门禁从 `apps/frontend-web` 执行 `bun run typecheck`、`bun run lint`、`bun run test`、`bun run build`、`bun run check:api-types`，并执行手写表单/AutoForm/CRUD 实际失败成功闭环；不能用纯规则单测替代。

## 10. Tauri 与不改范围

`apps/frontend-app/src-tauri/tauri.conf.json`：dev 使用 Web Vite 127.0.0.1:1420；build 执行 frontend-web build，frontendDist 指向同一个 `apps/web/dist`。因此共享组件/客户端改动会自动进入桌面壳，没有第二套表单实现。无需改 Rust、Tauri capabilities、窗口布局、图标或 runner。

Web Vite `/api` proxy 去前缀并转发到 `VITE_API_PROXY_TARGET`，不是 CORS 元数据协议。开发桌面验证普通 OPTIONS 可走相同 proxy；打包桌面无 Vite 开发代理，须使用已有部署 API base 配置验证真实 origin/credentials/CORS，不借本需求配置新权限或放宽 CSP。打包环境的离线 fallback、系统语言与 Date/Intl 差异、文件 DataTransfer 兼容性属于 smoke 证据，Tauri 不改代码不等于完全不受影响。本轮未运行桌面/浏览器，不能宣称其通过。

补充范围排除证据：当前 OpenAPI 无 `/sys/files`、`/sys/configs/file-storage/limits`、`/regions`、`/regions/{id}`；全仓也未找到 `/common/upload`。Web 当前无 AutoForm/CrudForm/ApiProvider/createApiClient 调用，因此保留遗留组件对外行为，以合成契约证明共享绑定能力，不为验收新增这些后端业务接口。`_leaflet-map.tsx` 的 Nominatim 搜索/逆地理编码属第三方服务，排除平台 OPTIONS 迁移；`use-auto-table.ts` 的 `fetch(query)` 是调用者注入回调，不是额外原生 HTTP transport。CommonTable 的 GET 走现有 ApiClient，opaque FormFactory 通过明确 descriptor 接入，不能猜函数中的请求。

不改数据库、后端业务路由、鉴权政策、视觉布局，不新增金额业务接口、不整站视觉审计、不批量替换所有表单为 AutoForm。发现 password renderer 默认类型、copy 提交分支、文件辅助端点等现有行为只在本任务绑定/兼容范围核实，未擅自修复无关问题。

## 11. 本轮实际验证与状态

- 源码脚本列举 FieldType：31 项；本矩阵逐项覆盖 31 项，无未登记类型。
- 三语 UI `form.validation`：各 8 个键；percent 键三语皆缺，已登记 A-UI-09，不假称现有三语规则完整。
- 实际执行（目录 `apps/frontend-web`）：`bun test packages/ui/src/components/auto-form/validation.test.ts packages/ui/src/lib/api-client.test.ts packages/ui/src/lib/api-client.auth.test.ts apps/web/src/lib/iam-api.test.ts apps/web/src/lib/api-route.test.ts apps/web/src/lib/i18n.test.ts`：**105 pass / 0 fail，24997 expect calls，6 files**。这只证明既有基线，不证明新功能。
- 文档矩阵与源码 FieldType 集合一致性脚本、UTF-8/LF/行尾空白检查：PASS。
- 新功能 T-UI-01 至 T-UI-15：NOT RUN（生产代码未实施）。本轮仅新增本文；不运行无关 build/全仓 CI，不更改已有工作树内容。
- 自审：scope/reuse/architecture/complexity/来源可核查 PASS（设计交付）；最终实施验收仍 NOT READY，D-UI-01 至 D-UI-06 已由主方案 v3 采纳，仍须完成 A-UI-01 至 A-UI-10 与全部 T-UI。
- 下一步：实施方按主方案 v3 已冻结的消息资源、绑定 API、转换与重试边界执行 A-UI-01 至 A-UI-10；本子任务文档调查及决定同步无剩余编辑项。
