# 页面级通用模块（商城资料参考）

> 来源：[design/modules.md](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/design/modules.md)；搬入日期：2026-10-14。
> 筛选：保留 Topbar、Sidebar、筛选条、AutoTable、AutoForm；去掉 MCH 订单紧凑顶栏。
> 本文是参考资料，不是本项目已采用的规则或已实现能力。尺寸、主题、技术栈、字段限制以本项目确认方案为准。
> 原文 SHA-256：`fd4cb27f501f1deafb3a08dd1472a48df43b1f527b80f64093d4f6e66761d698`。内嵌 Figma 转为链接；已搬入的资料互链改为本地相对路径，未搬入资料链接回源仓。

**模块(Modules)对应 Atomic Design 的第三阶段 Organisms**:由原子与分子组合成、能独立承载一段完整界面区域的**面向业务场景的复杂组件**。这类组件多为 **schema / 配置驱动**——管理后台的「增改查」页面几乎全部由它们生成,避免逐页手写。

> Module 只做跨分子的配置驱动、数据编排与持续页面区域;局部完整交互(下拉菜单、选择器、弹层)一律实例化 [Molecules](molecules.md)。模块如何拼成完整页面见[模板 Templates](templates.md);资源装配见 [Auto CRUD](auto-crud.md)。

## 顶栏 Topbar

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2312-8576&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Module`
  - Figma 资产:`Topbar / Control` 与子组件 `Topbar / BrandSwitcher Trigger|Panel`、`Topbar / ScopeSwitcher Trigger|Panel`、`Topbar / NotificationBell(+Panel)`、`.Topbar / NotificationItem`
  - 实例化 LanguageSwitcher、ThemeSwitcher、UserProfile、DropdownMenu、InputGroup、Badge、Separator、Icon;不得在顶栏内复制这些分子的菜单
- **尺寸与布局**
  - 总高 57px = 内容行 56px + 底边线 1px,横向内边距 24px,`sticky top-0`
  - 左组间距 24px:品牌字标(18 Semi Bold)→ 品牌切换器 → 主体切换器
  - 右组间距 8px:通知铃 → 语言 → 主题 → 用户
  - 切换器触发器为 36px 高胶囊(圆角 full、边线 input、Shadow/button),内含 16px 图标 + 14px 文本 + 上下箭头(50% 透明)
- **变体与属性**
  - `Platform=MCH|SYS`:MCH 含品牌/主体切换器与通知铃;SYS 仅字标 + 语言/主题/用户
  - BrandSwitcher:`State=default|hover|open`,`Brand` TEXT;面板 224px,含品牌列表(选中项对勾)、分隔线、创建品牌、加入品牌
  - ScopeSwitcher:`Mode=hq|store × State=default|hover|open`,`Label` TEXT;hq 图标为楼宇、store 为店铺;面板 280px,含品牌总部、门店搜索框(放大镜)、门店列表、新增门店
  - NotificationBell:`State=default|hover|open`,`Unread` BOOLEAN + `Count` TEXT;红点 16px 圆形绝对定位右上,数字 10px 白字;面板 384px 为通知条目列表
  - NotificationItem:`Icon` SWAP(冲突=盾牌警示/强制下发=闪电/强制清除=删除)、`Title/Body/Time` TEXT、`Unread` BOOLEAN(未读=solid info 徽章)
- **状态与反馈**
  - 触发器 hover/open 背景切换为 `accent`;铃铛为幽灵按钮语义
  - 品牌数 ≤1 时隐藏品牌切换器;门店岗隐藏「品牌总部」选项
- **交互与无障碍**
  - 切换器为 `menu` 语义,面板项可键盘上下移动、Enter 选择
  - 通知未读数为 `aria-live=polite` 更新
  - Do:各端共用同一 Topbar 组件,slot 差异只经 `Platform` 变体表达
  - Don't:在屏幕里内联重画顶栏、把语言/主题菜单复制进顶栏
- **组合边界**
  - 用户菜单内容由 UserProfile Molecule 承载;顶栏只编排位置与权限
- **适用场景**
  - 各端后台外壳的持续顶部区域;移动端形态由 Templates 层裁剪

## 侧栏 Sidebar

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2316-8838&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Module`
  - Figma 资产:`Sidebar / Item`(7 变体)、`Sidebar / Control`
  - 项图标实例化 Icon Atom(SWAP),不得内嵌位图
