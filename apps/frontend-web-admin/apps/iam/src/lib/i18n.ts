import { loadMessages as loadSharedMessages } from "@workspace/ui/i18n/load-messages"

export async function loadMessages(
  locale: string
): Promise<Record<string, unknown>> {
  const [shared, application] = await Promise.all([
    loadSharedMessages(locale),
    import(`../messages/${locale}.json`),
  ])
  return { ...shared, iam: application.default }
}
