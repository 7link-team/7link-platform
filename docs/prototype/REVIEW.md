# 原型评审与验收记录

> **当前结论：NOT READY。** 以下旧交付记录不是本次深审通过证明。2026-10-13 新深审仍在第一轮，连续无新增问题轮数 **0**；逐项证据见 COVERAGE，最新修正见本文末尾。

日期：2026-10-13。执行方式：直接代码 / 需求检查与独立浏览器动作复核，没有多人评审。基线：[PRD_FINAL](../PRD_FINAL.md)，全部 332 条需求的审阅去向见 [COVERAGE](COVERAGE.md)。这里的通过只针对可交互的设计原型，不是生产系统验收。

## 第一轮：结构、一致性与业务状态

| 检查 / 发现 | 修正与复核 |
|---|---|
| 原 PRD 与 REQ 的来源保留、冲突取舍、验收编号 | 保留原 291 条，补到 332 条；70 个场景；原文件哈希不变；两轮文档评审见 PRD T3 / T4，后续用户确认见 S9 |
| 界面有门禁但命令层可能直接接受或交付 | 模拟命令同时检查内部质量门、Human 角色、当前版本与工作状态；失败路径有测试 |
| 交付退回会丢失原共享版本 | 共享依据固定交付记录，重新提交不把新版自动共享；旧包、拒收和历史保留 |
| 不同项目复用了同一工作流草案 | 实例分别保存定义、草案和发布版本；新增隔离测试 |
| 侧栏跨页面跳转、顶部栏目跟着改变 | 侧栏 / 频道使用本地面板按钮，不改变 URL / history，顶部栏目稳定 |
| 租户与项目实例混淆、项目入口重复 | Logo 右侧创建 / 加入 / 切换租户；左栏仅一个实例选择器。全部项目仅在租户工作台；实例工作台显示当前概览；协作区没有重复概览入口 |
| 主题仅近似迁移、原生控件不一致 | 六套源主题与已有三套暗色变量逐项比对一致；另三套暗色注明扩展。统一 Select、Form、Field、Modal、确认框、主题滚动条与输入状态 |
| 排期没有日历入口 | 工作列表 / 看板增加月 / 周日历，成员筛选、项目截止、跨日、未排期和版本化排期修改共用工作项 |
| 工作流右栏不足以配置节点 | 增加九类节点库，右侧参数 / 输入输出 / 调试、复制 / 删除 / 连接管理、具体节点校验与发布阻断 |
| 实时预览只有单个固定画面 | 增加任务隔离的多标签 Web / API / 文件 / 终端 / 文稿；切换保留组件状态，断线 / 恢复 / 关闭有反馈 |
| 配置下发、报告生成可能误报完成 | 节点逐项显示下发状态；修改配置使旧审批失效；定时报告保持草稿，Human 确认独立 |

结果：发现项已修正；没有把模拟验收、模拟网络响应、模拟终端或本地角色切换描述为生产能力。

## 第二轮：实际交互与视觉验收

- `bun test`：**16 通过，59 条断言**。涵盖质量门、交付版本、返工、离线 / 角色、配置审批与部分生效、报告草稿、租户隔离、日期校验与过期更新、工作流缺配置 / 循环 / 分支。
- `bun run build`：Vite 构建成功，包括主入口及评审台；已实际打开构建产物，验证评审台切换日历与 375px 设备预设。上游 React Flow 的 `use client` 指令有构建提示，产物正常生成。
- 浏览器：**63 个路由 / 状态 × 375、768、1024、1440 四种宽度**；没有运行错误、崩溃页或文档级横向溢出。画布与日历使用自身交互 / 响应式呈现，不用隐藏页面溢出来掩盖布局问题。
- 核心闭环：失败门禁拒绝接受 → 模拟独立复核 → Human 接受 → 确认交付 → 固定包同步；发消息、版本批注、无效工作流发布阻断，均通过真实页面操作核对状态。
- 用户修订：侧栏 URL / history / DOM 稳定、租户创建切换、单一实例选择器、六主题切换、自定义错误与确认、下拉键盘选择及焦点恢复已检查。
- 新功能：实例 / 租户菜单，节点添加 / 编辑 / 复制 / 删除 / 调试，日历修改同步，预览添加 / 切换 / 断线 / 恢复 / 关闭，均通过浏览器动作检查。
- 可访问性：使用 axe 的 WCAG 2 A / AA 规则检查工作台、会话、工作流、设置、主题弹窗、排期日历、执行预览及 Aurora 暗色工作台，**最终扫描无自动规则违规**。渐变背景等人工核对项不计为自动通过；未宣称全部主题 / 所有内容组合通过完整 WCAG 认证。
- 修正了平板顶栏拥挤、小屏 Logo 无可访问名称、状态小字对比度、React Flow 署名对比度、预览关闭按钮放入 tablist 导致的语义问题。预览关闭操作位于标签栏外，键盘切换保持有效。
- 实际截图已查看：工作台桌面 / 手机、工作流节点面板、执行预览、排期日历；主题和暗色截图一并保留在 [screenshots](screenshots/)。

