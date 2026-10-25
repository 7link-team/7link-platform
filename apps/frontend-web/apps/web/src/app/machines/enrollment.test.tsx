import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import {Enrollment} from "./enrollment"

test("enrollment inspector has labeled controls and localized honest installer guidance", () => {
  for (const [locale, messages] of [["en-US", en], ["zh-CN", zh], ["ms-MY", ms]] as const) {
    const html = renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{iam: messages}}><Enrollment tenantID="7" onChanged={async () => {}}/></IntlAppProvider>)
    expect(html).toContain(messages.fleetInstallPrerequisites)
    expect(html).toContain(messages.fleetInspectEnrollment)
    expect(html).toContain(messages.fleetEnrollmentCodeHint)
    expect(html).toContain(messages.fleetUserCode)
    expect(html).not.toContain(messages.fleetEnrollmentID)
    expect(html.match(/<label/g)?.length).toBe(2)
    expect(html).not.toContain("npm install")
    expect(html).not.toContain("????")
    for (const state of ["PENDING", "APPROVED", "COMPLETE", "DENIED", "CANCELLED", "EXPIRED", "FAILED"] as const) {
      expect(messages[`fleetEnrollment_${state}`]).toBeTruthy()
      expect(messages[`fleetEnrollment_${state}`]).not.toContain("????")
    }
    for (const key of ["fleetEnrollmentCodeHint", "fleetEnrollmentReported", "fleetEnrollmentHostname", "fleetEnrollmentArch", "fleetEnrollmentName", "fleetEnrollmentRuntimeMissing", "fleetEnrollmentInspectUnknown", "fleetEnrollmentConfirmed", "fleetEnrollmentConnected", "fleetEnrollmentWaiting", "fleetEnrollmentOnline", "fleetEnrollmentWaitingOnline", "fleetEnrollmentDelayed"] as const) {
      expect(messages[key]).toBeTruthy()
      expect(messages[key]).not.toContain("?")
    }
  }
})
