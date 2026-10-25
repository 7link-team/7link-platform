import {expect,it} from "bun:test"
import {retrospectiveValues,issueSection,reportSectionLabels} from "./report-sections"
it("keeps explicit structured root causes and review confirmation",()=>{
 expect(retrospectiveValues({retro_went_well:"  quality ",retro_problems:"delay\r\n blocked ",retro_root_causes:" dependency\n",retrospective_confirmed:true})).toEqual({retrospective_sections:{went_well:["quality"],problems:["delay","blocked"],root_causes:["dependency"]},retrospective:"quality\ndelay\nblocked\ndependency",retrospective_confirmed:true})
 expect(retrospectiveValues({}).retrospective_confirmed).toBe(false)
 expect(issueSection("B1_UNBACKED_COMPLETION")).toBe("report-completed");expect(issueSection("B3_UNLINKED_PLAN_ITEM")).toBe("report-next-plan");expect(issueSection("B4_MISSING_SECTION")).toBe("report-retrospective")
 for(const locale of ["en-US","zh-CN","ms-MY"])expect(Object.keys(reportSectionLabels(locale))).toEqual(Object.keys(reportSectionLabels("en-US")))
})

it("Chinese report labels contain readable translations",()=>{expect(reportSectionLabels("zh-CN").rootCauses).toBe("根本原因（每行一项）");expect(Object.values(reportSectionLabels("zh-CN")).some(value=>value.includes("?"))).toBe(false)})
