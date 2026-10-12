# myr：最简乙方交付团队验证程序

Node.js ≥ 20、Git、本机 Codex CLI；零 npm 依赖。所有子进程通过参数数组、`shell:false`、`windowsHide:true` 启动。

## 要验证的理念

主管 Agent 先拆分工作项，执行 Agent 顺序实现。Agent 的自报只是 **L1 声明**；程序删除 `claim.json`、提交真实改动并运行任务指定命令，然后由测试 Agent 与产品验收 Agent 检查，全部通过才产生 **L2 内部验收通过**。通过后必须有 **L3 裁决**。全部接受后，程序在独立副本运行甲方测试，自动通过仍只是 `AWAITING_CLIENT`，甲方 `accept` 才完成 **L4**。

证据包括每次 Git commit、实际文件变化、命令数组、退出码、耗时、stdout/stderr 各 4KB 尾部、完整输出流 SHA256。`outputSha256` 是两个完整流指纹的 JSON 的 SHA256，`streamSha256` 保存各流指纹。命令最长运行 5 分钟；超时杀死直接子进程。

验证失败产生 `{category:"FUNCTIONAL_DEFECT", detail, failures}`，原样注入下一次执行上下文。单项预算优先 `maxInternalRounds`，兼容旧字段 `maxAttempts`，两者均无时为 5；Codex 非零退出属于执行故障，每次调用额外重试一次。执行器成功退出也必须提交有效 claim，并实际写/更新根级 `tests/*.test.mjs`；置信度和声明中的文件清单不能替代证据。

人工 `reject` 必须指定类别和反馈，明确开启一个新的内部轮次有界预算，因此预算耗尽也可以人工反馈后恢复。没有人工裁决，`resume` 不会绕过暂停。自动 L3 的来源是 `POLICY:auto_verified`，人工来源是 `HUMAN:<by>`；策略从不冒充人。

## D113：内部质量门、不穿透

每项顺序为：执行 → L1 → `AUTOMATED` → `AGENT_TEST` → `AGENT_ACCEPTANCE` → L2 → L3。内部失败携带阶段、结论和验证证据自动返工，不暂停、不提示人；仅用尽内部预算后发出 `ESCALATED_TO_HUMAN`（`reason: INTERNAL_ROUNDS_EXHAUSTED`），退出码 3。规划和执行基础设施故障仍保留原来的独立重试与升级机制。

测试 Agent 使用 `workspace-write`，仅允许在 `tests/qa/` 新增或修改测试。程序通过 Git diff 与文件快照检查范围，恢复越界改动并判失败；随后重跑全部验证命令，并额外运行 `tests/qa/**/*.test.mjs`（也支持 `.js`/`.cjs`），避免原有根级 glob 遗漏新测试。测试命令失败，或结论 `pass=false` 且含 BLOCKER/MAJOR，均阻止进入验收。通过的 QA 测试纳入最终 Git commit。

产品验收 Agent 使用 `read-only`，接收需求、当前项、已完成摘要、diff stat、关键文件内容及截图。只判定当前工作项 `acceptanceRefs` 中的标准，其他工作项标准上的 FAIL 不计入。任何 FAIL、BLOCKER/MAJOR 或不通过结论都会返工。无效 JSON/调用故障也不能穿透。`--acceptor-model <model>` 指定验收模型；执行模型可通过可选 `task.executorModel` 指定。报告记录模型；相同或 CLI 默认模型无法确认时注明「未满足跨模型家族要求，已降级为不同主体」。

可选配置 `qa.screenshot: {"page":"index.html","viewports":[[900,900],[420,800]]}`；缺省不截图。无需修改任务输入即可覆盖：

```powershell
node myr.mjs run tasks/2048 --executor codex --auto-approve-if-verified --qa-screenshot index.html --qa-viewports 900x900,420x800 --acceptor-model gpt-5.4 --run-id qa-2048
```

`--qa-screenshot <页面相对路径>` 覆盖页面；`--qa-viewports <宽x高,...>` 覆盖视口（默认 900x900、420x800）。静态服务仅监听随机端口的 `127.0.0.1`，截图使用 `MYR_BROWSER` 优先，否则探测 Edge/Chrome 安装位置与 PATH 中 Chromium/Google Chrome。PNG 保存在 `runs/<id>/qa/<工作项>.round<n>.<宽>x<高>.png`，以多个 `codex exec -i` 传入。只有 `files` 包含该页面的工作项才截图。无头浏览器窄于约 500px 时会按更宽的最小宽度排版再裁切，因此宽度 < 500 的视口在同尺寸 iframe 中渲染，证据 `notes` 注明。未找到浏览器或截图失败记 UNVERIFIED，验收 Agent 仍执行；不得据此声称视觉已验证。

报告「内部质量门」逐项逐轮记录阶段、发现、截图与未验证项，并汇总一次通过率、平均轮次、内部拦截问题数。比例与平均值以已开始工作项为分母；拦截数按升级给人之前已自动修复的失败发现次数计数，不将升级后人工参与解决的问题冒充内部拦截。事件新增 `INTERNAL_QA_STARTED`、`AGENT_TEST_COMPLETED`、`AGENT_ACCEPTANCE_COMPLETED`、`INTERNAL_QA_PASSED`、`INTERNAL_QA_FAILED`、`QA_SCOPE_VIOLATION`。

## 快速开始：真实 2048 任务

先安装并登录本机 Codex CLI（本程序不安装任何依赖）。真实执行会消耗 Codex 额度。

```powershell
node myr.mjs run tasks/2048 --executor codex --run-id real-2048
node myr.mjs status runs/real-2048
node myr.mjs review runs/real-2048 W1 approve --by lead
node myr.mjs resume runs/real-2048
```