- **尺寸与布局**
  - 侧栏固定宽 240px,内边距 12px,右边线 `sidebar-border`,背景 `sidebar`
  - 一级项宽 216px、二级项 196px;项内 `px-12 py-8 gap-8`、圆角 md
  - 二级缩进 = 外层左内边距 12px + 左边线 1px + 内层左内边距 8px
  - 分组标题 12px、60% 前景色、左缩进 12px;组间距 16px、项间距 4px
- **变体与属性**
  - `Depth=1|2 × State=default|hover|active|parent-closed|parent-open` 仅保留可达组合(7 个):二级无图标、无父项态
  - `Label` TEXT + `Icon` SWAP
- **状态与反馈**
  - **激活判定为纯函数**:当前路径与各项 `href` 前缀匹配,取最长命中项
  - 仅叶子项获得实心高亮(`sidebar-primary` 底 + `sidebar-primary-foreground` 字 + Medium)
  - 含激活子项的父级仅加粗并自动展开(箭头向下);收起父级箭头向右(14px)
  - hover 背景 `sidebar-accent`
- **交互与无障碍**
  - 可折叠父项为 `button[aria-expanded]`;叶子为链接
  - 权限过滤递归剔除无权项并裁掉空父级
- **组合边界**
  - 分组/层级结构由导航配置驱动;头部品牌区为可选 slot(默认由 Topbar 承载品牌)
- **适用场景**
  - 各端后台左侧持续导航;多端共享导航由统一配置生成,不在各页硬编码

## 表格筛选条 CommonTableFilter

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2319-8993&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Module`
  - Figma 资产:`CommonTableFilter / Field`(6 类型变体)、`CommonTableFilter / Control`
  - 实例化 Select(操作符与枚举值)、Input、DatePicker/DateTimePicker 触发器、Button、RefreshSplitButton
- **尺寸与布局**
  - 条容器:`wrap items-end gap-12 p-12`,圆角 md、边线 border、背景 `muted` 30%
  - 字段 = 标签(12px 弱化)+ 控件行(间距 6px):操作符下拉 56×32(文本居中、隐藏箭头)+ 值控件
  - 值控件宽:文本/数字/枚举/日期 160px、日期时间 190px;`between` 为双日期 + `~` 分隔
  - 右侧动作组:搜索(实心)/ 重置(描边)/ 自动刷新分裂按钮
- **变体与属性**
  - `Type=string|number|select|date|datetime|between`;`Label` TEXT;操作符与值控件为暴露实例
  - 操作符全集 10 个:`= ≠ ≈ ≉ > ≥ < ≤ ∈ ⟷`;字段可配置可用子集,仅一个操作符时隐藏操作符下拉
- **状态与反馈**
  - 本地草稿:输入不触发查询,仅「搜索/重置」提交;空值提交时丢弃
  - 自动刷新:手动刷新 + 间隔菜单(5/10/15/30/60 秒),启用时图标旋转并显示倒计时
- **交互与无障碍**
  - 操作符下拉有完整词义 title(如 `≈`=包含);枚举首项为「全部」
  - Do:字段类型 → 控件映射固定,由列表 schema 驱动
  - Don't:在页面另拼筛选布局、把筛选值写死在表格组件里
- **组合边界**
  - 只负责生成查询条件;查询执行、分页重置由 AutoTable 承担
- **适用场景**
  - 全部 CRUD 列表页顶部筛选;字段与操作符由资源配置声明

##### 字段类型 → 控件矩阵

| Type | 值控件 | 默认操作符 |
|---|---|---|
| `string` | 文本输入 160×32 | `≈` |
| `number` | 数字输入 160×32 | `≥` |
| `select` | 枚举下拉(全部/选项) | `=` |
| `date` | 日期选择器 160×32 | `=` |
| `datetime` | 日期时间选择器 190×32 | `≥` |
| `between` | 双日期 + `~` | `⟷` |

## 数据表 AutoTable

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2325-9533&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Module`
  - Figma 资产:`AutoTable / Control`(三态)+ 私有 `.AutoTable / Header Row`、`.AutoTable / Body Row`、`.AutoTable / Pagination Bar`
  - 全部单元格实例化 Table 原语(Header Cell / Cell Container / Cell Content);实体列表固定保留 120px `pin:right` 操作列。只读资源至少逐行实例化`查看详情`;可编辑资源按 PRD 权限追加编辑/克隆/删除/上架切换等动作。多操作使用 24px IconButton + 4px gap 的紧凑组,每项具 tooltip/aria-label;行内删除默认中性 ghost,hover/focus 与确认阶段再显示 destructive 红色并进入确认
