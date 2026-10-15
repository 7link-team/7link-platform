# 基础控件（商城资料参考）

> 来源：[design/atoms.md](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/design/atoms.md)；搬入日期：2026-10-14。
> 筛选：保留基础控件规范；商城名称仅为来源示例。
> 本文是参考资料，不是本项目已采用的规则或已实现能力。尺寸、主题、技术栈、字段限制以本项目确认方案为准。
> 原文 SHA-256：`72602f5f90c6f12a3a100abcb66d54b4ab71cb26e8e8b035d58ac76e1178e658`。内嵌 Figma 转为链接；已搬入的资料互链改为本地相对路径，未搬入资料链接回源仓。

原子是设计系统最小、不可再分的控件,直接消费[设计令牌](tokens.md),不含业务逻辑。组件视觉、状态和复用关系以 Figma 为准。

> **状态铁律**:每个交互原子必须逐项说明 **Default / Hover / Active / Focus / Disabled / Error** 六态的视觉与归属。源码无视觉差异的状态复用代表变体,不铺雷同矩阵;状态属于父组合时明确写回父组件。Focus 一律可见焦点环(`--ring`);Disabled 一律 `opacity 50%` + 禁止指针;Error 仅在控件支持 `aria-invalid` 时触发 `--destructive` 边框与环。
>
> 下文尺寸给 modern 的 px 基线值；圆角一律绑定 Dimensions Variables，并在 Light/Dark 下复用同一组件。

## 图标 Icon

界面图标的唯一来源。Icon 本身不承担点击行为；需要交互时必须放入 Button/Toggle 等可访问控件。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=112-2&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;来源:`lucide-react v1.23.0` 原始 24×24 viewBox path,禁止手画替代。
- 解剖:固定正方形 Frame + lucide Vector;默认渲染 16×16,stroke 使用 round linecap/linejoin 并绑定 `foreground`。
- 尺寸:Button `xs/sm/default/lg` 分别使用 12/14/16/16px;普通独立图标默认 16px。只允许等比缩放,不得单轴拉伸。
- 属性:`Icon` 使用 INSTANCE_SWAP;当前基础集包含 Mail、Plus、Search、Settings、LoaderCircle、RefreshCw、ChevronDown、Upload、File、X、Eye、Check、Bell、Minus、Bold、Italic、Underline、AlignLeft、AlignCenter、AlignRight、ExternalLink、ArrowUpDown、ArrowUp、ArrowDown,按真实业务需要增量补充。
- 颜色:默认继承父控件语义前景色;禁止在业务页面硬编码黑/白。危险、成功、禁用等颜色由父组件状态决定。
- 动效:LoaderCircle/RefreshCw 的旋转属于使用方状态,Icon 组件本体保持静态。不得为旋转状态复制另一枚图标。
- 无障碍:装饰图标 `aria-hidden`;纯图标操作必须由父控件提供 `aria-label` 或 Tooltip。图标不能代替必要文字。
- Do:复用 Icon 组件、保持方形、通过 Swap 更换。Don't:拉伸、混用其他图标风格、在页面内粘贴散落 SVG。
- 业务依据:统一使用 Lucide 图标体系;适用场景:Button、MCH 列表自动刷新及其他图标化操作。

## 按钮 Button

主操作与工具操作载体。Figma 中按 Text、Leading icon、Trailing icon、Icon only、Solid states、Quiet states 分成 6 个可复用 ComponentSet,共 138 个真变体,全部位于同一独立预览 Section。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=112-3&footer=false&page-selector=false)

### 生成契约

**层级与依赖**

- 层级:`Atom`。依赖 Semantic Color、Dimensions、Typography、Shadow Variables/Styles 与 lucide `Icon` Atom。
- 解剖:`Container` + 可选 `Leading icon` + `Label` + 可选 `Trailing icon`;纯图标形态只有 `Container + Icon`。
- Figma 资产:`Button / Text`、`Button / Leading Icon`、`Button / Trailing Icon`、`Button / Icon Only`、`Button / State / Solid`、`Button / State / Quiet`。上层只能实例化这些资产,不得重画按钮。

**属性轴**

| 属性 | 值 | 约束 |
|---|---|---|
| `Variant` | `default / secondary / outline / ghost / destructive / link` | 与 `button.tsx` 一致 |
| `Size` | 内容按钮:`xs / sm / default / lg`;纯图标:`icon-xs / icon-sm / icon / icon-lg` | 均对应 24/28/32/36px,命名与 `button.tsx` 一致 |
| `Icon position` | `none / leading / trailing / icon-only` | 图标来自 lucide Component Swap |
| `State` | `default / hover / active / focus / disabled / error / loading` | Solid 覆盖 default/secondary/destructive;Quiet 覆盖 outline/ghost/link |
| `Label` | 任意短文本 | Text property,禁止转曲或拆成图形 |

**modern 最终视觉**

| 变体 | 默认态 | Hover |
|---|---|---|
| `default` | `primary` 实心、`primary-foreground`、pill、`Shadow/button` | `primary/80` |
| `secondary` | `secondary` 实心、`secondary-foreground`、`border`、pill、轻阴影 | `secondary` 混入约 6% `foreground` |
| `outline` | 透明底、`border`、`foreground`、pill | `primary` 底 + `primary-foreground` 字 + 主色边框 |
| `ghost` | 透明无边、`foreground`、pill | `muted` 底 |
| `destructive` | `destructive` 实心、`destructive-foreground`、pill | 危险色增强态 |
| `link` | 透明无边、`primary` 字、`radius/lg` | 文字下划线 |

> 以上以 `themes.css` modern 最终级联为准。它会覆盖 `button.tsx` 中 destructive 的基础浅色样式和 `rounded-lg`,不可只读组件文件得出视觉。

**尺寸与几何**

| Size | 高 | 文字左右 padding | 有图标侧 padding | gap | 字号/行高 | 图标 |
|---|---:|---:|---:|---:|---:|---:|
| `xs` | 24 | 8 | 6 | 4 | 12/16 | 12 |
| `sm` | 28 | 10 | 6 | 4 | 12.8/16 | 14 |
| `default` | 32 | 10 | 8 | 6 | 14/20 | 16 |
| `lg` | 36 | 10 | 8 | 6 | 14/20 | 16 |

- 文字按钮宽度 `Hug`;高度固定并绑定 `size/6|7|8|9`。纯图标按钮宽高相等,对应 24/28/32/36。
- default/secondary/outline/ghost/destructive 的 radius 绑定 `radius/full`;link 绑定 `radius/lg`。
- 图标方形、不拉伸、`shrink-0`;文字与图标整体水平/垂直居中。leading 与 trailing 不同时出现,除非业务明确需要双向提示。

**状态**