对后续工作项重复 review/resume；工作项编号以 `status` 为准。若愿意让程序按已验证策略自动做 L3：

```powershell
node myr.mjs run tasks/2048 --executor codex --auto-approve-if-verified --run-id auto-2048
node myr.mjs verify runs/auto-2048
node myr.mjs accept runs/auto-2048 --by client
```

人工拒绝与返工：

```powershell
node myr.mjs review runs/real-2048 W1 reject --category FUNCTIONAL_DEFECT --detail "修复边界条件" --by lead
node myr.mjs resume runs/real-2048
node myr.mjs accept runs/auto-2048 --by client --reject --detail "体验不符合要求"
```

甲方拒绝为终态 `CLIENT_REJECTED`；自动验收失败为 `ACCEPTANCE_FAILED`。这两种情况报告保留失败证据，修订任务后开新 run，程序不擅自修改甲方输入。

`runId` 默认使用时间戳；指定时仅允许字母、数字、下划线、短横线。已存在的 run 不会被覆盖。退出码：`0` 操作成功（可能仍等待甲方）；`1` 命令/验收/复验失败；`2` 等待 L3 人工裁决；`3` 尝试耗尽或执行故障升级。

## 输入和目录

```text
tasks/2048/                 # 只读甲方输入
  task.json                 # name, requirement, acceptance, verify, maxAttempts, maxWorkItems
  requirement.md
  acceptance/
myr.mjs                     # CLI
lib/
  engine.mjs                # 状态机和命令行为
  process.mjs               # 无 shell 子进程、超时、捕获、Codex PATH 解析
  files.mjs                 # UTF-8/LF 写入、复制与指纹
  events.mjs                # 只追加事件链和校验
  report.mjs                # 中文报告
  schema.mjs                # 计划 JSON schema，运行时写入 run 目录
  executors/codex.mjs
  executors/mock.mjs
test/pipeline.test.mjs
runs/<runId>/               # 被 .gitignore 排除
  state.json                # 可 resume 的状态（原子替换）
  events.jsonl              # seq/ts/type/actor/data/prevHash/hash
  report.md
  workspace/                # 独立 Git 仓库，只有需求与 Agent 交付物
  acceptance-copy/          # 不含 .git；仅在验收阶段复制隐藏测试
  plan.schema.json
  plan.call1.last.json
  W1.attempt1.last.txt       # Codex 最后消息，重试为 .retry.last.txt
```

`requirement` 和 `acceptance.dir` 必须为任务内相对路径。`verify[].command` 和 `acceptance.command` 都是非空字符串数组，不支持 shell 管道/重定向；程序展开 `node --test` 的 `*`、`**`、`?` 文件路径模式，兼容 Node 20。Windows 的 npm Codex `.cmd` 解析为 JS 入口并用 Node 直接启动，原生 `codex.exe` 也可用。其他命令请使用原生可执行文件或 `["node", "脚本路径", ...]`。

`verify` 不调用 Agent，会独立重跑验证和验收、检查验收文件指纹、Git 证据与事件链，保存复验结果并更新报告；它不会将工作项或 L4 自动升级。损坏的日志拒绝追加。链公式为 `sha256(prevHash + JSON.stringify(除 hash 外的字段))`，按原字段顺序序列化，首条 `prevHash` 为空字符串。没有外部签名/锚点，整条日志重写或末尾完整行删除不在证明范围内。

同一 run 的 CLI 写操作用 `.myr.lock` 排他；进程意外退出后，确认进程已终止再删除该锁目录。执行中断后的 `resume` 会升级给人，避免默默重放不完整尝试。规划故障无工作项可裁决，显式 `resume` 可再尝试规划（每次仍只重试一次）。

这是本机验证程序，执行任务命令是对本机代码的信任。工作区隔离与 Codex sandbox 不是独立用户/容器隔离；隐藏验收不会被复制到执行工作区或写入提示词。甲方测试在副本运行，运行前后文件 SHA256 必须和 run 开始时的指纹一致。

## 自测（不消耗 token）

```powershell
node --test "test/*.test.mjs"
Get-ChildItem myr.mjs,lib,test -Recurse -File -Filter *.mjs | ForEach-Object { node --check $_.FullName }
```

测试在系统临时目录创建迷你任务，完全不使用 `tasks/`。Mock 固定计划两项，默认写 `src/value.mjs` 与 `tests/value.test.mjs`，供迷你任务测试完整流程；它不是 2048 的实现。

较早的 Node 20 不展开自测命令中的带引号 glob，可直接用 `node --test` 自动发现自测文件；任务内部的 glob 已由程序展开。

临时任务的 `task.json` 可加 `mock` 配置：`failFirst`（W1 首次质量失败）、`alwaysFail`、`executorFailOnce`、`executorFailure`；`files`、`failureFiles`、`successFiles` 是相对路径到文件文本的映射，后者分别覆盖失败/成功尝试的预设文件。Mock 还会在 run 目录保存上下文 JSON，以便断言反馈确实到达执行器。

`mock.testAgent` / `mock.acceptAgent` 支持 `failFirst`、`alwaysFail`、`executorFailure`、`files`、`conclusion`；测试 Agent 另支持 `scopeViolation`（首轮越界改 tracked/untracked 文件）。所有自测任务都在临时目录生成，`tasks/` 不修改。

常规自测用模拟浏览器启动器验证真实 localhost 服务与截图证据流程；真实 Edge 集成测试需要本机可运行 Edge：`$env:MYR_REAL_BROWSER_TEST='1'; node --test test/qa.test.mjs`。受限环境可能无法启动浏览器 GPU 子进程，此时生产流程记录 UNVERIFIED，不能宣称截图成功。
