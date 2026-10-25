import { expect, test } from "bun:test"
import { buildEvalTarget } from "./eval-api"

test("evaluation target preserves exact IDs and rejects incomplete Agent configuration", () => {
 expect(buildEvalTarget("visual", "provider:71", "", "", "")).toEqual({verifier_key:"visual", provider_ref:"provider:71", machine_id:0})
 expect(buildEvalTarget("visual", "provider:71", "", "9007199254740993", "configured-model")).toEqual({verifier_key:"visual", provider_ref:"provider:71", machine_id:0, agent_id:"9007199254740993", role:"ACCEPTOR", model:"configured-model"})
 for (const [agent,model] of [["","model"],["77",""],["0","model"],["77"," "]]) expect(() => buildEvalTarget("visual","provider:71","",agent!,model!)).toThrow()
})
