# 复合控件（商城资料参考）

> 来源：[design/molecules.md](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/design/molecules.md)；搬入日期：2026-10-14。
> 筛选：保留输入、容器、覆盖层、反馈、导航与通用选择器；从 MCH 首次品牌归属章节起的业务组件不导入。
> 本文是参考资料，不是本项目已采用的规则或已实现能力。尺寸、主题、技术栈、字段限制以本项目确认方案为准。
> 原文 SHA-256：`416d46cc4059125e7f80dab87bdd8a628745f17b608738b2f917de44a71e432e`。内嵌 Figma 转为链接；已搬入的资料互链改为本地相对路径，未搬入资料链接回源仓。

分子由多个[原子](atoms.md)实例组合而成,承担一段完整交互(一个卡片、一个对话框、一次翻页),仍不含业务数据。尺寸以 modern 为基线，圆角和色彩绑定[设计令牌](tokens.md)。

## 输入族

输入族不是一批近似 Input 的平铺。职责固定为 `Input Atom -> FormField / InputGroup / FileInput Molecule`;页面和 AutoForm 只能实例化这些资产,不得重新拼边框、Label、错误文案或前后缀。

### 表单字段 FormField

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=205-4&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`FormField / Input`
  - 依赖 Label Atom 与 Input Atom
  - 结构为 `Field -> Label row -> Control -> Meta row`,纵向 `gap 6px`
  - 受限输入组合 `Validation Rules Panel`,结构为 `Field anchor -> non-interactive Rule Popover -> Rule row[]`;规则面板复用 Popover Surface、lucide Check/X 与语义 Variables,不成为 Select/Menu/Listbox
- **尺寸与布局**
  - Label 为 14/20 Medium
  - 必填星号在 Label 左侧,12px `destructive`
  - Input 默认高 36px、宽随父容器
  - Hint/Error/Count 为 12/16,控件下方间距 4px
- **变体与属性**
  - `Mode` 只保留 6 个语义组合:`basic / required-hint / error / max-length / counter / mixed`
  - `mixed` 同时展示 required、Error Input、错误文案和超限计数,用于验证复杂组合,不再生成笛卡尔积
- **状态与反馈**
  - Focus/Hover/Disabled 视觉继承 Input Atom
  - Error 由 `aria-invalid` 和 AutoForm error class 触发
  - Readonly 有值但不可编辑,不额外制造视觉变体
  - 规则行状态为 `neutral / pass / fail`;字段为空时全部中性,输入时只实时校验本地确定性规则
  - 规则类型为 `required(all-of) / forbidden(none-of) / threshold(K-of-N) / recommended(non-blocking)`;通过/失败同时使用图标、文字与语义色
  - 禁止类规则必须使用用户可直接理解的具体文案,不得单独写“控制字符”等实现术语。密码统一为`不含空格、换行、制表符、全角字符或 Emoji`;名称类统一为`不含换行、制表符等不可见字符`
  - 唯一性/可用性、账号存在性、权限、库存等接口结果不进入规则面板,只显示为字段级服务端反馈;登录密码不展示设置密码规则
- **交互与无障碍**
  - `label[for]` 或 `aria-labelledby` 必须关联控件
  - Hint/Error/Count 通过 `aria-describedby` 关联,错误节点有稳定 `id`
  - Rule Popover 在字段 Focus/Input 时打开,Blur 后关闭;不抢焦点、不进入 Tab 顺序,状态变化使用 `aria-live="polite"` 汇总宣告
  - 所有密码、新密码、确认密码 InputGroup 都必须带独立的 eye IconButton,默认密文,允许切换明文后再切回;按钮留在输入框内部且有可访问名称
  - 常规面板与字段同宽、offset 4px、默认置下且空间不足时翻转置上;动态行/表格单元锚点窄于 320px 时使用 320px 最小面板并在 viewport 内左右收口,作为 overlay 不推动页面布局
  - 每个受限输入都必须登记操作意图、规则与反馈位置;仅注册/创建/编辑/设置/重置/修改/精确确认等写操作中的多条本地规则、特殊字符集、K-of-N、范围/精度或精确匹配使用 Rule Popover。登录/搜索/筛选/只读查看无浮层;单一必填/长度走 Hint/Count,闭集走 Select/Picker,接口结果走字段级状态
  - 登录/搜索/筛选/只读查看等非写操作**除无浮层外,任何常驻载体都不得展示位数、字符集、大小写、正则或格式限制**——包括 placeholder、Hint/描述、字数计数、卡片副标题与掩码区说明;只保留字段标签、示例型 placeholder 与提交失败后的字段主错误
  - PRD 未定义具体阈值/格式时登记合同缺口,不得为了填满规则面板臆造上下限;通用密码/邮箱状态板不能替代 MCH/SYS/OPS 等正式业务 Screen 的逐屏覆盖审计
  - 正式业务覆盖为 SYS 4、OPS 2、MCH HQ 21、MCH Outlet 10 个 Rule Popover 状态；每个状态紧邻对应默认 Screen，登录/查询类 Screen 保持无浮层
  - Placeholder 直接说明期望值、格式或可辨识示例,禁止以`请输入/请选择/搜索`开头或只重复 Label;它不承载规则列表、服务端可用性或错误结果
  - Do:一条字段只给一个主错误
  - Don't:用 placeholder 当 Label、只改边框不输出错误文字、在页面散放计数、把接口可用性混进本地规则列表
- **适用场景**
  - AutoForm 字段需要统一标签、提示、错误与计数结构,`FieldRow` 间距为 `gap-1.5`
  - 适用场景:各端结构化表单

##### Mode 矩阵

| Mode | 必须出现 | 文案规则 |
|---|---|---|
| `basic` | Label + Input | placeholder 不替代 Label |
| `required-hint` | `*` + Label + Input + Hint | Hint 说明格式或允许范围 |
| `error` | Label + Error Input + Error message | 错误信息必须指出修正方式 |
| `max-length` | Label + Input + 长度说明 | 例:`Maximum 40 characters` |
| `counter` | Label + Input + 右对齐计数 | 固定格式 `current/max`,使用 tabular nums |
| `mixed` | required + Error + message + count | 超限时 message 与 count 同取 `destructive` |

