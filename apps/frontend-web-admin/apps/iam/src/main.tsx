import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { RouterProvider } from "react-router"
import "./index.css"
import { ThemeProvider } from "./components/theme-provider"
import { appName } from "./lib/branding"
import { router } from "./router"
import { IntlAppProvider } from "@workspace/ui/i18n/intl-provider"
import { defaultLocale, locales } from "@workspace/ui/i18n/config"
import { loadMessages } from "./lib/i18n"

// The document title is deployment configuration like every other display
// name, so it follows VITE_APP_NAME instead of being baked into index.html.
document.title = appName

const savedLocale = document.cookie
  .split("; ")
  .find((value) => value.startsWith("NEXT_LOCALE="))
  ?.split("=")[1]
const initialLocale =
  locales.find((locale) => locale === savedLocale) ?? defaultLocale
// Every API request reads this attribute for its Accept-Language, and the
// first ones leave before the provider's effect has run. index.html can only
// declare one language, so until this line a caller whose locale differed got
// that one -- two requests from the same page load came back in two languages,
// and API messages are shown to the reader.
document.documentElement.lang = initialLocale
const root = createRoot(document.getElementById("root")!)
void loadMessages(initialLocale)
  .then((messages) =>
    root.render(
      <StrictMode>
        <IntlAppProvider
          initialLocale={initialLocale}
          initialMessages={messages}
          loadMessages={loadMessages}
        >
          <ThemeProvider>
            <RouterProvider router={router} />
          </ThemeProvider>
        </IntlAppProvider>
      </StrictMode>
    )
  )
  .catch((error) => {
    console.error(error)
    root.render(
      <p role="alert">Unable to load application language resources.</p>
    )
  })
