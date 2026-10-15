#!/usr/bin/env python3
"""Build the offline HTML relation graph from reviewed source data and pinned assets."""
import json
import re
from pathlib import Path
root = Path(__file__).resolve().parent
model = json.loads((root / 'data.json').read_text())
ids = [n['id'] for n in model['nodes']]
assert len(ids) == len(set(ids)), 'Duplicate term IDs'
assert all(e['from'] in ids and e['to'] in ids for e in model['edges']), 'Dangling relations'
assert all(n.get('definition') and n.get('sources') for n in model['nodes']), 'Missing definitions/evidence'
for n in model['nodes']:
    for s in n['sources']:
        source = root.parents[2] / s['file']
        assert source.is_file(), source
        lines = source.read_text().splitlines()
        assert 1 <= s['line'] <= len(lines), s
        if s.get('quote'):
            assert s['quote'] == lines[s['line'] - 1], s
        s['excerpt'] = '\n'.join(f'{i + 1}: {lines[i]}' for i in range(max(0, s['line'] - 2), min(len(lines), s['line'] + 1)))
# Validate relation evidence as well as node definitions; never invent edges to hide isolation.
for edge in model['edges']:
    citation = edge.get('source')
    assert citation and citation.get('file') and citation.get('line'), edge
    source_lines = (root.parents[2] / citation['file']).read_text().splitlines()
    assert 1 <= citation['line'] <= len(source_lines), citation
    if citation.get('quote'):
        assert citation['quote'] == source_lines[citation['line'] - 1], citation
    else:
        citation['quote'] = source_lines[citation['line'] - 1]
    assert citation['quote'].strip(), citation
for issue in model['issues']:
    assert issue.get('reason') and issue.get('evidence'), issue
    assert all(node_id in ids for node_id in issue.get('nodeIds', [])), issue
    for citation in issue['evidence']:
        source_lines = (root.parents[2] / citation['file']).read_text().splitlines()
        assert 1 <= citation['line'] <= len(source_lines), citation
        assert citation['quote'] == source_lines[citation['line'] - 1], citation
adjacency = {id: set() for id in ids}
for edge in model['edges']:
    adjacency[edge['from']].add(edge['to'])
    adjacency[edge['to']].add(edge['from'])
components = []
remaining = set(ids)
while remaining:
    queue = [min(remaining)]
    component = set()
    while queue:
        current = queue.pop()
        if current in component:
            continue
        component.add(current)
        queue.extend(sorted(adjacency[current] - component))
    remaining -= component
    components.append(sorted(component))
model['diagnostics'] = {'isolatedNodeIds': sorted(id for id in ids if not adjacency[id]),
                        'componentCount': len(components), 'components': components}
# Structural leaves are a review queue, not evidence of missing product behavior.
# A tenant or enum edge alone does not describe an object's business lifecycle.
node_by_id = {n['id']: n for n in model['nodes']}
structural_ids = {'tenant'} | {n['id'] for n in model['nodes'] if n['id'].startswith('enum_')}
scope_only = sorted(id for id in ids if id not in structural_ids and adjacency[id]
                    and adjacency[id] <= structural_ids)
model['diagnostics']['singleNeighborNodeIds'] = sorted(id for id in ids if len(adjacency[id]) == 1)
model['diagnostics']['scopeOnlyNodeIds'] = scope_only
# Keep business leaves visible without treating enum values or role classifications as broken flows.
business_leaves = [id for id in model['diagnostics']['singleNeighborNodeIds']
                   if node_by_id[id]['category'] not in {'状态与分类', '执行生命周期'}
                   and not id.startswith(('actor_', 'role_', 'business_role_', 'acceptance_L'))]
