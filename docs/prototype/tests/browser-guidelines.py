"""Real Chromium keyboard and six-theme mobile contrast regression.
Run from docs/prototype: python3 tests/browser-guidelines.py [base_url]
Uses a dedicated agent-browser session; browser data is local simulation only.
"""
import json
import subprocess
import sys
import time

BASE_URL = sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:4173'
CLI = ['bunx', 'agent-browser', '--session', 'guidelines-regression']
if sys.platform == 'darwin':
    CLI += ['--executable-path', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome']


def command(*args):
    result = subprocess.run(CLI + list(args), text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    return result.stdout


def evaluate(script):
    return json.loads(command('eval', script))


def navigate(path):
    command('open', BASE_URL + '/#' + path)
    command('tab', 't1')
    time.sleep(.2)


def keyboard_tabs(selector):
    # Selection/render changes must not steal focus until a user operates a tab.
    before = evaluate("document.activeElement?.getAttribute('role')")
    assert before != 'tab', 'Mount unexpectedly focused a tab'
    evaluate(f"document.querySelector({json.dumps(selector)}).querySelector('[role=tab]').focus()")
    for key, target in [('End', -1), ('Home', 0), ('ArrowRight', 1), ('ArrowLeft', 0), ('ArrowLeft', -1)]:
        command('tab', 't1')
        command('press', key)
        time.sleep(.12)
        value = evaluate(f"""(()=>{{const tabs=[...document.querySelector({json.dumps(selector)}).querySelectorAll('[role=tab]')];
          const next=tabs.at({target} % tabs.length);return {{focused:document.activeElement===next,selected:next.getAttribute('aria-selected'),
          stops:tabs.filter(t=>t.tabIndex===0).length}}}})()""")
        assert value == {'focused': True, 'selected': 'true', 'stops': 1}, (key, value)
    # Only one tab is reachable in ordinary Tab traversal.
    command('press', 'Tab')
    assert evaluate(f"document.querySelector({json.dumps(selector)}).contains(document.activeElement)") is False


try:
    navigate('/worklogs')
    command('set', 'viewport', '1440', '1000')
    keyboard_tabs('[aria-label="内容视图"]')
    navigate('/p/launch/artifacts/W-104')
    keyboard_tabs('[aria-label="成果视图"]')
    navigate('/p/launch/flow?view=list')
    evaluate("document.querySelector('.flow-list button').click()")
    time.sleep(.15)
    keyboard_tabs('[aria-label="节点详情"]')
    navigate('/chat?execution_id=W-104')
    keyboard_tabs('[aria-label="任务预览标签"]')
    navigate('/testplans')
    headings = evaluate("[...document.querySelectorAll('main h1,main h2,main h3')].map(e=>Number(e.tagName[1]))")
    assert headings[0] == 1 and all(b <= a + 1 for a, b in zip(headings, headings[1:])), headings
    print('PASS: four tab sets, arrows/Home/End/wrap/roving/no mount focus; QA heading')

    navigate('/reports')
    command('set', 'viewport', '375', '812')
    summaries = []
    for theme, name in [('aurora', 'Aurora 极光'), ('modern', 'Modern 简约'), ('business', 'Business 商务'),
                        ('eyeCare', '护眼纸感'), ('pinkKawaii', 'Pink Kawaii'), ('neoBrutalism', 'Neo Brutalism')]:
        for dark in [False, True]:
            observed = evaluate(f"""(async()=>{{
              document.querySelector('[aria-label="切换主题"]').click();await new Promise(r=>setTimeout(r,100));
              [...document.querySelectorAll('.theme-card')].find(e=>e.textContent.includes({json.dumps(name)})).click();
              await new Promise(r=>setTimeout(r,100));const toggle=document.querySelector('dialog input[type=checkbox]');
              if(toggle.checked!=={str(dark).lower()})toggle.click();await new Promise(r=>setTimeout(r,100));
              document.querySelector('dialog [aria-label="关闭弹窗"]').click();
              await new Promise(r=>setTimeout(r,600));return {{theme:document.documentElement.dataset.theme,dark:document.documentElement.classList.contains('dark'),
              overflow:document.documentElement.scrollWidth>innerWidth+1}}}})()""")
            assert observed == {'theme': theme, 'dark': dark, 'overflow': False}, observed
            audit = json.loads(command('a11y', '--selector', '.bottom-nav', '--json'))['data']
            assert not audit['violations'], (theme, dark, audit['violations'])
            summaries.append({'theme': theme, 'dark': dark, 'counts': audit['counts']})
    print(json.dumps({'result': 'PASS', 'mobile_bottom_nav': summaries}, ensure_ascii=False))
finally:
    command('close')
