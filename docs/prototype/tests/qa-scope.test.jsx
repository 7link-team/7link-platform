import { expect, test } from "bun:test";
import { qaPlanProjectIds, qaRowsInProject } from "../src/pages-qa";

test("QA project lists follow referenced work identities, include mixed plans, and never infer unknown scope", () => {
  const state={project:"film",work:[{id:"launch-work",project:"launch"},{id:"film-work",project:"film"}]};
  const plan=(id,ids)=>({id,name:"film plan",caseRefs:ids.map(work=>({snapshot:{work}}))});
  const launch=plan("launch",["launch-work"]), film=plan("film",["film-work"]), mixed=plan("mixed",["launch-work","film-work"]), unknown=plan("unknown",["missing"]);
  const plans=[launch,film,mixed,unknown];
  expect(qaRowsInProject(state,plans,"testplans")).toEqual([film,mixed]);
  expect(qaPlanProjectIds(state,mixed)).toEqual(["launch","film"]);
  expect(qaRowsInProject({...state,project:"launch"},plans,"testplans")).toEqual([launch,mixed]);
  expect(qaRowsInProject({...state,project:"all"},plans,"testplans")).toEqual(plans);
  const reports=plans.map(plan=>({id:`report-${plan.id}`,planSnapshot:plan,caseRefs:[{snapshot:{work:"film-work"}}]}));
  expect(qaRowsInProject(state,reports,"testreports").map(report=>report.id)).toEqual(["report-film","report-mixed"]);
  expect(qaRowsInProject({...state,work:[]},reports,"testreports")).toEqual([]);
});