model['diagnostics']['businessSingleNeighborNodeIds'] = business_leaves
structure_report = ['# 关系结构核对', '',
                    '这是已登记关系的结构检查，不是产品缺陷数量或业务闭环验收。', '',
                    f'总网：{len(ids)} 个术语，{len(model["edges"])} 条关系，'
                    f'{len(model["diagnostics"]["isolatedNodeIds"])} 个断线节点，{len(components)} 个连通分组。', '',
                    '## 仅租户归属或枚举连接', '',
                    '这些节点虽不完全断线，但还没有登记业务对象间关系，优先核对其真实上下游。不能为了清空清单虚构关系。', '']
for id in scope_only:
    n = node_by_id[id]
    structure_report.append(f'- {n["label"]}（{id}）：' + '、'.join(node_by_id[j]['label'] for j in sorted(adjacency[id])))
structure_report.extend(['', '## 单邻居节点', '',
                         '单邻居不等于缺陷：枚举值、入口和视图可能合理地只关联一个对象。需结合定义及来源核对，不能要求所有节点都有两个业务对象。', ''])
for id in model['diagnostics']['singleNeighborNodeIds']:
    n = node_by_id[id]
    structure_report.append(f'- {n["label"]}（{id}）→ {node_by_id[next(iter(adjacency[id]))]["label"]}')
structure_report.extend(['', '## 业务单邻居复核队列', '',
                         '排除状态、主体及角色分类后的单邻居概念。仍不是已确认缺陷；逐项核对来源、权限、动作、结果与异常，合理终点无需强行补线。', ''])
for id in business_leaves:
    node = node_by_id[id]
    review = node.get('relationshipReview', {})
    structure_report.append(f'- {node["label"]}（{id}）：' + review.get('explanation', '尚未逐项语义核对。'))
(root / 'STRUCTURE.md').write_text('\n'.join(structure_report) + '\n')
# Direct citations are a review index, not a percentage of requirements implemented.
chapters = []
for filename in sorted(set(model['meta']['sourceScope']) | {'docs/PRD.md'}):
    source_lines = (root.parents[2] / filename).read_text().splitlines()
    headings = [(i + 1, line.lstrip('#').strip()) for i, line in enumerate(source_lines)
                if re.match(r'^##\s+\S', line)]
    for index, (start, title) in enumerate(headings):
        end = headings[index + 1][0] if index + 1 < len(headings) else len(source_lines) + 1
        contains = lambda s: s.get('file') == filename and start <= s.get('line', 0) < end
        node_ids = sorted(n['id'] for n in model['nodes'] if any(contains(s) for s in n['sources']))
        edge_count = sum(contains(e['source']) for e in model['edges'])
        chapters.append({'file': filename, 'line': start, 'title': title,
                         'nodeIds': node_ids, 'edgeCount': edge_count,
                         'status': 'cited' if node_ids or edge_count else 'uncited'})
model['coverageAudit'] = {'chapters': chapters,
                          'uncitedChapterCount': sum(c['status'] == 'uncited' for c in chapters)}
# Index actual requirement declarations, not incidental references to an ID.
# Keep every occurrence: reused IDs must never silently overwrite earlier meanings.
requirements = []
for filename in ['docs/PRD_FINAL.md', 'docs/REQUIREMENTS.md']:
    for line_number, text in enumerate((root.parents[2] / filename).read_text().splitlines(), 1):
        match = re.match(r'^\|\s*(REQ-[A-Z0-9]+-\d+)\s*\|', text) or re.match(r'^-\s*(?:\*\*)?(REQ-[A-Z0-9]+-\d+)(?:\*\*)?\s*[：:]', text)
        if not match:
            continue
        contains = lambda s: s.get('file') == filename and s.get('line') == line_number
        node_ids = sorted(n['id'] for n in model['nodes'] if any(contains(s) for s in n['sources']))
        edge_count = sum(contains(e['source']) for e in model['edges'])
        requirements.append({'id': match.group(1), 'file': filename, 'line': line_number,
                             'quote': text, 'nodeIds': node_ids, 'edgeCount': edge_count,
                             'status': 'cited' if node_ids or edge_count else 'uncited'})
