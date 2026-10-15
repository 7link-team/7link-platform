(async () => {
  const checks = [];
  const check = (name, passed) => {
    checks.push({ name, pass: Boolean(passed) });
    if (!passed) throw new Error(name);
  };
  const settle = () => new Promise(resolve => setTimeout(resolve, 1400));
  const click = async label => {
    const button = Array.from(document.querySelectorAll('button')).find(node => node.textContent.trim() === label);
    if (!button) throw new Error(`Missing control: ${label}`);
    button.click();
    await settle();
  };
  const search = async text => {
    const input = document.querySelector('#term-search');
    input.value = text;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
  };
  await settle();
  await click('全部类别');
  await click('全部关系');
  const model = window.__terminologyModel;
  check('Complete registered model is rendered', model.nodes.length >= 375 && model.edges.length >= 707 && document.querySelectorAll('.graph-node').length === model.nodes.length);
  check('No isolated nodes; one connected total graph', model.diagnostics.isolatedNodeIds.length === 0 && model.diagnostics.componentCount === 1);
  check('Structural audit does not mistake connectivity for business closure',
    document.querySelector('.structure-audit').textContent.includes('不等于业务闭环') &&
    model.diagnostics.singleNeighborNodeIds.length > 0);
  const businessLinks = [
    ['planning_goal_contributors', 'planning_key_results'], ['planning_goal_contributors', 'principal'],
    ['planning_report_links', 'planning_reports'], ['planning_check_issues', 'planning_checks'],
    ['fleet_machine_runtimes', 'fleet_machines'], ['fleet_model_catalogs', 'fleet_machines'],
    ['delivery_automation_runs', 'delivery_automation_rules'], ['domain_event_parked', 'domain_events'],
    ['delivery_work_item_criteria', 'delivery_acceptance_criteria'],
    ['delivery_decompositions', 'delivery_decomposition_template_versions'],
    ['delivery_decompositions', 'delivery_work_items'], ['delivery_attention_sessions', 'principal'],
    ['delivery_attention_sessions', 'delivery_decisions'], ['planning_settings', 'planning_reports'],
    ['notification_push_subscriptions', 'principal'], ['delivery_metric_daily', 'delivery_projects']
  ];
  check('Business relationships extend beyond tenant and enum connections', businessLinks.every(([from, to]) =>
    model.edges.some(edge => edge.from === from && edge.to === to && edge.source?.quote)));
  check('Every relationship has endpoints and source quotation', model.edges.every(edge => model.nodes.some(node => node.id === edge.from) && model.nodes.some(node => node.id === edge.to) && edge.source?.quote));
  check('Omitted specification objects are registered with meaningful relationships', [
    ['governance_quality_escapes', 'knowledge_memories'],
    ['delivery_project_counters', 'delivery_requirements'],
    ['iam_menu_contributions', 'iam'], ['intake_client_settings', 'file'],
    ['intake_access_links', 'iam'], ['intake_attention_daily', 'principal'],
    ['authority_source', 'governance_delegations']
  ].every(([from, to]) => model.edges.some(edge => edge.from === from && edge.to === to)));
  check('Retired portal links are not presented as current schema requirements',
    model.nodes.find(node => node.id === 'intake_access_links')?.definition.includes('已退休'));
  check('Remaining business leaves have explicit semantic explanations',
    model.diagnostics.businessSingleNeighborNodeIds.every(id =>
      model.nodes.find(node => node.id === id)?.relationshipReview?.explanation));
  check('Invitation, shared materials and approved changes have downstream relationships', [
    ['invitation_response', 'tenant_membership'], ['shared_material_reference', 'delivery_projects'],
    ['knowledge_promotion', 'human_owner'], ['okr_revision', 'approval_request'],
    ['change_propagation', 'notifications'], ['search_index', 'iam']
  ].every(([from, to]) => model.edges.some(edge => edge.from === from && edge.to === to)));
  await click('业务关系待核对');
  const leaves = new Set([...model.diagnostics.businessSingleNeighborNodeIds, ...model.diagnostics.scopeOnlyNodeIds]);
  const leafContext = new Set(leaves);
  for (const edge of model.edges) if (leaves.has(edge.from) || leaves.has(edge.to)) {
    leafContext.add(edge.from); leafContext.add(edge.to);
  }
  check('Business leaf review retains exact direct context and precise URL',
    document.querySelectorAll('.graph-node').length === leafContext.size &&
    location.hash.includes('view=structure') && document.querySelector('.structure-audit').open);
  check('Role and state classifications are excluded from business leaf queue',
    model.diagnostics.businessSingleNeighborNodeIds.every(id => !/^(actor_|role_|business_role_|acceptance_L)/.test(id) &&
      !['状态与分类', '执行生命周期'].includes(model.nodes.find(node => node.id === id).category)));
  await click('全部关系');
  const required = ['product_test_plan', 'product_test_report', 'product_package_operations', 'product_business_effect_observation', 'project_archive', 'schedule_calendar', 'approval_request', 'health_monitor', 'tauri_shell', 'model_config_distribution', 'resource_directory', 'resource_import', 'workflow_node_configuration', 'workflow_scenario'];
  check('Previously omitted product paths retained', required.every(id => model.nodes.some(node => node.id === id)));
  check('Unresolved specification and implementation issues retained', model.issues.length >= 6);
  const supplemental = ['workflow_trigger', 'credential_reference', 'workflow_input_mapping',
    'workflow_error_policy', 'subworkflow_reference', 'performance_template', 'performance_calibration',
    'performance_confirmation', 'okr_writing_rules', 'okr_value_review', 'okr_checkin', 'okr_carryover',
    'archive_impact_check', 'project_restore', 'connector_capabilities', 'external_message_receipt', 'external_message_mapping'];
  check('Maintenance, confirmation and external receipt paths present with evidence', supplemental.every(id =>
    model.nodes.some(node => node.id === id) && model.edges.some(edge => edge.from === id || edge.to === id)));
  check('Requirement audit preserves both meanings of duplicate IDs', model.requirementAudit.entries.filter(entry =>
    entry.file === 'docs/PRD_FINAL.md' && entry.id === 'REQ-EXEC-012').length === 2 &&
    model.requirementAudit.reusedIds.some(item => item.id === 'REQ-EXEC-012' && item.lines.length === 2));
  check('Bold requirement declarations and uncited review queue retained', model.requirementAudit.entries.some(entry =>
    entry.id === 'REQ-MODEL-005') && model.requirementAudit.uncitedCount > 0 &&
    document.querySelector('.requirement-audit').textContent.includes('不是完整需求覆盖证明'));
  await click('界面与资源操作');
  const focused = new Set(model.nodes.filter(node => node.category === '界面与资源操作').map(node => node.id));
  const expected = new Set(focused);
  for (const edge of model.edges) if (focused.has(edge.from) || focused.has(edge.to)) {
    expected.add(edge.from);
    expected.add(edge.to);
  }
  check('Category retains all direct cross-category neighbors', document.querySelectorAll('.graph-node').length === expected.size && document.querySelector('.legend').textContent.includes('跨类别上下游'));
  const audit = document.querySelector('.coverage-audit');
  audit.open = true;
  check('Chapter citation audit exposes original PRD and uncited chapters without claiming completeness', model.coverageAudit.chapters.some(chapter => chapter.file === 'docs/PRD.md') && model.coverageAudit.uncitedChapterCount > 0 && audit.textContent.includes('不代表该章全部覆盖'));
  audit.open = false;
  await click('全部类别');
  await search('周报');
  check('Chinese report alias finds period report', Array.from(document.querySelectorAll('.term-list button')).some(button => button.textContent.includes('周期报告')));
  await search('排期日历');
  const calendar = document.querySelector('.term-list button');
  check('Calendar term found', calendar?.textContent.includes('排期日历视图'));
  calendar.click();
  await settle();
  await search('');
  await click('所选术语关联');
  check('Calendar view derives existing work rather than duplicate tasks', document.querySelector('.detail').textContent.includes('不是第二套日历任务') && document.querySelectorAll('.graph-node').length > 1);
  const source = document.querySelector('.relations details');
  source.open = true;
  check('Relationship evidence can be read inline', source.querySelector('pre').textContent.trim().length > 0);
  await click('待闭环路径');
  check('Unresolved issues remain accessible', document.querySelector('.detail').textContent.includes('全部待核对项'));
  const issueIds = new Set(model.issues.flatMap(issue => issue.nodeIds || []));
  const issueContext = new Set(issueIds);
  for (const edge of model.edges) if (issueIds.has(edge.from) || issueIds.has(edge.to)) {
    issueContext.add(edge.from);
    issueContext.add(edge.to);
  }
  check('Issue view retains direct upstream and downstream context',
    document.querySelectorAll('.graph-node').length === issueContext.size &&
    document.querySelector('.legend').textContent.includes('视图孤立 0'));
  location.hash = 'term=execution_conversation&view=neighbors&category=all&q=&depth=1';
  await settle();
  check('Execution deep link restores precise identity', document.querySelector('.detail').textContent.includes('Execution唯一ID'));
  check('No load error or viewport overflow', !document.querySelector('.error') && document.documentElement.scrollWidth <= innerWidth);
  return { pass: true, nodes: model.nodes.length, edges: model.edges.length, viewport: innerWidth, checks };
})()
