import { useViewState } from "./view-state";
import React, { useState, useId, useLayoutEffect, useRef, useEffect } from "react";
import { useStore, update, assertWrite, requestProjectLease, assertProjectRecordWritable } from "./store";
import {
  Icon,
  IconButton,
  Button,
  Modal,
  Field,
  Select,
  Form,
  Empty,
} from "./ui";
const kinds = [
  ["Web", "Globe2"],
  ["API", "Code2"],
  ["文件", "Folders"],
  ["终端", "Terminal"],
  ["文稿", "FileText"],
  ["仓库", "Code2"],
];
function defaultTabs(w) {
  // The execution resource manifest owns tabs; local view state never creates resources.
  if (Array.isArray(w.previewResources)) return w.previewResources;
  return [{ id: "primary", name: w.type === "软件" ? "Web · 主预览" : `${w.title} · 当前版本`,
    kind: w.type === "软件" ? "Web" : "文稿", status: "就绪" }];
}
function resourceTabs(work, saved) {
  return defaultTabs(work).map(resource => ({ ...resource,
    view: saved?.tabs?.find(tab => tab.id === resource.id)?.view,
    status: saved?.tabs?.find(tab => tab.id === resource.id)?.status || resource.status,
  }));
}
function PreviewContent({ tab, work, saveView }) {
  const view = tab.view || {};
  const path = view.path ?? tab.url ?? "/",
    method = view.method ?? "GET",
    response = view.response ?? null,
    file = view.file ?? "README.md",
    command = view.command ?? "",
    lines = view.lines ?? [
      "模拟终端 · 不执行本机命令。输入 help 查看演示命令。",
    ];
  const setPath = (path) => saveView({ path });
  const setMethod = (method) => saveView({ method });
  const setFile = (file) => saveView({ file });
  const setCommand = (command) => saveView({ command });
  const setResponse = (response) => saveView({ response });
  if (tab.kind === "Web" || tab.kind === "Swagger") {
    const history = view.history || [tab.url || "/"], index = view.historyIndex ?? 0;
    const current = history[index];
    const navigate = () => {
      const value = path.trim();
      // Prototype URLs stay local: no real proxy/service has been connected.
      if (!value || !value.startsWith("/") || value.startsWith("//")) {
        saveView({ addressError: "本地示例仅支持以 / 开头的路径；远程地址需由已授权预览资源提供。" });
        return;
      }
      if (value === current) return;
      saveView({ path: value, history: [...history.slice(0,index+1), value], historyIndex:index+1, addressError:"" });
    };
    const move = next => saveView({ historyIndex:next, path:history[next], addressError:"" });
    return <div className="mini-browser">
      <Form className="preview-address" onSubmit={navigate}>
        <IconButton type="button" name="ChevronLeft" label="预览后退" disabled={index === 0} onClick={() => move(index-1)} />
        <IconButton type="button" name="ChevronRight" label="预览前进" disabled={index === history.length-1} onClick={() => move(index+1)} />
        <IconButton type="button" name="RotateCcw" label="刷新预览" onClick={() => saveView({ refreshed:(view.refreshed || 0)+1 })} />
        <input aria-label="预览路径" name="preview-path" autoComplete="off" spellCheck={false} value={path} onChange={e => setPath(e.target.value)} aria-invalid={!!view.addressError} />
        <IconButton name="ArrowRight" label="打开预览路径" type="submit" />
      </Form>
      {view.addressError && <p className="preview-address-error" role="alert">{view.addressError}</p>}
      <div className="mini-site" key={view.refreshed || 0}>
        <small>固定 v{work.version} · 本地示例 · {current}</small>
        <h2>创造，更从容。</h2>
        <div className="product-art"><span /></div>
        <p>为专注而设计，陪你走得更远。</p>
        {!!view.refreshed && <small role="status">示例已刷新 · {view.refreshed}</small>}
      </div>
    </div>;
  }
  if (tab.kind === "API")
    return (
      <div className="preview-api">
        <Select
          aria-label="API 方法"
          value={method}
          options={["GET", "POST"]}
          onChange={(e) => setMethod(e.target.value)}
        />
        <Field label="请求路径" value={path} onChange={setPath} />
        <Button
          icon="Play"
          onClick={() =>
            setResponse({
              status: 200,
              method,
              path,
              work: work.id,
              version: work.version,
              source: "mock",
            })
          }
        >
          模拟请求
        </Button>
        {response && <pre data-preview-scroll="output">{JSON.stringify(response, null, 2)}</pre>}
        <small>不会访问真实服务。</small>
      </div>
    );
  if (tab.kind === "仓库") {
    const files = {
      "README.md": `# ${work.title}\n\n本地仓库示例 · v${work.version}\n\n${work.notes || "任务关联仓库的只读文件浏览。"}`,
      "src/index.ts": '// 本地代码示例，不连接真实仓库\nexport function main() {\n  return "Preview ready";\n}\n',
      "package.json": JSON.stringify({ private: true, description: "本地仓库示例", version: "0.0.0" }, null, 2),
      "acceptance.md": work.requirements.map(value => `- ${value}`).join("\n"),
    };
    const selected = Object.hasOwn(files, file) ? file : "README.md";
    const item = name => <button key={name} className={selected === name ? "active" : ""} aria-current={selected === name ? "true" : undefined} onClick={() => setFile(name)}><Icon name="FileCode2" size={14} />{name.split("/").at(-1)}</button>;
    return <div className="preview-repository">
      <nav aria-label="仓库文件" className="repository-tree">
        <small>文件 · 只读示例</small>
        {item("README.md")}
        <details open><summary><Icon name="Folders" size={14} />src</summary>{item("src/index.ts")}</details>
        {item("package.json")}{item("acceptance.md")}
      </nav>
      <div className="repository-editor"><div className="repository-path">{selected}</div><pre data-preview-scroll="output"><code>{files[selected]}</code></pre></div>
    </div>;
  }
  if (tab.kind === "文件")
    return (
      <div className="preview-files">
        <div className="preview-file-list">
          {["README.md", "result.json", "acceptance.md"].map((name) => (
            <button
              className={file === name ? "active" : ""}
              key={name}
              onClick={() => setFile(name)}
            >
              <Icon name="FileText" size={14} />
              {name}
            </button>
          ))}
        </div>
        <pre data-preview-scroll="output">
          {file === "result.json"
            ? JSON.stringify(
                { work: work.id, version: work.version, status: work.status },
                null,
                2,
              )
            : file === "acceptance.md"
              ? work.requirements.map((v) => `- ${v}`).join("\n")
              : `# ${work.title}\n\n固定版本 v${work.version}\n负责人：${work.owner}\n\n演示文件内容，原件需连接节点读取。`}
        </pre>
      </div>
    );
  if (tab.kind === "终端")
    return (
      <div className="task-terminal">
        <pre data-preview-scroll="output" aria-live="polite">{lines.join("\n")}</pre>
        <Form
          onSubmit={() => {
            const c = command.trim();
            if (!c) return;
            saveView({
              lines: [
                ...lines,
                `$ ${c}`,
                c === "help"
                  ? "演示命令：help / pwd / status"
                  : c === "pwd"
                    ? `/workspace/${work.id}`
                    : c === "status"
                      ? `${work.title} · ${work.status}`
                      : "此命令不在模拟范围内，不会执行。",
              ],
              command: "",
            });
          }}
        >
          <input
            aria-label="任务终端命令"
            name="terminal-command"
            autoComplete="off"
            spellCheck={false}
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            placeholder="help"
          />
          <Button type="submit" icon="ArrowRight" aria-label="执行模拟命令" />
        </Form>
      </div>
    );
  return (
    <article className="preview-document">
      <small>
        {work.type} · v{work.version}
      </small>
      <h3>{work.title}</h3>
      <p>{work.notes}</p>
      <ul>
        {work.requirements.map((v) => (
          <li key={v}>{v}</li>
        ))}
      </ul>
    </article>
  );
}
function PreviewPanel({ executionId, tab, active, work, prefix, modify, toast }) {
  const state = useStore();
  const content = useRef(null);
  const saveView = (patch) => {
    if (["lines","response","refreshed","history"].some(key=>Object.hasOwn(patch,key))) {
      try { requestProjectLease({tenantId:state.activeTenant,projectId:work.project,workId:work.id,resourceId:tab.id,executionId,kind:tab.kind === "终端" ? "terminal" : "preview"}); } catch(error) { toast(error.message,true); return; }
    }
    return modify((v) => {
      const target = v.tabs.find((t) => t.id === tab.id);
      target.view = { ...target.view, ...patch };
    });
  };
  useLayoutEffect(() => {
    if (!active || tab.status === "失联" || !content.current) return;
    for (const el of [content.current, ...content.current.querySelectorAll("[data-preview-scroll]")]) {
      const key = el.dataset.previewScroll || "content";
      const saved = tab.view?.scrollPositions?.[key];
      el.scrollTop = saved?.top ?? (key === "content" ? tab.view?.scrollTop || 0 : 0);
      el.scrollLeft = saved?.left || 0;
    }
  }, [active, tab.status]);
  return (
    <section
      role="tabpanel"
      aria-labelledby={`${prefix}-tab-${tab.id}`}
      id={`${prefix}-panel-${tab.id}`}
      hidden={!active}
    >
      <div className="preview-status">
        <span>
          <i className={tab.status === "就绪" ? "online-dot" : "offline-dot"} />
          {tab.status} · {work.id}
        </span>
        <button
          onClick={() =>
            modify((v) => {
              v.tabs.find((t) => t.id === tab.id).status =
                tab.status === "就绪" ? "失联" : "就绪";
            }, true)
          }
        >
          {tab.status === "就绪" ? "模拟断线" : "重新连接"}
        </button>
      </div>
      {tab.status === "失联" && (
        <Empty
          title="预览连接已断开"
          text="标签与输入状态保留，恢复连接后继续。"
        />
      )}
      <div
        ref={content}
        hidden={tab.status === "失联"}
        className="preview-content-scroll"
        onScrollCapture={(e) => {
          const el = e.target, key = el.dataset.previewScroll || "content";
          if (!active || tab.status === "失联" || !el.getClientRects().length) return;
          const position = { top: el.scrollTop, left: el.scrollLeft };
          modify((v) => {
            const target = v.tabs.find((t) => t.id === tab.id);
            target.view = { ...target.view, scrollPositions: { ...target.view?.scrollPositions, [key]: position } };
          });
        }}
      >
        <PreviewContent tab={tab} work={work} saveView={saveView} />
      </div>
    </section>
  );
}
export function TaskPreviews({ work, toast, expanded = false, scopeId }) {
  const previewKey = scopeId || work.id;
  const prefix = useId();
  const [fullscreen, setFullscreen] = useState(false);
  const [requestedTab,setRequestedTab] = useViewState(`preview.${previewKey}`, "");
  const s = useStore(),
    entry = s.taskPreviews?.[previewKey],
    tabs = resourceTabs(work, entry),
    active = tabs.some(tab => tab.id === requestedTab) ? requestedTab : tabs.some(tab => tab.id === entry?.active) ? entry.active : tabs[0]?.id;
  useLayoutEffect(() => {
    const frame = requestAnimationFrame(() => {
      const tab = document.getElementById(`${prefix}-tab-${active}`)?.parentElement;
      const bar = tab?.parentElement;
      if (!bar) return;
      const bounds = bar.getBoundingClientRect(), selected = tab.getBoundingClientRect();
      if (selected.right > bounds.right) bar.scrollLeft += selected.right - bounds.right;
      else if (selected.left < bounds.left) bar.scrollLeft -= bounds.left - selected.left;
    });
    return () => cancelAnimationFrame(frame);
  }, [active, tabs.length, prefix]);
  const modify = (fn, requiresConnection = false) => {
    try {
      if (requiresConnection) { assertWrite(); assertProjectRecordWritable(work); const tab=tabs.find(tab=>tab.id===active); requestProjectLease({tenantId:s.activeTenant,projectId:work.project,workId:work.id,resourceId:tab?.id,executionId:scopeId,kind:tab?.kind === "终端" ? "terminal" : "preview"}); }
      update((x) => {
        x.taskPreviews ??= {};
        x.taskPreviews[previewKey] ??= {
          tabs: defaultTabs(work),
          active: "primary",
        };
        x.taskPreviews[previewKey].tabs = resourceTabs(work, x.taskPreviews[previewKey]);
        fn(x.taskPreviews[previewKey], x);
      });
      return true;
    } catch (e) {
      toast(e.message, true);
      return false;
    }
  };
  const focusTab = (tabId) =>
    requestAnimationFrame(() => {
      const target = tabId
        ? document.getElementById(`${prefix}-tab-${tabId}`)
        : null;
      target?.focus();
      target?.scrollIntoView({ block: "nearest", inline: "nearest" });
    });
  const select = (tabId) => {
    if (modify((v) => (v.active = tabId))) {setRequestedTab(tabId);focusTab(tabId);}
  };
  return (
    <div className="task-previews">
      {!expanded && <div className="preview-heading">
        <h2>任务预览 <small>本地示例</small></h2>
        <IconButton name="Maximize2" label="展开任务预览" onClick={() => setFullscreen(true)} />
      </div>}
      <div className="preview-tablist" role="tablist" aria-label="任务预览标签">
        {tabs.map((tab, i) => (
          <div
            className={`preview-tab ${tab.id === active ? "active" : ""}`}
            key={tab.id}
          >
            <button
              role="tab"
              aria-selected={tab.id === active}
              aria-controls={`${prefix}-panel-${tab.id}`}
              id={`${prefix}-tab-${tab.id}`}
              tabIndex={tab.id === active ? 0 : -1}
              onClick={() => select(tab.id)}
              onKeyDown={(e) => {
                if (
                  ["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)
                ) {
                  e.preventDefault();
                  const next =
                    e.key === "Home"
                      ? 0
                      : e.key === "End"
                        ? tabs.length - 1
                        : (i +
                            (e.key === "ArrowRight" ? 1 : -1) +
                            tabs.length) %
                          tabs.length;
                  select(tabs[next].id);
                }
              }}
            >
              <Icon
                name={kinds.find((k) => k[0] === tab.kind)?.[1] || "FileText"}
                size={13}
              />
              <span className="preview-tab-name">{tab.name}</span>
            </button>
          </div>
        ))}
      </div>
      {tabs.length ? (
        tabs.map((tab) => (
          <PreviewPanel
            key={tab.id}
            tab={tab}
            active={tab.id === active}
            work={work}
            executionId={scopeId}
            prefix={prefix}
            modify={modify}
            toast={toast}
          />
        ))
      ) : (
        <Empty title={scopeId ? "此执行尚无预览资源" : "没有打开的预览"} text={scopeId ? "仅展示此执行登记的成果清单，不复制其他执行的资源。" : "任务产生可预览资源后，会自动显示在这里。"} />
      )}
      {fullscreen && <Modal title="任务预览 · 本地示例" className="preview-dialog" onClose={() => setFullscreen(false)}>
        <TaskPreviews work={work} toast={toast} scopeId={scopeId} expanded />
      </Modal>}
    </div>
  );
}