reused_ids = []
for filename in ['docs/PRD_FINAL.md', 'docs/REQUIREMENTS.md']:
    for requirement_id in sorted({r['id'] for r in requirements if r['file'] == filename}):
        occurrences = [r for r in requirements if r['file'] == filename and r['id'] == requirement_id]
        if len(occurrences) > 1:
            reused_ids.append({'id': requirement_id, 'file': filename,
                               'lines': [r['line'] for r in occurrences]})
model['requirementAudit'] = {'entries': requirements, 'reusedIds': reused_ids,
                              'uncitedCount': sum(r['status'] == 'uncited' for r in requirements)}
requirement_report = ['# 术语网逐条需求引用核对', '',
                      '只统计 PRD_FINAL 与 REQUIREMENTS 中声明行的直接引用；不是实现验收或需求覆盖率。', '',
                      '同一需求在两份文档分别保留。未引用需要语义核对，不能自动认定缺失；已引用也不能认定整条完整覆盖。', '',
                      '## 同一文档中的重复编号', '']
for item in reused_ids:
    requirement_report.append(f"- {item['id']} · {item['file']} · 行 {', '.join(map(str, item['lines']))}：保留全部声明，需核对含义与编号归属。")
requirement_report.extend(['', '## 逐条声明', '', '| 编号 | 来源行 | 需求声明与验收原文 | 定义引用 | 关系引用 | 核对状态 |',
                           '| --- | --- | --- | ---: | ---: | --- |'])
for r in requirements:
    quote = r['quote'].replace('|', '\\|')
    requirement_report.append(f"| {r['id']} | {r['file']}:{r['line']} | {quote} | {len(r['nodeIds'])} | {r['edgeCount']} | "
                              + ('尚无直接引用，待语义核对' if r['status'] == 'uncited' else '部分已引用，非完整覆盖证明') + ' |')
(root / 'REQUIREMENT_COVERAGE.md').write_text('\n'.join(requirement_report) + '\n')
report = ['# 术语网文档覆盖核对', '',
          '由 build.py 按原始 PRD、最终 PRD、需求清单及规格的二级章节生成。', '',
          '“尚无直接引用”只表示该章节需要人工核对；有引用也只证明部分内容已登记，不能证明整章覆盖、业务闭环或功能验收。', '',
          '| 来源 | 章节起始行 | 章节 | 定义引用数 | 关系引用数 | 核对状态 |',
          '| --- | ---: | --- | ---: | ---: | --- |']
for c in chapters:
    report.append(f"| {c['file']} | {c['line']} | {c['title'].replace('|', '/')} | {len(c['nodeIds'])} | {c['edgeCount']} | "
                  + ('尚无直接引用，待核对' if c['status'] == 'uncited' else '部分已引用，仍需语义核对') + ' |')
(root / 'COVERAGE.md').write_text('\n'.join(report) + '\n')
libraries = []
for name in ['vue-3.5.24.js','screenfull-5.2.0.js','html2canvas-1.4.1.js','relation-graph-2.2.11.js']:
    content = (root / 'vendor' / name).read_text().replace('</script', '<\\/script')
    libraries.append('<script>\n' + content + '\n</script>')
html = (root / 'template.html').read_text().replace('@@DATA@@', json.dumps(model, ensure_ascii=False).replace('<','\\u003c')).replace('@@THEMES@@',(root.parents[1] / 'prototype/src/themes.css').read_text()).replace('@@LIBRARIES@@','\n'.join(libraries))
output = root.parent / 'terminology-graph.html'
output.write_text('<!-- Generated by terminology-graph/build.py; edit template.html/data.json. Pinned MIT assets retain licenses in vendor/. -->\n' + html)
print(f'{output}: {len(ids)} terms / {len(model["edges"])} relations / {len(model["issues"])} issues')