## 复现方式

浏览器脚本位于 `tests/browser-*.js`，在隔离的 agent-browser 会话打开本地原型后执行 `agent-browser eval --stdin < 脚本路径`。每套状态测试前通过设置页重置演示；自动化也可只移除此原型的 `collaboration-prototype-v1` 键并刷新。不要清理整个浏览器存储。

- `browser-workflow.js`：交付与反馈闭环。
- `browser-navigation.js`：侧栏、租户、主题、自定义交互。
- `browser-features.js`：视角菜单、节点编辑、排期、多标签预览。
- `browser-routes.js`：路由 / 状态巡检；用浏览器 viewport 设置重复四种宽度。

## 必须区分的后续工程验收

接口接入之外，仍须实现真实鉴权、云端同步与审计归档、原件快照、模型 / runtime 与工具调用、工作流执行引擎、节点调度 / 升级、FRP、远程文件 / PTY 和真实租约回收，以及 Tauri 原生和移动真机能力。专业媒体目前是示例；历史批注固定版本不等于已存独立历史媒体二进制。日历按日排期，不代表已完成小时级资源容量优化。

上述为此前两轮有限范围检查的历史记录；新深审推翻了其足以完成全部原型验收的判断。不能把入口映射或模拟成功当作完整需求覆盖。


## 新深审第一轮：已复现并修正的交互缺陷（进行中）

范围：docs/prototype 的设计与本地行为，对照 PRD_FINAL。生产后台、真实外部连接、原生能力未因此实现。执行方式：主代理亲自复现与集成；deep-review 分派只读产品/UI视角及证据清单，后续按明确文件边界修复。无提交或推送。

