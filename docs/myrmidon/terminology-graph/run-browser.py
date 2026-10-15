#!/usr/bin/env python3
"""Verify the served, frozen graph at desktop and mobile sizes with agent-browser."""
import hashlib
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[2]
OUTPUT = REPO / 'docs/prototype/.audit-artifacts/terminology-graph'
OUTPUT.mkdir(parents=True, exist_ok=True)
SESSION = 'graph-semantic-followup'
BASE = ['bunx', 'agent-browser', '--session', SESSION]


def run(*args, source=None):
    result = subprocess.run(BASE + list(args), input=source, text=True,
                            capture_output=True, cwd=REPO / 'docs/prototype', timeout=45)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout.strip()


html = ROOT.parent / 'terminology-graph.html'
fingerprint = hashlib.sha256(html.read_bytes()).hexdigest()
suite = (ROOT / 'browser-check.js').read_text()
try:
    for width in (1280, 375):
        run('open', 'http://127.0.0.1:5500/docs/myrmidon/terminology-graph.html')
        run('set', 'viewport', str(width), '900')
        run('eval', '--stdin', source="window.__graphReview={state:'running'};Promise.resolve().then(()=>eval("
            + json.dumps(suite) + ")).then(value=>window.__graphReview={state:'passed',value},error=>window.__graphReview={state:'failed',error:String(error.stack||error)});'started'")
        deadline = time.monotonic() + 120
        while True:
            result = json.loads(run('eval', 'JSON.stringify(window.__graphReview)'))
            if isinstance(result, str):
                result = json.loads(result)
            if result and result['state'] != 'running':
                break
            if not result or time.monotonic() > deadline:
                (OUTPUT / f'semantic-followup-{width}-interrupted.json').write_text(json.dumps({
                    'state': 'interrupted', 'reason': 'Graph review state lost or deadline exceeded',
                    'width': width, 'htmlSha256': fingerprint, 'lastResult': result
                }, ensure_ascii=False, indent=2) + '\n')
                raise RuntimeError('Graph review state lost or deadline exceeded')
            time.sleep(2)
        result['htmlSha256'] = fingerprint
        result['width'] = width
        (OUTPUT / f'semantic-followup-{width}.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
        if result['state'] != 'passed':
            raise RuntimeError(result)
        assert hashlib.sha256(html.read_bytes()).hexdigest() == fingerprint, 'Source changed during browser verification'
        run('screenshot', str(OUTPUT / f'semantic-followup-{width}.png'))
        print(f'{width}: PASS', flush=True)
finally:
    run('close')