### 实时规则浮层 Rule Popover

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=3974-2&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`，正式 Section 为 `3974:2`
  - 由 Popover Surface、规则行、lucide Check/X/Minus 与语义 Variables 组成；业务 Screen 只使用正式组件实例
- **组件族**
  - Password 与 Email 使用 ComponentSet 覆盖 `neutral / pass / fail` 组合状态
  - Confirm Password、Merchant/Outlet/Operator/Category/Zone/Spec Name、Employee Number、PIN、SKU、Price、Sort、Badge Color、Table Number 为按字段合同定义的组件
- **交互边界**
  - 仅写操作中的本地确定性多规则字段显示；登录、查询、筛选、只读与找回邮箱发码无浮层
  - 唯一性、可用性、权限、库存和账号存在性不进入规则列表
  - 浮层不抢焦点、不推动布局；字段失焦关闭，下方空间不足时翻转置上
- **文案与状态**
  - 空值中性，输入后逐条实时更新；通过/失败同时使用图标、文字与语义色
  - 密码禁止项明确列为“空格、换行、制表符、全角字符或 Emoji”，不得显示未解释的“控制字符”
  - 规则详情与密码 3/4 复杂度见[统一数据校验](validation-feedback.md)

### 输入组合 InputGroup

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=207-54&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`InputGroup / Inline Addons`
  - 每个模式必须含一个 Input Atom 实例
  - 文本 addon 为 12/16 `muted-foreground`
  - 图标为 16px lucide Icon Atom
- **变体与属性**
  - `Mode=prefix-text / suffix-text / leading-icon / trailing-icon / dual-text / dual-icon-action`
  - 属性:`Value / Prefix / Suffix / Leading Icon / Trailing Icon`
  - 图标使用 INSTANCE_SWAP,禁止为每个图标新增 variant
- **尺寸与布局**
  - Input 高 36px、宽 320px 标本
  - prefix/suffix 位于左右 12px并垂直居中
  - 文本 addon 对应 `pl-10/pr-10`=40px
  - 单图标对应至少 36px 内容避让区
  - 前后同时存在时两侧分别让位,不得覆盖 Value
- **交互与无障碍**
  - trailing action 例如密码显隐必须可点击、可聚焦并有 `aria-label`
  - 纯装饰 icon 使用 `aria-hidden`
  - Focus/Error/Disabled 仍由内部 Input Atom 控制,addon 同步 muted/destructive 或 50% opacity
- **使用边界**
  - Do:金额符号、单位、邮箱/搜索/锁图标放在 InputGroup
  - Don't:把业务按钮塞进 Input Atom、用绝对定位散落到页面、让 addon 遮挡输入文本
- **适用场景**
  - prefix/suffix 采用绝对定位 `text-xs`,Input 用 `pl-10/pr-10` 避让
  - 适用场景:金额、单位、搜索与带动作输入

### 文件输入 FileInput

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=215-28&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`FileInput` + 内部构件 `.FileInput / FileRow`
  - 触发器必须实例化 `Button / Leading Icon` 的 `outline + sm` 和 Upload Icon
  - 文件行实例化 File/X Icon,不得重画
- **变体与属性**
  - `Mode=empty / single-selected / multiple-count / file-list / disabled`,只表达真实视觉差异
  - `accept` 仅约束文件类型,不制造视觉 variant
  - 错误信息由 FormField 组合
- **触发行**
  - `display:flex; align-items:center; gap:8px`
  - Button 高 28px
  - 非列表模式右侧为 14/20 单行截断文本:无文件取 `muted-foreground`,单文件显示文件名,多文件显示 `${count} files`
- **文件列表**
  - `showFileList=true` 时隐藏摘要,按钮下方 `gap 8px` 接文件列表
  - 列表纵向 `gap 4px`
  - FileRow 高 32px、左右 8px、上下 6px、内部 `gap 8px`,`radius/md` + 1px `border`
  - 文件名 `flex:1` 并尾部省略
  - X 为可访问删除操作
- **属性与行为**
  - `value / onChange / accept / multiple / disabled / buttonLabel / placeholder / showFileList`
  - 同名文件去重
  - 允许再次选择同一文件
  - 删除最后一个文件回调 `null`
  - Disabled 禁止打开选择器和删除并整体 50% opacity
- **交互与无障碍**
  - 隐藏原生 `input[type=file]` 负责系统选择器和键盘语义
  - 触发 Button 必须有可访问名称
  - 删除按钮为 `Remove <filename>`
  - 文件限制和错误通过 FormField 文案表达
- **使用边界**
  - Do:单文件摘要、多个文件计数或可删列表三选一
  - Don't:把 DropZone、缩略图或 maxFiles 臆造成 FileInput 内置能力
  - 这些是独立组件或业务约束
- **适用场景**
  - FileInput 只负责选择与文件摘要,DropZone 是独立能力而非 FileInput API
  - 适用场景:单文件、多文件计数与可删除文件列表

### 搜索字段 SearchField

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1470-4995&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`1470:4995`,实例预览板=`1470:4998`,资产:`SearchField / Control`=`1470:5209`,隐藏资产框=`1470:4999`
  - 依赖 InputGroup / Input / Spinner / Search / X
  - 解剖为 `SearchField -> Search icon + Input value|placeholder + optional Clear|Spinner`
  - 所有视觉必须从下层组件实例派生,不得重新画输入框
- **变体与属性**
  - `Size=compact|default`,`State=empty|filled|focus|disabled|error|loading`
  - `compact` 为 32px 高,用于列表工具条
  - `default` 为 36px 高,用于表单和独立搜索
  - 宽度由父容器决定,设计标本使用 320px
- **尺寸与布局**
  - Search 16px,左侧 12px,输入文字从 36px 开始
  - 右侧动作占 32/36px稳定命中区
  - Filled 显示 X
  - Loading 以 Spinner 替换 X
  - 动作变化不得推动文字或改变整体宽度
- **状态与反馈**
  - Hover 沿用 Input Default
  - Focus/Error/Disabled 继承 Input Atom
  - 清除后保留焦点并回到 Empty
  - Loading 保留 query 且禁止重复提交
  - 防抖、请求取消和搜索触发时机是行为属性,不创建视觉 variant
- **交互与无障碍**
  - 使用 `type=search` 或等价 `searchbox`
  - Search 图标 `aria-hidden`
  - Clear 的名称包含当前查询语义
  - Loading 通过 live region 宣告
  - Placeholder 不替代 FormField Label
  - Placeholder 必须列出可搜索对象,例如`订单号或桌号`、`门店名称或编号`;禁止只写`搜索`或`搜索…`
- **组合边界**
  - 只负责单一 query 输入与清除/加载反馈
  - 筛选字段集合属于 FilterBar Module,全局命令和多结果导航属于 CommandPalette
  - Do:支持 Escape 清空、IME 输入和防抖
  - Don't:把 Select、日期条件或查询按钮塞入 SearchField
- **适用场景**
  - 列表日志、门店权限、图标选择和菜单搜索均使用左侧 Search 图标 + Input
  - 适用于 AutoTable 关键词过滤、POS/KDS 搜索和 Combobox 内部查询

### 可搜索选择 Combobox

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1578-5200&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`1578:5200`,实例预览板=`1578:5203`,资产:`Combobox / Panel`=`1578:5341`,`Combobox / Control`=`1578:5484`,隐藏资产框=`1578:5204`
  - 依赖 Select Trigger / Popover / SearchField / Select Item / ScrollArea / Spinner Atom
  - 解剖为 `Control -> Trigger(value|placeholder + chevron) -> Popover(SearchField + Listbox(Option[]|Empty|Loading|Error))`
  - Option 必须复用 Select Item,不得复制选中标记和 Hover 视觉
- **变体与属性**
  - `State=empty|filled|open|disabled|error|loading`
  - Panel `Mode=results|empty|loading|error`
  - 选项、query、value、最大高度和过滤策略是数据/行为属性
  - 单选 Combobox 与 MultiSelect 分离,不在同一组件中切换值模型
- **尺寸与布局**
  - Trigger 高 36px、宽 fill
  - Panel 与 Trigger 左对齐,默认宽 320px,复杂时区列表可扩至 448px
  - padding 8px
  - SearchField 高 36px,下间距 8px
  - 结果区 max-height 256px并滚动,Option 最小高 36px
- **状态与反馈**
  - Open 保持 Trigger focus/active
  - Loading 保留旧结果并显示 Spinner
  - Empty 显示当前 query 无结果
  - Error 提供 Retry 且不能伪装成 Empty
  - 选择后更新 Trigger、关闭 Popover并清空临时 query
  - Disabled 不打开
- **交互与无障碍**
  - Trigger 使用 `combobox`,`aria-expanded`,`aria-controls`,`aria-activedescendant`
  - 结果使用具名 `listbox/option`
  - 上下键移动,Enter 选择,Esc 关闭并归还焦点
  - 异步状态通过 polite live region 宣告
- **组合边界**
  - 用于有限或本地可搜索的单选枚举
  - 远程请求使用 RemoteSelect,多选使用 MultiSelect,层级数据使用 TreeSelect
  - Do:稳定 Option key、显示可读 label并保留选中 Check
  - Don't:用普通 Select 假装搜索、把网络错误显示成无结果、只允许鼠标选择
- **适用场景**
  - 门店时区、主体切换和图标选择需要可搜索 Popover
  - 适用于 AutoForm combobox 字段和中等规模枚举

### 数字步进 NumberInput

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1630-5519&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`1630:5519`,实例预览板=`1630:5522`,资产:`NumberInput / Control`=`1630:5792`,隐藏资产框=`1630:5523`
  - 依赖 Input 与 Button Icon-only 的 Minus/Plus Atom
  - 解剖为 `Group -> Decrement IconButton + Numeric Input + Increment IconButton`
  - 三段共享一个视觉边界,但保留三个独立键盘命中区
- **变体与属性**
  - `Size=compact|default`,`State=default|focus|min|max|disabled|error`
  - `compact` 高 32px用于行内数量、最小宽 128px
  - `default` 高 36px用于表单、最小宽 144px
  - 值、`min/max/step/precision` 与可选单位是属性,不按数字创建 variant
  - `quantity preset` 复用 compact Control,固定整数、step=1、tabular nums,不新增 QuantityStepper 主组件
- **尺寸与布局**
  - 两侧按钮为 32/36px正方形,中间输入至少 64px并居中使用 tabular nums
  - 共享边框处消除重复描边,整体 `radius/md`
  - 按钮 Hover/Active/Focus 沿用 Button,字段 Focus/Error 沿用 Input
- **行为规则**
  - Plus/Minus 按 step 更新并规范化 precision
  - 到 min/max 时只禁用对应按钮
  - ArrowUp/ArrowDown 与按钮等价,Home/End 可选到边界
  - 空值和非法输入在 blur/submit 时由 FormField 报错,不得静默改成 0
  - quantity preset 的推荐范围为 1-99;到 1 时 Minus 禁用或由上层购物车行显式转换为删除动作
- **交互与无障碍**
  - 中间为 `spinbutton`,声明 `aria-valuemin/max/now`
  - 两侧按钮名称包含动作和字段标签
  - Disabled 禁用三段
  - Error 同时显示边框/ring与 FormField 错误文字
- **组合边界**
  - 用于数量、份数、上下限和可控数值
  - 金额前后缀由 InputGroup 组合,时间段继续使用 TimeSegment
  - 购物车乐观更新、请求冲突、金额重算和“减到零删除”属于购物车行或页面,不进入 NumberInput
  - Don't:保留浏览器原生 spinner再叠加两侧按钮
- **适用场景**
  - 商品选项价格、最小/最大选择数、套餐数量及 SMTP 端口均需要数值约束
  - AutoForm number 字段统一复用本组件

### 筛选标签 FilterChip

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1697-5713&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`1697:5713`,实例预览板=`1697:5716`,资产:`FilterChip / Control`=`1697:5878`,隐藏资产框=`1697:5717`
  - 依赖 Toggle / Badge / Button Icon-only / Separator / X Atom
  - 解剖为两种可复用模式:`selection = Toggle(label)`
  - `applied = Value region(label + value) + independent Remove IconButton`
  - 选择和移除必须是不同命中区,不能一次点击同时切换与删除
- **变体与属性**
  - `Kind=selection|applied`,`State=default|hover|active|focus|disabled|error`
  - 属性:`Label`,`Value`,`Removable`,`Tone`
  - 选项数量是数据重复,不得为每个筛选字段创建组件变体
- **尺寸与布局**
  - 高 28px、`radius/full`,水平 10px
  - applied 的 Value region 与 28px Remove 区之间用 1px分隔,文字区最大宽 220px并省略
  - 多个 Chip 横向 `gap 8px`,窄屏换行而非缩小点击目标
- **状态与反馈**
  - Active 表示当前选中或已应用
  - Focus 对当前命中区显示 ring
  - Disabled 禁止切换和移除
  - Error 只用于无效/冲突条件并必须有邻近文字说明
  - Remove 后焦点移动到相邻 Chip或“添加筛选”触发器
- **交互与无障碍**
  - selection 使用 `aria-pressed`
  - applied 读作“字段:值”,Remove 名称为“移除 字段:值”
  - 颜色不是唯一状态信号
  - 可删除 X 不得 `aria-hidden`
- **组合边界**
  - 只表达单个可见筛选条件
  - 字段编辑 Popover、查询提交与草稿状态属于 FilterBar Module
  - Do:用于活动筛选摘要、快速枚举切换
  - Don't:把完整表单塞进 Chip、用 Badge 冒充可点击控件、让长值无限撑宽
- **适用场景**
  - AutoTable 需要在复杂筛选后显示可扫描、可单独清除的条件
  - 订单/状态等快速筛选需要选择型 Chip

### 富文本编辑 RichTextEditor

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2327-7202&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`RichTextEditor / Control`
  - 工具栏按钮实例化 Toggle(ghost/sm)与 Icon(加粗/斜体/下划线/左中右对齐),分隔实例化竖向 Separator
- **变量绑定**
  - 容器边线 `input`、背景 `background`、圆角 md、描边宽 1;工具栏底边线 `input`
  - 占位文字 `muted-foreground`,正文 `foreground`,14/20
- **尺寸与布局**
  - 工具栏内边距 8px、按钮间距 4px;内容区内边距 12px、最小高 120px;宽随父容器
- **变体与属性**
  - `State=default|disabled`;`Value` TEXT
  - 加粗/斜体等激活态由 Toggle 自身的按下态表达,不在编辑器层复制
- **状态与反馈**
  - disabled 为详情只读渲染:整体 60% 透明、工具栏不可交互、正文展示既有内容
- **交互与无障碍**
  - 工具栏为 `toolbar` 语义,按钮可键盘左右移动;内容区为富文本可编辑区域
  - Don't:为凑状态复制工具栏按钮矩阵、在页面内联拼编辑器边框
- **适用场景**
  - AutoForm `rich-text` 字段(商品多语言详情等长内容),独占整行

### 代码编辑 CodeEditor

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2329-7228&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`CodeEditor / Control`
- **变量绑定**
  - 容器边线 `input`、背景 `background`、圆角 md;行号栏 `muted` 50% 底 + 右边线,行号 `muted-foreground`
  - 代码文本使用等宽字体样式(14px)
- **尺寸与布局**
  - 行号栏内边距 8px、右对齐;代码区内边距 12px;宽随父容器,高随内容
- **变体与属性**
  - `State=default|disabled`;`Code` TEXT
- **状态与反馈**
  - disabled 为详情只读渲染(60% 透明);语法错误提示由字段校验层输出,编辑器不自带错误样式
- **交互与无障碍**
  - Tab 缩进在编辑区内生效,需提供跳出焦点的快捷方式
  - Don't:把行号画进代码文本、用非等宽字体渲染代码
- **适用场景**
  - AutoForm `code-editor` 字段(扩展属性 JSON 等结构化文本),独占整行

## 身份族

### 头像组 AvatarGroup

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=269-130&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`AvatarGroup`
  - 每个成员必须是 `Avatar / Control` Atom 实例,不得在组件内复制圆形、图片或 Fallback 文字
- **结构解剖**
  - `Horizontal Auto Layout -> Member Avatar[] -> optional Overflow Avatar`
  - 默认成员使用 `Size=default` 40px
- **尺寸与布局**
  - `itemSpacing=-12px`(`-space-x-3`),后一个成员覆盖前一个成员
  - 每个成员使用 2px `background` 内描边形成分隔
  - 4 个可见项总宽为 `4x40 - 3x12 = 124px`
- **Overflow 规则**
  - Overflow 是最后一个 `Content=fallback` Avatar 实例,文本格式 `+N`
  - N 表示未显示成员数,不包含当前可见成员
  - 没有剩余成员时不渲染 Overflow
- **成员顺序**
  - 成员顺序必须稳定且与业务优先级一致
  - 当前资产固定复现源码的 3 个真实成员 + 1 个 Overflow
  - 出现已验证的新上限需求时再新增正式 Mode 变体,不得临时删改实例、缩小头像或改变重叠值
- **图像回退**
  - 平台头像继续通过每个 Image Avatar 的 Artwork Swap 指定
  - 头像组不新增平台风格轴
  - 图片失败时由对应 Avatar 实例自行回退
- **状态与反馈**
  - Hover/Focus/Active 仅在整个头像组可点击时由外层 Button/Popover trigger 提供
  - 不可点击的头像组不伪造焦点态
  - 若单个成员可操作,每个成员必须有独立可访问名称和至少 40px 点击目标
- **交互与无障碍**
  - 读屏名称应汇总成员语义,例如“3 位成员,另有 5 位”
  - 不要逐个朗读装饰性重复头像后再朗读同一名单
  - Overflow 不能只靠 `+N` 提供无上下文信息
- **审计例外**
  - 碰撞审计允许 Auto Layout `itemSpacing=-12` 产生的 3 组相邻重叠,每组必须精确 12px
  - 除此之外的 Section、预览和组件碰撞必须为空
- **使用边界**
  - Do:复用 Avatar、稳定顺序、明确 Overflow
  - Don't:用散落 Ellipse 拼头像、把重叠放进 Avatar Atom、用负坐标手工摆放、让边框颜色脱离 `background` Variable
- **适用场景**
  - 头像组采用 `flex -space-x-3`、`border-2 border-background` 与 `+5` overflow
  - 适用场景:参与者、成员与协作者摘要

### 用户资料 UserProfile

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2122-7973&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`2122:7973`,实例预览板=`2122:7974`,隐藏资产框=`2122:7750`
  - 资产:`UserProfile / Trigger`=`2122:7751`,`UserProfile / Panel`=`2122:7752`,`UserProfile / Control`=`2122:7972`
  - 依赖 Avatar、DropdownMenu Content/Item、Separator 与 Chevron Icon Atom
  - 解剖为 `Control -> Trigger(Avatar + optional Identity + Chevron) -> Panel(Account summary + MenuItem[])`
- **变量绑定**
  - `background / foreground / muted-foreground / accent / ring / border`
  - Trigger 使用 `radius/md`;Panel Surface 与阴影继承 DropdownMenu Content
  - 身份文字、Hover、Focus、菜单分隔与危险操作均继承下层 semantic Variables
- **变体与属性**
  - Trigger `Mode=avatar|identity`,`State=default|hover|focus|disabled`,共 8 variants
  - Control `Mode=avatar|identity`,`State=closed|open|disabled`,共 6 variants
  - Trigger 暴露 `Name#2122:8`,`Email#2122:9`;Panel 暴露 `Name#2122:18`,`Email#2122:19`
  - Panel 菜单文案通过 DropdownMenu Item 的 Label 属性覆盖,不按账号或角色创建 variant
- **尺寸与布局**
  - avatar Trigger 为 40×40px,内部 Avatar 为 32px
  - identity Trigger 为 220×40px,结构为 `32px Avatar + fill Identity + 16px Chevron`
  - Panel 为 256×228px,左右 8px 内缩;账号摘要高 52px,菜单项 240×32px
  - open Control 为 256×272px,Trigger 与 Panel 间距 4px,Panel 与 Trigger 右边缘对齐
- **状态与反馈**
  - Hover 使用 `accent`,Focus 使用 2px `ring`,Disabled 为 50% 且不打开菜单
  - Open 保持 Trigger 可见并显示同一 Panel 实例;关闭后焦点归还 Trigger
  - Sign out 使用 destructive MenuItem;请求中的 Loading 与失败反馈由动作层 Toast/Dialog 表达
  - Error 不属于 UserProfile 根状态,账号数据缺失时 Avatar 自行回退并保留可访问名称
- **交互与无障碍**
  - Trigger 使用 Button 语义、`aria-haspopup=menu` 与 `aria-expanded`
  - avatar 模式必须提供包含账号名称的可访问名称,不能只朗读“头像”
  - Panel 使用 DropdownMenu 的上下键、Home/End、Enter/Space 与 Esc 模型
  - 账号摘要不可伪装为菜单项;若可导航,必须单独实例化具名 MenuItem
- **组合边界**
  - UserProfile 只负责账号身份入口与账号级操作菜单
  - Topbar Module 负责位置、权限、会话数据和全局通知,只能实例化 UserProfile Control
  - 语言与主题继续使用各自 Molecule,不得塞进 UserProfile 形成重复菜单
  - Do:菜单短、危险操作分组、身份信息稳定
  - Don't:复制 DropdownMenu Surface、在 Panel 内放长表单、由 UserProfile 编排整条顶栏
- **适用场景**
  - 管理后台顶栏账号入口、侧栏底部账号入口与窄布局头像菜单
  - identity 用于有横向空间的工作台;avatar 用于紧凑工具栏

## 容器类

### 卡片 Card

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=205-5&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Figma 资产:`Card`
  - 解剖为 `Root -> optional Header(Title + Description) -> Content -> optional Footer`
  - 这些区域是内容槽,不是独立业务模块
