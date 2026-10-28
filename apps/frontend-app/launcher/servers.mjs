const storageKey = "desktop-server-origins"
const connectionKey = "desktop-last-connection"

export function rememberConnection(storage, connection) {
  const value = connection.mode === "local"
    ? { mode: "local" }
    : { mode: "remote", origin: serverOrigin(connection.origin) }
  storage.setItem(connectionKey, JSON.stringify(value))
}

export function lastConnection(storage) {
  const value = JSON.parse(storage.getItem(connectionKey) ?? "null")
  if (value === null) return null
  if (value.mode === "local") return { mode: "local" }
  if (value.mode === "remote") {
    const origin = serverOrigin(value.origin)
    if (savedServers(storage).includes(origin)) return { mode: "remote", origin }
  }
  throw new Error("上次连接记录无效，请重新选择运行方式。")
}

export function resumeConnection(storage, location, startLocal) {
  if (location.hash === "#choose") return
  const connection = lastConnection(storage)
  if (connection?.mode === "local") return startLocal()
  if (connection?.mode === "remote") location.replace(connection.origin)
}

export function serverOrigin(value) {
  const url = new URL(value.trim())
  if (!["https:", "http:"].includes(url.protocol) || url.username || url.password ||
      url.pathname !== "/" || url.search || url.hash) {
    throw new Error("请输入 HTTP(S) 网页入口，不含账号、密码、路径或查询参数。")
  }
  return url.origin
}

export function savedServers(storage) {
  const data = JSON.parse(storage.getItem(storageKey) ?? "[]")
  if (!Array.isArray(data) || data.some(value => typeof value !== "string")) {
    throw new Error("服务器列表格式无效。")
  }
  return [...new Set(data.map(serverOrigin))]
}

export function startChooser(doc, storage) {
  const status = doc.querySelector("#status")
  let servers
  try {
    servers = savedServers(storage)
  } catch {
    status.textContent = "无法读取已保存的服务器。请检查本地存储；仍可输入地址连接。"
    servers = []
  }
  const render = () => {
    doc.querySelector("#empty").hidden = servers.length > 0
    doc.querySelector("#servers").replaceChildren(...servers.map(origin => {
      const row = doc.createElement("li")
      const link = doc.createElement("a")
      link.href = origin
      link.textContent = origin
      link.addEventListener("click", event => {
        event.preventDefault()
        try {
          rememberConnection(storage, { mode: "remote", origin })
          doc.defaultView.location.assign(origin)
        } catch {
          status.textContent = "无法保存连接方式。请检查本地存储后重试。"
        }
      })
      const remove = doc.createElement("button")
      remove.type = "button"
      remove.textContent = "移除"
      remove.setAttribute("aria-label", `移除 ${origin}`)
      remove.addEventListener("click", () => {
        try {
          const next = servers.filter(value => value !== origin)
          let removeLast = false
          try { removeLast = lastConnection(storage)?.origin === origin } catch { /* Invalid history must not prevent removing a saved server. */ }
          storage.setItem(storageKey, JSON.stringify(next))
          if (removeLast) storage.removeItem(connectionKey)
          servers = next
          status.textContent = ""
          render()
        } catch {
          status.textContent = "无法保存更改，服务器地址未移除。"
        }
      })
      row.append(link, remove)
      return row
    }))
  }
  render()
  doc.querySelector("#server-form").addEventListener("submit", event => {
    event.preventDefault()
    let origin
    try {
      origin = serverOrigin(doc.querySelector("#server-address").value)
    } catch {
      status.textContent = "请输入完整的 HTTP(S) 网页入口，不含账号、密码、路径或查询参数。"
      doc.querySelector("#server-address").focus()
      return
    }
    try {
      storage.setItem(storageKey, JSON.stringify([...new Set([...servers, origin])]))
      rememberConnection(storage, { mode: "remote", origin })
    } catch {
      status.textContent = "无法保存服务器地址。请检查本地存储后重试。"
      return
    }
    doc.defaultView.location.assign(origin)
  })
}

if (typeof document !== "undefined") {
  // Access to localStorage itself can be denied by the runtime.
  try { startChooser(document, window.localStorage) } catch {
    document.querySelector("#status").textContent = "本地存储不可用，无法保存服务器地址。"
  }
}
