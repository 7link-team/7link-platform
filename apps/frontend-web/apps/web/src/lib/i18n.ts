import { loadMessages as loadSharedMessages } from "@workspace/ui/i18n/load-messages"

export async function loadMessages(
  locale: string
): Promise<Record<string, unknown>> {
  const [shared, application, files] = await Promise.all([
    loadSharedMessages(locale),
    import(`../messages/${locale}.json`),
    import(`../messages/files/${locale}.json`),
  ])
  return { ...shared, iam: application.default, files: files.default }
}