- **变量绑定**
  - `card / card-foreground / border / muted-foreground`,`radius/lg`,`Shadow/sm`,`spacing/6`
  - Root 使用 1px 描边、`card` 底与 `card-foreground` 前景
  - 任何主题变化都由 semantic Variables 驱动
- **变体与属性**
  - 无视觉 variant 轴
  - 通过 Header、Description、Content、Footer 的存在与内容形成组合
  - 不得为不同业务内容复制 Card 变体
- **尺寸与布局**
  - 宽度随父容器,高度 hug 内容
  - Header、Content、Footer 水平 padding 24px,Header 纵向 `gap 6px`
  - Title 24px 粗体,Description 14px `muted-foreground`
- **状态与反馈**
  - Card 默认是静态容器,自身不承担 Hover/Active/Focus/Disabled/Error/Loading
  - 可点击时由外层 Link 或 Button 提供全部交互态
  - 错误与加载反馈放在 Content 中,不改 Root 语义
- **交互与无障碍**
  - 只有具名独立内容才用 `article` 或带 `aria-labelledby` 的 `region`
  - 纯分组保持普通容器
  - 标题层级由页面上下文决定,不能固定为某一级
- **组合边界**
  - Card 可承载 FormField、Table、Alert 或操作区,但不吸收这些组件的状态和键盘逻辑
  - Do:保持槽位间距一致
  - Don't:嵌套仅用于装饰的 Card、把整页 Section 做成浮动卡片、为点击效果在 Root 上伪造按钮语义
- **适用场景**
  - Card 使用 `Card / CardHeader / CardTitle / CardDescription / CardContent / CardFooter` 结构与 modern `card`、`border`、`radius-lg`、`shadow-sm` token
  - 适用场景:后台页面内容分区


### 选项卡 Tabs

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=540-162&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Tabs / Trigger` 与 `Tabs / Control`
  - 解剖为 `Root -> TabList -> Trigger[]` 加与每个 Trigger 一一对应的 `TabPanel[]`
  - Control 只组合 Trigger 实例,不复制按钮形状
- **变量绑定**
  - `muted / muted-foreground / background / foreground / primary / ring`,`radius/md`,`Shadow/sm`,`spacing/1|3|4`,`border/width`
  - 所有底色、下划线、文字和焦点环绑定 semantic Variables
- **变体与属性**
  - `Style=default|line`
  - Trigger 的 `State=default|hover|active|focus|disabled`
  - Default List 高 40px、4px 内缩和 `muted` 底
  - Line List 高 44px、16px 项间距和 2px `primary` 激活下划线
  - 门店商品使用 `Tabs / Outlet Items` ComponentSet `3870:13914`，门店分类使用 `Tabs / Outlet Categories` ComponentSet `3870:33784`；两者统一暴露 `View=actual|hq|custom|local`，每个变体固定四个 Trigger 且恰好一个 active
- **尺寸与布局**
  - Trigger 水平 12px、垂直 6px,14px Medium
  - 标签宽度 hug 内容,列表可 hug 或 fill
  - 长标签不得压缩到裁字,窄屏优先横向滚动或由产品减少标签数
- **状态与反馈**
  - Hover 只增强前景
  - Active 使用背景/阴影或下划线
  - Focus 为 2px `ring` 加 2px offset
  - Disabled 50% 且不可选择
  - Error/Loading 属于对应 TabPanel,Loading 不应清除当前激活关系
- **交互与无障碍**
  - 遵循 WAI-ARIA Tabs:`tablist / tab / tabpanel`,`aria-selected`,`aria-controls` 与稳定 `id`
  - 左右方向键移动焦点,Home/End 到首尾
  - 自动激活只用于面板可即时显示的场景
- **组合边界**
  - Tabs 切换同一页面层级的并列内容,不替代主导航、步骤器或 Filter segmented control
  - Do:短且互斥的标签
  - Don't:在 Trigger 内放表单控件、用颜色作为唯一激活信号、让不可见 Panel 继续进入 Tab 顺序
- **适用场景**
  - Tabs 遵循 Radix Tabs 结构、modern default/line 表现与 WAI-ARIA Tabs 基础模式
  - 适用场景:同级并列内容切换,业务页面只提供标签与 Panel 内容


### 切换组 ToggleGroup

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1996-6689&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`1996:6689`,实例预览板=`1996:6690`,隐藏资产框=`1996:6687`
  - 资产:`ToggleGroup / Control`=`1996:6688`,`ToggleGroup / Segmented`=`2141:7252`
  - 依赖 `Toggle / State`,`Toggle / Icon`、Tooltip 与 Icon Atom
  - 解剖为 `Group -> ToggleItem[]`,每个 Item 必须是 Toggle Atom 实例
  - Group 只管理关联项的选择模型、尺寸、间距和组焦点,不得复制 Toggle 的按压视觉
- **变量绑定**
  - 继承 Toggle 的 `background / foreground / muted / muted-foreground / accent / accent-foreground / input / ring`
  - 组边界使用 `radius/md`,`border/width`,`spacing/1|2`
  - 文字、图标、描边、底色和焦点环全部绑定 semantic Variables
- **变体与属性**
  - `Selection=single|multiple`,`Content=text|icon`,`Size=compact|default`,`State=default|focus|disabled`
  - 共 24 variants;文本模式使用 `Toggle / State`,图标模式使用 `Toggle / Icon`
  - Segmented 子集为 `Size=compact|default`,`State=default|focus|disabled`,共 6 variants,语义固定为 single selection
  - Segmented 可把短数量作为标签内容展示;没有对应 TabPanel 时不得改用 Tabs 语义
  - Item 的 pressed/default 与 outline/ghost 由 Selection 和 Content 组合决定
  - Item 的 Label、Icon、Value 和 Disabled 是内容属性,不得按选项文案创建 variant
- **尺寸与布局**
  - compact Item 高 32px、Group 高 36px;default Item 高 36px、Group 高 40px;Icon 16px
  - Item 之间使用 4px gap,每项保持完整命中区与圆角
  - text 标本使用 60-70px Item,icon 标本使用 32-36px 正方形 Item
  - Group 宽度 hug 内容;扩展等分布局由上层工具栏约束,不得直接拉伸图标
  - Segmented 使用 `itemSpacing=-1`,只保留首尾外侧圆角;compact Item 92×32px,default Item 104×36px
- **状态与反馈**
  - Default/Hover/Pressed/Disabled 由各 ToggleItem 继承 Toggle Atom
  - Control Focus 标本提供 2px `ring`;运行时焦点仍落在 roving-focus 当前 Item
  - single 模式最多一个 Pressed;Required 时始终保留一个选中项
  - multiple 模式允许零到多项 Pressed,但必须保持稳定顺序
  - Error 由相邻 FormField/Alert 提供文字说明;Loading 由父级禁用整组并在邻近位置显示 Spinner,不伪造组内重复状态
- **交互与无障碍**
  - Root 必须有可访问组名,使用可见 Label、`aria-label` 或 `aria-labelledby`
  - 保持型按钮使用 `aria-pressed`;若业务语义是必须单选的枚举,使用 `radiogroup/radio`,同一实例不得混用两套语义
  - Tab 进入组内当前项,方向键按 Orientation 移动焦点,Home/End 到首尾,Space/Enter 切换
  - Disabled Item 跳过键盘选择;焦点环必须落在当前 Item,不能只给整个 Group 一圈模糊焦点
- **组合边界**
  - 只表达一组紧密相关、可保持的局部模式或工具状态
  - 一次性命令使用 Button,同级内容页面切换使用 Tabs,带文本内容区的互斥视图使用 SegmentedControl
  - FilterBar Module 可以实例化 ToggleGroup,但查询提交、条件 schema 和数据刷新不属于 ToggleGroup
  - Do:选项数量稳定、标签短、图标有 Tooltip 或可访问名称
  - Don't:混入下拉菜单、让单选组出现多个 Pressed、把页面权限或请求状态塞入组件
- **适用场景**
  - 富文本格式、对齐方式、列表密度和地图/列表视图模式
  - 适用于工具栏和局部设置,不作为页面主导航或多步骤流程


### 手风琴 Accordion

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=563-188&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Accordion / Item` 与 `Accordion / Group`
  - 解剖为 `Group -> Item[] -> Header -> Trigger(Label + Chevron) + Content`
  - Group 必须实例化 Item,Chevron 必须实例化 16px Icon Atom
- **变量绑定**
  - `foreground / muted-foreground / border / ring`,`spacing/4`,`border/width`
  - 触发文字使用 14px Medium
  - 每个 Item 以 1px `border` 下边框分隔,不另包 Card
- **变体与属性**
  - Group `Type=single|multiple`
  - Item `State=closed|open|hover|focus|disabled`
  - Single 可按产品规则允许或禁止全部收起
  - Multiple 允许并行展开
- **尺寸与布局**
  - Trigger 宽 fill、上下 16px,Label 占剩余宽度
  - Chevron 16px
  - Content 高度由内容决定,底部保留 16px
  - 展开/收起 200ms,Chevron 同期旋转 180°
- **状态与反馈**
  - Hover 可显示下划线
  - Open 保持清晰标题和旋转图标
  - Focus 使用可见 `ring`
  - Disabled 降低透明度且不响应
  - Error/Loading 属于 Content,不能把 Item 的 open 状态改名为错误或加载
- **交互与无障碍**
  - Trigger 是位于 heading 内的 Button,使用 `aria-expanded` 与 `aria-controls`
  - Enter/Space 切换,键盘焦点顺序保持 DOM 顺序
  - 动画遵循 `prefers-reduced-motion`
- **组合边界**
  - 适合可独立理解的补充内容,不用于必须同时比较的字段或页面级步骤
  - Do:标题具体、内容与标题一一对应
  - Don't:点击整个 Content 关闭、隐藏关键错误、在一个 Item 内再嵌同层 Accordion
- **适用场景**
  - Accordion 使用 Radix Accordion 结构、`py-4`、Chevron 旋转与 200ms 展开动画,交互语义遵循 WAI-ARIA Accordion 基础模式
  - 适用场景:可独立理解的补充内容


### 折叠块 Collapsible

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=569-221&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Collapsible / Set`
  - 解剖为 `Root -> Trigger(Label + Chevron) -> Content`
  - Trigger 复用 Button/Icon 原子语义,Content 是单一受控区域
- **变量绑定**
  - `foreground / muted / muted-foreground / ring`,`radius/md`,`spacing/2|3`
  - Chevron 16px,文字 14px Medium
- **变体与属性**
  - `State=closed|open|hover|focus|disabled`
  - 内容文案与图标通过属性覆盖,不因内容类型增加 variant
- **尺寸与布局**
  - Trigger 宽 fill、水平 12px、垂直 8px
  - Content 宽 fill、高度 hug
  - 开合与 Chevron 旋转使用 200ms 过渡,减少动态模式下立即切换
- **状态与反馈**
  - Hover 使用 `muted` 底
  - Open 旋转 Chevron 并显示 Content
  - Focus 显示 `ring`
  - Disabled 不可切换
  - Error/Loading 在 Content 内由 Alert/Spinner 表达,Root 仍保持当前开合状态
- **交互与无障碍**
  - Trigger 为 Button,使用 `aria-expanded` 和 `aria-controls`
  - Enter/Space 切换
  - 关闭时 Content 不可聚焦且不应被读屏重复读取
- **组合边界**
  - 只控制一个局部内容块
  - 多项关联内容使用 Accordion
  - Do:让 Trigger 清楚说明将显示什么
  - Don't:用 Collapsible 隐藏必须确认的错误、把 Chevron 做成唯一可点击的小目标、在 Header 里放第二个冲突按钮
- **适用场景**
  - Collapsible 使用 Radix Collapsible 结构、modern muted hover 与 200ms 开合表现,语义遵循 disclosure 基础模式
  - 适用场景:单个局部内容块展开与收起


### 滚动区 ScrollArea

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=571-233&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`ScrollArea / Set`
  - 解剖为 `Root -> Viewport -> Content` 与可选 `Scrollbar -> Thumb`
  - 横纵双轴同时存在时保留 Corner
- **变量绑定**
  - `background / border / scrollbar-thumb / scrollbar-thumb-hover`,`radius/full`,`spacing/1`
  - 滚动条不能使用与主题脱离的固定灰色
- **变体与属性**
  - `Orientation=vertical|horizontal`
  - `State=idle|hover|dragging`
  - 是否显示某一轴由内容溢出决定,不为具体列表类型增加 variant
- **尺寸与布局**
  - Viewport 随父容器,必须有明确的 max-height 或固定可用高度
  - 常态滚动条 10px,Hover 12px,200ms
  - Thumb 圆角且长度按可见比例计算,不能仅作装饰
- **状态与反馈**
  - Idle 可弱化轨道
  - Hover 增强 Thumb
  - Dragging 保持可见并锁定对应轴
  - Focus 属于 Viewport 内的可聚焦内容
  - Disabled/Error/Loading 属于内容所有者,Loading 时仍保留容器稳定高度
- **交互与无障碍**
  - 优先保留原生滚动行为、滚轮、触控和键盘
  - 有语义的独立区域才加 `tabindex=0`、`role=region` 与可访问名称
  - 不得隐藏系统高对比模式所需的滚动位置提示
- **组合边界**
  - 用于长列表面板、菜单或固定高度区域,不用于整页主滚动
  - Do:给 Viewport 明确尺寸并保留内容尾部 padding
  - Don't:嵌套同方向 ScrollArea、阻断 PageUp/PageDown、用 ScrollArea 掩盖无法换行的布局
- **适用场景**
  - ScrollArea 使用 Radix ScrollArea 结构与 modern scrollbar semantic token,滚动尺寸和输入行为沿用浏览器平台能力
  - 适用场景:长列表面板、菜单与固定高度区域


## 覆盖层类(Overlay)

> 统一层级 `z-50`;进出 **200ms** 过渡;均带遮罩 + 焦点陷阱 + Esc 关闭 + 可见焦点环。

### 对话框 Dialog

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=601-520&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Dialog / Surface` 与 `Dialog / Overlay`
  - 解剖为 `Portal -> Overlay + Surface(Header(Title + Description) + Content + Footer + Close)`
  - Surface 和 Overlay 必须配套实例化
