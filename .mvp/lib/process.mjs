import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';

async function expandTestPatterns(command, cwd) {
  if (!['node', process.execPath].includes(command[0]) || !command.includes('--test')) return command;
  const expanded = [];
  for (const arg of command.slice(1)) {
    if (!/[?*]/.test(arg) || arg.startsWith('-')) { expanded.push(arg); continue; }
    const normalized = path.resolve(cwd ?? process.cwd(), arg).split(path.sep).join('/');
    const base = normalized.slice(0, normalized.search(/[?*]/));
    const root = base.slice(0, base.lastIndexOf('/')) || '/';
    const regex = new RegExp('^' + normalized.split('**/').map(part => part.split('**').map(piece => piece
      .replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '[^/]*').replace(/\?/g, '[^/]')).join('.*')).join('(?:.*/)?') + '$', process.platform === 'win32' ? 'i' : '');
    const matches = [];
    async function walk(dir) {
      let entries;
      try { entries = await fs.readdir(dir, { withFileTypes: true }); }
      catch (err) { if (err.code === 'ENOENT') return; throw err; }
      for (const entry of entries) {
        if (entry.name === '.git' || entry.isSymbolicLink()) continue;
        const file = path.join(dir, entry.name);
        if (entry.isDirectory()) await walk(file);
        else if (entry.isFile() && regex.test(file.split(path.sep).join('/'))) matches.push(file);
      }
    }
    await walk(root);
    expanded.push(...(matches.length ? matches.sort() : [arg]));
  }
  return [command[0], ...expanded];
}

export async function runCommand(command, { cwd, input = '', timeoutMs = 300_000 } = {}) {
  if (!Array.isArray(command) || !command.length || command.some(x => typeof x !== 'string')) throw new Error('command 必须是非空字符串数组');
  const started = Date.now();
  // Node 20 does not expand test globs itself. Keep task command arrays usable without a shell.
  const spawnedCommand = await expandTestPatterns(command, cwd);
  return new Promise(resolve => {
    const stdoutHash = createHash('sha256'), stderrHash = createHash('sha256');
    let stdout = Buffer.alloc(0), stderr = Buffer.alloc(0), timedOut = false, error = null;
    const env = { ...process.env };
    // A nested node --test must create its own harness instead of inheriting the parent's worker context.
    delete env.NODE_TEST_CONTEXT;
    const child = spawn(spawnedCommand[0] === 'node' ? process.execPath : spawnedCommand[0], spawnedCommand.slice(1), {
      cwd, env, shell: false, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'],
    });
    const collect = (chunk, stream) => {
      if (stream === 'stdout') { stdoutHash.update(chunk); stdout = Buffer.concat([stdout, chunk]).subarray(-4096); }
      else { stderrHash.update(chunk); stderr = Buffer.concat([stderr, chunk]).subarray(-4096); }
    };
    child.stdout.on('data', chunk => collect(chunk, 'stdout'));
    child.stderr.on('data', chunk => collect(chunk, 'stderr'));
    child.on('error', err => { error = err.message; });
    child.stdin.on('error', () => {});
    child.stdin.end(input, 'utf8');
    const timer = setTimeout(() => { timedOut = true; child.kill(); }, timeoutMs);
    child.on('close', (exitCode, signal) => {
      clearTimeout(timer);
      const hashes = { stdout: stdoutHash.digest('hex'), stderr: stderrHash.digest('hex') };
      resolve({ command, spawnedCommand, exitCode, signal, timedOut, error, durationMs: Date.now() - started,
        stdout: stdout.toString('utf8'), stderr: stderr.toString('utf8'),
        outputSha256: createHash('sha256').update(JSON.stringify(hashes)).digest('hex'), streamSha256: hashes });
    });
  });
}
export const passed = result => result.exitCode === 0 && !result.error && !result.timedOut;

// Windows cannot spawn npm's .cmd shim without a shell. Resolve its JS entry point instead.
export async function findCodex() {
  for (const dir of (process.env.PATH ?? '').split(path.delimiter).filter(Boolean)) {
    for (const name of process.platform === 'win32' ? ['codex.exe', 'codex.cmd', 'codex'] : ['codex']) {
      const file = path.join(dir.replace(/^"|"$/g, ''), name);
      try {
        await fs.access(file);
        if (name.endsWith('.cmd')) {
          const shim = await fs.readFile(file, 'utf8');
          const match = shim.match(/%dp0%[\\/]([^"\r\n]*codex\.(?:js|mjs))/i);
          const entry = match ? path.resolve(path.dirname(file), match[1])
            : path.join(path.dirname(file), 'node_modules', '@openai', 'codex', 'bin', 'codex.js');
          await fs.access(entry);
          return [process.execPath, entry];
        }
        return [file];
      } catch { /* Try the next PATH entry. */ }
    }
  }
  throw new Error('PATH 中未找到可直接启动的 Codex CLI（Windows 支持 codex.exe 或 npm JS shim）');
}
