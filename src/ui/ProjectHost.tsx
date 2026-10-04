import { ComponentProps, useState } from "react"
import type { Session } from "../core/session.ts"
import { readUserConfig } from "../core/userConfig.ts"
import { App } from "./App.tsx"

type AppProps = Omit<ComponentProps<typeof App>, "sup" | "onOpenProject">

/**
 * Shows the session's project and swaps it when another one is opened. `App` is keyed by the
 * project, so every piece of per-project UI state (selection, filters, logs scroll) starts fresh.
 */
export function ProjectHost({ session, ...app }: AppProps & { session: Session }) {
  const [sup, setSup] = useState(session.sup)
  const [theme, setTheme] = useState(app.initialTheme)
  const [themeErrors, setThemeErrors] = useState(app.themeErrors)
  const [startWithPicker, setStartWithPicker] = useState(app.startWithPicker)

  const open = (dir: string, how: "stop" | "detach") =>
    session.switchTo(dir, how, (next) => {
      // the theme may have been changed with T since startup; load errors were already shown once
      const saved = readUserConfig().theme
      if (saved && app.themes?.[saved]) setTheme(saved)
      setThemeErrors([])
      setStartWithPicker(false)
      setSup(next)
    })

  return <App key={sup.stateDir} {...app} sup={sup} onOpenProject={open} initialTheme={theme} themeErrors={themeErrors} startWithPicker={startWithPicker} />
}