- **变量绑定**
  - `background / foreground / muted-foreground / border / overlay / ring`,`radius/lg`,`Shadow/lg`,`spacing/4|6`
  - Close 使用 16px X Icon Atom
- **变体与属性**
  - `State=open|closed`
  - Surface 内容通过槽位覆盖,不按业务表单建 variant
  - Overlay 为全视口
  - Surface 居中、最大宽 512px、padding 24px、内部 `gap 16px`
- **尺寸与布局**
  - 移动端左右保留安全边距
  - Header 移动端居中、`sm` 起左对齐
  - Footer 移动端反向纵排、`sm` 起右对齐横排
  - Footer 文本按钮默认最小宽 100px、同组等宽、间距 8px；仅当文案无法容纳时整组提升到 120px 或更宽的统一档位，不能按各自文案 Hug 成忽大忽小
  - 该规则属于 Dialog Footer，不修改 Button Atom；图标按钮和移动端全宽纵排不受桌面最小宽规则约束
  - Title 18px 粗体,Description 14px muted
- **状态与反馈**
  - Open 为 95% 到 100% 缩放并淡入,Closed 反向,200ms
  - Focus 落在首个合理控件或 Surface
  - Disabled 属于触发器
  - Error/Loading 在 Content/Footer 内表达,提交 Loading 时关闭动作按业务规则决定
- **交互与无障碍**
  - 使用 `role=dialog`,`aria-modal=true`,`aria-labelledby` 和可选 `aria-describedby`
  - 打开后陷阱焦点,Esc/Close 关闭并把焦点还给 Trigger
  - 破坏性确认必须明确动作对象与后果
- **组合边界**
  - 用于确认与短表单
  - 长流程或持续辅助面板使用 Sheet/页面
  - Do:一个明确主操作和安全的取消
  - Don't:叠加 Dialog、点击遮罩导致未保存内容静默丢失、缺少可访问 Title
- **适用场景**
  - Dialog 使用 Radix Dialog 结构、modern 512px Surface 与响应式 Header/Footer,焦点管理遵循 WAI-ARIA Modal Dialog 模式
  - 适用场景:确认与短表单


### 抽屉 Sheet

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=601-708&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Sheet / Surface` 与 `Sheet / Overlay`
  - 预览板:`Sheet / Preview Board`=`1246:3001`
  - 解剖为 `Portal -> Overlay + Surface(Header(Title Block + Close) + Content + Footer)`
  - Surface 复用 Dialog 的标题、描述、关闭与焦点语义,但位置贴边
- **变量绑定**
  - `background / foreground / muted-foreground / border / overlay / ring`,`Shadow/lg`,`spacing/6`
  - 边缘描边随 Side 使用对应一侧的 `border`
- **变体与属性**
  - `Side=left|right|top|bottom`,`State=open|closed`
  - 内容与操作通过槽位覆盖,不为筛选、详情等业务用途建视觉 variant
- **尺寸与布局**
  - Left/Right 满高、宽 75%、最大 400px
  - Top/Bottom 宽满、高度约 1/3
  - Header/Footer padding 24px
  - Footer 文本按钮沿用 Dialog 的同组等宽规则：桌面默认最小 100px、间距 8px，长文案整组使用统一的更宽档位
  - Header 占满内容宽,Title Block 使用剩余宽度,Close 保持 28px 方形独立命中区
  - Content 必须可滚动且不能把 Footer 推出视口
- **状态与反馈**
  - Open 从指定边缘滑入,Closed 滑出,300ms `ease-in-out`
  - Overlay 同步淡入淡出
  - Focus/Disabled/Error/Loading 的归属与 Dialog 相同
  - Loading 保持 Surface 尺寸稳定
- **交互与无障碍**
  - Modal Sheet 使用 `role=dialog`,`aria-modal`,具名 Title、焦点陷阱、Esc 关闭与焦点归还
  - 非模态辅助面板不得渲染遮罩或抢占页面焦点
- **组合边界**
  - 适合侧栏筛选、详情或辅助编辑
  - 关键短确认用 Dialog,永久导航用 Sidebar Module
  - Do:让关闭动作始终可见
  - Don't:在 Sheet 内再打开同侧 Sheet、把主页面唯一任务藏入抽屉、忽略移动端安全区
- **适用场景**
  - Sheet 使用 Radix Dialog 派生结构、modern 四方向尺寸与 300ms 滑入规则
  - 适用场景:MCH/SYS 侧栏筛选与辅助面板


### 气泡卡 Popover

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=598-481&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Popover / Content` 与 `Popover / Control`
  - 解剖为 `Trigger -> Portal -> Positioned Content`
  - Control 组合真实 Trigger 实例和 Content 实例
- **变量绑定**
  - `popover / popover-foreground / border / ring`,`radius/md`,`Shadow/md`,`spacing/4`
  - Content 使用 1px `border`,不能复用菜单选中底色充当容器底
- **变体与属性**
  - `Side=top|right|bottom|left`,`Align=start|center|end`,`State=closed|open`
  - 碰撞避让可改变最终位置,内容类型不增加 variant
- **尺寸与布局**
  - 基准宽 288px、padding 16px、距锚点 4px,高度 hug 内容
  - 移动端最大宽不超过视口安全区
  - 起点必须与 Trigger 几何关联
- **状态与反馈**
  - Trigger 的 Hover/Active/Focus/Disabled 由其 Atom 控制
  - Open 显示 Content 并保持 Trigger 状态
  - Error/Loading 在 Content 内表达,不得用关闭浮层掩盖错误
- **交互与无障碍**
  - Trigger 使用 `aria-expanded` 与 `aria-controls`
  - Esc 和外部点击关闭并归还焦点
  - 含交互控件时把焦点移入内容
  - 纯说明内容保持 Trigger 焦点
- **组合边界**
  - 承载轻量表单、筛选或说明,菜单行为必须使用 DropdownMenu,短文本说明使用 Tooltip
  - Do:内容简短且就地完成
  - Don't:加入全屏遮罩、承担长流程、把 Hover 作为唯一打开方式
- **适用场景**
  - Popover 使用 Radix Popover 结构、modern 288px `popover` Surface 与 4px offset
  - 适用场景:日期、筛选等锚定面板


### 下拉菜单 DropdownMenu

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=598-558&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`DropdownMenu / Item`,`DropdownMenu / Content`,`DropdownMenu / Control`
  - 解剖为 `Trigger -> Content(Group/Label/Item/CheckboxItem/RadioItem/Separator/SubTrigger)`
  - Content 只能由菜单构件实例组成
- **变量绑定**
  - `popover / popover-foreground / accent / accent-foreground / muted / muted-foreground / destructive / ring`,`radius/md|sm`,`Shadow/md`,`spacing/1|2`
  - Check、Chevron 与快捷键提示使用 Icon/Kbd Atom
- **变体与属性**
  - Item `Type=default|checkbox|radio|submenu|destructive`,`State=default|focus|disabled`
  - Content `Side` 与 `Align`
  - Control `State=closed|open`
  - 图标通过 INSTANCE_SWAP,文案通过 TEXT 属性
- **尺寸与布局**
  - Content 最小宽 128px、padding 4px
  - Item 水平 8px、垂直 6px、14px、`radius/sm`
  - 选择项左侧预留 14px 图标位,Label 8×6px 粗体,Separator 1px
- **状态与反馈**
  - Focus 使用 `accent`
  - Open 保持 Trigger active
  - Disabled 降低透明度且跳过选择
  - Destructive 使用 `destructive`
  - Error 不属于菜单项
  - 异步动作 Loading 由触发动作的 Toast/Dialog 表达,菜单关闭规则由动作结果决定
- **交互与无障碍**
  - 使用 `menu / menuitem / menuitemcheckbox / menuitemradio`
  - 上下键移动,Home/End 首尾,Enter/Space 选择,Esc 关闭并归还焦点
  - 仅视觉分组标题不可进入 Tab 顺序
- **组合边界**
  - 用于一组即时命令或互斥选项,不承载任意表单布局
  - 复杂筛选使用 Popover/Sheet
  - Do:动词开头、危险动作分组
  - Don't:把导航与命令混成无语义列表、仅靠图标表达命令、在 Item 内嵌 Button
- **适用场景**
  - DropdownMenu 使用 Radix Dropdown Menu 结构、modern `accent` focus 与 128px 最小宽
  - 适用场景:Topbar、行操作与主题/语言菜单


### 命令面板 CommandPalette

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2016-7934&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`2016:7934`,实例预览板=`2016:7935`,隐藏资产框=`2015:7377`
  - 资产:`CommandPalette / Item`=`2015:7378`,`CommandPalette / Panel`=`2016:7466`,`CommandPalette / Control`=`2016:7933`
  - 依赖 Dialog、SearchField、ScrollArea、EmptyState、Icon、Kbd、Spinner、Separator 与 Button
  - 解剖为 `Trigger(optional) -> Dialog -> SearchField -> Results(Group(Label + CommandItem[])) -> Status|Footer`
  - CommandItem 解剖为 `optional Icon + Label + optional Description + optional Kbd`,所有下层控件必须使用现有组件实例
- **变量绑定**
  - `popover / popover-foreground / background / foreground / muted / muted-foreground / accent / accent-foreground / border / ring / destructive`
  - Surface 使用 `radius/lg`,`Shadow/lg`,`spacing/1|2|3|4`;分隔线使用 `border/width`
  - 图标、文字、状态底色和焦点环全部绑定 semantic Variables
- **变体与属性**
  - Control `State=closed|results|empty|loading|error`
  - Panel `State=results|empty|loading|error`
  - Item `State=default|hover|selected|disabled`,`Meta=shortcut|description`
  - Item 暴露 `Label#2015:24`,`Description#2015:25`,`Icon#2015:26`
  - 属性:`Query`,`Groups`,`Commands`,`ActiveId`,`Placeholder`,`RecentCommands`
  - 命令数据、权限结果、最近使用记录和搜索算法是输入属性,不得按命令数量或业务名称创建视觉 variant
- **尺寸与布局**
  - Panel 为 480×360px,移动端最大宽 `calc(100vw - 32px)`;closed Trigger 为 260×36px
  - SearchField 为 448×36px,Header Separator 为 448×1px,结果区为 448×268px
  - Group Label 为 11px Semi Bold;CommandItem 为 420×44px、水平 10px、内容 gap 10px
  - Icon 16px,Kbd 固定在尾部且不挤压 Label;长描述最多两行,超出省略
- **状态与反馈**
  - Open 后 SearchField 自动获得焦点并保持 Surface 尺寸稳定
  - Results 中只允许一个 Active Item;Hover 可以更新 Active,但不能触发命令
  - Empty 明确显示当前查询无匹配项;Loading 保留 query 并显示 Spinner;Error 提供可聚焦 Retry
  - Disabled Item 可见但不可执行;命令执行期间锁定重复触发,结果通过页面、Toast 或 Dialog 表达
- **交互与无障碍**
  - Surface 使用具名 modal dialog;搜索框使用 `combobox` 并通过 `aria-controls`、`aria-activedescendant` 关联结果列表
  - 结果使用 `listbox/option` 或等价 command menu 语义,Group Label 不进入 Tab 顺序
  - 上下键移动,Home/End 到首尾,Enter 执行,Esc 先清空 query 或关闭并归还 Trigger 焦点
  - 状态变化通过 polite live region 宣告;快捷键提示不能成为唯一操作说明
- **组合边界**
  - 组件只负责一次命令搜索、定位和执行入口的局部交互
  - 命令注册、权限过滤、路由跳转、远程索引和审计记录属于应用层或对应 Module
  - Topbar Module 可以实例化 Trigger,但不得复制 CommandPalette Surface
  - Do:动词开头、稳定 command id、明确无结果与错误
  - Don't:把任意长表单塞入结果项、混用 DropdownMenu 键盘模型、把不可用命令静默隐藏到无法解释
- **适用场景**
  - 全局跳转、后台快捷操作、实体搜索入口和带快捷键的上下文命令
  - 少量固定命令使用 DropdownMenu,单一关键词过滤使用 SearchField


### 工具提示 Tooltip

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=598-532&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Tooltip / Content` 与 `Tooltip / Control`
  - 解剖为 `Provider -> Trigger -> Portal -> Content`
  - Control 必须展示可聚焦 Trigger 与对应 Content
- **变量绑定**
  - `popover / popover-foreground / border`,`radius/md`,`Shadow/md`,`spacing/1_5|3`
  - 文字 14px,不硬编码反色底
- **变体与属性**
  - `Side=top|right|bottom|left`,`Align=start|center|end`,`State=closed|open`
  - 内容仅为短文本,不按文案长度建 variant
- **尺寸与布局**
  - 水平 12px、垂直 6px,高度 hug,最大宽度由文档语言与可读行长约束
  - 与 Trigger 保持可辨识间距且不得遮住目标
- **状态与反馈**
  - Hover 与 keyboard Focus 均可打开,移出或 Esc 关闭
  - Disabled Trigger 需要可聚焦包装器才可解释禁用原因
  - Active/Error/Loading 不属于 Tooltip,不能用 Tooltip 承载必须持续可见的状态
- **交互与无障碍**
  - Content 使用 `role=tooltip`,由 Trigger 的 `aria-describedby` 关联
  - 不能包含链接、按钮或表单控件
  - 触屏不能依赖 Tooltip 才能完成任务
- **组合边界**
  - 只补充图标按钮名称或简短说明
  - 丰富内容用 Popover,校验错误用 FormField/Alert
  - Do:一句直接说明
  - Don't:重复可见标签、放关键操作、仅支持 hover
- **适用场景**
  - Tooltip 使用 Radix Tooltip Provider/Trigger/Content 结构与 modern popover surface,行为遵循 WAI-ARIA Tooltip 模式
  - 适用场景:图标按钮名称与简短补充说明


## 反馈类

### 空状态 EmptyState

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2007-6657&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`2007:6657`,实例预览板=`2007:6658`,隐藏资产框=`2007:6655`,资产:`EmptyState / Control`=`2007:6656`
  - 依赖 Icon、Button 与 Link Atom
  - 解剖为 `Root -> Visual(optional Icon|Artwork) -> Body(Title + Description) -> Actions(optional Primary + Secondary)`
  - Visual、Primary 和 Secondary 必须复用下层资产,不得在 EmptyState 内重新画图标或按钮
- **变量绑定**
  - `background / foreground / muted / muted-foreground / border / primary / primary-foreground / ring`
  - 使用 `radius/lg`,`spacing/2|3|4|6|8`;交互状态由 Button/Link 自身 Variables 提供
  - EmptyState 默认无独立 Card Surface;只有父容器明确需要边界时才使用 1px `border`
- **变体与属性**
  - `Mode=compact|standard`,`Tone=neutral|error`,`Actions=none|primary|primary-secondary`
  - 共 12 variants;暴露 `Title#2007:24` 与 `Description#2007:25`
  - Icon、PrimaryLabel 与 SecondaryLabel 通过嵌套实例覆盖,不按业务文案创建 variant
  - 业务名称、数据数量和具体操作文案均为内容属性,不得复制视觉变体