| State | 可见变化 | 行为 |
|---|---|---|
| `default` | 基础变体 | 可点击 |
| `hover` | 按上表改变底/字/边框 | 仅指针设备 |
| `active` | 沿 Hover 视觉并向下 1px | `aria-haspopup` 触发器不下沉 |
| `focus` | 边框转 `ring`,外加 3px `ring/50` | `:focus-visible`,键盘可见 |
| `disabled` | 整体 50% opacity | 禁止点击,禁止指针 |
| `error` | `destructive` 边框 + 3px `destructive/20` 环 | 仅 `aria-invalid=true` 时使用 |
| `loading` | lucide LoaderCircle + Loading 文案,50% opacity | 同时 disabled,防重复提交 |

**内容、无障碍与使用规则**

- 默认按钮文案使用明确动词,如“保存”“新增商品”;同一区域只保留一个 `default` 主操作。
- 纯图标按钮必须有 `aria-label` 或 Tooltip;未知图标不可仅凭外形表达含义。
- Loading 时保留原按钮宽度或使用稳定文案,禁止因 spinner 出现造成布局跳动。
- `destructive` 只用于不可逆动作;普通取消使用 outline/ghost。
- Dropdown/Popover 触发器使用 trailing ChevronDown,展开态必须可见;列表自动刷新不是 Button Atom,见 Molecules 的 AutoRefreshControl。

**Do / Don't**

- Do:复用 ComponentSet、绑定 Variables、lucide 图标、Hug 文案、固定高度。
- Don't:把按钮当彩色标签、用手画 SVG、在页面里重画 hover/error、把 split button 伪装成单个 Button。

**业务依据 / 适用场景**:遵循 7link modern 按钮基线,覆盖 MCH 列表与 CRUD 工具栏的命令、提交和图标操作。

## 输入框 Input

单行文本输入控件。Figma 只展示 6 个有视觉意义的代表变体,不把尺寸、内容和无差异状态展开成重复矩阵。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=129-2&footer=false&page-selector=false)

### 生成契约

**层级与解剖**

- 层级:`Atom`;Figma 资产:`Input / Control`。依赖 Semantic Color、Dimensions、`Typography/Body/md`、`Shadow/sm`。
- 解剖:`Container + Text`。Atom 内不含 Label、提示、错误文案、字数、prefix/suffix 或图标。
- 运行时宽度 `w-full`,由父容器控制;Figma 使用 280px 标本宽度验证内容和状态,实例允许横向拉伸。

**属性轴**

| 属性 | 值 | 约束 |
|---|---|---|
| `Size` | `compact / default` | 32/36px;compact 来自 MCH 列表筛选真实 `h-8` 覆盖 |
| `Content` | `placeholder / value` | placeholder 使用 muted foreground;value 使用 foreground |
| `State` | `default / focus / disabled / error` | Hover 与 read-only 复用 Default 视觉,只在行为契约说明 |
| `Text` | 任意单行文本 | Text property;超长截断仅用于 Figma 标本,运行时由 input 横向滚动 |

**尺寸与视觉**

| Size | 高 | 左右 padding | 字号/行高 | 圆角 |
|---|---:|---:|---:|---:|
| `compact` | 32 | 12 | 14/20 | `radius/md`=11.2 |
| `default` | 36 | 12 | 14/20 | `radius/md`=11.2 |

- 背景透明;边框 1px `input`;阴影 `Shadow/sm`;文字使用 `Typography/Body/md`,letter-spacing 0。
- 组件集只包含 `Default/Placeholder`、`Default/Value`、`Compact/Value`、`Focus`、`Disabled`、`Error` 六个组件。未列出的组合通过这些代表变体复用,禁止补成笛卡尔积。
- `text/email/password/search/number/tel/url/date/datetime-local` 等原生 type 共用同一视觉资产,类型不制造 Figma variant。
- `file` 类型沿用同一外壳,浏览器内置 file selector 由实现层处理,不在 Atom 中手画。

**状态**

| State | 视觉 | 行为 |
|---|---|---|
| `default` | input 边框 + Shadow/sm | 可输入 |
| `hover` | 与 default 相同 | 源码没有 hover 视觉覆盖 |
| `focus` | 保留边框/阴影,外加 1px `ring` | `focus-visible`,键盘可见 |
| `disabled` | 整体 50% opacity | 禁止输入,禁用光标 |
| `error` | destructive 边框 + 1px destructive 环 | AutoForm 真实 error class 注入 |
| `read-only` | 与有值 default 相同 | 可选择/复制,不可编辑 |

**组合边界**

- Label、required、hint、error message、maxLength/当前字数归 `FormField` Molecule。
- prefix/suffix 文本、leading/trailing icon、密码显隐按钮、单位、addon 归 `InputGroup` Molecule,必须实例化本 Input。
- MCH AutoForm 的 prefix/suffix 与 error class 是组合来源,不得复制进 Input Atom。

**无障碍与使用规则**

- 必须有可访问名称:`label[for]` 或 `aria-label`;错误通过 `aria-invalid` + `aria-describedby` 关联 FormField 消息。
- `placeholder` 不能代替 Label;数字上下限、step、maxLength 等约束由原生属性和 FormField 文案共同表达。
- `placeholder` 必须直接给出期望对象、格式或可辨识示例,禁止使用`请输入… / 请选择… / 搜索…`等动作句或只机械重复 Label。邮箱可用`name@example.com`,金额可用`0.00`,日期可用`YYYY-MM-DD`;未确认的阈值、枚举和接口结果不得塞入 placeholder。
- Do:父容器控制宽度、复用 Text property、按真实 state 选择 variant。Don't:页面内重画边框/ring、为每种 HTML type 复制组件、把 prefix/icon 绝对定位散落到业务页。

**业务依据 / 适用场景**:统一承载筛选与 AutoForm 单行字段,适用于 MCH 商品、门店筛选及 CRUD 表单。


## 多行输入 Textarea

多行纯文本输入原子。只负责输入表面与值,Label、错误正文、帮助文案和字数统计继续由 FormField Molecule 组合。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=326-495&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;Figma 资产:`Textarea / Control`;依赖 Semantic Color、Dimensions、`Typography/Body/md`。解剖为 `Container + multiline Text`。
- 属性:`Content=placeholder|value`,`State=default|focus|disabled|error`,`Value` 为 TEXT property。Hover 和 Read-only 与 Default 同视觉,不复制雷同变体。
- 几何:标本宽 360px;最小高 80px;上/下 padding 8px、左/右 padding 12px;1px `input` 边框;`radius/md`=11.2px;文字 14/20、letter-spacing 0。
- Default 使用 `background + input`;Placeholder 使用 `muted-foreground`;Value 使用 `foreground`。Focus 使用 `ring` 2px 可见环;Disabled 整体 50% 且禁止输入;Error 使用 `destructive` 边框与环。
- 运行时允许纵向拉伸,但不能低于 80px;内容超出时由浏览器滚动或父布局增长。禁止把固定高度标本当作业务最大高度。
- Label/required/hint/error/maxLength/count 归 FormField;prefix/suffix、工具按钮或格式化控件归对应 Molecule,不得塞进 Textarea Atom。
- 无障碍:必须有 Label 或 `aria-label`;错误态同时设置 `aria-invalid` 和 `aria-describedby`;Disabled 与 Read-only 语义不可互换。
- Do:保持真实换行和可选文本。Don't:用 contenteditable 仿造普通 Textarea、在页面内重画错误边框、让 placeholder 代替 Label。
- Placeholder 写明内容目的并在有依据时附格式或长度,例如`说明退款原因，1–200 字`;不得只写`请输入备注`或`请输入内容`。
- 业务依据:长文本字段需要稳定换行、校验和禁用行为;适用场景:MCH 商品与门店 CRUD 的描述、备注等多行输入。


