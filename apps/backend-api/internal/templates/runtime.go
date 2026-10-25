package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func workflowRefKey(ref TemplateWorkflowRef) string {
	return fmt.Sprintf("%d:%s@%d", len(ref.WorkflowID), ref.WorkflowID, ref.Version)
}

func runtimeKey(prefix, id string) string { return prefix + strconv.Itoa(len(id)) + ":" + id }
func pinRuntimeChildren(w TemplateWorkflowDef, children map[TemplateWorkflowRef]TemplateWorkflowDef, out map[string]TemplateWorkflowDef) {
	for _, n := range w.Nodes {
		if n.Subflow == nil {
			continue
		}
		key := workflowRefKey(*n.Subflow)
		if _, exists := out[key]; exists {
			continue
		}
		child, ok := children[*n.Subflow]
		if !ok {
			continue
		}
		out[key] = child
		pinRuntimeChildren(child, children, out)
	}
}
func validateRuntime(w TemplateWorkflowDef, children map[TemplateWorkflowRef]TemplateWorkflowDef, depth int) error {
	if depth > 16 {
		return ErrInvalid
	}
	for _, n := range w.Nodes {
		for _, edge := range w.Edges {
			if edge.Source != n.ID {
				continue
			}
			if n.Kind == "CONDITION" {
				if edge.Branch != "true" && edge.Branch != "false" {
					return ErrInvalid
				}
			} else if edge.Branch != "" {
				return ErrInvalid
			}
		}
		if n.Subflow != nil {
			child, ok := children[*n.Subflow]
			if !ok {
				return ErrInvalid
			}
			if e := validateRuntime(child, children, depth+1); e != nil {
				return e
			}
		}
	}
	return nil
}

// advanceOn is an explicit command. Reads never schedule work. The project
// serialization fence and binding lock cover every expansion and provider write.
func (m *Module) authorizeInstanceCommandOn(ctx context.Context, tx *gorm.DB, project guid.ID) (*model.Binding, []TemplateNodeRun, *model.Version, error) {
	tenant := authn.TenantIDFromContext(ctx)
	q := query.BindBindings(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, nil, nil, ErrNotFound
	}
	if e != nil {
		return nil, nil, nil, e
	}
	var runs []TemplateNodeRun
	if e = json.Unmarshal([]byte(row.Runs), &runs); e != nil {
		return nil, nil, nil, e
	}
	if len(runs) == 0 || runs[0].AccountableID <= 0 {
		return nil, nil, nil, ErrUnavailable
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), runs[0].AccountableID}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return nil, nil, nil, ErrForbidden
		}
		if e = m.principals.Lock(ctx, tx, id); e != nil {
			return nil, nil, nil, e
		}
	}
	if e = m.scope.AuthorizeOn(ctx, tx, tenant, project, "project_manage"); e != nil {
		return nil, nil, nil, e
	}
	row, e = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Take()
	if e != nil {
		return nil, nil, nil, e
	}
	if e = json.Unmarshal([]byte(row.Runs), &runs); e != nil {
		return nil, nil, nil, e
	}
	version, e := m.versionOn(ctx, tx, row.TemplateVersionID, "template_read")
	if e != nil {
		return nil, nil, nil, e
	}
	if version.SHA256 != row.SHA256 {
		return nil, nil, nil, ErrConflict
	}
	return row, runs, version, nil
}