- **尺寸与布局**
  - Root 宽 480px并允许父容器约束为 fill;内容居中,文本宽 400px
  - compact 高 142-184px、上下 20px padding、40px Visual、14px Title
  - standard 高 220-264px、上下 32px padding、48px Visual、16px Title
  - Description 为 14px `muted-foreground`;Actions 横向 8px gap,窄屏纵向排列且按钮不缩窄命中区
- **状态与反馈**
  - EmptyState 是已完成查询后的稳定结果,不承担 Hover/Active/Focus/Disabled
  - Actions 自行覆盖完整交互态
  - Loading 使用 Skeleton/Spinner;局部可恢复请求错误可用 `Tone=error + Retry`,系统级故障使用 Alert/Result
  - no-results 必须保留查询上下文并提供清除筛选或返回动作;first-use 优先提供创建动作
- **交互与无障碍**
  - 动态出现时使用 `role=status` 或 polite live region,避免每次分页刷新重复打断读屏
  - Title 作为区域可访问名称,Description 通过 `aria-describedby` 关联
  - 装饰 Icon 隐藏于读屏;表达业务含义的 Artwork 提供等价文本
  - 焦点不自动移入 EmptyState;若原操作删除最后一项,焦点移动到主操作或稳定区域标题
- **组合边界**
  - 只描述一个局部数据区域为什么为空以及用户下一步可以做什么
  - AutoTable Module 决定查询结果和筛选状态,EmptyState 只接收 Reason、文案和 Actions
  - 页面级无权限、系统故障和操作结果使用专用 Result/Alert,不扩展 EmptyState 承担所有反馈
  - Do:具体说明原因、给一个明确主动作
  - Don't:只写“暂无数据”、同时堆叠三个主按钮、用大面积装饰掩盖关键信息
- **适用场景**
  - 空列表、搜索无结果、首次创建前的引导和局部关联数据为空
  - 适用于 AutoTable 内容槽、Card 内容区和选择器结果区


### 内联提示 Alert

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=576-259&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Alert / Set`
  - 解剖为 `Root -> optional Icon + Body(Title + Description) + optional Action`
  - Icon 必须是 16px Icon Atom,Action 使用 Button/Link Atom
- **变量绑定**
  - `background / foreground / border` 与 `status/success|warning|error|info` 对应 background、border、foreground,以及 `radius/lg`,`spacing/3|4`,`border/width`
- **变体与属性**
  - `Tone=default|destructive|success|warning`,`Icon=show|hide`
  - 文案和可选 Action 通过属性/槽位覆盖
  - 不要为每条业务消息新增 variant
- **尺寸与布局**
  - 宽 fill、高度 hug,水平 16px、垂直 12px
  - 有图标时为 `16px + 12px gap + 1fr` 网格
  - Title 14px Medium、下间距 4px,Description 14px 宽松行高
- **状态与反馈**
  - 静态 Alert 不承担 Hover/Active/Focus/Disabled
  - Action 自行提供这些状态
  - Error 对应 destructive tone
  - Loading 只有在持续过程确需说明时组合 Spinner,不能把 warning 当 Loading
- **交互与无障碍**
  - 进入页面即存在的一般说明使用具名容器
  - 动态成功/提示使用 `role=status`,阻断性错误使用 `role=alert`
  - Title 与 Description 必须提供文字,不能只靠颜色或图标区分语义
- **组合边界**
  - 用于页面或表单区内持续可见反馈
  - 短暂反馈用 Toast,字段错误用 FormField
  - Do:说明发生了什么及下一步
  - Don't:所有信息都用 destructive、堆叠重复 Alert、让关闭按钮成为唯一获知信息的方式
- **适用场景**
  - Alert 使用 Root/Title/Description 结构与 modern semantic status tokens
  - 适用场景:业务页面持续可见的错误与说明,由页面提供内容和严重级别


### 详细警告提示 Warning Callout

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=3710-13422&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Warning Callout / Control`=`3710:13433`,Section=`3710:13422`
  - 解剖为 `Root -> Accent + Icon + Content(Title + Consequence x3)`
  - 高风险操作弹窗必须实例化该组件，不得在业务 Screen 内复制提示块
- **变量绑定**
  - background、border、foreground、solid 分别使用 warning Variables `298:28`、`298:29`、`298:30`、`298:31`
  - 图标复用 `shieldAlert` `2301:1171`
- **属性**
  - `Title`,`Line 1`,`Line 2`,`Line 3`
  - 属性只承载已确认的业务后果，不为不同业务操作新增视觉 variant
- **尺寸与布局**
  - Desktop 为 608×100px，内边距 12px，水平 gap 8px，正文列 536px
  - Title 13px Medium；三条后果为 12px Regular、行间 2px
  - Desktop 在视口允许时每条后果保持单行；不能通过扩大页面左右空白或压缩字号解决换行
- **组合边界**
  - 用于提交后会产生不可逆、跨资源或跨系统影响的确认操作
  - 普通说明继续使用 Alert；字段错误继续使用 FormField
  - Warning Callout 只说明后果，不替代 Dialog Title、操作原因或 Footer 确认动作


### 瞬时反馈 Toast

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=598-401&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Toast / Control`
  - 解剖为 `Viewport -> Toast stack -> Toast(Icon + Body(Title + Description) + optional Action + Close)`
  - 单条 Toast 不控制全局 Viewport 位置
- **变量绑定**
  - `popover / popover-foreground / border` 与 status semantic colors,以及 `radius/lg`,`Shadow/lg`,`spacing/3|4`
  - Icon 使用 Icon Atom,Action/Close 使用 Button Atom
- **变体与属性**
  - `Tone=default|success|destructive|warning`,`State=enter|visible|exit`
  - promise 流程可用 `Loading -> success|destructive`,但 Loading 必须替换同一条 Toast,不能堆叠重复消息
- **尺寸与布局**
  - 桌面宽度不超过 356px,移动端随安全边距填充
  - padding 16px、内部 gap 12px
  - 堆叠间距固定,新消息位置与全站约定一致,不能遮挡主导航或固定提交栏
- **状态与反馈**
  - Enter/Exit 只做短位移与淡入淡出
  - Hover/Focus 暂停自动关闭
  - Action 提供 Focus/Active/Disabled
  - Error 使用 destructive
  - Loading 显示 Spinner 并设置合理的持续时长或由结果替换
- **交互与无障碍**
  - 一般反馈进入 `aria-live=polite`/`role=status`,失败用 `aria-live=assertive`/`role=alert`
  - 不把焦点自动移入 Toast
  - 关闭和 Action 必须有可访问名称,自动消失时间允许用户读完和暂停
- **组合边界**
  - 用于操作结果等短反馈,不能承载唯一的字段错误、长说明或必须确认的决策
  - Do:简短、去重、说明结果
  - Don't:无限堆叠、仅写“失败”、在页面初始加载时弹成功消息
- **适用场景**
  - Toast 使用全局 toaster、统一控制层与 modern status colors
  - 适用场景:创建、保存、删除等业务动作的短反馈
  - 见[模板·成功反馈](templates.md)

## 导航类

### 步骤器 Stepper

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2011-7371&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`2011:7371`,实例预览板=`2011:7372`,隐藏资产框=`2011:6717`
  - 资产:`Stepper / Item`=`2011:6718`,`Stepper / Control`=`2011:7370`
  - 依赖 Icon、Separator 与 Badge Atom
  - 解剖为 `Control -> ordered StepItem[] + Connector[]`,StepItem 为 `Indicator(number|check|error) + Content(Label + optional Description)`
  - Indicator、Icon 和可点击 Label 必须复用下层资产;Connector 是组内布局构件,不承载业务状态
- **变量绑定**
  - `background / foreground / muted / muted-foreground / border / primary / primary-foreground / destructive / ring` 与 status semantic colors
  - 使用 `radius/full`,`spacing/2|3|4|6`,`border/width`;连接线、文字、指示器和焦点环全部绑定 Variables
- **变体与属性**
  - Item `Orientation=horizontal|vertical`,`Status=complete|current|upcoming|error|disabled`
  - Control `Orientation=horizontal|vertical`,`State=initial|progress|complete|error|disabled`
  - Item 暴露 `Step#2011:30`,`Label#2011:31`,`Description#2011:32`
  - 当前 Figma Control 固定展示 3 步;实现层通过重复 Item/Connector 支持稳定步骤数组
  - 步骤数量、文案和业务校验结果是数据属性,不得按流程名称创建 variant
- **尺寸与布局**
  - Indicator 24px;Label 13px Medium,Description 12px `muted-foreground`
  - 水平 Item 为 132×70px,Connector 为 56×1px,三步 Control 为 508×70px
  - 垂直 Item 为 260×48px,Connector 为 1×24px,三步 Control 为 260×192px
  - 窄屏优先切换为 vertical 或仅显示当前步骤摘要,不得压缩 Indicator、裁切 Label 或让步骤相互覆盖
- **状态与反馈**
  - upcoming 使用 muted, current 使用 primary 并同时显示 `aria-current=step`,complete 使用 Check,error 使用 destructive 图标和文字
  - Disabled 不可进入;clickable 模式仅允许访问产品规则许可的 complete/current 步骤
  - Focus 只出现在可点击 StepItem;static 模式不伪造 Hover/Active
  - Loading 属于当前步骤内容或提交动作,Stepper 保持当前状态;校验失败将对应步骤标为 error并提供邻近错误摘要
- **交互与无障碍**
  - Root 使用带名称的 `ol`,每个步骤为 `li`;当前步骤声明 `aria-current=step`
  - clickable StepItem 使用 Button/Link,按正常 Tab 顺序访问并支持 Enter/Space;static StepItem 不进入 Tab 顺序
  - 完成图标、错误色和数字必须同时有状态文字,不能只靠颜色或图形表达
  - 动态推进后焦点进入新步骤的主标题或首个错误字段,并通过 polite live region 宣告步骤变化
- **组合边界**
  - Stepper 只呈现和导航一段有限流程的当前位置、已完成项和异常项
  - 表单 schema、步骤校验、草稿保存、提交请求和路由守卫属于 Wizard/AutoForm Module 或页面流程
  - Tabs 用于可自由切换的并列内容,Progress 用于无离散标签的完成比例,Breadcrumb 用于信息架构路径
  - Do:步骤顺序稳定、标签简短、明确是否可返回
  - Don't:让用户跳过必填校验、用 Stepper 表示无限任务列表、把每步表单直接画进组件主资产
- **适用场景**
  - 账号开通、门店配置、商品创建、结算和分阶段导入流程
  - 适用于 3-7 个稳定步骤;更长或动态任务使用任务列表、时间线或专用 Module


### 面包屑 Breadcrumb

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=577-289&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Breadcrumb / Item` 与 `Breadcrumb / Control`
  - 解剖为 `nav -> ol -> Item(Link|Current|Ellipsis) + Separator`
  - Control 必须实例化 Item,Separator 使用 14px Chevron Icon Atom
- **变量绑定**
  - `muted-foreground / foreground / ring`,`spacing/1_5|2_5`,`radius/sm`
  - 文字与图标继承 semantic foreground,不写固定灰色
- **变体与属性**
  - Item `Type=link|current|ellipsis`,`State=default|hover|focus`
  - Control 的 `Collapse=none|middle`
  - Current 永远是最后一项,Ellipsis 只替代中间层级
- **尺寸与布局**
  - 文字 14px,项间 gap 6-10px,Separator 14px
  - Ellipsis 为 36px 方形目标
  - 长标签单行截断时保留完整可访问名称和 Tooltip
- **状态与反馈**
  - Link Hover 转 `foreground`,Focus 使用可见 ring
  - Current 非链接并设置 `aria-current=page`
  - Disabled/Error/Loading 不属于 Breadcrumb,路由未就绪时应保留稳定层级或显示页面 Skeleton
- **交互与无障碍**
  - `nav` 使用“面包屑”可访问名称,列表使用 `ol`
  - Separator `aria-hidden`
  - 折叠项打开菜单时复用 DropdownMenu 并提供“显示中间层级”名称
- **组合边界**
  - 表达当前页面在信息架构中的位置,不替代浏览器返回、Tabs 或步骤器
  - Do:从稳定上级到当前页
  - Don't:把当前页做成可点击链接、在窄屏删除语义层级、用斜杠文本让读屏重复朗读
- **适用场景**
  - Breadcrumb 使用 modern 14px muted/foreground 表现
  - 适用场景:MCH 商品、门店等 CrudFormPage,标签与链接由页面信息层级提供


### 分页 Pagination

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=578-345&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Pagination / Ellipsis` 与 `Pagination / Control`
  - 解剖为 `nav -> list -> Previous + Page[] + Ellipsis + Next`
  - 页码和前后操作必须实例化 Button Atom,Ellipsis 使用专用资产
- **变量绑定**
  - `background / foreground / muted-foreground / border / ring`,`radius/md`,`spacing/1`
  - 箭头使用 Icon Atom,当前页沿用 Button selected/default 视觉