| 事实/复现 | 修正 | 当前证据 |
|---|---|---|
| 新建工作同会话提交时 history 未初始化，抛异常 | 提交建立历史，不依赖刷新 hydrate | domain 新工作回归；browser-domain 创建→确认→启动→提交 |
| 通用编辑器暴露 nodeResults，修改后把数组存为字符串导致详情崩溃 | 全资源显式可编辑配置字段，保留运行元数据 | 全 schema 集合断言、browser-domain 模型应用后编辑 |
| 连接器 history 同时表示历史可见范围和审计数组，动作 push 崩溃 | historyScope 与审计 history 分离；兼容迁移当前及缓存租户 | 所有连接器流转与旧数据迁移测试 |
| 当前版本测试失败，工作旧 qa=true 仍允许接受/交付 | 当前版本结果及证据共同校验，失败撤销当前接受，重验同步 | domain 集合与 browser-domain 失败→拒绝接受→重验 |
| 未执行显示失败，其他条款无证据却显示已检查 | 显式未执行/待验证/失败/通过；条款不伪造逐条结论 | quality display 测试、browser-domain 新工作验收页 |
| 执行者通过“进行中”状态批准目标 | 负责人决定覆盖目标批准、工具授权，拒绝时状态不变 | executor 目标/工具集合回归 |
| brief→delivery 可发布，未经过检查与 Human 审批 | 逐路径检查门禁顺序；检查后再产出新结果使旧门禁失效 | flow.test 四测试；4类产出节点×2个插入位置 |
| 发布仅覆盖唯一图，旧工作没有版本引用，却宣称锁版本 | 归档固定定义、工作确认绑定版本、历史只读选择器；旧未记录版本须重新确认 | flow 版本测试；browser-domain 发布/回看/未知版本 |
| 新版本选择器把 event 当 value 产生 version=[object Object] | 使用共享 Select 的 target.value 契约 | browser-domain 实际选择固定版本 |
| A任务预览路径切到B再回来变成 / | 交互状态按租户/任务/标签持久，包含终端历史、草稿与阅读位置 | browser-previews 跨任务复挂载与终端 scrollTop=80 |
| 离线时标签选择被业务写权限阻止 | 本地阅读/选择/关闭与连接操作分开；失败保留现状 | browser-previews 离线切换/全部关闭/重开 |
| 关闭标签焦点无归宿 | 选择相邻标签；最后一项关闭后聚焦添加；方向/Home/End/Delete 键 | browser-previews 键盘与关闭断言 |
| 执行消息回复线程丢 execution_id，发送到默认项目频道 | 进入/回复/退出统一保留执行上下文 | browser-collab |
| 分享提示说不开放附件，却复制 work/file/thread/history | 明确复制选定文本字段 | browser-collab 共享字段断言 |
| 私人/总频道/空租户右栏均显示 launch，空租户进度NaN | 按会话与项目范围构建上下文，私人及空态独立 | browser-collab |
| 手机选择左侧菜单后抽屉仍挡正文；会话右侧浮层无焦点管理 | 选择后收起菜单；紧凑上下文复用共享 Modal | browser-collab；URL/history不变、焦点返回 |

本轮当前已执行：`bun test` **31 pass / 392 assertions**；构建成功（React Flow既有 use client 提示）。桌面六套原有/新增浏览器脚本全部通过；额外 browser-domain 通过。主代理另在375×812实际复跑 browser-previews（含终端阅读位置）和 browser-collab，全通过。没有据此声明332需求或70完整业务场景全部通过。

### 测试有效性证据

- 原有 browser-features 虽测试标签切换，故意让输入在组件复挂载时重置、或让离线阻断标签选择时仍通过。新增 browser-previews 分别以 `Remount retains Web path`、`Offline tab switching` 失败；两次都运行全部六个浏览器脚本和全部 domain 测试，唯一失败的是新预览脚本。已按原工作字节恢复。
- 质量结果的“失败”分支改为不执行，原门禁阻断测试仍绿，因为待验证也会阻断。这是**显示语义的漏测**；增加独立状态分类测试后，同一变异被该测试检出。
- 主代理其余变异：改用旧 qa 缓存、删除测试→工作同步、丢弃历史失败证据、允许执行者批准目标、未记录版本默认用最新、无检查也接受，共6项均被对应测试检出。新产出不清除旧工作流门禁的变异也被专项测试检出。
- 子代理修复验证：删除首次 history 初始化、重新暴露 nodeResults、删除旧连接器迁移、删除工作流门禁检查、删除旧版本归档，共5项被测试检出；精确字节备份还原，未用 Git 回退。
- 以上是明确列出的变异集合，不是全源码变异覆盖或全测试有效性的完成声明。

### 尚未关闭的范围

COVERAGE逐项记录设计部分、缺失专用设计及生产未验证，三者不能混为“已实现”。仍有结构化退回与影响传播、逐项交付接收、专业测试计划/职责分离、组织和小团队编辑、计划/报告事实引用、材料共享与专业定位等设计缺口。通用表单存在不代表这些闭环完成。

完整 Web Interface Guidelines、六主题全状态人工检查、全部控件动作与两轮完整回归尚未完成；本轮发现过新问题，因此无新增问题计数保持0。必须修复/核对剩余项后才能开始计连续完整干净轮次。

### 深审增量复核：预览、回执与菜单

