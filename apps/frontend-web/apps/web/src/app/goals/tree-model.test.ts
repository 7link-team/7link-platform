import {expect,it} from "bun:test"
import {goalViewLabels} from "./tree-model"
it("provides three complete locale label sets",()=>{for(const locale of ["en-US","zh-CN","ms-MY"])expect(Object.keys(goalViewLabels(locale))).toEqual(Object.keys(goalViewLabels("en-US")))})

it("uses readable Chinese tree labels",()=>{expect(goalViewLabels("zh-CN").tree).toBe("目标树");expect(Object.values(goalViewLabels("zh-CN")).some(value=>value.includes("?"))).toBe(false)})