## 标签 Label

表单控件的可访问名称原子。只负责标签文字和关联控件禁用时的视觉反馈;必填、提示、错误和计数属于 FormField Molecule。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=129-3&footer=false&page-selector=false)

### 生成契约

**层级与解剖**

- 层级:`Atom`;Figma 资产:`Label / Control`;HTML 语义为 `<label>`。解剖只有一个 Text 节点,不得内置 Input、星号、Hint 或 Error。
- 依赖 `Typography/Label/tight` 与 modern `color/foreground`;默认使用 Hug Contents,由实际文案决定宽度。

**属性与状态**

| 属性 | 值 | 规则 |
|---|---|---|
| `Text` | 任意标签文字 | Figma TEXT property;默认单行 Hug,禁止固定窄宽裁字 |
| `State` | `default / disabled` | disabled 对应关联 peer 控件禁用,整体 opacity 70% |

- Default:Inter Medium 14px、行高 14px、letter-spacing 0、`foreground`。
- Disabled:保持同一颜色和排版,只将整体 opacity 调为 70%;运行时同时使用 `cursor:not-allowed`。
- Hover/Active/Focus/Error/Loading 不属于 Label 自身视觉状态。Focus 落在关联控件;Error 与 required marker 由 FormField 组合表达,不得为凑六态复制 Label。

**内容与布局**

- 短文案和长文案使用同一组件与 Text property;容器空间足够时保持单行。确需换行时由父布局限制宽度,行高仍为 14px,不得缩小字号。
- 标签使用字段名称,不写输入格式或错误原因;这些说明进入 Hint/Error。多语言优先完整显示,除非业务规范明确允许省略。
- 必填星号在 FormField Label row 中作为独立 `destructive` 文本实例组合,不写入 Label 文案,避免读屏重复。

**无障碍与使用规则**

- 可见标签通过 `htmlFor` 指向唯一控件 `id`;非原生组合控件使用 `aria-labelledby` 指向 Label 的稳定 `id`。
- `peer-disabled` 要求关联控件与 Label 处于可匹配的 peer 结构;仅改变 Label opacity 不等于真正禁用控件。
- Do:标签简短明确、复用 Text property、让 FormField 管理 required/error。Don't:用 placeholder 代替 Label、把星号硬编码进翻译文案、给 Label 自造 focus/error 边框。

**业务依据 / 适用场景**:MCH/SYS 表单字段统一使用 `text-sm font-medium leading-none peer-disabled:opacity-70`,并由 Label 关联具体控件。


## 下拉选择 Select

离散选项选择原子族。Trigger、Item 与 Popup 分别是可复用资产;Popup 必须由 Item 实例组成。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=326-627&footer=false&page-selector=false)

### 生成契约

**资产与属性**

| 资产 | 属性轴 | 说明 |
|---|---|---|
| `Select / Trigger` | `Size=default|compact`,`Content=placeholder|value`,`State=default|focus|open|disabled|error`,`Value` | 业务表单 36px;紧凑筛选 32px |
| `Select / Item` | `Selected=false|true`,`State=default|focus|disabled`,`Label` | 选中态实例化 Check Icon |
| `Select / Popup` | 无笛卡尔轴 | 由 Select Item 实例纵向组成 |
| `Select / Group Label` | `Label` | 分组选项的可访问组名,32px 高 |
| `Select / Separator` | 无 | 组间 1px `muted` 分隔线,组件总高 9px |
| `Select / Scroll Button` | `Direction=up|down`,`State=default|disabled` | 24px 高,实例化 Chevron 图标 |
| `Select / Grouped Popup` | 组合组件 | 只由 Scroll Button、Group Label、Item、Separator 实例组成 |

- Trigger:宽度由父容器决定;高 36/32px;左右 padding 12px;文字 14/20;1px `input`;`radius/md`=11.2px;`Shadow/sm`;右侧 ChevronDown 16px、opacity 50%。
- Popup:最小宽等于 Trigger;最大高 `min(384px, available-height)`;距 Trigger 4px;碰撞边距 8px;4px 内边距;`popover/popover-foreground`;1px `border`;`radius/md`;`Shadow/md`;超出纵向滚动。
- Item:高 32px;视觉 padding 为 6px 8px,左侧预留到 32px;Indicator slot 16px;Check 16px;`radius/sm`;Focus 填 `accent` 并使用 `accent-foreground`;Disabled 50%。
- Trigger Placeholder 使用 `muted-foreground`;Value 使用 `foreground`;Focus/Open 使用可见 `ring` 环;Error 使用 `destructive` 边框与环;Hover 与 Default 同视觉。Popup 开合为 200ms opacity transition,动效不制造 Figma 变体。
- 统一使用 `SimpleSelect`:Trigger 显示选项 Label,不能显示 raw value。必选型不提供“全部”;筛选型可提供“全部”语义项和外部清除动作。
- Label、Hint、Error 归 FormField;搜索型 Combobox、多选、级联和日期选择属于 Pickers/Molecule,不得用 Select 假装。
- 无障碍:Trigger 暴露当前值、`aria-expanded`、`aria-controls` 和控件关系;箭头键移动、Enter/Space 选择、Escape 关闭;分组使用 group + label;滚动按钮有可访问名称;Disabled Item 不可聚焦;Label 使用 `aria-labelledby` 或关联 id。
- Do:Popup 复用 Item 实例、按标签显示值、保持宽度对齐。Don't:页面内手画菜单、把 Hover 当选中、用 placeholder 代替 Label。
- 业务依据:枚举字段需要一致的选择、键盘与弹层行为;适用场景:MCH 列表筛选与 AutoForm Select 字段。


## 复选框 Checkbox

