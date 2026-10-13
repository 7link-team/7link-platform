"""Opt-in modal discard guard; real Escape and backdrop pointer regression.
Run: python3 tests/browser-dirty-forms.py [base_url] [width]
Includes same-tab navigation recovery for goal editors and calibration.
"""
import json
import subprocess
import sys
import time

BASE = sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:4173'
WIDTH = int(sys.argv[2]) if len(sys.argv) > 2 else 1440
CLI = ['bunx', 'agent-browser', '--session', 'dirty-form-regression']
if sys.platform == 'darwin':
    CLI += ['--executable-path', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome']


def cmd(*args):
    result = subprocess.run(CLI + list(args), capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    return result.stdout


def js(code):
    return json.loads(cmd('eval', code))


def click(text):
    js(f"(()=>{{const scope=[...document.querySelectorAll('dialog[open]')].at(-1)||document;const e=[...scope.querySelectorAll('button')].find(e=>e.textContent.trim()==={json.dumps(text)}||e.getAttribute('aria-label')==={json.dumps(text)});if(!e)throw Error('Missing '+{json.dumps(text)});e.focus();e.click();return true}})()")
    time.sleep(.12)


def fill(name, value):
    cmd('fill', f'dialog[open] [name="{name}"]', value)


def count():
    return js("document.querySelectorAll('dialog[open]').length")


def escape():
    cmd('tab', 't1')
    cmd('press', 'Escape')
    time.sleep(.15)


def route(path):
    js('location.hash=' + json.dumps(path))
    time.sleep(.2)


def history(action):
    cmd(action)
    time.sleep(.2)


try:
    cmd('open', BASE + '/#/goals/G-01')
    cmd('set', 'viewport', str(WIDTH), '900')
    cmd('tab', 't1')
    click('编辑')
    original = js("document.querySelector('dialog [name=名称]').value")
    escape()
    assert count() == 0, 'Pristine editor warned'
    click('编辑')
    fill('名称', 'dirty-value')
    escape()
    assert count() == 2, 'Dirty Escape bypassed guard'
    assert js("[...document.querySelectorAll('dialog[open]')].at(-1).getAttribute('aria-label')") == '放弃未保存的修改？'
    escape()
    assert count() == 1, 'Confirmation Escape closed parent'
    assert js("document.querySelector('dialog [name=名称]').value") == 'dirty-value'
    assert js("document.querySelector('dialog').contains(document.activeElement)"), 'Nested focus lost'
    fill('名称', original)
    escape()
    assert count() == 0, 'Reverted editor still dirty'

    for exit_action in ['取消', '关闭弹窗', 'backdrop']:
        click('编辑')
        fill('名称', 'discard-' + exit_action)
        if exit_action == 'backdrop':
            cmd('mouse', 'move', '2', '300')
            cmd('mouse', 'down')
            cmd('mouse', 'up')
            time.sleep(.15)
        else:
            click(exit_action)
        assert count() == 2, (exit_action, 'No guard')
        click('继续编辑')
        assert count() == 1
        assert js("document.querySelector('dialog [name=名称]').value").startswith('discard-')
        click('关闭弹窗')
        click('放弃修改')
        assert count() == 0
        assert js("document.activeElement.textContent.trim()") == '编辑', 'Discard did not restore opener'

    click('编辑')
    fill('名称', '')
    click('保存')
    assert count() == 1 and js("document.querySelector('dialog [aria-invalid=true]')!==null"), 'Invalid form closed'
    fill('名称', '已明确保存的目标')
    click('保存')
    assert count() == 0, 'Save success prompted discard'
    assert js("document.querySelector('main h1').textContent") == '已明确保存的目标'

    click('手工校准进度')
    fill('手工进度（%）', '75')
    fill('校准依据', '   ')
    click('保存校准')
    assert count() == 1 and js("document.querySelector('dialog [role=alert]')?.textContent.includes('依据')"), 'Store validation failed to retain form'
    click('取消')
    assert count() == 2, 'Failed save incorrectly marked clean'
    click('继续编辑')
    fill('校准依据', '实际核对证据后校准')
    click('保存校准')
    assert count() == 0, 'Valid calibration incorrectly guarded'

    route('/goals')
    route('/goals/G-01')
    click('编辑')
    fill('名称', '浏览器后退保留的草稿')
    history('back')
    assert count() == 0, 'Route did not unmount editor'
    history('forward')
    click('编辑')
    assert js("document.querySelector('dialog [name=名称]').value") == '浏览器后退保留的草稿', 'Browser Back lost draft'
    assert js("document.querySelector('dialog').textContent.includes('已恢复未保存的修改')"), 'Recovery not disclosed'
    escape()
    assert count() == 2, 'Recovered draft baseline was silently reset'
    click('放弃修改')
    click('编辑')
    assert js("document.querySelector('dialog [name=名称]').value") == '已明确保存的目标', 'Discard failed to clear recovery'
    fill('名称', '旧修订不得覆盖新校准')
    history('back')
    history('forward')
    click('手工校准进度')
    fill('手工进度（%）', '76')
    fill('校准依据', '用于验证旧草稿冲突的新校准')
    click('保存校准')
    click('编辑')
    click('保存')
    assert count() == 1 and js("document.querySelector('dialog [role=alert]')?.textContent.includes('变更')"), 'Recovered editor overwrote newer revision'
    click('取消')
    click('放弃修改')

    click('手工校准进度')
    fill('校准依据', '待恢复的校准证据')
    history('back')
    history('forward')
    click('编辑')
    fill('名称', '使旧校准失效的新修订')
    click('保存')
    click('手工校准进度')
    assert js("document.querySelector('dialog [name=校准依据]').value") == '待恢复的校准证据', 'Calibration draft lost'
    click('保存校准')
    assert count() == 1 and js("document.querySelector('dialog [role=alert]')?.textContent.includes('变更')"), 'Calibration recovery hid original revision'
    click('取消')
    click('放弃修改')
    click('手工校准进度')
    assert js("document.querySelector('dialog [name=校准依据]').value") == '', 'Discard failed to clear calibration cache'
    escape()
    assert count() == 0

    js("location.hash='/reports'")
    time.sleep(.2)
    click('生成 / 打开周期报告')
    fill('周期内日期', '2024-02-29')
    js("document.querySelector('.date-trigger').click()")
    escape()
    assert count() == 1 and not js("!!document.querySelector('.date-popover')"), 'Date Escape did not stop at popover'
    escape()
    assert count() == 2, 'Date edit missing from dirty value'
    click('放弃修改')

    route('/reports/RP-01')
    click('编辑')
    fill('人工校准正文', '恢复后明确保存的报告正文')
    history('back')
    history('forward')
    click('编辑')
    assert js("document.querySelector('dialog [name=人工校准正文]').value") == '恢复后明确保存的报告正文', 'Report recovery lost manual text'
    click('保存')
    assert count() == 0, 'Recovered report could not save'
    click('编辑')
    assert not js("document.querySelector('dialog').textContent.includes('已恢复未保存的修改')"), 'Save did not clear cached report'
    escape()
    assert count() == 0, 'Saved report reopened dirty'

    js("location.hash='/agents'")
    time.sleep(.2)
    click('创建 Agent 提案')
    cmd('click', 'dialog fieldset:first-of-type label:first-of-type input[type=checkbox]')
    escape()
    assert count() == 2, 'Configuration checkbox not dirty'
    click('继续编辑')
    cmd('click', 'dialog fieldset:first-of-type label:first-of-type input[type=checkbox]')
    escape()
    assert count() == 0, 'Reverted checkbox still dirty'

    route('/goals')
    click('新建目标')
    fill('名称', '仅属于原租户的新目标草稿')
    route('/overview')
    js("document.querySelector('.tenant-trigger').click()")
    time.sleep(.2)
    click('待处理邀请 · 1')
    click('接受并进入')
    route('/goals')
    click('新建目标')
    assert js("document.querySelector('dialog [name=名称]').value") == '', 'Draft leaked into another tenant'
    escape()
    js("document.querySelector('.tenant-trigger').click()")
    time.sleep(.2)
    js("[...document.querySelectorAll('.tenant-popover button')].find(e=>e.textContent.includes('Northstar Studio')).click()")
    time.sleep(.2)
    route('/goals')
    click('新建目标')
    assert js("document.querySelector('dialog [name=名称]').value") == '仅属于原租户的新目标草稿', 'Tenant round trip lost original draft'
    click('取消')
    click('放弃修改')

    click('切换主题')
    click('Modern 简约')
    escape()
    assert count() == 0, 'Instant-save theme dialog warned'
    assert js("document.documentElement.dataset.theme") == 'modern'
    click('全局搜索')
    cmd('fill', '[aria-label="全局搜索关键词"]', 'unsaved search is not a draft')
    escape()
    assert count() == 0, 'Search dialog warned'
    print(json.dumps({'result': 'PASS', 'width': WIDTH, 'checks': ['pristine and reverted no warning', 'real Escape / nested Escape and focus', 'cancel / X / real backdrop discard or continue', 'invalid form retained', 'business error remains dirty', 'explicit success dismiss', 'real Back/Forward goal and calibration recovery', 'recovered original baseline and revision conflict', 'report recovery and save clears cache', 'discard clears recovery', 'tenant-isolated round trip', 'date popover Escape isolation', 'configuration checkbox dirty and reverted', 'theme/search opt-out']}, ensure_ascii=False))
except Exception:
    print(cmd('errors'), file=sys.stderr)
    print(js("({url:location.href,ready:document.readyState,body:document.body.innerText.slice(0,800),dialogs:[...document.querySelectorAll('dialog')].map(d=>({title:d.getAttribute('aria-label'),open:d.open}))})"), file=sys.stderr)
    raise
finally:
    cmd('close')