- 主代理 `bun test`：35 项/414 断言通过；build通过。独立重跑 browser-receipts，固定清单/逐项回执/结构化反馈通过。此结果早于正在实现的接收契约与报告核对，后续必须重跑。
- browser-previews 在1440和375复跑通过；新增切回任务后选中标签完整可见断言。修正为下一帧只滚动标签容器，避免滚动整个页面。手机执行预览弹层 axe WCAG2A/AA：0 violations、0 incomplete，仅代表本次状态。
- browser-controls 主代理12个主题/亮暗组合复跑通过（输入、主按钮、错误文本和自定义滚动条）；不是全站全部状态对比度通过声明。
- 主代理复现手机导航 Escape不关闭、租户按钮 Escape不关闭；已修抽屉焦点环绕/返回/展开语义及租户菜单键盘关闭。独立复测确认主要路径通过；租户弹窗关闭焦点时序曾再次失败，改用共享Modal的稳定returnFocusRef，正在复核。
- 新发现待关闭：报告核对为静态承诺；无外部接收方完成分支缺失且待回执过早完成。正在实施，不记为通过。组织责任/目标贡献/依赖传播及双返工计数仍待完善。完整无新增问题轮数仍为0。

### 整合复验（构建产物，非完整验收轮次）

主代理51项测试/484断言、构建通过；静态4174桌面11套脚本全部通过（workflow/navigation/features/previews/collab/domain/receipts/controls/completion/reports/routes）。375px另复跑previews/collab/completion/reports全部通过。租户弹窗8条关闭/成功路径及手机菜单真实键盘由独立子代理验证，主代理控件脚本也通过。

新增接收契约：内部使用记录L4不适用理由，L2/L3仍必需；对外交付等待当前包全部回执才完成。报告改为真实本地来源、版本、日期与计数核对，Human/Agent/工具耗时分列并按来源去重；未审核状态不被提交抹掉。主代理4个完成契约变异、子代理8个报告变异全部被专项测试检出，均在临时副本运行。

预览测试曾因旧浏览器标签处于hidden、rAF暂停而报焦点失败；新可见会话与静态构建复跑通过，未把该环境失败当作新增产品缺陷。增加visible前置断言，新增标签焦点安排在弹窗清理后。截图重新生成。依赖传播、返工计数、组织与目标计划等剩余缺口仍在，连续完整无新增问题轮次为0。

## 2026-10-13 — 七项流程与身份层级专项整合

本节范围是租户邀请、周期报告、会话发起工作、Human / Agent 投入分账、Agent 配置、工作流模板、成员页，以及用户追加的 Human 组织 / Agent 名册 / 任务编组边界。**不将该专项通过扩大为完整 332 REQ 原型审计通过。** 原全量审计缺口继续按 COVERAGE 登记。

执行方式：主代理整合与独立实际验收，三个子代理分工实施和交叉只读评审。真实邮件、模型、后台定时器、遥测、远程执行仍未接入；所有“模拟”状态保留明确标识。没有提交或推送代码。

发现与处置：

| 发现 | 修正 | 证据范围 |
|---|---|---|
| 组织页混放 Agent 与任务小团队 | 组织仅 Human，Agent 创建来源分列；编组在任务详情，引用两份名册 | organization / agent-lineage / work-team 单测及对应浏览器脚本 |
| Agent 姓名被当作身份，改名断联或同名串报告 | 固定 Agent ID、遥测所有者 ID；重复名称拒绝，旧未知来源不猜测 | reports / agent-lineage / work-team，姓名匹配回退变异被捕获 |
| 父 Agent 退役及临时编组可能误导生命周期 | Agent 和子身份保留；退役 / 待批准不可新增任务角色；旧引用可保留与解除 | agent-lineage / work-team，Human接收人与可分派状态变异被捕获 |
| 报告多入口与自由标题无法保证周期唯一 | 同主体 / 周期 / 时区单一逻辑 ID，手动与模拟调度复用，已提交快照与人工修订保留 | reports / browser-reports；日历边界、冻结、重复触发等变异捕获 |
| Human 未校准时长与 Agent 耗时混算 | 预计、建议、校准、本人实际确认分开；Agent四类只读维度，缺失显示未知 | effort / reports；本人权限与日上限变异捕获 |
| Agent 配置可用旧表单覆盖或引用非 Human 负责人 | 当前租户 Human 校验与 expectedRevision，审批绑定配置 / 模型版本 | agents-templates |
| 日期仍调用浏览器原生 picker | 共享主题日期弹层、ISO 文本输入、校验、键盘与焦点复用 | browser-date 桌面/375；真实Tab/ShiftTab/Escape和闰日跨月操作；日历axe0违规/0待人工 |
| Business / EyeCare / Neo 浅色报告提示与 amber 小字对比不足 | 仅调整共享控件前景混色，不改六主题源 token | 新构建报告12主题组合均0自动违规；Aurora/Pink渐变4组合存在自动检查不完整项 |
| 邀请旧通用表单绕过专用角色 / 范围 / 有效期 | `/invitations`及深链接复用组织邀请界面；通用资源创建 / 状态命令拒绝绕过 | organization 专项新增原子拒绝测试；统一入口浏览器回归 |
| 草稿报告可直接“审核通过”，绕过本人上报 | 报告状态门禁、UI可用性与新建状态保护 | 最终报告专项回归记录见后续验证 |