func (m *Module) advanceOn(ctx context.Context, tx *gorm.DB, project guid.ID) (TemplateInstanceView, error) {
	var empty TemplateInstanceView
	tenant := authn.TenantIDFromContext(ctx)
	row, runs, version, e := m.authorizeInstanceCommandOn(ctx, tx, project)
	if e != nil {
		return empty, e
	}
	q := query.BindBindings(tx, m.names)
	v, e := versionView(version)
	if e != nil {
		return empty, e
	}
	parameters, _, e := resolveBindingParameters(v.Definition, row)
	if e != nil {
		return empty, e
	}
	v.Definition.Workflow.Parameters = parameters
	before := row.Runs
	accountable := runs[0].AccountableID
	children := runs[0].Children
	currentChildren, e := m.childrenOn(ctx, tx)
	if e != nil {
		return empty, e
	}
	for key, pinned := range children {
		current, ok := currentChildren[TemplateWorkflowRef{pinned.WorkflowID, pinned.Version}]
		a, _ := json.Marshal(current)
		b, _ := json.Marshal(pinned)
		if !ok || string(a) != string(b) || key != workflowRefKey(TemplateWorkflowRef{pinned.WorkflowID, pinned.Version}) {
			return empty, ErrForbidden
		}
	}
	// Refresh facts once, under the same project fence. Never infer acceptance
	// from a successful execution or from an optimistic client status.
	for i := range runs {
		if runs[i].WorkItemID != nil {
			state, err := m.delivery.WorkStatusOn(ctx, tx, tenant, project, *runs[i].WorkItemID)
			if err != nil {
				return empty, err
			}
			runs[i].State = state
		}
	}
	var brief *intake.SignedBriefFact
	externalFacts := map[string]any{}
	if m.briefs.ReadOn != nil {
		signed, found, err := m.briefs.ReadOn(ctx, tx, tenant, project, row.ID)
		if err != nil {
			return empty, err
		}
		if found {
			brief = &signed
			externalFacts["signed_brief"] = map[string]any{"approved": true, "items": signed.Items, "hash": signed.Hash}
		}
	}
	var walk func(TemplateWorkflowDef, string, int) (bool, error)
	walk = func(w TemplateWorkflowDef, prefix string, depth int) (bool, error) {
		if depth > 16 {
			return false, ErrInvalid
		}
		index := map[string]int{}
		for _, n := range w.Nodes {
			key := runtimeKey(prefix, n.ID)
			i := slices.IndexFunc(runs, func(r TemplateNodeRun) bool { return r.ExpansionKey == key })
			if i < 0 {
				if len(runs) >= 1000 {
					return false, ErrInvalid
				}
				runs = append(runs, TemplateNodeRun{NodeID: n.ID, ExpansionKey: key, State: "WAITING"})
				i = len(runs) - 1
			}
			index[n.ID] = i
		}
		// A DAG may be stored in any order. Revisit only while a control state changes.
		for pass := 0; pass <= len(w.Nodes); pass++ {
			changed := false
			for _, n := range w.Nodes {
				i := index[n.ID]
				r := runs[i]
				nodeWorkflow := w
				if len(r.Parameters) != 0 {
					if err := json.Unmarshal(r.Parameters, &nodeWorkflow.Parameters); err != nil {
						return false, ErrUnavailable
					}
				}
				w := nodeWorkflow
				if r.State == "COMPLETED" || r.State == "SKIPPED" || r.State == "ESCALATED" || r.WorkItemID != nil {
					continue
				}
				ready, live := true, n.ID == w.EntryNodeID
				for _, edge := range w.Edges {
					if edge.Target != n.ID {
						continue
					}
					pred := runs[index[edge.Source]]
					if pred.State != "COMPLETED" && pred.State != "ACCEPTED" && pred.State != "SKIPPED" {
						ready = false
						break
					}
					if pred.State != "SKIPPED" {
						if pred.Value == nil || edge.Branch == strconv.FormatBool(*pred.Value) {
							live = true
						}
					}
				}
				if !ready {
					runs[i].Reason = "WAITING_PREDECESSOR"
					continue
				}
				if !live {
					runs[i].State = "SKIPPED"
					runs[i].Reason = "CONDITION_NOT_MATCHED"
					changed = true
					continue
				}
				runs[i].Reason = ""
				switch n.Kind {
				case "WORK_ITEM":
					criteria := make([]delivery.TemplateCriterion, len(n.Criteria))
					for j, c := range n.Criteria {
						criteria[j] = delivery.TemplateCriterion{Text: c.Text, VerificationMethod: c.VerificationMethod}
					}
					id, err := m.delivery.CreateWorkOn(ctx, tx, tenant, project, delivery.TemplateWorkInput{Title: n.Name, Type: "TASK", Objective: n.Objective, Description: runtimeItemDescription(w), AccountablePrincipalID: accountable, Criteria: criteria})
					if err != nil {
						return false, err
					}
					for _, edge := range w.Edges {
						if edge.Target != n.ID {
							continue
						}
						predecessor := runs[index[edge.Source]]
						if predecessor.WorkItemID == nil {
							continue
						}
						if m.delivery.AddDependencyOn == nil {
							return false, ErrUnavailable
						}
						if err = m.delivery.AddDependencyOn(ctx, tx, tenant, project, id, *predecessor.WorkItemID); err != nil {
							return false, err
						}
					}
					runs[i].WorkItemID = &id
					runs[i].State = "DRAFT"
					changed = true
				case "HUMAN_GATE":
					runs[i].Gate = n.Gate
					runs[i].Reason = "MISSING_GATE_DECISION"
					if m.gates.ReadOn == nil {
						continue
					}
					decision, found, err := m.gates.ReadOn(ctx, tx, governance.WorkflowDecisionRef{TenantID: tenant, ProjectID: project, BindingID: row.ID, DefinitionHash: row.SHA256, ExpansionKey: r.ExpansionKey, Gate: n.Gate})
					if err != nil {
						return false, err
					}
					if found {
						runs[i].DecisionID = &decision.ID
						if decision.Outcome == "APPROVE" {
							runs[i].State = "COMPLETED"
							runs[i].Reason = ""
							changed = true
						} else {
							runs[i].Reason = "GATE_REJECTED"
						}
					}
				case "CONDITION":
					// Literal parameters are frozen by the published definition. Other facts
					// must be supplied by canonical providers, never by caller claims.
					value, err := evalLogic(n.Condition, runtimeFacts(w, runs, index, externalFacts))
					if err != nil {
						if errors.Is(err, ErrUnavailable) {
							runs[i].Reason = "MISSING_CONDITION_FACT"
							continue
						}
						return false, err
					}
					b := logicTruthy(value)
					runs[i].Value = &b
					runs[i].State = "COMPLETED"
					changed = true
				case "SUBFLOW", "FAN_OUT":
					if n.Kind == "FAN_OUT" && n.InputPath != "" && brief == nil {
						runs[i].Reason = "MISSING_SIGNED_BRIEF_ITEMS"
						continue
					}
					if n.Subflow == nil {
						runs[i].Reason = "MISSING_CONTAINER_BODY"
						continue
					}
					child, ok := children[workflowRefKey(*n.Subflow)]
					if !ok {
						return false, ErrConflict
					}
					raw, _ := json.Marshal(child)
					hash := sha256.Sum256(raw)
					pinned := hex.EncodeToString(hash[:])
					if r.ChildHash != "" && r.ChildHash != pinned {
						return false, ErrConflict
					}
					runs[i].ChildHash = pinned
					items := []string{"body"}
					if n.Kind == "FAN_OUT" {
						items = n.Items
						if n.InputPath != "" {
							if n.InputPath != "signed_brief.items" {
								return false, ErrInvalid
							}
							items = make([]string, len(brief.Items))
							for j, item := range brief.Items {
								items[j] = item.ID
							}
							if len(r.FrozenItems) > 0 && !slices.Equal(r.FrozenItems, items) {
								return false, ErrConflict
							}
							if r.BriefHash != "" && r.BriefHash != brief.Hash {
								return false, ErrConflict
							}
							runs[i].BriefHash = brief.Hash
							runs[i].FrozenItems = slices.Clone(items)
						}
					}
					complete := true
					for _, item := range items {
						iterationChild := child
						if n.Kind == "FAN_OUT" && n.InputPath != "" {
							iterationChild.Parameters = map[string]any{}
							for k, v := range child.Parameters {
								iterationChild.Parameters[k] = v
							}
							for _, signedItem := range brief.Items {
								if signedItem.ID == item {
									iterationChild.Parameters["item"] = map[string]any{"id": signedItem.ID, "text": signedItem.Text, "signed_brief_id": brief.ID.String(), "signed_brief_hash": brief.Hash}
								}
							}
						}
						done, err := walk(iterationChild, runtimeKey(prefix, n.ID)+"/"+strconv.Itoa(len(item))+":"+item+"/", depth+1)
						if err != nil {
							return false, err
						}
						complete = complete && done
					}
					if complete {
						runs[i].State = "COMPLETED"
						changed = true
					}
				case "LOOP":
					if m.loopCosts.WorkOn == nil {
						runs[i].Reason = "MISSING_LOOP_BUDGET_FACTS"
						continue
					}
					if n.Subflow == nil {
						runs[i].Reason = "MISSING_CONTAINER_BODY"
						continue
					}
					if n.EscalateTo != "accountable" {
						runs[i].Reason = "MISSING_LOOP_ESCALATION_TARGET"
						continue
					}
					child, ok := children[workflowRefKey(*n.Subflow)]
					if !ok {
						return false, ErrConflict
					}
					raw, _ := json.Marshal(child)
					hash := sha256.Sum256(raw)
					pinned := hex.EncodeToString(hash[:])
					if r.ChildHash != "" && r.ChildHash != pinned {
						return false, ErrConflict
					}
					runs[i].ChildHash = pinned
					if runs[i].Iteration == 0 {
						runs[i].Iteration = 1
					}
					loopPrefix := runtimeKey(prefix, n.ID) + "/iteration/"
					workIDs := []guid.ID{}
					for _, fact := range runs {
						if strings.HasPrefix(fact.ExpansionKey, loopPrefix) && fact.WorkItemID != nil {
							workIDs = append(workIDs, *fact.WorkItemID)
						}
					}
					cost, settled, err := m.loopCosts.WorkOn(ctx, tx, tenant, project, workIDs)
					if err != nil {
						return false, err
					}
					if !settled {
						runs[i].Reason = "MISSING_LOOP_BUDGET_FACTS"
						continue
					}
					if cost > int64(n.Budget) {
						if err = m.escalateLoopOn(ctx, tx, tenant, project, row.ID, row.SHA256, r.ExpansionKey, "BUDGET", accountable); err != nil {
							return false, err
						}
						runs[i].State = "ESCALATED"
						runs[i].Reason = "LOOP_BUDGET_EXCEEDED"
						changed = true
						continue
					}
					iterationPrefix := loopPrefix + strconv.Itoa(runs[i].Iteration) + "/"
					done, err := walk(child, iterationPrefix, depth+1)
					if err != nil {
						return false, err
					}
					if !done {
						continue
					}
					iterationFacts := runtimeFacts(child, runs, runtimeIndex(child, runs, iterationPrefix), externalFacts)
					iterationFacts["iteration"] = float64(runs[i].Iteration)
					iterationFacts["cost_micros"] = float64(cost)
					stop, err := evalLogic(n.Condition, iterationFacts)
					if errors.Is(err, ErrUnavailable) {
						runs[i].Reason = "MISSING_LOOP_STOP_FACT"
						continue
					}
					if err != nil {
						return false, err
					}
					if logicTruthy(stop) {
						runs[i].State = "COMPLETED"
						changed = true
						continue
					}
					if cost >= int64(n.Budget) {
						if err = m.escalateLoopOn(ctx, tx, tenant, project, row.ID, row.SHA256, r.ExpansionKey, "BUDGET", accountable); err != nil {
							return false, err
						}
						runs[i].State = "ESCALATED"
						runs[i].Reason = "LOOP_BUDGET_EXCEEDED"
						changed = true
						continue
					}
					if runs[i].Iteration >= n.MaxIterations {
						if err = m.escalateLoopOn(ctx, tx, tenant, project, row.ID, row.SHA256, r.ExpansionKey, "REWORK_LIMIT", accountable); err != nil {
							return false, err
						}
						runs[i].State = "ESCALATED"
						runs[i].Reason = "LOOP_ITERATIONS_EXHAUSTED"
						changed = true
						continue
					}
					runs[i].Iteration++
					changed = true
				case "TRANSFORM":
					facts := runtimeFacts(w, runs, index, externalFacts)
					value, err := evalLogic(n.Condition, facts)
					if errors.Is(err, ErrUnavailable) {
						runs[i].Reason = "MISSING_TRANSFORM_FACT"
						continue
					}
					if err != nil {
						return false, err
					}
					output, err := json.Marshal(value)
					if err != nil {
						return false, err
					}
					if len(output) > 65536 {
						return false, ErrInvalid
					}
					runs[i].Output = output
					runs[i].State = "COMPLETED"
					changed = true
				default:
					return false, ErrInvalid
				}
			}
			// Waiting for an authoritative fact does not freeze a parameter choice.
			// Persist the exact parameters only after work or a control materializes.
			for _, n := range w.Nodes {
				i := index[n.ID]
				r := &runs[i]
				if r.DefinitionHash == "" && (r.State != "WAITING" || r.WorkItemID != nil || r.ChildHash != "" || r.DecisionID != nil) {
					r.DefinitionHash = row.SHA256
					r.TemplateVersionID = row.TemplateVersionID
					r.Parameters, _ = json.Marshal(w.Parameters)
				}
			}
			if !changed {
				break
			}
		}
		for _, n := range w.Nodes {
			state := runs[index[n.ID]].State
			if state != "COMPLETED" && state != "ACCEPTED" && state != "SKIPPED" {
				return false, nil
			}
		}
		return true, nil
	}
	if _, e = walk(v.Definition.Workflow, "", 0); e != nil {
		return empty, e
	}
	raw, e := json.Marshal(runs)
	if e != nil {
		return empty, e
	}
	if string(raw) != before {
		if _, e = q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.TenantID.Eq(tenant)).Update(q.Runs, string(raw)); e != nil {
			return empty, e
		}
		if e = m.eventOn(ctx, tx, version, project, "template.advanced"); e != nil {
			return empty, e
		}
	}
	return m.instanceOn(ctx, tx, project)
}

func logicTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	default:
		return true
	}
}
func evalLogic(raw json.RawMessage, facts map[string]any) (any, error) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, ErrInvalid
	}
	return evalValue(v, facts, 0)
}
func evalValue(v any, facts map[string]any, depth int) (any, error) {
	if depth > 32 {
		return nil, ErrInvalid
	}
	if array, ok := v.([]any); ok {
		out := make([]any, len(array))
		for i, item := range array {
			value, e := evalValue(item, facts, depth+1)
			if e != nil {
				return nil, e
			}
			out[i] = value
		}
		return out, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return v, nil
	}
	if len(obj) != 1 {
		return nil, ErrInvalid
	}
	for op, arg := range obj {
		if op == "var" {
			path, ok := arg.(string)
			if !ok || path == "" {
				return nil, ErrInvalid
			}
			var value any = facts
			for _, part := range strings.Split(path, ".") {
				m, ok := value.(map[string]any)
				if !ok {
					return nil, ErrUnavailable
				}
				value, ok = m[part]
				if !ok {
					return nil, ErrUnavailable
				}
			}
			return value, nil
		}
		args, ok := arg.([]any)
		if !ok {
			return nil, ErrInvalid
		}
		if op == "!" && len(args) != 1 {
			return nil, ErrInvalid
		}
		if op != "!" && op != "and" && op != "or" && len(args) != 2 {
			return nil, ErrInvalid
		}
		vals := make([]any, len(args))
		for i, a := range args {
			value, e := evalValue(a, facts, depth+1)
			if e != nil {
				return nil, e
			}
			vals[i] = value
			if op == "and" && !logicTruthy(value) {
				return value, nil
			}
			if op == "or" && logicTruthy(value) {
				return value, nil
			}
		}
		switch op {
		case "!":
			return !logicTruthy(vals[0]), nil
		case "and":
			if len(vals) == 0 {
				return true, nil
			}
			return vals[len(vals)-1], nil
		case "or":
			if len(vals) == 0 {
				return false, nil
			}
			return vals[len(vals)-1], nil
		case "==", "!=":
			equal, err := logicEqual(vals[0], vals[1])
			if err != nil {
				return nil, err
			}
			if op == "!=" {
				equal = !equal
			}
			return equal, nil
		case ">", ">=", "<", "<=":
			if a, ok := vals[0].(string); ok {
				if b, ok := vals[1].(string); ok {
					switch op {
					case ">":
						return a > b, nil
					case ">=":
						return a >= b, nil
					case "<":
						return a < b, nil
					default:
						return a <= b, nil
					}
				}
			}
			a, ok := logicNumber(vals[0])
			b, ok2 := logicNumber(vals[1])
			if !ok || !ok2 {
				return nil, ErrInvalid
			}
			switch op {
			case ">":
				return a > b, nil
			case ">=":
				return a >= b, nil
			case "<":
				return a < b, nil
			default:
				return a <= b, nil
			}
		case "in":
			switch hay := vals[1].(type) {
			case string:
				needle, ok := vals[0].(string)
				if !ok {
					return nil, ErrInvalid
				}
				return strings.Contains(hay, needle), nil
			case []any:
				needle, _ := json.Marshal(vals[0])
				for _, item := range hay {
					b, _ := json.Marshal(item)
					if string(needle) == string(b) {
						return true, nil
					}
				}
				return false, nil
			}
		}
	}
	return nil, ErrInvalid
}

func logicNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if strings.TrimSpace(x) == "" {
			return 0, true
		}
		n, e := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return n, e == nil
	default:
		return 0, false
	}
}

// Scalar equality follows JSON Logic's loose comparison. Structured equality
// is deliberately unavailable instead of substituting Go structural equality.
func logicEqual(a, b any) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	switch x := a.(type) {
	case string:
		if y, ok := b.(string); ok {
			return x == y, nil
		}
	case float64:
		if y, ok := b.(float64); ok {
			return x == y, nil
		}
	case bool:
		if y, ok := b.(bool); ok {
			return x == y, nil
		}
	default:
		return false, ErrInvalid
	}
	x, ok := logicNumber(a)
	y, ok2 := logicNumber(b)
	if !ok || !ok2 {
		return false, nil
	}
	return x == y, nil
}

func runtimeFacts(w TemplateWorkflowDef, runs []TemplateNodeRun, index map[string]int, extra ...map[string]any) map[string]any {
	workFacts, outputFacts := map[string]any{}, map[string]any{}
	for _, node := range w.Nodes {
		fact := runs[index[node.ID]]
		if fact.WorkItemID != nil {
			workFacts[node.ID] = map[string]any{"state": fact.State}
		}
		if fact.Output != nil {
			var value any
			if json.Unmarshal(fact.Output, &value) == nil {
				outputFacts[node.ID] = value
			}
		}
	}
	facts := map[string]any{"parameters": w.Parameters, "work": workFacts, "outputs": outputFacts}
	for _, values := range extra {
		for key, value := range values {
			facts[key] = value
		}
	}
	return facts
}

func runtimeItemDescription(w TemplateWorkflowDef) string {
	if item, ok := w.Parameters["item"]; ok {
		raw, e := json.Marshal(item)
		if e == nil {
			return string(raw)
		}
	}
	return ""
}

func runtimeIndex(w TemplateWorkflowDef, runs []TemplateNodeRun, prefix string) map[string]int {
	out := map[string]int{}
	for _, n := range w.Nodes {
		out[n.ID] = slices.IndexFunc(runs, func(r TemplateNodeRun) bool { return r.ExpansionKey == runtimeKey(prefix, n.ID) })
	}
	return out
}
