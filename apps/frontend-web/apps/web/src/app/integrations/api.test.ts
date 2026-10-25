import { describe, expect, it } from "bun:test"
import { connectionPath, mappingInput, mappingPath } from "./api"
describe("integration API input boundaries", () => {
  it("encodes each tenant and connection path segment", () => {
    expect(connectionPath("1/other")).toBe(
      "/tenants/1%2Fother/integration-connections"
    )
    expect(mappingPath("1", "2/other")).toBe(
      "/tenants/1/integration-connections/2%2Fother/project-mappings"
    )
  })
  it("parses field ownership without discarding unrelated existing configuration", () => {
    expect(
      mappingInput({
        field_masters: '{"state":"MYRMIDON"}',
        codeup_repositories: ["group/repo"],
        trusted_pipelines: ["verified-pipeline"],
      })
    ).toEqual({
      field_masters: { state: "MYRMIDON" },
      codeup_repositories: ["group/repo"],
      trusted_pipelines: ["verified-pipeline"],
    })
    for (const field_masters of ["[]", "null", "1", "invalid"])
      expect(() => mappingInput({ field_masters })).toThrow()
  })
})
