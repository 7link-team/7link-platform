import { createContext } from "react"
import type { ClientCollection } from "../../lib/clientwork-api"
export const ClientPickerScope = createContext({ project: "", collection: "sheets" as ClientCollection })