- **布局模型(占满剩余高度,仅 items 区滚动)**
  - 整张 AutoTable 是**纵向 flex 容器**,占据父容器(列表页 Template)的**剩余高度**(`flex-1` + `min-h-0`),**不是**固定 `maxHeight`——因此不管数据多少,分页 footer 恒定钉在可视区底部。
  - 容器分三段,自上而下:**① 表头(固定顶,不滚)→ ② items 区(`flex-1`,唯一滚动区)→ ③ 分页 footer(固定底,始终可见,不滚)**。
  - **只有 items 区滚动**,且四向:行多则纵向滚、列多则横向滚(横向时左右冻结列不动)。表头、footer、左右冻结列都不随 items 滚动;设计稿必须显示与当前 overflow 方向一致的 track + thumb,不得只裁切内容。
  - items 区 `overflow:auto` + `min-h-0`;高度由 flex 分配,不写死行数。空/少数据时 items 区仍撑满剩余高度,footer 依旧贴底;滚动条属于 items 区,不得画到表头或分页上。
- **尺寸与布局**
  - 表格容器:圆角 md + 边线 + 裁切;表头行 48px、`muted` 50% 底、底边线;数据行 72px、底边线
  - **首列固定为本页本地序号 `#`**:48px、从 1 递增、`pin:left`,不是业务 ID,所有资源列表均不得省略。实体列表末列固定为 120px `操作`、`pin:right`,只读列表至少提供`查看详情`;中间列以基准宽起算并通过 grow 平摊剩余宽度。最终宽度还要按视觉角色成组:图片/序号/操作固定,名称/描述等主识别列可更宽,来源/状态/调整等同类短字段优先等宽,不得留下无语义的零碎宽差。范本基准为 图片 88(缩略 44×44)/ 名称 320 / SKU 160 / 分类 140 / 价格 120(右对齐)/ 状态 100 / 来源 100 / 更新时间 180 / 操作 120。
  - 分页条:左「共 N 条」,右「上一页 · 页码/总页 · 下一页」;不可用态 50% 透明
- **变体与属性**
  - `State=data|loading|empty|scrolled`;分页条暴露 `Total`/`Page` TEXT
  - 列排序为表头单元格变体:`sort-none → sort-asc → sort-desc` 三态循环,单列生效;默认按更新时间倒序
- **粘性滚动(四向冻结,本模块自带能力)**
  - 高度来自父容器 flex 剩余空间(见「布局模型」);items 区横向溢出时唯行内容滚动,表头/footer/左右列四处保持冻结
  - **表头 sticky 顶**:纵向滚动 items 时表头行始终可见
  - **分页 footer sticky 底**:分页条脱离 items 滚动区,恒定贴容器底部,始终可见(不因行数增多被挤出可视区)
  - **左侧冻结列**:`#` 永远是第一冻结列;可继续配置冻结图片/名称等左起 2~3 列。冻结区横向滚动时保持不动,右缘必须显示描边 + 向右 `Shadow/md` 层次。
  - **右侧冻结列**:120px 操作列(及可配置的右起 N 列)始终贴右,左缘带描边 + 向左阴影;实体只读表同样保留该列与`查看详情`
  - 表头与冻结列的交叠形成**冻结四角**;冻结列宽度计入内容宽度,不与中段滚动区重复渲染
  - `scrolled` 变体为横向滚动中标本:视口窄于内容,中段显示价格/状态/来源/更新时间,左右列冻结、无重复列
- **状态与反馈**
  - loading:表头保留,主体为 3 条骨架占位(通栏×32),分页条保留上次总数
  - empty:主体 96px 居中「暂无数据」,总数为 0
  - 行 hover 弱底色;行操作可按行禁用(置灰)或隐藏
  - 行操作集合由`资源归属 × 当前角色权限 × 行状态`决定,不得全表硬编码成同一详情按钮;本店独有商品与本店独有分类均为查看/编辑/克隆/删除
- **交互与无障碍**
  - 排序表头输出 `aria-sort`;行可点击时支持 Enter/Space 与焦点环
  - 状态列使用业务状态徽章(上架=soft success、下架=soft neutral)
  - Do:换页/换每页条数/排序/筛选后页码重置为 1
  - Don't:前端自造导出格式(导出由接口驱动)、在页面内联画表格
