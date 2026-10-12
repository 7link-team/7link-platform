# 需求：2048 小游戏

甲方需要一个可在浏览器里玩的 2048，并且核心规则必须可以被自动化测试验收。

## 交付物

1. `src/game.mjs`：纯逻辑模块（不依赖 DOM），ES Module，导出下列函数。
2. `index.html`：单文件页面，用 `<script type="module">` 引入 `./src/game.mjs`，方向键（以及 WASD）可玩，显示分数、胜利与结束提示、「新游戏」按钮；直接用浏览器打开即可运行，不需要构建与外部依赖。
3. `tests/`：你自己编写的单元测试（`node:test`），文件名以 `.test.mjs` 结尾。

## 逻辑模块接口（必须严格遵守）

状态对象：`{ board: number[][], score: number, won: boolean, over: boolean }`，`board[row][col]`，0 表示空格。

- `fromBoard(board, score = 0)`：用给定棋盘构造状态。必须深拷贝 `board`；`won` = 棋盘上存在 ≥ 2048 的方块；`over` = `!canMove(state)`。
- `canMove(state)`：存在空格，或存在横向/纵向相邻且相等的两个方块时返回 `true`。
- `move(state, direction, rng = Math.random)`：`direction` 为 `'left' | 'right' | 'up' | 'down'`。返回 `{ state, moved, gained }`。
  - 方块向指定方向滑动并合并；**同一次移动中每个方块最多参与一次合并**；合并优先发生在靠近移动方向一侧。示例（向左）：`[2,2,2,2] → [4,4,0,0]`，`[2,2,4,0] → [4,4,0,0]`，`[4,4,8,0] → [8,8,0,0]`，`[0,2,0,2] → [4,0,0,0]`。
  - `gained` = 本次所有合并产生的新方块数值之和；新状态 `score` = 原 `score` + `gained`。
  - 若棋盘有任何变化（`moved = true`）：滑动合并后，按**行优先**顺序收集所有空格，选第 `Math.floor(rng() * 空格数)` 个空格放置新方块，其值为 `rng() < 0.9 ? 2 : 4`（恰好调用两次 `rng`，先选位置后选数值）。
  - 若棋盘没有变化（`moved = false`）：不放置新方块、**不调用 `rng`**，`gained = 0`，返回与原状态等价的新状态。
  - 新状态的 `won` = 原 `won` 或棋盘上存在 ≥ 2048 的方块；`over` = `!canMove(新状态)`。
  - 不得修改传入的 `state` 或其 `board`（纯函数）。
- `createGame({ size = 4, rng = Math.random } = {})`：创建 `size × size` 的空棋盘，按与 `move` 相同的放置规则依次放置 **两个** 新方块，`score = 0`，`won = false`，`over = !canMove(state)`。

## 验收标准

- AC1 滑动与合并规则符合上述示例，四个方向一致。
- AC2 计分正确。
- AC3 新方块的位置与数值严格由 `rng` 决定；未移动时不生成方块、不调用 `rng`。
- AC4 胜利与结束判定正确。
- AC5 纯函数：不修改输入。
- AC6 支持 `size` 参数（如 5×5）。
- AC7 `index.html` 引入 `./src/game.mjs` 且可直接打开游玩（此项由甲方人工验收）。

甲方持有一套独立的验收测试，不会提供给执行方。
