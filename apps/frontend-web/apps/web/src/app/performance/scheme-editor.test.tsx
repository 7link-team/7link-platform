import {expect,test} from "bun:test"
import {blankScheme} from "./api"
test("new configurations contain no invented rules, scores or enabled cohort",()=>{
 const a=blankScheme(),b=blankScheme()
 expect(a.enabled).toBe(false);expect(a.scope).toBe("");expect(a.subject_ids).toEqual([]);expect(a.dimensions).toEqual([]);expect(a.template.dimension_order).toEqual([])
 a.template.title="Changed";expect(b.template.title).toBe("")
 expect(a).not.toHaveProperty("score");expect(a).not.toHaveProperty("confirmed")
 expect(a.schedule).toBeUndefined()
})