- **组合边界**
  - 列显隐、导入导出、新增按钮属于列表页头部(Templates 层);本模块只含筛选结果的表体、分页与粘性冻结
  - **高度归属**:AutoTable 负责「表头/items/footer 三段 + 仅 items 滚动」的内部结构;**占满剩余高度的 flex 容器由列表页 Template(CrudListPage)提供**——Template 把页头(面包屑/工具条)、筛选条、AutoTable 竖排,给 AutoTable `flex-1 min-h-0`,footer 自然落到视口底。
- **适用场景**
  - 全部 CRUD 列表页数据区;列、排序、行操作、冻结列由资源配置声明(`sticky:{header,pagination,left,right}`)

## 表单 AutoForm

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=2332-10132&footer=false&page-selector=false)

#### 生成契约

- **层级与复用**
  - `Module`
  - Figma 资产:`AutoForm / Control`、`AutoForm / Section Title`、`.AutoForm / Field Label`
  - 字段控件全部实例化既有 Atom/Molecule:Input、Textarea、Select、RadioGroup、NumberInput、InputGroup(前后缀)、Combobox(远程单选/多选)、FileInput、Pickers、RichTextEditor、CodeEditor
- **尺寸与布局**
  - 表单主体 1024px 居中;当前可见字段`1–6`项时使用 500px 单列并水平居中，`7+`项时使用动态响应式栅格
  - 响应式两列的行内间距 24px、同列字段块间距 16px、半宽字段 500px
  - 分节标题为通栏 40px 深色横条(`primary` 底 + `primary-foreground` 字、圆角 md、左内边距 12px)
  - Section Title 到首字段 16px;字段 = 标签行(必填星号 `destructive` + 14px Medium 标签)+ 控件,纵向间距 6px
  - Radio/Switch/Checkbox 等短控件按真实内容 Hug，不继承 Input 的固定字段高度
  - 块级字段(富文本/代码/多选/文件列表等)独占整行
- **变体与属性**
  - Section Title:`Title` TEXT;Field Label:`Required` BOOLEAN + 标签文本(暴露实例)
  - 模式 `add / edit / detail / copy` 共用同一 schema:detail 为只读渲染(控件走 disabled/只读变体),copy 预填但重置不可复制字段
- **状态与反馈**
  - 校验:失焦校验单字段、提交全量校验;错误定位到字段(错误结构复用 FormField 契约)
  - 提交中锁定提交动作,防重复提交
- **交互与无障碍**
  - 标签与控件必须关联;必填以星号 + 校验双重表达
  - Do:字段类型 → 控件映射由 schema 驱动;动态显隐(如单品/套餐互斥分节)由字段条件表达
  - Don't:同一资源为不同模式维护多份表单、绕过分节标题自拼分组样式
- **组合边界**
  - 底部提交栏属表单页模板并统一复用`Form Action Bar / Control`:按钮组居中，1/2/3 个按钮的最小宽分别为 240/180/120px
  - 本模块只到字段网格为止，不在业务页内手搭提交栏
  - 「添加规格/加料」等子表单编辑器为业务字段插槽,弹层复用 Dialog/Sheet
- **适用场景**
  - 全部 CRUD 新增/编辑/详情/克隆页;字段齐全范本为商品表单(文本/枚举/单选/数字/金额/多选/文件/富文本/JSON)

##### 字段类型 → 控件矩阵

| 字段类型 | 控件 | 宽度 |
|---|---|---|
| `text / password / email` | Input | 半宽 |
| `textarea` | Textarea | 半宽或整行 |
| `number` | NumberInput | 144px |
| `price` | InputGroup(后缀货币) | 半宽 |
| `select / bool-select` | Select | 半宽 |
| `remote-select` | Combobox | 半宽 |
| `remote-multi-select` | Combobox(多选) | 整行或半宽 |
| `radio` | RadioGroup | 半宽 |
| `checkbox / checkbox-group` | Checkbox | 半宽/整行 |
| `date / datetime / time / range` | Pickers | 半宽(range 整行) |
| `image / media` | FileInput | 半宽 |
| `rich-text` | RichTextEditor | 整行 |
| `code-editor` | CodeEditor | 整行 |
| `title` | Section Title | 整行 |
| `custom` | 业务插槽 | 整行 |

**来源提示**
这些模块如何拼成完整页面,见[模板 Templates](templates.md);资源级装配(列表页/表单页)见 [Auto CRUD](auto-crud.md)。