- **变体与属性**
  - `Item=previous|page|next`,`State=default|hover|active|focus|disabled`,`Current=true|false`
  - Control 的总页数和窗口由数据计算,不按具体页数建 variant
- **尺寸与布局**
  - 页码与 Ellipsis 36px 方形,项间 gap 4px
  - Previous/Next 高 36px并使用箭头+文案
  - 窄屏可隐藏文案但必须保留可访问名称,控件不得因位数变化跳动
- **状态与反馈**
  - Current 不可重复导航并设 selected 视觉
  - 首尾页禁用对应操作
  - Focus 显示 ring
  - Loading 保留当前页与控件宽度并禁用重复请求
  - Error 由邻近 Alert/Toast 说明,不清空当前数据
- **交互与无障碍**
  - `nav` 使用“分页”名称,每个页码名称为“第 N 页”,当前页 `aria-current=page`
  - Ellipsis `aria-hidden` 或具名为“更多页面”但不可冒充可点击按钮
  - 键盘顺序与视觉顺序一致
- **组合边界**
  - 只负责换页
  - page size、总数、筛选与排序属于 AutoTable Module
  - Do:保持选中页可见并在数据更新后管理焦点
  - Don't:让 Ellipsis 可点击却无菜单、同时使用无限滚动、用禁用色隐藏当前页
- **适用场景**
  - Pagination 由 Button/Icon 原子组合
  - 适用场景:MCH/SYS AutoTable,分页、总数和每页条数由列表壳提供


### 语言 / 主题切换器

#### LocaleThemeToggle

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1264-3745&footer=false&page-selector=false)

- **层级与复用**

  `Molecule`;资产:`LocaleThemeToggle`=`9:33`,独立 Section=`1264:3745`;只负责把语言与主题两个紧凑 Trigger 组成共享工具条,具体菜单分别委托 LanguageSwitcher 与 ThemeSwitcher。
- **结构解剖**

  `Toolbar group -> Language Trigger(Button Leading + Globe Icon + endonym) + Theme Trigger(Button Leading + Palette Icon + theme label)`;两个 Trigger 必须实例化 Button/Icon Atom,不得直接画边框或向量图标。
- **尺寸与布局**

  两个 Trigger 使用 `ghost/sm` 28px 高,组间 gap 8px,整体 Hug 内容;语言显示固定 endonym(`English`/`中文`/`Bahasa Melayu`),主题显示当前主题名称。
- **状态与反馈**

  Hover/Active/Focus/Disabled 由 Button Atom 表达;展开状态属于各自 Switcher。两个 Trigger 是独立命中区和独立 Tab stop,不能整组合并为一次点击。
- **交互与无障碍**

  工具组具名;两个 Trigger 分别声明语言菜单与主题菜单的 expanded/controls 关系。Do:用于空间有限的顶栏。Don't:在该组合内复制菜单项、让主题文案跟随语言名称规则、把两个动作合成一个菜单。

#### LanguageSwitcher

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=749-2635&footer=false&page-selector=false)

- **层级与复用**

  `Molecule`;资产:`LanguageSwitcher / Item`=`749:2652`,`LanguageSwitcher / Panel`=`749:2653`,`LanguageSwitcher / Control`=`749:2713`;依赖 Button、Icon、Popover 与 Text Atom。
- 语言名称固定使用语言自称:`English`、`中文`、`Bahasa Melayu`;它们不随当前界面 locale 翻译。`EN/ZH/MS` 仅作为稳定辅助缩写,不得用 `Chinese` 或 `Simplified Chinese` 替换 `中文`。
- Item 轴:`State=default|hover|active`,属性:`Tag`,`Label`;Control 轴:`State=closed|open`。Active 使用选中背景和 Check,Focus 使用 ring;打开后焦点进入当前项,Esc 关闭并归还 Trigger。
- Panel 使用 `menu/listbox` 等价语义并声明当前语言;上下键移动,Enter/Space 切换。切换后更新界面 locale,但列表自称保持不变。

#### ThemeSwitcher

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=749-2714&footer=false&page-selector=false)

- **层级与复用**

  `Molecule`;资产:`ThemeSwitcher / Item`=`749:2742`,`Panel Compact`=`749:2743`,`ThemeSwitcher / Card`=`953:2855`,`Panel Grid`=`954:2855`,`Control`=`749:2885`;依赖 ThemePreview、Button、Icon、Separator 与 Popover/Dialog。
- `Layout=compact` 用于顶栏下拉:每行左侧必须实例化对应 `ThemePreview Size=compact`,不能用同一个通用图标代替所有主题。`Layout=grid` 用于设置页:3×2 卡片矩阵,每张卡实例化 `ThemePreview Size=card`。
- Item/Card 共用 `Kind=theme|custom`,`State=default|hover|active`,并暴露 `Label` 与 `Preview`。Modern/Business/Eye Care/Pink Kawaii/Neo Brutalism/Custom 六项必须使用各自 Preview mode。
- Custom 的主选择区和 Settings Button 是两个独立命中区:点击文字/预览立即选中并切换自定义主题;点击右侧 Palette IconButton 只打开主题定制 Dialog/Sheet,不能重复触发选择。两区分别具备 Hover/Focus/Active。
- Appearance 使用 System/Light/Dark 分段操作;颜色主题和明暗模式是两组独立状态。Compact 与 Grid 必须共享 ThemePreview、Item/Card 状态和 CustomThemePanel,不得维护两套主题色。
- **交互与无障碍**

  Trigger 声明 expanded 与当前主题;选择项使用 `aria-pressed` 或单选语义;Settings Button 有独立名称“编辑自定义主题”。详见[令牌·多主题](tokens.md)。

## 选择器类(Pickers)

组合 Input 触发器 + Popover + 面板,统一走[原子](atoms.md)的输入与浮层规范:

| 选择器 | 行为规范 |
|---|---|
| **DatePicker** | 输入触发器 + 日历面板;单日选择 |
| **DateTimePicker** | 日历 + 时间列,日期时间一体 |
| **TimePicker** | 时/分/秒;可配粒度(hour/minute/second)与是否显秒 |
| **FileInput** | outline-sm Button + Upload 图标触发系统文件选择;支持 `multiple`、`accept`、摘要/计数/可删文件列表;拖拽区是独立组件 |
| **MapPicker** | 地图选点组件,输出经纬度;搜索定位 |

### 选择器触发器 PickerTrigger

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=605-1825&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`PickerTrigger / Set`=`605:1984`
  - 解剖为 `Trigger -> optional Leading Icon + Value|Placeholder + optional Clear + Chevron`
  - Input、Button 与 Icon 必须实例化已验收 Atom,不得重画边框或图标
- **变量绑定**
  - `background / foreground / muted-foreground / border / input / ring / destructive`,`radius/md`,`Shadow/sm`,`spacing/2|3`
  - Calendar、Clock、MapPin、Palette、ChevronDown 与 X 使用 16px lucide Icon Atom
- **变体与属性**
  - `Content=placeholder|value`,`State=default|hover|active|focus|disabled|error`,`Clearable=true|false`
  - 文本和图标通过 TEXT/INSTANCE_SWAP 属性覆盖,不得按选择器类型复制 Trigger
- **尺寸与布局**
  - 默认高 36px、宽 fill、水平 padding 12px、内部 gap 8px
  - Value 单行省略并占据剩余宽度,Clear 与 Chevron 各保留稳定的 20px 图标目标,内容变化不得推动整体几何
- **状态与反馈**
  - Hover 增强 border
  - Active/Open 保持 `Shadow/sm`
  - Focus 显示 2px `ring`
  - Disabled 50% 且不可打开
  - Error 使用 `destructive` border/ring
  - Loading 以 Spinner 替换 Chevron 并锁定重复打开,不清空当前 Value
- **交互与无障碍**
  - Trigger 使用 Button/Combobox 语义、可访问名称、`aria-haspopup`,`aria-expanded`,`aria-controls`
  - Clear 必须有独立名称且不能抢走 Trigger 的展开语义
  - Placeholder 不替代 FormField Label
- **组合边界**
  - 只负责统一锚点和当前值展示,面板、值解析、校验与时区属于父 Picker
  - Do:所有日期、时间、颜色和地图选择器复用同一 Trigger
  - Don't:在每个 Picker 内复制输入框、只靠图标表示当前值、让 Clear 与打开动作共用一个无区分点击区
- **适用场景**
  - PickerTrigger 统一复用 Input/Popover 组合
  - 适用场景:AutoForm 的 Date、Time、Map 与 Color picker 字段

