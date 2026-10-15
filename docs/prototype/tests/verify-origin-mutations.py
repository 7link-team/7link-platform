#!/usr/bin/env python3
"""Verify origin guards in an isolated copy; never mutate the working prototype."""
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / '.audit-artifacts/origin-integrity-mutations'
OUTPUT.mkdir(parents=True, exist_ok=True)
original = (ROOT / 'src/store.js').read_text()
variants = {
    'accept-wrong-execution-channel': ('executionId && source.channel !== `exec-${executionId}`', 'false'),
    'ignore-message-execution-identity': ('source.executionId && source.executionId !== executionId', 'false'),
    'accept-nested-thread-root': ('root.thread && root.thread !== root.id', 'false'),
    'ignore-explicit-thread-channel': ('(!channel || message.channel === channel)', 'true'),
    'discard-explicit-thread-origin': ('message.id === threadId && (!channel || message.channel === channel)', 'false && (!channel || message.channel === channel)'),
}
results = []
with tempfile.TemporaryDirectory(prefix='prototype-origin-mutations-') as directory:
    stage = Path(directory)
    shutil.copytree(ROOT / 'src', stage / 'src')
    (stage / 'tests').mkdir()
    shutil.copy(ROOT / 'tests/work-drafts.test.js', stage / 'tests/work-drafts.test.js')
    shutil.copy(ROOT / 'package.json', stage / 'package.json')
    (stage / 'node_modules').symlink_to(ROOT / 'node_modules', target_is_directory=True)

    def run(name):
        result = subprocess.run(['bun', 'test', 'tests/work-drafts.test.js'], cwd=stage,
                                text=True, capture_output=True, timeout=60)
        (OUTPUT / f'{name}.log').write_text(result.stdout + result.stderr)
        return result.returncode

    assert run('control') == 0, 'Unmutated control must pass before interpreting mutants'
    for name, (before, after) in variants.items():
        assert original.count(before) == 1, f'{name}: mutation anchor is not unique'
        (stage / 'src/store.js').write_text(original.replace(before, after))
        code = run(name)
        results.append({'name': name, 'detected': code != 0, 'exitCode': code})
    (OUTPUT / 'results.json').write_text(json.dumps({'control': 'PASS', 'mutations': results}, indent=2) + '\n')
    assert all(result['detected'] for result in results), 'An origin invariant mutant survived'
print(f'Control PASS; {len(results)}/{len(results)} origin mutants detected')