独立布尔或批量选择原子,明确区分未选、已选与不确定态。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=329-393&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;Figma 资产:`Checkbox / Control`;依赖 Check/Minus Icon 与 Semantic Color/Dimensions。
- 属性:`Selection=unchecked|checked|indeterminate`,`State=default|focus|disabled`。Hover/Active 沿用当前 Selection 视觉;Error 由 FormField 外层表达,Checkbox 本体不伪造红色选中语义。
- 几何:16×16px;1px `primary` 描边;`radius/sm`;Check/Minus 14px。Checked/Indeterminate 填 `primary`,Icon 使用 `primary-foreground`。
- Focus 为 2px `ring` + 2px `background` offset;Disabled 整体 50%、禁止指针且不响应输入。
- 单个 Checkbox 与可见 Label 组合时整行可点,但 Label/描述属于 Field/Option 组合;表格全选的不确定态必须由子项选择状态计算。
- 无障碍:原生 `checked`/`indeterminate` 语义同步;必须有可访问名称;键盘 Space 切换;禁用态使用真实 `disabled`。
- Do:三态明确、Icon 实例化、焦点可见。Don't:用减号表示普通未选、只变图标不更新状态、把 Checkbox 当 Radio。
- 业务依据:布尔、多选和不确定态需要统一语义;适用场景:AutoTable 行选择与 AutoForm 多选字段。


## 单选 RadioGroup

互斥单选原子族。`RadioGroup / Option` 必须实例化 `Radio / Item`,不能在每个选项中重画圆点。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=329-454&footer=false&page-selector=false)

### 生成契约

- 资产:`Radio / Item` 与 `RadioGroup / Option`。Item 属性:`Selected=false|true`,`State=default|focus|disabled`;Option 属性:`Description=false|true`,`Disabled=false|true`,`Label`,`Description`。
- Item 16×16px、1px `primary` 描边、`radius/full`;Selected 显示居中的 10×10 `primary` 圆点;Focus 为 2px环 + 2px offset;Disabled 50%。
- Option 解剖:`Radio Item Instance + Copy`;水平 gap 8px。Label 14/20 `foreground`;可选 Description 12/16 `muted-foreground`;Copy 内 gap 2px。
- Group 默认纵向 gap 8px;整行 Label 可点击。横向排列仅在短选项且父容器有足够宽度时使用,仍保持 8px 以上间距。
- Hover 不改变 Item 图形;Active 等同所选状态;Error 属于整个 Group 的 FormField 消息,不是每个圆点的独立 variant。
- 无障碍:组有可访问名称;方向键在组内移动并选择;Tab 只进入当前可选项;Description 通过描述关系关联;Disabled Option 不可选择。
- Do:同组只允许一个 Selected、Option 复用 Item。Don't:把 Checkbox 排成单选、让副描述替代组 Label、为每个业务选项新建组件。
- 业务依据:互斥选项必须共享单一组语义;适用场景:MCH 商品类型与 AutoForm Radio 字段。


## 开关 Switch

即时生效的布尔设置原子;提交后才生效的选择优先使用 Checkbox。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=329-497&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Switch / Control`;属性:`Checked=false|true`,`State=default|focus|disabled`。
- Track 固定 44×24px、`radius/full`;Thumb 20×20px、`background`、`Shadow/lg`;四周保留 2px。Unchecked Track 使用 `input`;Checked 使用 `primary`,Thumb 从 x=2 移到 x=22,净位移 20px。
- Focus 为 2px `ring` + 2px `background` offset;Disabled 整体 50% 且不可操作。Hover 不改色;颜色和 Thumb 位移使用运行时 transition,不复制动画帧。
- Switch 必须伴随明确 Label;状态文案需要时放在组合层,不能把“开/关”硬塞进 Track。
- 无障碍:使用 `role=switch` 与 `aria-checked`;Space/Enter 切换;异步保存时保持尺寸稳定并由上层反馈 pending/error。
- Do:用于立即切换功能、保持 44×24。Don't:用于多选列表、用颜色作为唯一状态信息、把 loading spinner 画进 Thumb。
- 业务依据:布尔设置需要即时、可读的开关反馈;适用场景:各端即时生效的设置项。


## 滑块 Slider

连续数值或区间选择原子。真实源码轨道为 **8px**,禁止沿用 2px 近似值。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=329-571&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Slider / Control`;属性:`Type=single|range`,`State=default|focus|disabled`。运行时由 `value/defaultValue` 数组长度决定 Thumb 数量。
- Track 高 8px、`secondary`、`radius/full`;Indicator 同高、`primary`;Thumb 20×20px、`background`、2px `primary` 描边。
- 标本宽 360px只用于验证比例;业务实例横向 Fill。Single 示例值 60%;Range 示例 25%–75%,不能把示例百分比固化成组件限制。
- Focus 环只落在当前活动 Thumb;Disabled 整体 50%、Thumb 禁止事件;Hover 不改变 Track。Thumb 交叉规则使用源码 `thumbCollisionBehavior=none`。
- Label、当前值、最小/最大值、单位与刻度属于 SliderField Molecule;Atom 不显示未经要求的 tooltip 或数字。
- 无障碍:每个 Thumb 有独立名称、当前值与 min/max/step;方向键和 PageUp/PageDown 可调;区间双 Thumb 的名称必须可区分。
- Do:连续范围、足够可点击区域、键盘可调。Don't:用于少量离散选项、把 8px 改成 2px、把两个 Thumb 合成一个不可访问图形。
- 业务依据:连续数值输入需要指针与键盘双通道;适用场景:范围、强度和比例调整。


## 双态按钮 Toggle