专项整合的中间验证快照：主代理执行 **123 tests / 1004 assertions PASS**、Vite build PASS；冻结构建的桌面 **21** 套浏览器脚本 PASS。375px 首轮19套PASS，另2套原脚本误按桌面直接访问隐藏侧栏/预览；改为真实手机入口与可见触发器焦点断言后针对性2套PASS，未降低功能断言。之后新增的报告状态门禁、邀请统一入口及工时审核恢复需使用更新后的构建回归，不能以该中间快照代替。


最终专项验收（2026-10-13）：

- 主代理执行 `bun test`：**130 tests / 1051 assertions PASS**；`bun run build` PASS，仅既有 XYFlow 指令警告。
- 最终冻结构建在 **1440px、375px 各 21 套浏览器脚本全部 PASS**；路由脚本额外检查实际错误、空标题、坏页面与横向溢出。42 次脚本执行不是完整 PRD 两轮验收。
- 冻结前后全部 14 个原型源码 SHA-256 一致，浏览器实际验证的是本次最终源码。报告草稿提交/审核门禁、邀请统一入口和工时主管审核均包含在此构建。
- 主代理按最新要求核对：Human 组织独立、Agent 来源分列与准确创建者、任务内引用两份名册、任务/执行会话/日历联动、历史作者不被改写、动态子 Agent 保留。组织与 Agent 生命周期检查及移动端任务编组均通过。
- PRD / COVERAGE 的 **332 个需求 ID 集合一致**；原 PRD.md / REQUIREMENTS.md 内容哈希保持不变；`git diff --check` PASS。
- 自审：本次复用现有身份、工作与状态存储，未引入生产依赖；范围、回归及身份隔离已核对。本专项可供用户验收；完整 PRD 审计仍 **NOT READY / 连续完整干净轮数 0**，不能据此宣称所有产品功能实现。

独立最终只读复核：25 项状态/身份/权限不变量探针通过，复跑 130 tests / 1051 assertions PASS，未发现新的可复现缺陷；与主代理最新请求验收结论一致。


## 全量续审：计数、专业验证与共享范围（进行中）

本轮新增可复现问题，完整干净轮数仍为 **0**：

- 页面展示软件内部5轮/正式3次上限，但四次正式退回及六次内部重验均未停线。固定包多项拒收需要聚合业务事件，不能逐条计次。
- 测试计划只有文本用例ID与通用状态，没有固定批次和测试报告；Agent自测可满足独立质量门，工作级模拟复核将全部当前失败替换为通过。保留失败历史仍不能证明逐条独立验收。
- 执行会话W-201属于film，共享给“项目成员”却写入launch；已改为明确项目与接收频道，私聊/总频道不猜测项目。
- 外部视角访问 `/chat?channel_id=external&execution_id=W-104` 可读内部执行记录并出现输入框；主代理浏览器亲自复现，已同时修正路由、会话与提交边界。原型权限语义检查不能代替服务端鉴权验证。
- OKR当前值改为目标值后旧60%仍保留且不可校准，缺少与指标修订绑定的进度依据；文件知识记录显示“禁用记忆”，按钮对象语义错误。

修复和整合仍在进行，不以子任务局部通过作为全套验收。QA门禁改为固定计划后，须改写依赖“一键变绿”的测试前置；保留原拒绝/完成断言，不降级门禁。


