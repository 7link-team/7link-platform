import * as fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';

export const sha256 = value => createHash('sha256').update(value).digest('hex');
export const readJSON = async file => JSON.parse(await fs.readFile(file, 'utf8'));
export async function writeText(file, text) {
  await fs.mkdir(path.dirname(file), { recursive: true });
  await fs.writeFile(file, text.replace(/\r\n?/g, '\n'), 'utf8');
}
export const writeJSON = (file, value) => writeText(file, JSON.stringify(value, null, 2) + '\n');
export async function saveState(dir, state) {
  await writeJSON(path.join(dir, 'state.tmp'), state);
  await fs.rename(path.join(dir, 'state.tmp'), path.join(dir, 'state.json'));
}
export function inside(root, relative) {
  if (typeof relative !== 'string' || !relative || path.isAbsolute(relative)) throw new Error('必须使用非空相对路径');
  const target = path.resolve(root, relative);
  const rel = path.relative(root, target);
  if (!rel || rel.startsWith('..') || path.isAbsolute(rel)) throw new Error(`路径越界: ${relative}`);
  return target;
}
export async function treeHashes(dir, prefix = '') {
  const result = {};
  for (const entry of (await fs.readdir(dir, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
    const file = path.join(dir, entry.name);
    const key = prefix + entry.name;
    if (entry.isSymbolicLink()) throw new Error(`不允许符号链接: ${file}`);
    if (entry.isDirectory()) Object.assign(result, await treeHashes(file, key + '/'));
    else if (entry.isFile()) result[key] = sha256(await fs.readFile(file));
  }
  return result;
}
export async function copyTree(source, target, excludeGit = false) {
  await fs.mkdir(target, { recursive: true });
  for (const entry of await fs.readdir(source, { withFileTypes: true })) {
    if (excludeGit && entry.name === '.git') continue;
    if (entry.isSymbolicLink()) throw new Error(`不允许符号链接: ${entry.name}`);
    const src = path.join(source, entry.name), dst = path.join(target, entry.name);
    if (entry.isDirectory()) await copyTree(src, dst, excludeGit);
    else if (entry.isFile()) await fs.copyFile(src, dst);
  }
}
