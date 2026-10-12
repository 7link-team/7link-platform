// 甲方验收测试：执行方不可见、不可修改。运行位置为工作区根目录下的 acceptance/。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createGame, fromBoard, move, canMove } from '../src/game.mjs';

const seq = (...values) => {
  let i = 0;
  const calls = { count: 0 };
  const rng = () => {
    calls.count++;
    if (i >= values.length) throw new Error('rng called more times than expected');
    return values[i++];
  };
  rng.calls = calls;
  return rng;
};
const noRng = () => { throw new Error('rng must not be called'); };
const row = (r) => [r, [0, 0, 0, 0], [0, 0, 0, 0], [0, 0, 0, 0]];
const tiles = (board) => board.flat().filter((v) => v !== 0);

test('AC1 left: slide without merge', () => {
  const { state, moved } = move(fromBoard(row([0, 2, 0, 4])), 'left', seq(0.99, 0.5));
  assert.equal(moved, true);
  assert.deepEqual(state.board[0].slice(0, 2), [2, 4]);
});

test('AC1 left: merge rules', () => {
  const cases = [
    [[2, 2, 0, 0], [4, 0, 0, 0]],
    [[2, 2, 2, 2], [4, 4, 0, 0]],
    [[2, 2, 4, 0], [4, 4, 0, 0]],
    [[4, 4, 8, 0], [8, 8, 0, 0]],
    [[0, 2, 0, 2], [4, 0, 0, 0]],
    [[2, 2, 2, 0], [4, 2, 0, 0]],
  ];
  for (const [input, expected] of cases) {
    // place the spawned tile in the last empty cell of the bottom row so row 0 stays observable
    const { state } = move(fromBoard(row(input)), 'left', seq(0.999999, 0.5));
    assert.deepEqual(state.board[0], expected, `left ${JSON.stringify(input)}`);
  }
});

test('AC1 right, up and down follow the same rules', () => {
  const r = move(fromBoard(row([2, 2, 2, 0])), 'right', seq(0.999999, 0.5)).state;
  assert.deepEqual(r.board[0], [0, 0, 2, 4]);
  const col = [[2, 0, 0, 0], [2, 0, 0, 0], [2, 0, 0, 0], [2, 0, 0, 0]];
  const up = move(fromBoard(col), 'up', seq(0.999999, 0.5)).state;
  assert.deepEqual(up.board.map((x) => x[0]), [4, 4, 0, 0]);
  const down = move(fromBoard(col), 'down', seq(0, 0.5)).state;
  assert.deepEqual(down.board.map((x) => x[0]).slice(2), [4, 4]);
});

test('AC2 score adds merged values', () => {
  const start = fromBoard(row([2, 2, 2, 2]), 10);
  const { state, gained } = move(start, 'left', seq(0.999999, 0.5));
  assert.equal(gained, 8);
  assert.equal(state.score, 18);
});

test('AC3 spawn position and value come from rng, row-major', () => {
  // after moving left, empties in row-major order start at board[0][1]
  const { state } = move(fromBoard(row([0, 0, 0, 2])), 'left', seq(0, 0.95));
  assert.deepEqual(state.board[0], [2, 4, 0, 0]);
  const two = move(fromBoard(row([0, 0, 0, 2])), 'left', seq(0, 0.1)).state;
  assert.deepEqual(two.board[0], [2, 2, 0, 0]);
});

test('AC3 no move: no spawn, no rng call, nothing gained', () => {
  const start = fromBoard(row([2, 4, 8, 16]), 5);
  const { state, moved, gained } = move(start, 'left', noRng);
  assert.equal(moved, false);
  assert.equal(gained, 0);
  assert.equal(state.score, 5);
  assert.deepEqual(state.board, start.board);
});

test('AC3 createGame places exactly two tiles via rng', () => {
  const rng = seq(0, 0.5, 0, 0.95);
  const g = createGame({ rng });
  assert.equal(g.board.length, 4);
  assert.deepEqual(tiles(g.board).sort(), [2, 4].sort());
  assert.equal(g.board[0][0], 2);
  assert.equal(g.board[0][1], 4);
  assert.equal(g.score, 0);
  assert.equal(rng.calls.count, 4);
});

test('AC4 game over and won', () => {
  const full = fromBoard([[2, 4, 2, 4], [4, 2, 4, 2], [2, 4, 2, 4], [4, 2, 4, 2]]);
  assert.equal(canMove(full), false);
  assert.equal(full.over, true);
  const near = fromBoard(row([1024, 1024, 0, 0]));
  assert.equal(near.won, false);
  const after = move(near, 'left', seq(0.999999, 0.5)).state;
  assert.equal(after.board[0][0], 2048);
  assert.equal(after.won, true);
  assert.equal(canMove(fromBoard([[2, 2], [4, 8]])), true);
});

test('AC5 move does not mutate its input', () => {
  const board = row([2, 2, 0, 0]);
  const start = fromBoard(board);
  const snapshot = JSON.stringify(start);
  move(start, 'left', seq(0.5, 0.5));
  assert.equal(JSON.stringify(start), snapshot);
  board[0][0] = 99;
  assert.equal(start.board[0][0], 2, 'fromBoard must deep-copy');
});

test('AC6 size option', () => {
  const g = createGame({ size: 5, rng: seq(0, 0.5, 0, 0.5) });
  assert.equal(g.board.length, 5);
  assert.ok(g.board.every((r) => r.length === 5));
  assert.equal(tiles(g.board).length, 2);
});

test('AC7 index.html loads the module', () => {
  const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
  assert.match(html, /<script[^>]*type=["']module["']/);
  assert.match(html, /\.\/src\/game\.mjs/);
});