本轮冻结整合验证（包括新增预览宽度要求）：

- 主代理 `bun test`：**164 PASS / 1407 assertions**；`bun run build` PASS。
- 固定构建4182：**26套 JavaScript 浏览器脚本 × 1440 / 375 = 52次全部 PASS**；每套独立初始状态，QA先逐项录入再固定报告，旧一键全绿前置已移除。持久复现入口：`tests/run-browser.py`。
- 主代理独立执行 `tests/browser-guidelines.py`：四类标签组真实方向键/Home/End/Tab操作通过，12主题模式手机底部导航0自动违规；Pink浅色仍有1项渐变对比自动判定不完整，不声称完整可访问性通过。
- 新预览splitter真实mouse操作344→584px，保存584、无溢出、拖动结束释放；键盘最小/最大宽度、会话最小空间、跨任务记忆和375px全宽弹层通过。预览底部两处工作跳转按钮已删除。
- 分享目标/租户边界两类变异被新测试捕获；返工6类、QA6类变异在隔离副本中被对应测试捕获。未对共享源码做未还原的变异。
- **全量审计仍 NOT READY，连续完整干净轮数0。** 新建的[指南逐规则矩阵](GUIDELINES_REVIEW.md)明确保留未验证和未修项；缺陷仍非WorkItem统一实体，完整OKR/计划事实、专业共享/定位、组织离职交接、环境与范围变更等缺口没有被局部通过覆盖。


## 2026-10-13 — Delivery-driven previews, navigation and visual refinement
Latest user scope: no manual preview-tab add/remove; content fills available panel with desktop expand/mobile fullscreen; actual delivered resources determine tabs; web browser controls and repository tree are distinct from format viewers. Work-detail parent-menu highlighting and original execution/thread back navigation fixed. Original font loading and conversation hierarchy restored.

Verified: 164 unit tests / 1407 assertions; build; six browser suites at1440/375 on immutable4183, allPASS (source unchanged). Evidence: /tmp/preview-toolbar-desktop.json, /tmp/preview-toolbar-mobile.json. Real pointer breakpoint teardownPASS; keyboard/theme gatePASS with existing pink-light gradient incomplete. Per-tab navigation remains explicitly local simulated content; actual FRP/iframe/Office/media integration not claimed.

Self-review and latest-request scoped acceptancePASS. Full product audit remains NOT READY, consecutive complete clean rounds0; new independent QA/OKR/defect counterexamples remain in MEMORY and /tmp/final-cross-review.md.


## 2026-10-13 — Audit chronology, project ownership and integration follow-up

Main-agent reproduction found two additional reachable classes: audit/message persistence lost the occurrence date (HH:mm only), and new-tenant project creation accepted a Human absent from that tenant through hardcoded choices. New records now retain ISO UTC, legacy dates are not fabricated; project creation binds a current-tenant Human ID and rechecks the tenant at save. Display formatting reuses Intl through formatting.js. Both fixes have negative evidence; isolated audit-time and project-owner guard mutations are killed by their regression tests.

Scoped validation: domain32PASS/392assertions; tenant4PASS/40assertions. Project creation/navigation/audit list at1440/375 PASS on immutable4189. Message timestamp regression fails old4189, then collaboration+sharing at1440/375 PASS on immutable4190. Latest4190 frozen entire suite208PASS/1885assertions and buildPASS; ongoing OKR/Plan/dirty-form work occurred afterward. Evidence paths in MEMORY. Unified defect browser test now executes its actual QA prerequisite, records evidence/hash, and passes both sizes; never injects accepted flags.

Independent typed-OKR review caught two issues handed back and repaired by owner: fixed-v3 evidence link opened current work; legacy same-name Human conversion selected first match. Final integrated replay still pending.

**Still NOT READY / consecutive complete clean rounds0.** Actual report command probe permits Human MEMBER author to submit then approve their own report without SOD policy; current test suite erroneously endorses it. Historical period generation also uses current completion state. `/tmp/prototype-report-scope-probe.mjs` reproduces both. Report authority/period facts/structured content are upcoming required repairs, not covered by the green scoped results. Member Plan implementation is underway; it does not replace independent project/stage planning requirements.


