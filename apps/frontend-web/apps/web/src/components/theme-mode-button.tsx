import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useEffect, useState } from "react"
import { Moon, Sun } from "lucide-react"
import { useTheme } from "next-themes"

export function ThemeModeButton() {
  const t = useTranslations("iam")

  const { resolvedTheme, setTheme } = useTheme()
  const [mounted, setMounted] = useState(false)

  useEffect(() => {
    const handle = setTimeout(() => setMounted(true), 0)
    return () => clearTimeout(handle)
  }, [])
  const dark = mounted && resolvedTheme === "dark"
  return (
    <button
      type="button"
      className="grid size-9 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
      onClick={() => setTheme(dark ? "light" : "dark")}
      aria-label={dark ? t("switchLight") : t("switchDark")}
      title={dark ? t("light") : t("dark")}
    >
      {dark ? <Sun className="size-4" /> : <Moon className="size-4" />}
    </button>
  )
}
