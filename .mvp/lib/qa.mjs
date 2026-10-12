import * as fs from 'node:fs/promises';
import path from 'node:path';
import http from 'node:http';
import os from 'node:os';
import { inside } from './files.mjs';
import { runCommand, passed } from './process.mjs';

export function screenshotConfig(task, options = {}) {
  const configured = task.qa?.screenshot;
  const page = options.qaScreenshot ?? configured?.page;
  if (options.qaViewports && !page) throw new Error('--qa-viewports 需要截图页面');
  if (!page) return null;
  inside(process.cwd(), page);
  const viewports = options.qaViewports ? options.qaViewports.split(',').map(value => {
    if (!/^\d+x\d+$/.test(value)) throw new Error('视口必须为 900x900,420x800 格式');
    return value.split('x').map(Number);
  }) : configured?.viewports ?? [[900, 900], [420, 800]];
  if (!Array.isArray(viewports) || !viewports.length || viewports.some(v => !Array.isArray(v) || v.length !== 2 || v.some(n => !Number.isInteger(n) || n < 1 || n > 10000))) throw new Error('截图视口无效');
  return { page, viewports };
}
async function browser() {
  if (process.env.MYR_BROWSER) return process.env.MYR_BROWSER;
  const candidates = [];
  for (const root of [process.env['PROGRAMFILES(X86)'], process.env.PROGRAMFILES, process.env.LOCALAPPDATA].filter(Boolean)) {
    candidates.push(path.join(root, 'Microsoft/Edge/Application/msedge.exe'), path.join(root, 'Google/Chrome/Application/chrome.exe'));
  }
  for (const root of (process.env.PATH ?? '').split(path.delimiter).filter(Boolean)) {
    for (const name of ['chromium', 'google-chrome', ...(process.platform === 'win32' ? ['chromium.exe', 'google-chrome.exe'] : [])]) candidates.push(path.join(root.replace(/^"|"$/g, ''), name));
  }
  for (const candidate of candidates) { try { await fs.access(candidate); return candidate; } catch { /* Next candidate. */ } }
  return null;
}
const MIN_LAYOUT_WIDTH = 500;
const FRAME_PATH = '/__myr_viewport_frame';
export async function screenshots(dir, workspace, item, round, config, launch = runCommand) {
  const evidence = { files: [], unverified: [], notes: [], invocations: [] };
  if (!config) return evidence;
  const executable = await browser();
  if (!executable) { evidence.unverified.push('未能截图：未找到浏览器'); return evidence; }
  const server = http.createServer(async (req, res) => {
    try {
      const requested = new URL(req.url, 'http://localhost');
      if (requested.pathname === FRAME_PATH) {
        const width = Number(requested.searchParams.get('w')), height = Number(requested.searchParams.get('h'));
        const src = `/${config.page.split(/[\\/]/).map(encodeURIComponent).join('/')}`;
        res.setHeader('Content-Type', 'text/html; charset=utf-8');
        res.end(`<!doctype html><meta charset="utf-8"><style>html,body{margin:0;overflow:hidden}iframe{display:block;border:0}</style><iframe src="${src}" width="${width}" height="${height}"></iframe>`);
        return;
      }
      const relative = decodeURIComponent(new URL(req.url, 'http://localhost').pathname).replace(/^\/+/, '') || 'index.html';
      const file = inside(workspace, relative);
      const real = await fs.realpath(file);
      inside(await fs.realpath(workspace), path.relative(await fs.realpath(workspace), real));
      if (relative.split(/[\\/]/).some(part => part.startsWith('.'))) throw new Error('private path');
      const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml' };
      res.setHeader('Content-Type', types[path.extname(file)] ?? 'application/octet-stream');
      res.end(await fs.readFile(file));
    } catch { res.writeHead(404); res.end('Not found'); }
  });
  try {
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    await fs.access(inside(workspace, config.page));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const url = `${origin}/${config.page.split(/[\\/]/).map(encodeURIComponent).join('/')}`;
    await fs.mkdir(path.join(dir, 'qa'), { recursive: true });
    for (const [width, height] of config.viewports) {
      const file = path.join(dir, 'qa', `${item.id}.round${round}.${width}x${height}.png`);
      const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'myr-browser-'));
      // Headless browsers lay out narrow windows at a wider minimum width and crop the capture, so a narrow
      // viewport is rendered in an iframe of exactly that size placed where the capture is taken.
      const framed = width < MIN_LAYOUT_WIDTH;
      const target = framed ? `${origin}${FRAME_PATH}?w=${width}&h=${height}` : url;
      if (framed) evidence.notes.push(`${width}x${height}：页面在 ${width}×${height} 的 iframe 中按真实视口宽度渲染`);
      let result;
      try {
        result = await launch([executable, '--headless=new', '--disable-gpu', '--hide-scrollbars', `--user-data-dir=${profile}`,
          `--window-size=${width},${height}`, '--virtual-time-budget=4000', `--screenshot=${file}`, target], { cwd: workspace, timeoutMs: 30_000 });
      } finally { await fs.rm(profile, { recursive: true, force: true, maxRetries: 5 }).catch(() => {}); }
      evidence.invocations.push(result);
      const valid = await fs.readFile(file).then(bytes => bytes.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])), () => false);
      if (passed(result) && valid) evidence.files.push(file);
      else evidence.unverified.push(`未能截图：${width}x${height}`);
    }
  } catch (err) { evidence.unverified.push(`未能截图：${err.message}`); }
  finally { await new Promise(resolve => server.close(resolve)); }
  return evidence;
}

export function validConclusion(value, acceptance = false) {
  const findings = acceptance ? value?.uxFindings : value?.findings;
  return typeof value?.pass === 'boolean' && Array.isArray(findings)
    && findings.every(f => f && ['BLOCKER', 'MAJOR', 'MINOR'].includes(f.severity) && ['title', 'evidence', 'expected'].every(k => typeof f[k] === 'string'))
    && (!acceptance || (Array.isArray(value.criteria) && value.criteria.every(c => c && typeof c.ref === 'string' && typeof c.evidence === 'string' && ['PASS', 'FAIL', 'UNVERIFIED'].includes(c.verdict))));
}
export const severe = findings => findings.some(f => ['BLOCKER', 'MAJOR'].includes(f.severity));