## 2026-10-13 — Editable form boundaries final handoff
- Bounded subtask complete: all inventoried editable Modal close paths and organization inline Cancel/X now share dirty confirmation. Successful writes explicitly dismiss; rejected saves retain edits. QA/calendar/work-team/organization recover per tenant/entity with original revision retained; prior goals/reports recovery retained. Confirming discard after the originating inline editor unmounts now clears its cached draft without invoking a stale close callback.
- Verification: frozen /tmp/form-boundaries-batch4 build PASS; browser-form-boundaries.py core/work/collab/org each at1440/375 PASS. Existing organization/reports/QA browser regressions PASS with unchanged source (/tmp/organization-guard-regression.json). Scoped domain tests: QA/domain/tenant57PASS675assertions; team/rework18PASS175; sharing3PASS75; organization16PASS123. git diff --check and Python syntax PASS. Owned server4186 stopped; no commit/staging.
- Recovery boundaries: other guarded forms (ordinary resources, resource transitions, report generation/schedules, workflow templates, Agent config, work actions/defects, new project, message edit/share, tenant forms) do not provide generic route recovery. Refresh clears in-memory drafts. Message edit has no CAS and no stale-recovery guarantee. Standalone effort, rework settings, work inputs/contract and artifact comments were outside the Modal+organization task and are not certified.
- Integration: latest OKR/Plan UI is owned by requirements/parent. Old browser-dirty-forms.py new-goal label may need adaptation to Objective/KR; legacy G-01 still delegates to legacyPage. Parent owns final merged build/browser acceptance; scoped PASS does not imply full product acceptance.
- Next: audit_ui performs read-only report authorization/store/action wiring cross-review; parent owns all report business fixes and final verification. Full product status remains NOT READY / complete clean rounds0.


## 2026-10-14 — Report authorization independent read-only review
- Scope: report-authorization.js, report save/transition branches in store.js, report action/edit wiring in pages-admin.jsx. Business sources remain parent-owned; no business edits from this review. Shared files changed concurrently, so this is scoped command evidence, not a frozen full-product acceptance.
- Verified: report authorization + reports suites35PASS/196assertions; independent9role/subject submission combinations preserve state on rejection;4fixed submission field corruptions (authorId/authorKind/revision/content) reject acceptance. Save metadata whitelist strips forged approval/submission/history/author/factSnapshot. Human peer editing/submission denied, self-review denied, explicit Agent simulation attribution and final Human acceptance preserved; UI sends current revision/status and explicit simulateAgent.
- Reproduced boundary1: with configured tenant owner H-zhou and a team node owned by H-lin, team-node report submission becomes accepted by SYSTEM:team_top. reportReviewAuthority applies top-level exception to every team: node; BR-10-20 explicitly documents top-level owner submission. Parent must adjudicate team-node mapping and narrow exception if appropriate. This probe is independent of the subsequently added owner-role check (probe role is owner).
- Reproduced boundary2 (public save command; UI does not expose subject editing): legacy own report with no periodic key accepts new subjectKey=human:H-zhou plus valid calendar descriptor. save stores authorId=H-lin but reportAuthor resolves H-zhou, and H-lin-written narrative becomes the peer's report. previousKey=null skips identity lock. Recommend compare resolved old/new author identities even when upgrading legacy descriptor.
- Reproduced boundary3 (legacy/imported fixed submission input): a peer submission with valid author/body/revision but confirmerId=null is accepted by current H-lin reviewer. Missing→configured authority is not caught by truthy comparer. Recommend require exact fixed confirmer on acceptance while retaining correction/return path for legacy records.
- Evidence: /tmp/report-auth-cross-review.mjs and /tmp/report-auth-cross-review-results.txt. Parent informed with concrete inputs; no production exploit or server authorization claim. Existing tests green do not negate these uncovered branches.
- Views: identity/authorization, legacy compatibility, state-machine/CAS and UI wiring reviewed directly; no extra subagents (bounded parent delegation). HTTP/SQL/dependencies/performance views skipped as absent from this scope. Failure-class check: A1 local draft guards covered prior handoff; A3/A4/A7 covered here; A6 repeat/state rejection covered existing tests; A8 scheduled regeneration existing tests preserve submitted records; A2 bulk/network and A5 monetary reconciliation not touched.
- Next: parent decides and applies report boundary fixes, then freezes merged build and runs its planned browser/mutation acceptance. Full product remains NOT READY / complete clean rounds0.