### 日历 Calendar

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=605-1985&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Calendar / Control`=`605:2214`,5 周变体=`1248:3141/1248:3202`
  - 依赖 `CalendarDay / Control` Atom
  - 解剖为 `Calendar -> Header(Previous + Month/Year + Next) -> Weekday row -> Day grid -> optional Footer`
  - 每个日期格必须是 CalendarDay 实例
- **变量绑定**
  - `popover / popover-foreground / muted-foreground / primary / primary-foreground / border / ring`,`radius/md`,`Shadow/md`,`spacing/1|2|3`
  - 左右翻月使用 16px Chevron Icon Atom
- **变体与属性**
  - `Mode=single|range`,`Weeks=5|6`
  - `Months=one|two` 由上层布局组合,不复制 Calendar 根组件
  - 日期本身沿用 CalendarDay 的 today/selected/range/outside/focus/disabled 状态,不在 Calendar 根重复制造日期状态
- **尺寸与布局**
  - 单月根宽 304px、padding 12px
  - 7 列日期格固定 36×36px,列 gap 4px、周行 gap 8px,Header 高 28px
  - `Weeks=5` 高 296px,`Weeks=6` 高 360px
  - 双月以 24px gap 横排,窄屏改为纵排且保持每格尺寸
- **状态与反馈**
  - 翻月 Hover/Active/Focus/Disabled 继承 Button Atom
  - Loading 保留月历高度并对网格使用 Skeleton,Error 由相邻 Alert/FormField 表达
  - 禁用日期不可选择,但仍保留可辨识日期文本
- **交互与无障碍**
  - 使用具名 `grid`,星期标题为列头
  - 方向键按日移动,Home/End 到周首尾,PageUp/PageDown 切月,Enter/Space 选择
  - 月份切换通过 polite live region 宣告,范围起止不能只靠颜色区分
- **组合边界**
  - 周起始、locale、时区和最小/最大日期由父组件注入
  - Calendar 只输出日期选择
  - 当前月按实际周数选择 `Weeks=5|6`
  - 1-31 能在 5 周排完时不得附加无意义第 6 周,需要 6 周的月份显式切换且上层高度随之适配
  - Do:保持连续范围背景和稳定月份标题
  - Don't:内联画日期数字、让 outside day 与 disabled day 语义混同、把业务时段塞入日期格
- **适用场景**
  - Calendar 遵循 DayPicker 几何、locale 能力与 WAI-ARIA Grid 模式
  - 适用场景:Date/DateRange 表单字段

### 时间选择器 TimePicker

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=605-2215&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - Section=`605:2215`,实例预览板=`1446:4205`
  - 资产:`TimePicker / ClockFace`=`694:3154`,`TimePicker / Panel`=`694:3671`,`TimePicker / Control`=`605:2316`
  - 依赖 PickerTrigger、Popover、TimeSegment 与 Icon Atom
  - 解剖为 `Control(Trigger + Popover Panel)`,`Panel -> Active unit label + ClockFace + TimeSegment row`
  - 可见文档板只使用上述资产的实例,主组件集位于隐藏资产容器 `1451:4995`
- **变量绑定**
  - `popover / popover-foreground / background / foreground / muted / muted-foreground / border / input / primary / ring / destructive`,`radius/md|full`,`Shadow/md`,`spacing/2|3|4`
- **变体与属性**
  - `ClockFace.Unit=hour|minute|second`
  - Panel 只保留可达组合:`hour/hour`,`hour/minute`,`hour/second`,`minute/minute`,`minute/second`,`second/second`
  - Control 为 `Size=compact|default × State=default|open|error|disabled`
  - `Precision=hour` 只显示小时段,`minute` 显示 `HH:mm`,`second` 显示 `HH:mm:ss`
  - 当前能力固定 24 小时制,不声明不存在的 AM/PM 模式
- **尺寸与布局**
  - `compact` Trigger 为 136×32px且是默认业务尺寸,`default` 为 144×36px
  - ClockFace 固定 184×184px,Panel 固定 208×284px、padding 12px、垂直 gap 12px
  - TimeSegment 为 48×36px,Separator 为 8×36px
  - 小时外环 1-12 半径 73px、内环 13-23/00 半径 47px
  - 分钟/秒使用 60 个刻度并每 5 个单位显示标签
- **状态与反馈**
  - Segment Hover 与 Default 同视觉,Active/Focus/Disabled/Error 继承 TimeSegment
  - Open 保持 Trigger active
  - Loading 保留面板尺寸并禁用提交
  - 解析错误同时关联 Trigger 和字段错误文字,不得静默回退到当前时间
- **交互与无障碍**
  - 小时 00–23、分钟/秒 00–59
  - 表盘点击或拖动更新当前单位,提交小时后按 Precision 前进到分钟/秒,完成最后单位后关闭
  - 数字段上下键循环步进,聚焦时全选,输入限制两位
  - Control 使用 `aria-haspopup=dialog`,`aria-expanded`
  - Panel 和当前单位有可访问名称,表盘始终提供等价数字输入路径
- **组合边界**
  - 只选择本地时间值,日期、时区与日期时间序列化属于父组件
  - Do:按 Precision 隐藏不需要的段并保留两位格式
  - Don't:内联数字输入、把 clock-face 作为唯一输入方式、在校正完成前声称其视觉已定稿
- **适用场景**
  - TimePicker 内部数字段由 TimeSegment Atom 表达
  - 通用带步进操作的 NumberInput 是另一分子能力,不得与时间段混为一套组件
  - 适用场景:AutoForm time/timeRange 字段

### 日期选择器 DatePicker

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=605-2317&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`DatePicker / Set`=`605:2484`
  - 必须实例化 PickerTrigger、Popover 与 Calendar,不得在 Control 内复制日历网格
  - 解剖为 `Control -> PickerTrigger -> Popover(Calendar + optional Actions)`
- **变量绑定**
  - 继承 PickerTrigger/Calendar 的 semantic Variables,新增值区只使用 `foreground / muted-foreground`
  - Calendar Icon 使用 16px Icon Atom,不得写固定颜色
- **变体与属性**
  - `Mode=single|range`,`State=empty|filled|open|disabled|error|loading`,`Clearable=true|false`
  - `Format`,`Locale`,`Min`,`Max` 为行为属性,不制造视觉 variant
- **尺寸与布局**
  - Trigger 高 36px、宽随 FormField
  - Popover 以 Trigger 起点对齐、offset 4px、高度 hug,单月内容不小于 280px
  - 日期格式变长时 Value 单行省略但保留完整可访问值
- **状态与反馈**
  - Hover/Active/Focus/Disabled/Error/Loading 由 PickerTrigger 表达
  - Open 显示 Calendar 并保持 active
  - 选择完成后按 Mode 关闭或等待范围结束
  - Loading 不清空已选值,Error 不自动改写输入
- **交互与无障碍**
  - Trigger 使用 `aria-haspopup=dialog`,`aria-expanded`
  - 打开后焦点进入当前选中日或今天,Esc 关闭并归还焦点
  - 可读名称同时包含字段标签和格式提示
  - 范围值朗读明确起止日期
- **组合边界**
  - 展示文本按 locale 格式化,提交值按表单契约序列化
  - 禁用日期由 Calendar 接收
  - Do:单日和范围共用底层 Calendar
  - Don't:把 placeholder 当 Label、在展示文本中混入未声明时区、允许键盘选中禁用日
- **适用场景**
  - DatePicker/DateRange 复用 Calendar 与 Popover 基础组件
  - 适用场景:AutoForm date/dateRange 字段

### 日期时间选择器 DateTimePicker

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=605-2485&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`DateTimePicker / Panel`=`605:2488`,`DateTimePicker / Set`=`605:2777`
  - 依赖 PickerTrigger、Popover、Calendar、TimePicker Panel 与 Button
  - 解剖为 `Control -> Trigger -> Popover -> Unified Surface(Calendar + Time controls + Actions)`
  - 左右区域必须保留为下层组件实例,不得拆散重画
- **变量绑定**
  - 完整继承 Calendar/TimePicker/PickerTrigger
  - 统一外层 Panel 使用 `popover / popover-foreground / border`,`radius/md`,`Shadow/md`,子组件根不再重复描边、圆角与阴影
  - CalendarClock、Chevron 与操作图标使用 Icon Atom
- **变体与属性**
  - `Precision=minute|second`,`Format=24h|12h`,`State=empty|filled|open|disabled|error|loading`
  - 日期时间值和 locale/timezone 通过属性输入,不按业务格式复制组件
- **尺寸与布局**
  - Trigger 高 36px
  - Panel 桌面为 548×320px、padding 12px、区间 gap 12px
  - Calendar 与 TimePicker 都由父容器拉伸到 296px 高,当前标本使用 `Weeks=5`
  - Calendar 保持 36px 日期格,TimeSegment 保持 48×36px
  - 若月份切为 6 周,父 Panel 与 TimePicker 同步等高
  - 窄屏纵排并让内容区在安全边距内滚动
- **状态与反馈**
  - 日期与时间子控件分别保留 Focus,但整个值只产生一条 Error
  - Open 保持 Trigger active
  - Disabled 禁用全部子控件
  - Loading 锁定确认并保持已选日期时间
  - 未选完整时确认动作 Disabled
- **交互与无障碍**
  - 打开后焦点进入当前日期
  - 完成日期后可移动到小时段
  - Panel 使用具名 dialog,Tab 在日历、时间和操作区之间移动,Esc 关闭并归还焦点
  - 错误文字通过 FormField `aria-describedby` 关联 Trigger
- **组合边界**
  - 组件必须显式接收 timezone 并区分展示值与提交值
  - 不自行猜测浏览器时区
  - Do:一个面板完成完整值并提供清晰确认
  - Don't:分别打开两个互相遮挡的 Popover、在日期切换时重置已选时间、用模糊格式隐藏时区含义
- **适用场景**
  - DateTimePicker 复用 Calendar 与 TimePicker 交互契约
  - 适用场景:AutoForm datetime 字段

### 颜色选择器 ColorPicker

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=605-2778&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`ColorPicker / Panel`=`695:3720`,`ColorPicker / Set`=`605:2907`
  - 依赖 ColorArea=`695:1123`、ColorPreset=`786:1137`、PickerTrigger、Popover、ColorSwatch、Input、Slider 与 Button Atom
  - 解剖为 `Control -> Trigger(Current swatch + value) -> Panel(SV field + Hue + optional Alpha + format tabs + value input + square presets)`
- **变量绑定**
  - `popover / popover-foreground / background / foreground / muted-foreground / border / input / ring / destructive`,`radius/sm|md|lg|full`,`Shadow/lg`,`spacing/2|3|4`
  - 棋盘透明底与色谱仅限 ColorArea 的功能性色域,其余容器继续绑定 Variables
- **变体与属性**
  - `Format=hex|rgb|hsl`,`Alpha=true|false`,`State=empty|filled|open|disabled|error|loading`
  - preset 数量和值是内容属性,不按颜色创建 variant
  - Swatch 使用 ColorSwatch 实例并同时显示文本值
- **尺寸与布局**
  - Trigger 高 36px
  - Panel 宽 320px、padding 16px、区间 gap 12px
  - SV field 288×180px,Hue/Alpha 288×12px
  - Preset 固定为 24px 方块、8px gap,Selected 内描边不得推挤同行
- **状态与反馈**
  - Open 保持 Trigger active
  - Focus 可依次进入二维颜色场、滑杆、输入和 presets
  - Disabled 禁止编辑
  - Error 指出无效通道或格式
  - Loading 保留当前色和面板尺寸
  - 键盘修改必须与指针结果一致
- **交互与无障碍**
  - 每个通道使用具名 slider/spinbutton 并宣告当前数值
  - 二维颜色场提供方向键操作
  - Swatch 不能成为唯一信息,必须输出颜色名称或规范化值
  - 透明度需要独立可读百分比
- **组合边界**
  - 输出规范化颜色值,主题 token 的保存和对比度校验属于上层表单
  - Do:文本输入、键盘和指针双向同步,预设色只用 ColorPreset
  - Don't:只给不可聚焦画布、仅靠棋盘格表达 alpha、用圆形 ColorSwatch 冒充 presets
- **适用场景**
  - ColorPicker 复用 ColorArea、ColorPreset、ColorSwatch、Slider 与 Input 契约
  - 适用场景:自定义主题及其他精确颜色配置

### 文件拖放区 FileDropzone

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=622-2571&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`FileDropzone / Set`=`622:2664`,`FileDropzone / Queue`=`622:2665`
  - 依赖 Button、Icon、Progress、Badge 与 FileInput/FileRow
  - 解剖为 `Root -> Drop target(Icon + Title + Constraint + Browse action) -> optional Queue(File row + Progress + Status + Remove/Retry)`
- **变量绑定**
  - `background / foreground / muted / muted-foreground / border / input / primary / ring / destructive` 与 status colors,`radius/lg`,`spacing/2|3|4|6`
  - Upload/File/X/RotateCcw 使用 lucide Icon Atom
- **变体与属性**
  - `State=idle|hover|drag-active|disabled|error`,`Mode=empty|queued|uploading|complete`
  - `Multiple`,`Accept`,`MaxFiles`,`MaxSize` 是行为属性
  - Queue 以数据重复 File row,不按文件数建 variant
- **尺寸与布局**
  - Drop target 宽 fill、最小高 160px、padding 24px、2px 虚线 border
  - 图标 24px,文案和约束居中
  - Queue 与 Drop target 间距 12px,文件行最小高 40px,名称可省略但状态和操作位不跳动
- **状态与反馈**
  - Hover/Drag-active 增强 border 和轻量背景
  - Focus 显示 ring
  - Disabled 50% 且拒绝 drop
  - Error 同时给出文字
  - Loading/Uploading 使用 Progress 并禁用重复提交
  - 单文件失败只影响对应行并保留 Retry/Remove
- **交互与无障碍**
  - 隐藏 file input 仍是实际输入
  - Drop target 可聚焦并用 Enter/Space 打开选择器,拖放不是唯一方式
  - 约束、错误和队列状态通过 `aria-describedby`/live region 关联
  - Remove/Retry 包含文件名
- **组合边界**
  - 负责收集与展示本次文件队列,服务器对象管理、裁剪和媒体预览属于上层
  - Do:在选择前展示真实限制并逐文件反馈
  - Don't:把 Dropzone 并入 FileInput API、全局错误抹掉成功文件、只用颜色表示 drag-active
- **适用场景**
  - FileDropzone 复用 FileInput/FileRow 契约并支持上传队列
  - 适用场景:AutoForm 文件字段的拖放、多文件与进度反馈

### 导入弹窗 ImportDialog

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=1327-3898&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`ImportDialog / Control`=`1327:3899`,Section=`1327:3898`
  - 依赖 Dialog Surface、FileDropzone、FileRow、Button 与 Icon
  - 解剖为 `Dialog -> Header(Title + Description + Close) -> Body(optional Template download + FileDropzone + Constraint/Validation + Selected Files viewport) -> Footer(Cancel + Import|Close)`
- **变体与属性**
  - `State=empty|files|error|importing|complete|partial`
  - 属性:`Show template=true|false`,`Title`,`Description`
  - 文件类型、大小、数量、最大行数和模板地址是内容/行为输入,不按业务资源复制组件
  - `empty` 禁用 Import
  - `files` 只表示本地已选择且校验通过,文件尚未上传
  - 点击 Import 才进入 `importing`,依次完成上传、服务端校验和持久化
- **尺寸与布局**
  - 桌面宽 720px、padding 24px、区块 gap 16px
  - Dropzone 宽 672px、高 210px
  - 文件队列为 672×108px 滚动视口,行高 32px、gap 6px,同时展示 3 行并允许继续滚动
  - Footer 占满内容宽并右对齐；本多状态流程使用 120px 同组等宽文本按钮，间距 8px
  - 移动端占安全区内可用宽度,Body 滚动而 Header/Footer 保持可见
- **模板与下载**
  - 模板行位于上传区上方,下载动作使用 `Button / Leading Icon` 的 `link/xs` 实例 + 12px `Icon / download`=`1340:1223`
  - 模板文件名使用明确的链接样式、下划线和下载图标,并包含格式,例如 `Product import template (.xlsx)`,不能呈现为普通 Label
  - 没有模板的业务通过 `Show template=false` 隐藏整行且自动收紧高度,不得保留空占位
- **文件选择**
  - 上传区同时支持点击与拖放,选择前明确 `accept / maxSize / maxFiles`
  - 多文件逐行显示文件名、类型图标和 Remove
  - 队列超过视口高度时滚动,不能无限撑高 Dialog
  - 导入中隐藏 Remove,失败文件保留 Retry/Remove 入口
- **状态与反馈**
  - `files` 使用中性选择 Surface,明确“Files stay local until you click Import”,严禁出现 Upload complete
  - 类型、大小、数量、最大行数、模板列或可预检内容失败进入 `error`,保留队列并显示具体值,例如 `6,240 rows · max 5,000`
  - `importing` 显示上传+校验+持久化进度和 `Importing...`,隐藏文件移除动作
  - 全部成功进入 `complete`,显示导入条数、`Close now` 与“3 秒后自动关闭”
  - 部分失败进入 `partial`,显示成功/失败数和 `Download failed report`,保持弹窗打开且不自动关闭
- **交互与无障碍**
  - `role=dialog`,`aria-modal`,`aria-labelledby`,`aria-describedby`
  - Dropzone 使用真实 file input,约束与错误经 `aria-describedby` 关联
  - 文件队列是具名滚动区域,Remove/Retry 包含文件名
  - 进度使用 `progressbar`/live region
  - Esc/Close 的中止行为由业务在导入中显式决定
- **组合边界**
  - 负责文件选择、客户端校验、上传/导入提交与即时反馈
  - 复杂行级结果表可转入独立结果页
  - Do:标题明确“导入什么”,模板可选,错误保留上下文
  - Don't:无标题直接开文件选择器、把格式限制藏到失败后、文件一多就撑出视口、用 Toast 代替行级错误
- **适用场景**
  - AutoTable 的资源导入统一实例化本组件
  - 字典、商品、门店、账户等资源只覆盖标题、模板、文件/行数限制和接口,共享 `empty -> files -> importing -> complete|partial|error` 状态机与视觉资产

### 地图选点 MapPicker

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=622-2692&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`MapPicker / Canvas`=`622:2739`,`MapPicker / Control`=`622:2826`
  - 依赖 InputGroup/Search、Button、Icon、Popover/Dropdown 和 FormField
  - 解剖为 `Control -> Search + Map Canvas(Marker + Zoom + Locate + attribution) + Coordinate fields/status`
- **变量绑定**
  - `background / foreground / muted / muted-foreground / border / input / primary / ring / destructive`,`radius/lg|md`,`Shadow/sm`,`spacing/2|3`
  - Search/MapPin/Locate/Plus/Minus 使用 lucide Icon Atom,地图瓦片本身不伪装成 design token
- **变体与属性**
  - `Provider=osm|google|amap`,`Mode=edit|readonly`,`State=empty|located|loading|error|disabled`
  - 经纬度、地址、zoom 和 marker 是数据属性,不按地点建 variant
- **尺寸与布局**
  - Canvas 宽 fill、桌面基准高 320px、最小宽 280px并裁切在 `radius/lg`
  - 搜索条高 36px,地图操作目标至少 36×36px,attribution 始终可见
  - 容器响应式缩放但不拉伸瓦片或 Marker
- **状态与反馈**
  - 搜索/定位 Loading 保持旧地图并显示 Spinner
  - Error 在 Canvas 上方或下方给可恢复文字,不能用空白地图代替
  - Disabled/Readonly 禁止改点但保留平移缩放按产品规则
  - Focus 在搜索、地图和控制按钮间清晰可见
- **交互与无障碍**
  - 搜索结果选择、地图点击或拖动 Marker 都更新同一 latitude/longitude
  - 两值必须同时存在或同时为空
  - Canvas 提供具名 region 和文本坐标替代路径
  - 键盘用户可通过坐标字段精确输入,Provider attribution 不可隐藏
- **组合边界**
  - 输出地址候选和坐标,门店表单负责确认可编辑地址及提交
  - Do:Provider 失败时保留可编辑坐标和明确重试
  - Don't:只允许鼠标点图、把搜索结果当已保存地址、在 readonly 态隐藏坐标
- **适用场景**
  - ADR-0007 规定门店 AddressMapField 与 `provider="osm"` 决策
  - 适用场景:MCH 门店表单与详情中的地图选点、地址搜索及坐标编辑

### 多选 MultiSelect

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=622-2827&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`MultiSelect / Set`=`622:2878`
  - 依赖 PickerTrigger/Combobox、Popover、Input、Checkbox、Badge、ScrollArea 与 Icon Atom
  - 解剖为 `Control -> Trigger(Token[] + Search) -> Popover(Listbox(Option[] + optional Select all/Empty))`
- **变量绑定**
  - `background / foreground / muted / muted-foreground / border / input / accent / accent-foreground / ring / destructive`,`radius/md|sm`,`Shadow/md`,`spacing/1|2|3`
  - Check/ChevronDown/X/Search 使用 Icon Atom
- **变体与属性**
  - `State=empty|filled|open|disabled|error|loading`,`Overflow=wrap|summary`,`Clearable=true|false`
  - 选项、已选值与最大数量是数据属性,不得按选项数创建 variant
- **尺寸与布局**
  - Control 最小高 36px并随 token 换行 hug
  - Token 高 24px、最大宽不挤压移除动作
  - Popup 与 Trigger 等宽且最小 220px,列表设明确 max-height 并使用 ScrollArea
  - Summary 使用 `+N`,不改变控制器宽度
- **状态与反馈**
  - Hover/Focus/Error 由 Control 边框/ring 表达
  - Open 保持 active
  - Disabled 禁止打开和移除
  - Loading 在列表中保留稳定高度并显示 Spinner
  - 达到 max 后未选项 Disabled,已选项仍可取消
- **交互与无障碍**
  - 使用 `combobox` + `listbox` 多选模式、`aria-multiselectable`
  - 上下键移动,Enter/Space 切换,Backspace 仅在搜索为空时移除最后一项,Esc 关闭
  - 每个 Token 移除动作和 `+N` 摘要必须有完整可访问名称
- **组合边界**
  - 用于有限或可搜索的多值枚举
  - 远程数据使用 RemoteSelect,复杂实体摘要使用 RelationList
  - Do:选中项在列表与 Trigger 双向同步
  - Don't:把 Checkbox 组和 Token 维护成两份状态、只显示无法朗读的 `+N`、Loading 时清空已选项
- **适用场景**
  - MultiSelect 遵循 ARIA Combobox/Listbox 多选模式
  - 适用场景:AutoForm 可搜索多选字段

### 远程选择 RemoteSelect

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=623-1633&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`RemoteSelect / Set`=`623:1675`
  - 依赖 PickerTrigger/Combobox、Popover、Input、ScrollArea、Spinner、EmptyState 与 Icon Atom
  - 解剖为 `Control -> Trigger -> Popover(Search -> Results -> Loading|Empty|Error -> optional Pagination sentinel)`
- **变量绑定**
  - `background / foreground / muted / muted-foreground / border / input / accent / accent-foreground / ring / destructive`,`radius/md|sm`,`Shadow/md`,`spacing/2|3`
  - Search/Check/Chevron/Refresh 使用 Icon Atom
- **变体与属性**
  - `Mode=single|multiple`,`State=idle|open|loading|empty|error|disabled`
  - query、value、result label/metadata、page 和 debounce 是数据/行为属性,不为接口或实体类型建视觉 variant
- **尺寸与布局**
  - Trigger 高 36px
  - Popup 与 Trigger 等宽、最小 260px
  - Search 固定在顶部,结果区 max-height 240px并滚动,Option 最小高 36px且 metadata 可形成第二行
  - Loading/Empty/Error 区域使用同一稳定结果高度
- **状态与反馈**
  - 打开或 query 变化触发请求
  - Loading 保留现有结果并显示进度,过期响应不得覆盖新 query
  - Error 提供可聚焦 Retry
  - Empty 说明当前 query 无结果
  - Disabled 禁止请求
  - Error/Loading 不清除已选值
- **交互与无障碍**
  - 使用 `combobox`,`aria-controls`,`aria-expanded`,`aria-activedescendant` 和具名 listbox
  - 异步结果数、Loading 与 Error 通过 polite live region 宣告
  - 键盘移动不能聚焦已被新响应移除的 option
- **组合边界**
  - 只负责异步搜索和选择,缓存策略与实体权限由数据层提供
  - Do:debounce、取消过期请求并保留选择
  - Don't:把网络错误显示成“无结果”、滚动触底重复并发、用内部 ID 代替可读 label
- **适用场景**
  - RemoteSelect 遵循异步 Combobox 模式
  - 适用场景:AutoForm 远程实体搜索与选择字段

### 关联列表 RelationList

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=623-1676&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`RelationList / Row`=`623:1703`,`RelationList / Control`=`623:1786`
  - 依赖 RemoteSelect、Avatar/Image、Badge、Button、Separator 与 Icon Atom
  - 解剖为 `Control -> Add relation trigger -> List(Row(Identity + Metadata + optional Status + Actions))`
- **变量绑定**
  - `background / foreground / muted-foreground / border / destructive / ring` 与 status colors,`radius/md`,`spacing/2|3|4`
  - Plus/X/GripVertical/ChevronRight 使用 Icon Atom,实体图像必须实例化 Avatar/Image 资产
- **变体与属性**
  - Row `State=default|hover|focus|disabled|error`,`Action=remove|open|none`
  - Control `Mode=empty|filled|loading|error|disabled`,`Reorderable=true|false`
  - 实体类型和数量是内容属性
- **尺寸与布局**
  - Control 宽 fill
  - Row 最小高 48px、padding 8px、内部 gap 12px,Identity 占剩余宽并允许两行 metadata,状态和操作位固定
  - 列表间 gap 4px
  - 长名称省略但保留完整可访问名称
- **状态与反馈**
  - Hover/Focus 只作用当前 Row
  - Disabled 禁止添加、移除和排序
  - Loading 保留现有行并在增量位置显示 Spinner
  - 单行 Error 就地说明并允许 Retry/Remove,全局 Error 不抹掉成功关系
- **交互与无障碍**
  - 列表使用具名 list
  - 每行操作包含实体名称
  - 可排序时提供键盘上移/下移和 live region 位置反馈,不能只依赖拖拽
  - Remove 后焦点移动到合理相邻行或 Add trigger
- **组合边界**
  - 呈现已选实体及行级操作,实体查找委托 RemoteSelect
  - 复杂表格字段属于 AutoTable
  - Do:稳定实体标识、状态与操作位
  - Don't:把整行做成多个冲突点击目标、只显示内部 ID、移除后让焦点丢失
- **适用场景**
  - RelationList 承载 AutoForm 实体关系与行级操作
  - 适用场景:角色、门店、商品等关联编辑

### PIN 输入 PinInput

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=623-1787&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`PinInput / Set`=`623:1900`
  - 每一格必须实例化 `PinInputCell / Control` Atom
  - 解剖为 `Group -> hidden logical input -> Cell[] -> optional Separator -> optional Status`
  - 视觉分格与单一值模型保持同步
- **变量绑定**
  - 继承 PinInputCell 的 `background / foreground / input / ring / destructive`,以及 `spacing/2|3`
  - Separator 使用现有 Separator Atom,Loading 使用 Spinner Atom
- **变体与属性**
  - `Length=4|6`,`Mask=true|false`,`State=empty|partial|complete|focus|disabled|error|loading`
  - 字符值是内容属性,不得为每个数字创建 variant
- **尺寸与布局**
  - Cell 固定 40×40px,格间 gap 8px
  - 6 位可按 `3 + Separator + 3` 分组,但逻辑值无分隔符
  - Group 宽度稳定,错误/加载状态不能改变 Cell 尺寸
- **状态与反馈**
  - 输入后自动前进,Backspace 在空格时回退,粘贴按顺序分发并截断到 Length
  - Focus/Error/Disabled 继承 Cell
  - Loading 锁定重复提交并保留值
  - Error 可触发短暂整体反馈但必须同时输出文字
- **交互与无障碍**
  - 提供一个整体标签、长度、输入用途与错误关联
  - 避免读屏把每格重复播报为独立字段
  - 使用适当 `autocomplete=one-time-code`/`inputmode`,粘贴和键盘输入必须可用
- **组合边界**
  - 用于 PIN/OTP/一次性验证码,发送、倒计时、重试和服务端校验属于上层认证流程
  - Do:支持完整粘贴并明确当前位
  - Don't:禁用粘贴、用自动提交隐藏错误、在 Mask 模式泄露真实字符
- **适用场景**
  - PinInput 复用 PinInputCell Atom 并遵循 OTP 输入模式
  - 适用场景:KDS/POS 员工 PIN 与 MCH 验证码流程

### 验证码 Captcha

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=623-1901&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Molecule`
  - 资产:`Captcha / Image`=`1044:2955`,`Captcha / Turnstile`=`1048:2957`,`Captcha / Text Click`=`1051:2984`,`Captcha / Slider`=`1054:2993`
  - 依赖 Input、Button、Spinner、Icon 与位图 Surface
  - 四类 Provider 共同输出单次 ticket/status,不得绕过服务端验证
