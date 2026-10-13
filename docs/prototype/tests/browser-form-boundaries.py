"""Real-browser close/recovery checks for editable forms.
Run: python3 tests/browser-form-boundaries.py [base_url] [width] [all|core|work|collab|org]
"""
import json
import subprocess
import sys
import time
import uuid

BASE = sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:4173'
WIDTH = int(sys.argv[2]) if len(sys.argv) > 2 else 1440
GROUP = sys.argv[3] if len(sys.argv) > 3 else 'all'
if GROUP not in {'all', 'core', 'work', 'collab', 'org'}:
    raise ValueError('Expected all, core, work, collab or org')
if GROUP == 'all':
    for group in ['core', 'work', 'collab', 'org']:
        subprocess.run([sys.executable, __file__, BASE, str(WIDTH), group], check=True)
    sys.exit(0)
CLI = ['bunx', 'agent-browser', '--session', 'form-boundaries-' + uuid.uuid4().hex[:8]]
if sys.platform == 'darwin':
    CLI += ['--executable-path', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome']


def cmd(*args):
    result = subprocess.run(CLI + list(args), capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    return result.stdout


def js(source):
    return json.loads(cmd('eval', source))


def click(text):
    js("(()=>{const scope=[...document.querySelectorAll('dialog[open]')].at(-1)||document;const target=[...scope.querySelectorAll('button')].find(e=>e.textContent.trim()===" + json.dumps(text) + "||e.getAttribute('aria-label')===" + json.dumps(text) + ");if(!target)throw Error('Missing button '+" + json.dumps(text) + ");target.focus();target.click();return true})()")
    time.sleep(.15)


def fill(name, value):
    scope = 'dialog[open] ' if dialogs() else ''
    selector = scope + '[name=' + json.dumps(name, ensure_ascii=False) + ']'
    if value == '':
        js("(()=>{const field=document.querySelector(" + json.dumps(selector) + ");field.focus();field.select();return true})()")
        cmd('press', 'Backspace')
    else:
        cmd('fill', selector, value)


def route(path):
    js('location.hash=' + json.dumps(path))
    time.sleep(.25)


def escape():
    cmd('tab', 't1')
    cmd('press', 'Escape')
    time.sleep(.2)


def dialogs():
    return js("document.querySelectorAll('dialog[open]').length")


def tenant_menu():
    js("document.querySelector('.tenant-trigger').click()")
    time.sleep(.15)


def original_tenant():
    tenant_menu()
    js("[...document.querySelectorAll('.tenant-popover button')].find(e=>e.textContent.includes('Northstar Studio')).click()")
    time.sleep(.2)


def calendar_task():
    js("[...document.querySelectorAll('.calendar-task')].find(e=>e.textContent.includes('新品落地页')).click()")
    time.sleep(.2)


def choose(label, prefix):
    cmd('click', '[role=combobox][aria-label=' + json.dumps(label, ensure_ascii=False) + ']')
    js("[...document.querySelectorAll('[role=option]')].find(e=>e.textContent.trim().startsWith(" + json.dumps(prefix) + ")).click()")
    time.sleep(.15)


try:
    cmd('open', BASE + '/#/overview')
    cmd('set', 'viewport', str(WIDTH), '900')
    cmd('tab', 't1')
    checks = []
    if GROUP in {'all', 'core'}:
        tenant_menu()
        click('待处理邀请 · 1')
        escape()
        assert dialogs() == 0, 'Readonly invitation dialog warned'
        tenant_menu()
        click('创建租户')
        fill('租户名称', '   ')
        click('创建并进入')
        assert dialogs() == 1, 'Failed create dismissed'
        click('取消')
        assert dialogs() == 2, 'Failed create lost dirty state'
        click('放弃修改')
        tenant_menu()
        click('创建租户')
        assert js("document.querySelector('[name=租户名称]').value") == '', 'Discard retained tenant input'
        escape()
        tenant_menu()
        click('加入租户')
        fill('邀请码', 'INVALID-CODE')
        click('验证邀请并加入')
        assert dialogs() == 1, 'Caught join error incorrectly dismissed'
        escape()
        assert dialogs() == 2, 'Caught join error incorrectly marked clean'
        click('继续编辑')
        fill('邀请码', 'DEMO-JOIN')
        click('验证邀请并加入')
        assert dialogs() == 0, 'Successful join prompted discard'
        original_tenant()

        calendar = '/p/launch/work-items?view=calendar'
        route(calendar)
        calendar_task()
        escape()
        assert dialogs() == 0, 'Pristine schedule warned'
        calendar_task()
        fill('开始日期', '2026-02-31')
        fill('调整说明', '恢复的排期说明')
        click('保存排期')
        assert dialogs() == 1 and js("document.querySelector('dialog [role=alert]')?.textContent.includes('真实有效')"), 'Invalid schedule not retained'
        click('取消')
        assert dialogs() == 2
        click('继续编辑')
        js("document.querySelector('dialog a.text-link').click()")
        time.sleep(.2)
        cmd('back')
        time.sleep(.25)
        calendar_task()
        assert js("document.querySelector('[name=调整说明]').value") == '恢复的排期说明', 'Calendar internal link lost draft'
        assert js("document.querySelector('dialog').textContent.includes('已恢复')")
        fill('开始日期', '2026-10-15')
        fill('结束日期', '2026-10-19')
        click('保存排期')
        assert dialogs() == 0, 'Recovered schedule save did not dismiss'
        calendar_task()
        assert not js("document.querySelector('dialog').textContent.includes('已恢复')"), 'Schedule success retained cache'
        escape()

        route('/p/launch/work-items/W-104?tab=review')
        click('开始独立验证')
        plan_route = js('location.hash.slice(1)')
        click('修订测试计划')
        fill('变更影响与重验范围', '未保存的计划修订依据')
        route('/testplans')
        route(plan_route)
        click('修订测试计划')
        assert js("document.querySelector('[name=变更影响与重验范围]').value") == '未保存的计划修订依据', 'QA plan draft lost'
        click('固定计划修订')
        assert dialogs() == 1 and js("document.querySelector('dialog [role=alert]')?.textContent.includes('当前批次')"), 'Open-batch plan failure did not retain draft'
        click('取消')
        assert dialogs() == 2
        click('放弃修改')
        result_button = js("[...document.querySelectorAll('button')].find(e=>e.textContent.trim().startsWith('记录结果 · ')).textContent.trim()")
        click(result_button)
        fill('实际结果 / 跳过或阻塞原因', '待恢复的逐项结果')
        route('/testplans')
        route(plan_route)
        click(result_button)
        assert js("document.querySelector('[name=\"实际结果 / 跳过或阻塞原因\"]').value") == '待恢复的逐项结果', 'QA result draft lost'
        choose('运行主体', 'Verifier')
        fill('证据引用', 'mock/dirty-form-result')
        fill('工具版本（手测填写不适用）', 'browser-fixture/1')
        click('保存逐项结果')
        assert dialogs() == 0, 'Recovered result did not save'
        assert not js("[...document.querySelectorAll('button')].some(e=>e.textContent.trim()===" + json.dumps(result_button) + ")"), 'Saved result still editable'
        checks += ['tenant failure/discard/success', 'calendar internal-link recovery/save', 'QA plan and fixed-batch result recovery/save']
    if GROUP in {'all', 'work'}:
        work_route = '/p/launch/work-items/W-104'
        route(work_route)
        click('配置工作小团队')
        escape()
        assert dialogs() == 0, 'Pristine team warned'
        click('配置工作小团队')
        fill('编组变更说明', '   ')
        click('保存工作小团队')
        assert dialogs() == 1 and js("document.querySelector('dialog [role=alert]')?.textContent.includes('说明')"), 'Team error closed editor'
        click('取消')
        assert dialogs() == 2
        click('继续编辑')
        fill('编组变更说明', '恢复后明确保存编组')
        cmd('check', '[aria-label="检查者 Verifier"]')
        route('/overview')
        route(work_route)
        click('配置工作小团队')
        assert js("document.querySelector('[name=编组变更说明]').value") == '恢复后明确保存编组', 'Team navigation lost draft'
        click('保存工作小团队')
        assert dialogs() == 0, 'Recovered team failed save'
        click('配置工作小团队')
        assert not js("document.querySelector('dialog').textContent.includes('已恢复')"), 'Team save did not clear recovery'
        escape()
        click('取消工作')
        fill('理由与交接说明', '待确认的取消理由')
        escape()
        assert dialogs() == 2, 'Work action close bypassed guard'
        click('放弃修改')
        click('取消工作')
        assert js("document.querySelector('[name=理由与交接说明]').value") == '', 'Work action discard retained reason'
        fill('理由与交接说明', '明确取消当前演示工作')
        click('确认')
        assert dialogs() == 0, 'Work action success prompted discard'
        route('/projects')
        click('创建项目')
        fill('项目名称', '应丢弃的项目草稿')
        click('取消')
        assert dialogs() == 2, 'Project cancel bypassed guard'
        click('放弃修改')
        click('创建项目')
        assert js("document.querySelector('[name=项目名称]').value") == '', 'Project discard retained fields'
        fill('项目名称', '明确保存的项目')
        fill('目标与预期结果', '验证表单保护且不自动创建')
        click('创建项目')
        assert dialogs() == 0 and js("location.hash.startsWith('#/p/')"), 'Project successful creation failed'
        checks += ['team failed-save guard and revision-preserving recovery', 'work action discard and explicit success', 'project discard reset and explicit success']
    if GROUP in {'all', 'collab'}:
        route('/chat?channel_id=team')
        marker = 'message-close-protection-' + uuid.uuid4().hex[:8]
        cmd('fill', '.composer textarea', marker)
        js("document.querySelector('.composer button[type=submit]').click()")
        time.sleep(.25)
        message_id = js("JSON.parse(localStorage.getItem('collaboration-prototype-v1')).messages.find(m=>m.text===" + json.dumps(marker) + ").id")
        def message_state():
            return js("JSON.parse(localStorage.getItem('collaboration-prototype-v1')).messages.find(m=>m.id===" + json.dumps(message_id) + ")")
        def open_message(action='更多'):
            js("(()=>{const m=[...document.querySelectorAll('.message')].find(e=>e.textContent.includes(" + json.dumps(marker) + "));[...m.querySelectorAll('button')].find(e=>e.textContent.trim()===" + json.dumps(action) + ").click();return true})()")
            time.sleep(.2)
        open_message()
        escape()
        assert dialogs() == 0, 'Pristine message warned'
        open_message()
        fill('消息内容', '')
        click('保存修订')
        assert dialogs() == 1, 'Empty message failed save dismissed'
        escape()
        assert dialogs() == 2
        click('继续编辑')
        marker += '-saved'
        fill('消息内容', marker)
        click('保存修订')
        assert dialogs() == 0, 'Message save did not dismiss'
        assert message_state()['text'] == marker, ('Message save text mismatch', message_state()['text'], marker)
        for action in ['切换置顶', '保存为记忆候选']:
            open_message()
            fill('消息内容', 'unsaved-' + action)
            before = js("localStorage.getItem('collaboration-prototype-v1')")
            click(action)
            assert dialogs() == 2, action + ' discarded edits without warning'
            assert js("localStorage.getItem('collaboration-prototype-v1')") == before, action + ' ran before confirmation'
            click('继续编辑')
            assert js("document.querySelector('dialog [name=消息内容]').value") == 'unsaved-' + action
            click(action)
            click('放弃修改')
            assert dialogs() == 0 and message_state()['text'] == marker, 'Non-save action stored edited text'
        open_message('共享')
        before = js("JSON.parse(localStorage.getItem('collaboration-prototype-v1')).messages.length")
        choose('接收范围', '外部客户')
        click('取消')
        assert dialogs() == 2, 'Sharing target change not dirty'
        click('继续编辑')
        assert js("JSON.parse(localStorage.getItem('collaboration-prototype-v1')).messages.length") == before, 'Share ran before explicit submit'
        click('确认共享选定消息')
        assert dialogs() == 0 and js("JSON.parse(localStorage.getItem('collaboration-prototype-v1')).messages.length") == before + 1, 'Explicit sharing failed'
        open_message()
        fill('消息内容', 'unsaved-recall')
        click('撤回并保留历史')
        assert dialogs() == 2 and not message_state().get('recalled'), 'Recall ran before discard'
        click('放弃修改')
        assert dialogs() == 0 and message_state()['recalled'], 'Confirmed recall failed'
        checks += ['message failed and successful save', 'pin/memory/recall side effects wait for discard', 'sharing target guard and explicit submit']
    if GROUP in {'all', 'org'}:
        route('/overview')
        route('/members')
        click('添加成员')
        click('关闭编辑')
        assert dialogs() == 0 and not js("!!document.querySelector('.org-editor')"), 'Pristine inline editor warned'
        click('添加成员')
        fill('演示成员名称', '组织草稿恢复成员')
        fill('变更说明', '   ')
        click('保存')
        assert js("document.querySelector('.org-editor [role=alert]')?.textContent.includes('说明')"), 'Organization failed save missing'
        click('取消')
        assert dialogs() == 1, 'Inline cancel discarded without guard'
        escape()
        assert dialogs() == 0 and js("document.querySelector('[name=演示成员名称]').value") == '组织草稿恢复成员', 'Continue lost inline draft'
        location_before = js('location.href')
        history_before = js('history.length')
        click('邀请记录')
        assert js('location.href') == location_before and js('history.length') == history_before, 'Organization tab rewrote URL/history'
        click('添加成员')
        assert js("document.querySelector('[name=演示成员名称]').value") == '组织草稿恢复成员', 'Tab switch lost inline draft'
        route('/overview')
        cmd('back')
        time.sleep(.25)
        click('添加成员')
        assert js("document.querySelector('[name=演示成员名称]').value") == '组织草稿恢复成员', 'Route switch lost inline draft'
        fill('变更说明', '明确保存恢复的成员')
        click('保存')
        assert dialogs() == 0 and not js("!!document.querySelector('.org-editor')"), 'Inline successful save failed'
        click('添加成员')
        assert js("document.querySelector('[name=演示成员名称]').value") == '', 'Inline save failed to clear cache'
        click('关闭编辑')
        click('邀请成员')
        assert js("document.querySelector('[name=电子邮件]').autocomplete") == 'email'
        assert js("document.querySelector('[name=电子邮件]').spellcheck") is False
        assert js("document.querySelector('[name=邀请说明]').autocomplete") == 'off'
        click('关闭编辑')
        route('/templates')
        click('新建工作流模板')
        fill('工作流模板名称', 'discard-template-name')
        click('取消')
        assert dialogs() == 2
        click('放弃修改')
        click('新建工作流模板')
        assert js("document.querySelector('[name=工作流模板名称]').value") == '', 'Template discard retained name'
        escape()
        route('/overview')
        route('/members')
        click('添加成员')
        fill('演示成员名称', '应在卸载后丢弃的草稿')
        click('关闭编辑')
        assert dialogs() == 1
        cmd('back')
        time.sleep(.25)
        click('放弃修改')
        cmd('forward')
        time.sleep(.25)
        click('添加成员')
        assert js("document.querySelector('[name=演示成员名称]').value") == '', 'Discard after editor unmount retained cached draft'
        click('关闭编辑')
        checks += ['inline clean/cancel/failure guards', 'same-URL tab and route recovery', 'inline successful save clears cache', 'email/textarea attributes', 'template discard resets state', 'discard after unmount clears cache']
    print(json.dumps({'result': 'PASS', 'width': WIDTH, 'group': GROUP, 'checks': checks}, ensure_ascii=False))
except Exception:
    print(cmd('console'), file=sys.stderr)
    print(js("({url:location.href,body:document.body.innerText.slice(-1200)})"), file=sys.stderr)
    raise
finally:
    cmd('close')
