import { resumeConnection, rememberConnection } from "./servers.mjs"

// Native commands validate that the caller is the bundled chooser. Remote Web
// and the locally served Web receive no process or setup capability.
export function startLocal(doc, invoke, navigate) {
  const start = doc.querySelector("#start-local")
  const form = doc.querySelector("#local-setup")
  const status = doc.querySelector("#local-status")
  let busy = false
  const perform = async (command, args) => {
    if (busy) return
    busy = true
    start.disabled = true
    form.querySelector("button").disabled = true
    form.setAttribute("aria-busy", "true")
    status.textContent = command === "start_local" ? "正在启动本地服务，首次启动需要初始化数据，请稍候……" : "正在创建管理员并启动服务……"
    try {
      const event = await invoke(command, args)
      if (event.event === "setup") {
        const wasHidden = form.hidden
        form.hidden = false
        status.textContent = event.message || "请设置本地管理员账号和密码。"
        if (wasHidden) doc.querySelector("#local-login").focus()
      } else if (event.event === "ready") {
        status.textContent = "本地服务已启动，正在进入……"
        navigate(event.origin)
      } else {
        throw new Error("本地服务返回了无效状态。")
      }
    } catch (error) {
      status.textContent = `无法进入本地服务：${error instanceof Error ? error.message : String(error)}`
    } finally {
      busy = false
      start.disabled = false
      form.querySelector("button").disabled = false
      form.setAttribute("aria-busy", "false")
    }
  }
  start.addEventListener("click", () => perform("start_local"))
  form.addEventListener("submit", async event => {
    event.preventDefault()
    const password = doc.querySelector("#local-password")
    const confirm = doc.querySelector("#local-password-confirm")
    if (password.value !== confirm.value) {
      status.textContent = "两次输入的密码不一致。"
      confirm.focus()
      return
    }
    const args = { login: doc.querySelector("#local-login").value.trim(), password: password.value }
    password.value = ""
    confirm.value = ""
    await perform("setup_local", args)
    args.password = ""
  })
  return () => perform("start_local")
}

if (typeof document !== "undefined") {
  const start = startLocal(document, (command, args) => {
    const native = window.__TAURI_INTERNALS__
    if (!native?.invoke) throw new Error("本地服务需要从桌面应用启动。")
    return native.invoke(command, args)
  }, origin => {
    try { rememberConnection(window.localStorage, { mode: "local" }) } catch {
      document.querySelector("#local-status").textContent = "无法保存连接方式，下次启动需要重新选择。"
    }
    window.location.assign(origin)
  })
  // Returning through the native menu is an explicit request to choose again.
  // Keep that request in the URL so reloading this page cannot reconnect it.
  try {
    void resumeConnection(window.localStorage, window.location, start)
  } catch {
    document.querySelector("#status").textContent = "无法恢复上次连接，请重新选择运行方式。"
  }
}