- **结构解剖**
  - `Image = Code Input + Challenge Image + Refresh + Helper`
  - `Turnstile = official Provider Surface`
  - `Text Click = Instruction + Challenge Photo + ordered Markers + Refresh + Helper`
  - `Slider = Instruction + Puzzle Photo/Target + Track/Progress/Handle + Helper`
- **变体与属性**
  - `Provider=image|turnstile|text-click|slider`
  - Image 状态 `ready|loading|error|expired|disabled`
  - 其余 Provider 覆盖 `ready` 及各自 `verifying|selected/dragging|passed|error|expired`
  - ticket、captcha_id、image、expires_in、provider token 和目标坐标是数据属性,不得创建内容 variant
- **尺寸与布局**
  - Image 根 440×66px,Input 260×36px、Challenge 172×44px、gap 8px,保持约 6:4
  - Turnstile 固定 300×65px
  - Text-click 为 360×228px,Photo 336×160px
  - Slider 为 360×272px,Photo 336×144px,Track 336×44px
  - 状态说明固定在轨道下方,不得压住 Handle
- **状态与反馈**
  - Loading/Verifying 使用 Spinner 并锁定重复提交
  - Text-click 以 1/2…编号表达点击顺序
  - Slider 的 Handle/Progress/Target 是受控坐标平面
  - Passed 显示文字与 status icon
  - Error/Expired 必须重置并获取新 challenge
  - Disabled 不可交互
- **第三方校验**
  - Turnstile:Provider Surface 保留官方比例、Cloudflare mark、品牌色和 `Privacy · Help`
  - 产品主题不得改写官方控件
  - 产品只控制外层间距、说明和错误恢复
- **交互与无障碍**
  - Image 不把答案写入 alt
  - Refresh 有独立名称
  - Turnstile 遵循 Provider 焦点/读屏契约
  - Text-click 必须提供无法使用视觉点选时的替代验证路径
  - Slider 同时提供键盘等价操作
  - 状态变化通过 live region 宣告,Error 不只靠颜色
- **组合边界**
  - 设计系统提供四种 Provider 视觉与交互能力,具体页面只启用 PRD/API 明确开放的 Provider
  - Do:明确过期、重试、单次核销和替代路径
  - Don't:在前端伪造通过态、失败后复用旧 challenge、用展示位图泄露真实答案
- **适用场景**
  - SYS 当前开放 Image/Turnstile 双渠道、4 位 `[0-9A-Z]` 去混淆字符、300 秒过期与单次核销
  - Text-click/Slider 作为通用设计能力,只有业务规则和后端接口启用后才可进入具体页面

### 认证登录 Auth Login

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=3849-33902&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - Section `Molecule / Auth Login`=`3849:33902`
  - `Auth / Social Provider Button`=`3849:33911`,Provider=`Google|Apple`
  - `Auth / Links / Control`=`3849:33917`
  - `Auth / Image Captcha Field`=`3849:33933`,State=`Default|Refreshing`
  - `Auth / Login Card`=`3851:13996`,Verification=`Image|Turnstile`,State=`Default|Refreshing`
  - 业务 Screen 只放 `Auth / Login Card` 实例,不得 detach 后重画登录卡
- **结构与尺寸**
  - Desktop Card 固定 440px 宽、32px 内边距;移除副标题后 Image 高 584px、Turnstile 高 583px
  - 主标题固定为`商户管理中心`,不再附加重复的小字说明
  - Google/Apple 同行 184×36px、gap 8px;品牌图形 slot 为 16×16px
  - 认证链接居中纵排、gap 8px:`忘记密码？`在前,`还没有账号？ 注册`在后
- **图形验证码交互**
  - 字段宽 376px,输入/图片为 224×36 / 144×44,gap 8px
  - 常态图片不显示刷新图标;整张图片为点击目标,使用 pointer 手势
  - 点击以 0.1s dissolve 切换 Refreshing;遮罩 60% background + 16px Spinner,0.6s 后返回 Default
- **Turnstile 与页面边界**
  - 官方 Surface 固定 300×65px,在 376px 字段槽中居中;保留 Cloudflare 品牌与 Privacy/Help
  - MCH 登录生产 Section `2404:2778` 同时承载 Turnstile 与 Image 两张 1440×900 Screen,当前验证方式由平台配置决定
  - 运行态 cursor-follow glow 不在 Figma 中用有矩形边缘的静态渐变代替
