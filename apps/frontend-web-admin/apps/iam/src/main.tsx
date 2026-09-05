import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { RouterProvider } from "react-router"
import "./index.css"
import { ThemeProvider } from "./components/theme-provider"
import { appName } from "./lib/branding"
import { router } from "./router"

// The document title is deployment configuration like every other display
// name, so it follows VITE_APP_NAME instead of being baked into index.html.
document.title = appName

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <RouterProvider router={router} />
    </ThemeProvider>
  </StrictMode>
)