按压式二态工具按钮。Text、State 与 Icon 分为三个 ComponentSet,图标通过 INSTANCE_SWAP 更换。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=332-670&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Toggle / Text`,`Toggle / State`,`Toggle / Icon`;依赖 Icon、Semantic Color、Dimensions、Typography。
- 属性:`Variant=default|outline|ghost`,`Size=sm|default|lg`,`State=default|hover|pressed|focus|disabled`,`Label`,`Icon` INSTANCE_SWAP。
- 尺寸:sm 32px、px10、12/16;default 36px、px12、14/20;lg 40px、px14、14/20。Icon-only 为对应 32/36/40 正方形,Icon 16px;`radius/md`;文字与图标 gap 8px。
- Default variant 默认透明;Hover 填 `muted`、Pressed 填 `accent`。Outline 默认 1px `input`,Hover/Pressed 填 `accent`。Ghost Hover/Pressed 填 `muted`。Focus 为可见 ring;Disabled 50%。
- Icon-only 仍是 Toggle,不是普通 Icon;必须通过 `aria-pressed` 暴露按压状态。Bold/Italic/Underline 只是 Swap 示例,对齐类图标复用同一资产。
- 多个 Toggle 互斥或成组时属于 ToggleGroup Molecule;组管理单选/多选行为和相邻边框,不得在 Atom 内硬编码。
- Do:表示可保持的模式、稳定尺寸、Tooltip/可访问名称。Don't:把一次性命令做成 Toggle、仅靠底色表达状态、为每枚图标复制 variant。
- 业务依据:可保持的双态模式需要稳定按压反馈;适用场景:编辑与视图工具栏。


## 徽章 Badge

用于状态、分类和紧凑计数的非交互标签。基础 API 与业务状态分成两个稀疏 ComponentSet,可见标本全部是组件实例,不会为凑矩阵重复默认文案。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=129-5&footer=false&page-selector=false)

### 生成契约

**层级与解剖**

- 层级:`Atom`;Figma 主资产:`Badge / Base` 与 `Badge / Status`。依赖 Semantic Color、Dimensions、`Typography/Badge/{default,compact}` 与 `Icon` Atom。
- 解剖:`Container + optional Icon Instance + Label`。Container 水平居中、Hug 宽度、胶囊圆角;Icon 与 Label 都是组件属性,不得在业务页面另贴 SVG 或重画文字。
- `Badge / Base` 保留通用组件 API;`Badge / Status` 归一真实业务页的 utility class 组合。上层页面只能实例化这两个资产。

**属性轴**

| Figma 资产 | 属性 | 值 | 规则 |
|---|---|---|---|
| `Badge / Base` | `variant` | `default / secondary / destructive / outline` | 与 `badge.tsx` 一致 |
| `Badge / Base` | `state` | `default / hover / focus` | 仅交互父容器需要;Badge 默认不可点 |
| `Badge / Status` | `Emphasis` | `soft / solid / compact` | 稀疏组合,不补无业务依据的笛卡尔积 |
| `Badge / Status` | `Tone` | `success / warning / error / info / local / changed / neutral` | 各 Emphasis 的可用 Tone 见下表 |
| 两者 | `Label` | 任意短文本 | TEXT property,Hug 宽度 |
| 两者 | `Show Icon` | `true / false` | BOOLEAN property;默认 false |
| 两者 | `Icon` | Check / Bell / Settings | INSTANCE_SWAP;默认 Check,12px |

`Badge / Base` 是 `4 variant × 3 state = 12` 个真变体。`Badge / Status` 只建以下 15 个源码可证组合:

| Emphasis | Tone | 真实用途 |
|---|---|---|
| `soft` | success / warning / error / info / local / changed / neutral | 表格状态、同步状态、来源标签 |
| `solid` | success / warning / error / info / changed | 强提醒、未读、冲突数和强调状态 |
| `compact` | warning / error / info | 稠密表格中的数字计数 |

**尺寸与排版**

| 形态 | 高 | 水平 / 垂直 padding | gap | 字体 | 图标 |
|---|---:|---:|---:|---|---:|
| Base / Soft / Solid | 22px | 10 / 2px | 4px | Inter Semi Bold 12/16,letter-spacing 0 | 12px |
| Compact solid | 18px | 6 / 0px | 4px | Inter Semi Bold 10/16,letter-spacing 0 | 12px |

- 圆角绑定 `radius/full`;10/6/2/4px 分别绑定 `spacing/2_5 / 1_5 / 0_5 / 1`。高度来自真实 line-height + padding + 1px 边框盒模型,不是随意缩放。
- Base default/secondary/destructive 使用对应 semantic fill + foreground;outline 透明底、1px `border`、`foreground`。
- Hover:有底色的三个 Base 变体使用 fill 80%;outline 与 Default 视觉一致。Focus:2px `background` offset + 2px `ring`,对应 `focus:ring-2 focus:ring-offset-2`。

**业务状态 Variables**

| Tone | Soft background / border / foreground | Solid |
|---|---|---|
| success | emerald 50 / 200 / 700 | emerald 600 |
| warning | amber 50 / 200 / 700 | amber 500 |
| error | red 50 / 200 / 700 | red 600 |
| info | sky 50 / 200 / 700 | sky 600 |
| local | purple 50 / 200 / 700 | 无真实 Solid 来源,不得臆造 |
| changed | orange 50 / 200 / 700 | orange 500 |
| neutral | muted / border / muted-foreground | secondary |

这些颜色统一绑定 `color/status/<tone>/{background,border,foreground,solid}` 与 `color/status/on-solid`,真实 hex 和 alias 见[设计令牌](tokens.md)。

**状态、内容与组合边界**

- Badge 默认是 `<div>` 非交互信息,没有自身 Active/Disabled/Error/Loading 行为。被 Button、Link、DropdownMenuTrigger 等可点击父容器包裹时,Active/Disabled/Error/Loading 与键盘行为由父控件承担,不得复制一套伪 Badge 状态。
- Hover/Focus 变体用于预览交互父容器下的可见结果;纯信息 Badge 不获得 tab stop。状态变化不能只靠颜色,文案必须可理解。
- Label 保持单行短文本;数字计数可用 Compact。长解释、错误正文或操作入口分别归 Alert/FormField/Button 等上层组件。
- 图标只作辅助语义:成功 Check、通知 Bell、设置 Settings。未知图标不单独承担含义;换图必须通过 Icon Swap。

**无障碍与使用规则**

- 普通 Badge 由可见文本提供名称;装饰图标 `aria-hidden`。状态更新需要读屏播报时,由包含它的状态区域设置合适的 `aria-live`,不要给每个 Badge 单独加 live region。
- Do:复用 Base/Status 实例、使用语义 Tone、短文本、真实计数。Don't:把 Badge 当按钮、硬编码 utility 色、为每个业务词新建变体、生成无来源的 Local Solid。

**业务依据 / 适用场景**:业务状态与稠密表格计数统一使用 Badge。标准基线为 22px 高、10×2 padding、Inter Semi Bold 12/16、1px border、12px leading icon,适用于同步、门店权限、品牌等状态展示。


## 头像 Avatar

圆形身份图像原子。尺寸与图片/回退结构由通用组件控制,各端头像风格通过同一个 Artwork Swap 注入。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=129-4&footer=false&page-selector=false)

### 生成契约

**层级与解剖**

- 层级:`Atom`;Figma 主资产:`Avatar / Control`;依赖 Semantic Color、Dimensions、`Typography/Label/md` 与 `Avatar / Platform Artwork/*`。
- 解剖:`Root -> Artwork Instance` 或 `Root -> Fallback Text`。Root 固定正方形、`overflow:hidden`、`radius/full`;Image 与 Fallback 互斥。
- `Avatar / Control` 只有 `4 Size x 2 Content = 8` 个主变体。五端风格使用 Image 变体的 `Artwork` INSTANCE_SWAP,不得再与 Size 相乘制造重复变体。

**属性轴与尺寸**

| 属性 | 值 | 规则 |
|---|---|---|
| `Size` | `sm / default / lg / xl` | 分别绑定 `size/8 / size/10 / size/14 / size/18` |
| `Content` | `image / fallback` | 图片可用时显示 image;加载失败或无图片时显示 fallback |
| `Artwork` | 可替换的头像图像组件 | 仅 Image 变体暴露 INSTANCE_SWAP |
| `Fallback` | 最多 2 个可读字符 | TEXT property;从姓名/账号开头生成,统一大写 |

| Size | 宽高 | 真实用法 |
|---|---:|---|
| `sm` | 32px | SYS/OPS/MCH 顶栏用户菜单 |
| `default` | 40px | 默认资料、列表成员与头像组 |
| `lg` | 56px | 资料摘要与平台风格预览 |
| `xl` | 72px | 强身份展示或资料页 |

- Image:正方形图片完整等比缩放后以 `object-cover` 铺满圆形 Root;不可只缩外框后裁掉原图。更换 Size 时 Artwork 实例与 Root 必须同宽高。
- Fallback:`muted` 背景 + `muted-foreground` 文字;全部尺寸统一使用 Inter Medium 14/20、letter-spacing 0,与 `avatar.tsx` 的 `text-sm font-medium` 一致。
- Root 与 Artwork 的圆角都绑定 `radius/full`;Image Root 不附加默认描边。需要分隔边时由 AvatarGroup 等上层组合覆盖,不得污染 Avatar Atom。

**平台 Artwork 映射**

| 平台 | DiceBear 9.x style | Figma Artwork | 运行时 seed |
|---|---|---|---|
| SYS | `pixel-art` | `Avatar / Platform Artwork / SYS` | email / username / display name |
| OPS | `avataaars` | `Avatar / Platform Artwork / OPS` | operator name |
| MCH | `adventurer` | `Avatar / Platform Artwork / MCH` | merchant email / JWT identity |
| POS | `notionists` | `Avatar / Platform Artwork / POS` | cashier identity |
| KDS | `open-peeps` | `Avatar / Platform Artwork / KDS` | kitchen identity |

- 所有平台共用 `radius=50` 与背景色候选 `b6e3f4,c0aede,d1d4f9,ffd5dc,ffdfbf`;seed 必须稳定,同一账号跨刷新保持同一头像。
- Figma 样本来自与运行代码相同的 DiceBear 参数。样本只验证风格和裁切;业务实例仍按真实账号 seed 生成。

**状态与组合边界**

- Avatar 本身非交互,没有独立 Hover/Active/Focus/Disabled/Error/Loading 视觉。被 Button、DropdownMenuTrigger 或其他可点击容器包裹时,六态和焦点环由父控件承担。
- 图片请求期间保持 Root 尺寸不变;成功后显示 Image,失败后显示 Fallback,禁止因加载切换造成布局位移。
- 多头像叠放、成员计数和分隔描边属于 `AvatarGroup` Molecule;“头像 + 姓名 + 菜单”属于身份菜单组合,均必须实例化本 Atom。

**内容、无障碍与使用规则**

- 信息型头像的 `alt` 使用可识别姓名;相邻文字已完整表达同一身份时可用空 `alt` 避免读屏重复。Fallback 文本不能替代可访问名称。
- 姓名为空时使用平台稳定占位,不得随机生成导致水合或刷新前后不一致。Fallback 不放 Emoji、状态点或操作图标。
- Do:稳定 seed、真实 Artwork Swap、完整等比缩放、失败回退。Don't:按端复制整套 Size 变体、在页面手画头像、把 Focus/Error 画进非交互 Avatar、用 `resize()` 裁切 72px Artwork。

**业务依据 / 适用场景**:用户身份在 SYS 用户列表及各端用户菜单中保持统一头像、平台图形与回退规则。


## 分隔线 Separator

相邻内容的视觉分隔原子,不承担容器或布局职责。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=333-417&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Separator / Rule`;属性:`Orientation=horizontal|vertical`。
- Horizontal 固定高 1px、宽度 Fill;Vertical 固定宽 1px、高度 Fill;颜色绑定 `border`。Figma 纵向标本 60px,内联文本常用 20px,二者不构成新 variant。
- 无 Hover/Active/Focus/Disabled/Error/Loading 状态。运行时 `decorative=true` 时不进入可访问树;若确有结构分区语义,由实现层使用 separator role。
- Do:分隔同级内容、由父布局决定长度。Don't:用 Separator 制造外边框、堆叠多条线、用空 div 模拟间距。
- 业务依据:同级内容需要非交互的视觉分隔;适用场景:列表、菜单、表单与工具区分组。


## 骨架屏 Skeleton

加载占位形状原子。Shape 只定义视觉几何;完整列表/表单骨架由上层按真实内容结构实例化组合。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=333-441&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Skeleton / Shape`;属性:`Shape=line|avatar|control|block`。代表尺寸:Line 320×16、Avatar 48×48、Control 280×36、Block 320×160。
- 全部填 `muted`;Line/Control/Block 使用 `radius/md`;Avatar 使用 `radius/full`;无描边、文字或图标。
- 运行时 `animate-pulse`;动画不是 Figma variant。遵循 reduced-motion 时可停用脉冲但保持占位尺寸。
- 业务骨架必须匹配最终内容的稳定宽高和层级,避免加载结束布局跳动。不同宽度用实例缩放/父约束,不为每个页面新建 Shape。
- 无交互和焦点;加载区域的可访问状态由父容器 `aria-busy`/状态文案承担,Skeleton 自身对读屏隐藏。
- Do:按目标内容结构组合、保持布局稳定。Don't:把整页涂成一块、在 Skeleton 上放真实文字、永久显示脉冲。
- 业务依据:加载期间保持目标内容布局稳定;适用场景:CRUD 列表、表单和详情占位。


## 进度条 Progress

确定性任务进度原子。真实源码轨道为 **8px**,只展示有数据意义的完成比例。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=333-485&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Progress / Control`;代表属性:`Value=0|25|50|75|100`。运行时接受 0–100 连续值,代表值不是离散限制。
- Track 高 8px、宽度 Fill、`secondary`、`radius/full`;Indicator 同高、`primary`、从左起增长;无 Thumb、边框或阴影。
- Indicator 宽度变化 300ms `ease-in-out`;Reduced motion 时允许直接跳到新值。未知进度不能伪造百分比,应使用 Spinner/Skeleton。
- Label、百分比、任务名称、取消/重试属于 ProgressField/上传任务 Molecule,本 Atom 只保留轨道。
- 无障碍:父实现提供 progressbar role、`aria-valuemin=0`,`aria-valuemax=100`,`aria-valuenow`;完成后由任务区域播报结果。
- Do:上传/导入等可量化任务。Don't:把 8px 改成 2px、把 Slider 当 Progress、用颜色以外无任何完成信息。
- 业务依据:可量化任务需要持续表达完成比例;适用场景:上传、导入与批处理进度。


## 表格原语 Table primitives

表格的最底层复用资产。Header Cell、Cell、Row、Caption、Scrollbar Thumb 与 Shell 逐级实例化,后续 AutoTable Module 只能从这些原语继续组合。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=334-704&footer=false&page-selector=false)

### 生成契约

**层级与资产**

| 资产 | 属性轴/结构 | 规则 |
|---|---|---|
| `Table / Header Cell` | `Type=text|sortable|numeric|sort-none|sort-asc|sort-desc|selection-unchecked|selection-indeterminate`,`Label` | 48px 高;排序实例化 Arrow 图标;选择实例化 Checkbox |
| `Table / Cell` | `Type=primary|text|numeric|status`,`Label`,`Metadata` | 69px 业务标本;Status 实例化 Badge |
| `Table / Row` | `Kind=header|body|footer`,`State=default|hover|selected` | 只由 Header Cell/Cell 实例组成 |
| `Table / Cell Content` | `Type=primary|text|numeric|status|image|icon|qrcode|area|actions|selection|custom` | 内容职责独立;图片/Badge/Icon/QRCode/Button/Checkbox 均为嵌套实例或真实媒体资产 |
| `Table / Cell Container` | `Density=compact|default|comfortable`,`Align=left|center|right`,`Content` INSTANCE_SWAP | 48/56/72px;将密度和对齐从内容类型中解耦 |
| `Table / Configurable Row` | `State=default|striped|hover|selected|disabled` | 1080px 标本宽;每行恰含 6 个 Cell Container 实例 |
| `Table / Caption` | `Text` | 表格下方说明/计数 |
| `Table / Scrollbar Thumb` | 固定视觉原语 | 横向溢出时持续可见的 6px thumb |
| `Table / Shell` | Header/Body/Footer Row + Caption + Thumb | 可见预览只实例化 Shell |

**几何与视觉**

- Header Row/Cell 高 48px;兼容 Body/Footer 标本高 69px;新 Container 密度为 48/56/72px,水平 padding 16px。AutoTable 必须选择 Container density,不得另写页面级行高。
- Header 文字 `Typography/Label/md`、`muted-foreground`;Body 14/20 `foreground`;Primary Cell 为主文字 + 12/16 metadata、内部 gap 2px;Numeric 右对齐。
- Row 底部 1px `border`;Default 透明;Hover `muted/50`;Selected `muted`;Footer `muted/50` + medium 语义。最后一行可由实现层移除 border。
- Status Cell 必须实例化 `Badge / Status`;排序图标必须实例化 Icon;QR Cell 必须实例化 `QRCode / Control`;Actions 与 Selection 分别实例化 Button、Checkbox。Image 使用真实 `object-cover` 媒体填充,禁止占位色块冒充成品。
- Shell 宽度由父容器决定、可横向滚动。源码为 Safari 提供持续可见的 6px overlay scrollbar thumb;Sticky Header 由 scroll container 的 max-height 与实现层处理。

**行为与组合边界**

- Header/Body/Footer/Caption 是 HTML table 语义映射;Row Hover 不能替代选择态;Selected 必须同步 `data-state=selected` 和选择控件。
- `aria-sort=none|ascending|descending` 与 Header sort variants 一一对应;Header Checkbox 覆盖 unchecked/indeterminate,全选结果由 AutoTable 管理。
- 排序、筛选、列显隐、分页、批量操作、空态和 Loading 属于 AutoTable Module;本层只提供稳定原语。业务页不得直接拼散落 Cell 绕过 Configurable Row/AutoTable。
- 单元格内容长时优先定义列宽和截断/换行规则;操作列保持稳定宽度;表头与对应列必须共享同一宽度约束。
- 无障碍:使用真实 table/thead/tbody/tfoot/th/td/caption;Sortable Header 暴露 `aria-sort`;行操作有可访问名称;横向滚动区域键盘可达且不吞掉表格导航。
- Do:按层实例化、保持列对齐、用 Badge 表状态。Don't:每页重画表格、用 Row Hover 表示选中、把分页/筛选塞进 Table Atom。
- 业务依据 / 适用场景:Table primitives 支撑 AutoTable 与 MCH 商品、门店列表。表头 48px、Cell padding 16px、Row Hover `muted/50`;AutoTable 业务行高约 65px。

## 链接 Link

行内导航与外部资源跳转原子。命令行为使用 Button,不得用 Link 假装按钮。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=386-802&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Link / Text`、`Link / State`、`Link / External`;依赖 Typography、Semantic Color 与 `Icon / externalLink`。
- 属性:`Emphasis=default|muted`,`State=default|hover|active|focus|disabled|error`,`External=true|false`,`Label`。外部图标固定 12px、位于文案后、gap 4px。
- Default 使用 `primary`;Hover/Active 显示下划线;Focus 使用可见 `ring`;Disabled 50% 且不可导航;Error 仅用于失效或危险目标,使用 `destructive`。
- 文案必须描述目标,禁止“点击这里”;外链保留可见图标并按安全策略设置 `rel`;当前页导航用 `aria-current=page`。
- Do:用于页面/资源导航。Don't:触发保存、删除、弹窗等命令;不要把整段正文染成链接色。
- 来源:PRD 导航/帮助链接、通用 WAI-ARIA Link 模式、lucide ExternalLink。

## 加载指示 Spinner

未知时长的小范围加载原子;页面骨架优先 Skeleton,可量化任务使用 Progress。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=386-907&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Spinner / Control`;属性:`Size=sm|default|lg`,`Tone=default|muted|inverse`。
- 尺寸 12/16/24px,方形固定;视觉为 lucide LoaderCircle,stroke 继承父语义色,运行时线性旋转,Reduced Motion 下保留静态可识别形态。
- Spinner 本体不含“加载中”文字;Button Loading、字段加载、页面加载由父组件提供稳定尺寸和可访问名称。
- 无障碍:装饰性 spinner `aria-hidden`;独立加载区使用 `role=status`、可本地化隐藏文本;不可重复播报。
- 来源:Button Loading、48 篇 PRD Loading 状态、通用进度反馈模式。

## 键盘键 Kbd

命令面板、快捷操作与可访问提示中的按键标记。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=386-952&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`Kbd / Key`;属性:`Size=sm|default`,`Label`;依赖 `Typography/Mono/sm`、border/muted/radius Variables。
- 单键最小 24×24px,Hug 文案,水平 padding 6px;组合键由父 Molecule 以多个 Kbd 实例和分隔文本组合。
- 文案使用平台真实符号或键名,不可作为可点击按钮;读屏文案应展开符号含义。
- 来源:CommandPalette/全局搜索 PRD 与通用快捷键模式。

## 状态指示 StatusIndicator

连接、同步、任务与设备状态的最小视觉标记。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=386-1055&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`StatusIndicator / Dot`;属性:`Tone=success|warning|error|info|neutral`,`Size=sm|default|lg`,`Pulse=true|false`。
- 点直径 6/8/10px,展示容器稳定为 20px;颜色绑定 Status Variables。Pulse 仅表达进行中/在线心跳,静态状态不得无意义闪烁。
- 本体不承担完整语义;父组件必须提供状态文字或 `aria-label`,不能只靠颜色区分在线/离线/重连。
- 来源:POS/KDS online/offline/reconnecting、任务同步与 PRD 状态机。

## 颜色样本 ColorSwatch

当前颜色值、状态色和图例使用的圆形色块;不是可点击的 preset 色格。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=389-840&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`ColorSwatch / Base`、`ColorSwatch / State`;用于当前颜色摘要、状态色和图例。
- Base 属性:`Tone=primary|success|warning|error|info|neutral`,`Size=sm|default|lg`,尺寸 16/24/32px,圆形绑定 `radius/full`。
- State 属性:`default|hover|selected|focus|disabled`;Selected 显示明确选中环,Focus 使用 `ring`,Disabled 50%。Active 与 Selected 同归选择语义。
- 父组件必须同时显示可访问颜色名称/值;色块不能单靠颜色传递业务含义。ColorPicker 的 presets 必须改用下方方形 `ColorPreset`,不得混用圆形 ColorSwatch。

## 预设颜色 ColorPreset

ColorPicker 中用于快速选色的固定方形色格。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=783-1219&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`ColorPreset / Control`=`786:1137`;属性:`Tone=primary|success|warning|error|info|neutral`,`State=default|hover|selected`。
- 固定 24×24px,绑定 `spacing/6`;方形轮廓绑定 `radius/sm`,不得使用 `radius/full`。Default 使用 border,Hover 使用 ring,Selected 使用 2px 内描边,三态不得改变外部尺寸。
- ColorPicker preset 行只实例化 ColorPreset;当前值摘要仍可使用 ColorSwatch。自定义颜色值由上层内容属性注入,不得为每个色值创建 variant。
- 每个色格提供颜色名称或规范化值作为可访问名称;键盘以方向键移动,Enter/Space 选择,选中态不能只靠颜色差异表达。

## 主题预览 ThemePreview

主题选择器共享的不可交互缩略图资产,同时服务紧凑菜单和大卡片矩阵。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=868-1219&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`ThemePreview / Thumbnail`=`876:1137`;属性:`Theme=modern|business|eyeCare|pinkKawaii|neoBrutalism|custom`,`Size=compact|card`。
- `compact` 固定 24×24px,由背景、边框和 10px 主色块组成;`card` 固定 176×96px,由侧栏、顶栏、主操作和内容线组成。两档表达同一主题,不得各自编色。
- 全部颜色和圆角继承 `Theme Preview` Variables collection 的对应 mode;缩略图自身不可点击,Hover/Active/Focus 由 ThemeSwitcher Item/Card 父组件承担。
- ThemePreview 只表达主题识别,不承诺业务页面已完成该主题适配;可访问名称由父选择项提供完整主题名称。

## 日历日期 CalendarDay

Calendar/DatePicker/DateRangePicker 唯一可复用日期单元。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=389-924&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`CalendarDay / Control`;固定 36×36px;属性:`State=default|hover|focus|today|selected|range-start|range-middle|range-end|outside|disabled`,`Label`。
- Today 使用可见内描边;Selected/Range 起止使用 primary 实心;Range middle 保持连续背景;Outside 与 Disabled 降低前景但语义不同。
- Calendar Molecule 必须只实例化 CalendarDay;禁止在月历中重画数字格。键盘覆盖方向键、Home/End、PageUp/PageDown、Enter/Space。
- 来源:`pickers/calendar.tsx` 的 DayPicker 几何、DateRange PRD 与 WAI-ARIA Grid 模式。

## 时间分段 TimeSegment

TimePicker 面板中的小时/分钟/秒数字输入单元;完整 TimePicker 属于 Molecule。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=397-761&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`TimeSegment / Input`、`TimeSegment / Separator`;真实尺寸 48×36px(`w-12 h-9`),Separator 8×36px。
- Input 属性:`State=default|active|focus|disabled|error`,`Value`;Roboto Mono Medium 14/20、tabular nums、居中、`radius/md`、`input` border。
- Active 使用 `Shadow/sm`;Focus 为 Active + `ring`;Error 使用 `destructive`;Hover 与 Default 同视觉。上下键步进并循环,聚焦时全选两位数字。
- 小时范围 00–23,分钟/秒 00–59;冒号必须实例化 Separator。TimePicker 不得内联数字框或冒号。
- 来源:`pickers/time-picker.tsx` 的 `NumberInput`、TimePicker Demo 与 AutoForm time/timeRange。

## PIN 单元 PinInputCell

PIN、OTP、邮箱验证码的单字符输入原子;4/6 位分组属于 Molecule。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=403-771&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`PinInputCell / Control`;固定 40×40px;稀疏轴:`Content=empty|value|masked`,`State=default|focus|error|disabled`,`Value`。
- 文字为 `Typography/Mono/sm`;Masked 使用居中圆点;Focus/Error 分别使用 ring/destructive;Disabled 50%。Hover 与 Default 同视觉。
- 单元只容纳一个字符;父 PIN/OTP Molecule 负责 4/6 位数量、自动前进/回退、粘贴分发、错误抖动、锁定与自动提交。
- 无障碍:视觉分格不能造成逐格冗长播报;父控件提供整体名称、长度和错误关联。
- 来源:KDS/POS 员工 PIN、MCH 找回密码 6 位验证码及通用 OTP 模式。

## 二维码 QRCode

订单、桌台、分享与 AutoTable QR 列使用的真实可扫描二维码原子。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=409-787&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`QRCode / Artwork`、`QRCode / Control`;属性:`Size=cell|table|default`,对应 56/72/128px。
- Artwork 必须由 QR 编码器生成,不可手画随机格。业务基线采用 `qrcode@1.5.4`,`margin:1`,`errorCorrectionLevel:'M'`,标本 payload 为 `https://7link.app/order/demo`。
- 黑/白固定绑定 Primitive Variables,保留完整 quiet zone;不同尺寸实例化同一 Artwork 并等比缩放,不得裁切、拉伸或叠 logo 破坏纠错能力。
- Loading/Expired/Retry 属于 QRCodeCard/Result Molecule;Table QR content 必须实例化 `Size=cell`,页面主二维码使用 `default`。
- 无障碍:同时提供目标文字、复制/打开动作与二维码用途说明;二维码不能成为唯一访问路径。

## 时钟选项 ClockOption

ClockOption 是 TimePicker 列表中的单项原子，承载小时 / 分钟候选值及默认、悬停、选中、焦点和禁用状态。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=694-1123&footer=false&page-selector=false)

## 操作系统图标 OS Icon

设备列表和详情使用统一的 Windows、macOS、iOS、Android 图标变体，不以文字首字母或临时图片替代。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=3627-4019&footer=false&page-selector=false)

### 生成契约

- 层级:`Atom`;资产:`OS Icon / Control`;变体:`Windows / macOS / iOS / Android`。
- 图标和底色必须绑定设计系统 Variables；页面只实例化对应变体，不手绘或上传另一套平台图标。
- 用于设备信息时保持固定命中尺寸，平台名称和版本仍由相邻文本提供，图标不是唯一识别信息。

## 色域 ColorArea

ColorArea 是 ColorPicker 的二维色彩输入平面；位置本身表达饱和度与明度，属于受控坐标平面例外。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=695-1123&footer=false&page-selector=false)


**来源提示**
**规范约束的是尺寸/圆角/状态/图标/行为**:px 为默认主题基线值,圆角与色彩随主题令牌变化。组合用法见[分子](molecules.md)。
