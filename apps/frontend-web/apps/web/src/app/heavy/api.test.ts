import {describe, expect, test} from "bun:test"
import {executionIDs, ensembleBase} from "./api"
describe("heavy real execution identifiers", () => {
 test("preserves immutable IDs beyond JavaScript integer precision", () => {expect(executionIDs("9007199254740993, 22")).toEqual(["9007199254740993", "22"]); expect(ensembleBase("7", "9")).toBe("/tenants/7/entities/9/ensemble-plans")})
 test("rejects duplicate, fabricated, unbounded branch input", () => {for (const raw of ["1", "1,1", "0,2", "1,-2", "9223372036854775808,2", "1,2,3,4,5,6"]) expect(() => executionIDs(raw)).toThrow()})
})
