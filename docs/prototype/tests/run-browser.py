#!/usr/bin/env python3
"""Run the prototype's browser assertions against an already running local server."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', default='http://127.0.0.1:4173')
parser.add_argument('--width', type=int, default=1440)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--session', default=f'prototype-audit-{uuid.uuid4().hex[:10]}')
parser.add_argument('suites', nargs='*', help='Optional browser-*.js basenames; default: all suites')
args = parser.parse_args()
paths = sorted((ROOT / 'tests').glob('browser-*.js'))
if args.suites:
    unknown = set(args.suites) - {path.name for path in paths}
    if unknown:
        parser.error(f'Unknown suites: {sorted(unknown)}')
    paths = [path for path in paths if path.name in args.suites]
if args.width < 320:
    parser.error('Viewport width must be at least 320')
base = ['bunx', 'agent-browser', '--session', args.session]

def run(command, source=None):
    result = subprocess.run(base + command, input=source, text=True, capture_output=True, cwd=ROOT, timeout=180)
    if result.returncode:
        raise RuntimeError((result.stdout + result.stderr).strip())
    return result.stdout.strip()

def decoded(output):
    data = json.loads(output)
    # agent-browser quotes JS string results; some older suites stringify their result.
    if isinstance(data, str):
        data = json.loads(data)
    if not isinstance(data, dict):
        raise ValueError('Suite must return a structured assertion result')
    return data

def fingerprints():
    return {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted((ROOT / 'src').glob('*')) if path.is_file()}

before = fingerprints()
report = {'url': args.url, 'width': args.width, 'session': args.session, 'sourceHashes': before, 'suites': []}
try:
    run(['open', args.url.rstrip('/') + '/#/overview'])
    run(['set', 'viewport', str(args.width), '812' if args.width < 700 else '1000'])
    for path in paths:
        try:
            run(['eval', '--stdin'], "localStorage.removeItem('collaboration-prototype-v1');location.hash='/overview';")
            run(['reload'])
            if path.name in {'browser-previews.js', 'browser-preview-resize.js', 'browser-features.js'}:
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}}; const {read}=await import('./src/store.js'); const s=structuredClone(read()); s.work.find(w=>w.id==='W-104').previewResources=" + json.dumps([
                    {'id':'primary','name':'Web · 主预览','kind':'Web','status':'就绪'},
                    {'id':'secondary','name':'Web · 管理端','kind':'Web','status':'就绪'},
                    {'id':'api','name':'API','kind':'API','status':'就绪'},
                    {'id':'terminal','name':'终端','kind':'终端','status':'就绪'},
                    {'id':'files','name':'仓库代码','kind':'仓库','status':'就绪'},
                    {'id':'readme','name':'README.md','kind':'文稿','status':'就绪'},
                ], ensure_ascii=False) + ";console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            if path.name == 'browser-audit-list.js':
                fixture = subprocess.run(['bun', '-e', "globalThis.localStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}}; const {read}=await import('./src/store.js'); const s=structuredClone(read()); s.events=Array.from({length:501},(_,i)=>({id:'audit-'+i,text:i===0?'LongTitle'+('LongUnbrokenText'.repeat(80)):'核对任务 '+i,actor:'审计员',time:'2026-10-13T08:00:00Z',target:'/p/launch/work-items/W-104?evidence='+i})); console.log(JSON.stringify(s));"], cwd=ROOT, text=True, capture_output=True, check=True)
                run(['eval', '--stdin'], "localStorage.setItem('collaboration-prototype-v1'," + json.dumps(fixture.stdout.strip()) + ");")
                run(['reload'])
            helper = ROOT / 'tests' / 'qa-browser-helpers.js'
            if helper.exists():
                run(['eval', '--stdin'], helper.read_text())
            prerequisites = []
            if path.name == 'browser-defects.js':
                # Regression starts from an actually verified closure. Run its UI
                # prerequisite after this suite's reset, never inject green flags.
                prerequisite = ROOT / 'tests' / 'browser-qa.js'
                outcome = decoded(run(['eval', '--stdin'], prerequisite.read_text()))
                if outcome.get('pass') is not True and outcome.get('result') != 'PASS':
                    raise RuntimeError('Defect QA prerequisite failed: ' + json.dumps(outcome, ensure_ascii=False))
                prerequisites.append({'suite': prerequisite.name, 'evidence': outcome,
                                      'scriptHash': hashlib.sha256(prerequisite.read_bytes()).hexdigest()})
            result = decoded(run(['eval', '--stdin'], path.read_text()))
            if path.name == 'browser-routes.js':
                failures = [page for page in result.get('pages', []) if page.get('broken') or page.get('overflow') or not page.get('heading')]
                passed = bool(result.get('pages')) and not result.get('errors') and not failures
            else:
                passed = result.get('pass') is True or result.get('result') == 'PASS'
            if not passed:
                raise RuntimeError(json.dumps(result, ensure_ascii=False))
            report['suites'].append({'suite': path.name, 'pass': True, 'evidence': result,
                                    'prerequisites': prerequisites,
                                    'scriptHash': hashlib.sha256(path.read_bytes()).hexdigest()})
            print(path.name, 'PASS', flush=True)
        except (RuntimeError, ValueError, subprocess.TimeoutExpired) as error:
            report['suites'].append({'suite': path.name, 'pass': False, 'error': str(error)})
            print(path.name, 'FAIL', str(error)[-900:], flush=True)
except (RuntimeError, subprocess.TimeoutExpired) as error:
    report['setupError'] = str(error)
finally:
    report['sourceUnchanged'] = before == fingerprints()
    report['pass'] = bool(report['suites']) and len(report['suites']) == len(paths) and all(item['pass'] for item in report['suites']) and report['sourceUnchanged'] and 'setupError' not in report
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    try:
        run(['close'])
    except (RuntimeError, subprocess.TimeoutExpired):
        pass
print('Evidence:', args.output, 'PASS' if report['pass'] else 'NOT READY', flush=True)
sys.exit(0 if report['pass'] else 1)
