import React, { useState } from "react";
import { useStore, update, id, log, assertWrite } from "./store";
import {
  Icon,
  IconButton,
  Button,
  Modal,
  Field,
  ActionForm,
  Select,
  Form,
  Empty,
  Notice,
  Link,
} from "./ui";
const kinds = [
  ["Web", "Globe2"],
  ["API", "Code2"],
  ["文件", "Folders"],
  ["终端", "Terminal"],
  ["文稿", "FileText"],
];
function defaultTabs(w) {
  return [
    {
      id: "primary",
      name: w.type === "软件" ? "Web · 主预览" : "成果 · 当前版本",
      kind: w.type === "软件" ? "Web" : "文稿",
      status: "就绪",
    },
    { id: "files", name: "交付文件", kind: "文件", status: "就绪" },
  ];
}
function PreviewContent({ tab, work }) {
  const [path, setPath] = useState("/"),
    [method, setMethod] = useState("GET"),
    [response, setResponse] = useState(null),
    [file, setFile] = useState("README.md"),
    [command, setCommand] = useState(""),
    [lines, setLines] = useState([
      "模拟终端 · 不执行本机命令。输入 help 查看演示命令。",
    ]);
  if (tab.status === "失联")
    return (
      <Empty
        title="预览连接已断开"
        text="标签与输入状态保留，恢复连接后继续。"
      />
    );
  if (tab.kind === "Web")
    return (
      <div className="mini-browser">
        <div className="preview-address">
          <Icon name="LockKeyhole" size={12} />
          <input
            aria-label="预览路径"
            value={path}
            onChange={(e) => setPath(e.target.value)}
          />
          <Icon name="RotateCcw" size={13} />
        </div>
        <div className="mini-site">
          <small>固定 v{work.version} · 本地示例</small>
          <h2>创造，更从容。</h2>
          <div className="product-art">
            <span />
          </div>
          <p>为专注而设计，陪你走得更远。</p>
        </div>
      </div>
    );
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
        {response && <pre>{JSON.stringify(response, null, 2)}</pre>}
        <small>不会访问真实服务。</small>
      </div>
    );
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
        <pre>
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
        <pre aria-live="polite">{lines.join("\n")}</pre>
        <Form
          onSubmit={() => {
            const c = command.trim();
            if (!c) return;
            setLines((v) => [
              ...v,
              `$ ${c}`,
              c === "help"
                ? "演示命令：help / pwd / status"
                : c === "pwd"
                  ? `/workspace/${work.id}`
                  : c === "status"
                    ? `${work.title} · ${work.status}`
                    : "此命令不在模拟范围内，不会执行。",
            ]);
            setCommand("");
          }}
        >
          <input
            aria-label="任务终端命令"
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
export function TaskPreviews({ work, toast }) {
  const s = useStore(),
    entry = s.taskPreviews?.[work.id],
    tabs = entry?.tabs || defaultTabs(work),
    active = entry?.active || tabs[0]?.id;
  const [adding, setAdding] = useState(false),
    [name, setName] = useState(""),
    [kind, setKind] = useState("Web");
  const modify = (fn) => {
    try {
      assertWrite();
      update((x) => {
        x.taskPreviews ??= {};
        x.taskPreviews[work.id] ??= {
          tabs: defaultTabs(work),
          active: "primary",
        };
        fn(x.taskPreviews[work.id], x);
      });
    } catch (e) {
      toast(e.message, true);
    }
  };
  const select = (tabId) => modify((v) => (v.active = tabId));
  const close = (tabId) =>
    modify((v, x) => {
      v.tabs = v.tabs.filter((t) => t.id !== tabId);
      if (v.active === tabId) v.active = v.tabs[0]?.id;
      log(
        x,
        `关闭 ${work.id} 预览标签，释放当前观察引用`,
        `/chat?execution_id=${work.id}`,
      );
    });
  return (
    <div className="task-previews">
      <div className="section-title">
        <h2>任务预览</h2>
        {active && (
          <IconButton
            name="X"
            label={`关闭 ${tabs.find((t) => t.id === active)?.name}`}
            onClick={() => close(active)}
          />
        )}
        <IconButton
          name="Plus"
          label="添加预览标签"
          onClick={() => {
            setName("");
            setAdding(true);
          }}
        />
      </div>
      <div className="preview-tablist" role="tablist" aria-label="任务预览标签">
        {tabs.map((tab, i) => (
          <div
            className={`preview-tab ${tab.id === active ? "active" : ""}`}
            key={tab.id}
          >
            <button
              role="tab"
              aria-selected={tab.id === active}
              aria-controls={`preview-panel-${tab.id}`}
              id={`preview-tab-${tab.id}`}
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
                  document
                    .getElementById(`preview-tab-${tabs[next].id}`)
                    ?.focus();
                }
              }}
            >
              <Icon
                name={kinds.find((k) => k[0] === tab.kind)?.[1] || "FileText"}
                size={13}
              />
              {tab.name}
            </button>
          </div>
        ))}
      </div>
      {tabs.length ? (
        tabs.map((tab) => (
          <section
            role="tabpanel"
            aria-labelledby={`preview-tab-${tab.id}`}
            id={`preview-panel-${tab.id}`}
            key={tab.id}
            hidden={tab.id !== active}
          >
            <div className="preview-status">
              <span>
                <i
                  className={
                    tab.status === "就绪" ? "online-dot" : "offline-dot"
                  }
                />
                {tab.status} · {work.id}
              </span>
              <button
                onClick={() =>
                  modify((v) => {
                    v.tabs.find((t) => t.id === tab.id).status =
                      tab.status === "就绪" ? "失联" : "就绪";
                  })
                }
              >
                {tab.status === "就绪" ? "模拟断线" : "重新连接"}
              </button>
            </div>
            <PreviewContent tab={tab} work={work} />
          </section>
        ))
      ) : (
        <Empty title="没有打开的预览" text="添加 Web、API、文件或终端标签。" />
      )}
      <p className="preview-footnote">
        关闭标签释放当前观察引用；共享服务由租约与健康检查回收。此处为本地模拟。
      </p>
      <Link
        to={`/p/${work.project}/artifacts/${work.id}`}
        className="btn primary full"
      >
        成果与版本批注
      </Link>
      <Link
        to={`/p/${work.project}/work-items/${work.id}?tab=review`}
        className="btn full"
      >
        检查与接受
      </Link>
      {adding && (
        <Modal title="添加预览标签" onClose={() => setAdding(false)}>
          <ActionForm
            label="打开预览"
            onCancel={() => setAdding(false)}
            onSubmit={() => {
              assertWrite();
              if (!name.trim()) throw Error("请填写标签名称。");
              modify((v) => {
                const nid = id("preview");
                v.tabs.push({
                  id: nid,
                  name: name.trim(),
                  kind,
                  status: "就绪",
                });
                v.active = nid;
              });
              setAdding(false);
            }}
          >
            <Field
              label="预览类型"
              value={kind}
              onChange={setKind}
              options={kinds.map((k) => k[0])}
            />
            <Field
              label="标签名称"
              value={name}
              onChange={setName}
              required
              placeholder="例如：Web · 管理端"
            />
            <Notice>
              仅绑定当前任务。正式预览须校验服务、节点与访问权限。
            </Notice>
          </ActionForm>
        </Modal>
      )}
    </div>
  );
}