## Periodic report fact correction — 2026-10-14 (bounded verification)
- Reproduced historic-period bug: changing today's work status affected the report generated for 2000. Replaced it with immutable acceptance event dates/subject keys, timezone filtering, event-ID dedupe and distinct-work completion counts. All accepted versions retain their exact event IDs; today's rework or reassignment cannot remove prior-period facts. Missing legacy dates/scope remain unknown.
- Report Plan aggregation uses the explicit historical `planReportFacts` mode; default current Plan completion behavior is unchanged. Historical content without a stored work snapshot is disclosed as unavailable; it is never borrowed from today's work. New acceptance events capture minimal fixed work-display metadata.
- Regeneration updates generated suggestions/facts only; manual prose, selected sources and submitted snapshots remain unchanged. Valid explicit empty calendar reports may submit; missing legacy references and unsupported zero claims remain rejected.
- Scoped regression evidence: initial facts suite7 tests/41assertions PASS; eight isolated semantic mutations killed; browser-report-facts at1440/375 PASS on frozen4197. Logs: `/tmp/report-facts-new-tests.log`, `/tmp/report-facts-mutations.json`, `/tmp/report-facts-browser-desktop.json`, `/tmp/report-facts-browser-mobile.json`.
- Further actual reproduction: changing W-104's project made launch-month effort suggestions change from WL-01/WL-02/WL-03 to WL-01. New effort captures immutable scope; backdated records do not infer historical project/team from today's organization; old missing scope remains unknown. Existing stable ownerId/ownerAgentId still proves personal ownership. Manual report selection cannot bypass the same scope check. Submitted history remains fixed.
- Spec10 §3.8 report_key and BR-10-43 verified: timezone is not part of report identity; an existing calendar report retains its saved timezone/boundaries when reopened under changed settings.
- Latest integrated unit suite245 tests/2109 assertions PASS; extended report-facts+effort21 tests/164assertions PASS. Browser-reports expanded to prove unknown legacy ownership plus real estimate→calibration→confirmation and separate Human/Agent totals; final browser run pending at this checkpoint.
- This is scoped evidence, not acceptance of full four report sections, next-period plans, production schedulers, or the entire PRD. Full audit remains NOT READY until parent integration/acceptance.

### Periodic facts final bounded gate — 2026-10-14
- Latest full unit run: **246 tests / 2111 assertions PASS** (`/tmp/report-effort-all-tests.log`); production build PASS (`/tmp/report-effort-final-build.log`); `git diff --check` PASS.
- Final frozen4197 source: **browser-reports + browser-report-facts each1440/375 PASS**, fingerprints unchanged (`/tmp/report-final-browser-desktop.json`, `/tmp/report-final-browser-mobile.json`). Tests explicitly reject foreign Agent effort in a Human report, correct references through UI, establish a real Human estimate→calibration→confirmation, and assert personalHuman1.25h/Agent0 versus tenantHuman2.75h/Agent1h independently.
- **14 isolated semantic mutations killed** (`/tmp/report-facts-mutations.json`), including historical period/scope/timezone bypasses, version double-counting, rework erasing Plan history, overwriting human/submitted data, fabricating historical content, rewriting effort scope, backdating current org membership, unknown effort counting, timezone duplicate identity/changed boundary, and saved legacy Agent display-name identity inference.
- Persisted legacy Agent worklogs with no stable identity now retain unknown identity; only pristine demo fixtures may initialize their known author. Regression reloads saved data to verify no name-based identity inference.
- Bounded self-review PASS: reused stable subject-key capture, no current state used as historical completion, no old-data backfill, no manual/submitted overwrite, no new dependency, exact period identity follows spec. Parent final original-request acceptance remains required; full four-section reports/nextPlan/full-PRD clean rounds are outside this gate.
