import React, {
  useEffect,
  useRef,
  useState,
  useId,
  createContext,
  useContext,
} from "react";
import { createPortal } from "react-dom";
import {
  Palette,
  Info,
  AlertTriangle,
  Copy,
  Trash2,
  CalendarDays,
  ChevronLeft,
  ThumbsUp,
  Ellipsis,
  X,
  FileCheck,
  Activity,
  AlertCircle,
  ArrowRight,
  ArrowUp,
  ArrowUpRight,
  Asterisk,
  Bell,
  BookOpen,
  Bot,
  Boxes,
  Bug,
  Building2,
  CalendarClock,
  Check,
  CheckCheck,
  CheckCircle2,
  ChevronRight,
  ChevronsUpDown,
  Circle,
  CircleAlert,
  CircleCheck,
  CircleDot,
  CircleStop,
  CircleX,
  Clapperboard,
  ClipboardCheck,
  Clock3,
  Code2,
  Columns3,
  CornerUpLeft,
  Download,
  FileCode2,
  FileJson,
  FileText,
  Fingerprint,
  FlaskConical,
  Folders,
  GitBranch,
  GitCompareArrows,
  Globe2,
  Hand,
  Hash,
  Image,
  Inbox,
  Layers,
  LayoutDashboard,
  Library,
  List,
  ListPlus,
  ListTodo,
  LockKeyhole,
  Mail,
  Megaphone,
  Menu,
  MessageSquarePlus,
  MessagesSquare,
  Monitor,
  Moon,
  NotebookPen,
  Orbit,
  PackageCheck,
  PanelRight,
  Paperclip,
  Pause,
  Pencil,
  Pin,
  Play,
  Plug,
  Plus,
  Radio,
  RotateCcw,
  Save,
  Search,
  Server,
  Settings2,
  Share2,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
  Slack,
  SlidersHorizontal,
  Smartphone,
  Sparkles,
  Stamp,
  Sun,
  Target,
  Terminal,
  UserCheck,
  UserPlus,
  UsersRound,
  Wallet,
  Waves,
  Workflow,
  Wrench,
} from "lucide-react";
const Icons = {
  Palette,
  Info,
  AlertTriangle,
  Copy,
  Trash2,
  CalendarDays,
  ChevronLeft,
  ThumbsUp,
  Ellipsis,
  X,
  FileCheck,
  Activity,
  AlertCircle,
  ArrowRight,
  ArrowUp,
  ArrowUpRight,
  Asterisk,
  Bell,
  BookOpen,
  Bot,
  Boxes,
  Bug,
  Building2,
  CalendarClock,
  Check,
  CheckCheck,
  CheckCircle2,
  ChevronRight,
  ChevronsUpDown,
  Circle,
  CircleAlert,
  CircleCheck,
  CircleDot,
  CircleStop,
  CircleX,
  Clapperboard,
  ClipboardCheck,
  Clock3,
  Code2,
  Columns3,
  CornerUpLeft,
  Download,
  FileCode2,
  FileJson,
  FileText,
  Fingerprint,
  FlaskConical,
  Folders,
  GitBranch,
  GitCompareArrows,
  Globe2,
  Hand,
  Hash,
  Image,
  Inbox,
  Layers,
  LayoutDashboard,
  Library,
  List,
  ListPlus,
  ListTodo,
  LockKeyhole,
  Mail,
  Megaphone,
  Menu,
  MessageSquarePlus,
  MessagesSquare,
  Monitor,
  Moon,
  NotebookPen,
  Orbit,
  PackageCheck,
  PanelRight,
  Paperclip,
  Pause,
  Pencil,
  Pin,
  Play,
  Plug,
  Plus,
  Radio,
  RotateCcw,
  Save,
  Search,
  Server,
  Settings2,
  Share2,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
  Slack,
  SlidersHorizontal,
  Smartphone,
  Sparkles,
  Stamp,
  Sun,
  Target,
  Terminal,
  UserCheck,
  UserPlus,
  UsersRound,
  Wallet,
  Waves,
  Workflow,
  Wrench,
};
export function Icon({ name, size = 18, ...props }) {
  const C = Icons[name] || Icons.Circle;
  return <C size={size} strokeWidth={1.7} aria-hidden="true" {...props} />;
}
export function Button({
  children,
  icon,
  variant = "",
  className = "",
  ...props
}) {
  return (
    <button className={`btn ${variant} ${className}`} {...props}>
      {icon && <Icon name={icon} />} {children}
    </button>
  );
}
export function IconButton({ name, label, ...props }) {
  return (
    <button className="icon-button" aria-label={label} title={label} {...props}>
      <Icon name={name} />
    </button>
  );
}
export function Badge({ children, tone }) {
  const green = [
    "已完成",
    "通过",
    "在线",
    "有效",
    "已连接",
    "已授权",
    "已发布",
    "已接受",
    "已批准",
    "生效",
    "已提交",
    "启用",
    "已关闭",
  ];
  const amber = [
    "待验收",
    "待审核",
    "等待",
    "待输入",
    "待认领",
    "未连接",
    "待授权",
    "待应用",
    "待回执",
    "草稿",
  ];
  const red = ["失败", "失联", "待修复", "已取消", "已拒绝"];
  return (
    <span
      className={`badge ${tone || (green.includes(children) ? "green" : red.includes(children) ? "red" : amber.includes(children) ? "amber" : "violet")}`}
    >
      <span className="status-dot" />
      {children}
    </span>
  );
}
export function Avatar({ name = "林予", agent = false, size = "" }) {
  return (
    <span
      className={`avatar ${agent ? "agent" : ""} ${size}`}
      aria-label={name}
      role="img"
    >
      {agent ? <Icon name="Bot" size={16} /> : name.slice(0, 1)}
    </span>
  );
}
export function PageHead({ eyebrow, title, subtitle, children }) {
  return (
    <header className="page-head">
      <div>
        <div className="eyebrow">{eyebrow || "WORKSPACE / MYRMIDON"}</div>
        <h1>{title}</h1>
        {subtitle && <p>{subtitle}</p>}
      </div>
      <div className="actions">{children}</div>
    </header>
  );
}
export function Section({ title, extra, children, className = "" }) {
  return (
    <section className={`section ${className}`}>
      <div className="section-title">
        <h2>{title}</h2>
        {extra}
      </div>
      {children}
    </section>
  );
}
export function Empty({
  title = "这里还没有记录",
  text = "开始第一项工作，让协作从这里发生。",
  children,
}) {
  return (
    <div className="empty">
      <span className="empty-icon">
        <Icon name="Inbox" size={30} />
      </span>
      <h2>{title}</h2>
      <p>{text}</p>
      {children}
    </div>
  );
}
export function Progress({ value }) {
  return (
    <div
      className="progress"
      role="progressbar"
      aria-label="完成进度"
      aria-valuenow={value}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <span style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  );
}
export function Tabs({ items, value, onChange }) {
  return (
    <div className="tabs" role="tablist">
      {items.map(([key, label]) => (
        <button
          key={key}
          role="tab"
          aria-selected={value === key}
          className={value === key ? "selected" : ""}
          onClick={() => onChange(key)}
        >
          {label}
        </button>
      ))}
    </div>
  );
}
export function Link({ to, children, className = "", ...props }) {
  return (
    <a href={`#${to}`} className={className} {...props}>
      {children}
    </a>
  );
}
export function PanelButton({
  to,
  onView,
  children,
  className = "",
  ...props
}) {
  return (
    <button
      type="button"
      className={`panel-button ${className}`}
      onClick={() => onView(to)}
      {...props}
    >
      {children}
    </button>
  );
}
export function Modal({ title, children, onClose, wide = false }) {
  const ref = useRef();
  useEffect(() => {
    const last = document.activeElement;
    ref.current.showModal();
    return () => last?.focus();
  }, []);
  return (
    <dialog
      ref={ref}
      aria-label={title}
      className={wide ? "wide" : ""}
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <div className="modal-heading">
        <h2>{title}</h2>
        <IconButton name="X" label="关闭弹窗" onClick={onClose} />
      </div>
      <div className="modal-body">{children}</div>
    </dialog>
  );
}
export function Field({
  label,
  value,
  onChange,
  type = "text",
  options,
  required = false,
  help,
  name,
  ...props
}) {
  const [error, setError] = useState("");
  const uid = useId();
  const shared = {
    ...props,
    id: uid,
    name: name || label,
    required,
    value,
    "aria-invalid": !!error,
    "aria-describedby": error
      ? `${uid}-error`
      : help
        ? `${uid}-help`
        : undefined,
    onChange: (e) => {
      setError("");
      onChange(e.target.value);
    },
    onInvalid: (e) => {
      e.preventDefault();
      setError(validityText(e.target));
    },
    onBlur: (e) => {
      if (e.target.validity && !e.target.validity.valid)
        setError(validityText(e.target));
    },
  };
  return (
    <div className="field">
      <label htmlFor={uid}>
        {label}
        {required && <b aria-hidden="true"> *</b>}
      </label>
      {options ? (
        <Select {...shared} aria-label={label} options={options} />
      ) : type === "textarea" ? (
        <textarea {...shared} rows={4} />
      ) : (
        <input
          {...shared}
          type={type}
          autoComplete={type === "email" ? "email" : "off"}
        />
      )}
      {help && <small id={`${uid}-help`}>{help}</small>}
      {error && (
        <span className="field-error" id={`${uid}-error`} role="alert">
          <Icon name="AlertCircle" size={14} />
          {error}
        </span>
      )}
    </div>
  );
}
export function ActionForm({ onSubmit, children, label = "保存", onCancel }) {
  const [error, setError] = useState("");
  return (
    <Form
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          setError("");
          await onSubmit();
        } catch (err) {
          setError(err.message);
        }
      }}
    >
      {children}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="modal-footer">
        <Button type="button" onClick={onCancel}>
          取消
        </Button>
        <Button variant="primary" type="submit">
          {label}
        </Button>
      </div>
    </Form>
  );
}
export function Metric({ label, value, detail, icon }) {
  return (
    <div className="metric">
      <div className="metric-label">
        {label}
        <Icon name={icon || "TrendingUp"} />
      </div>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}
export function Notice({ children, tone = "info" }) {
  return (
    <div className={`notice ${tone}`}>
      <Icon name={tone === "warn" ? "AlertTriangle" : "Info"} />
      <div>{children}</div>
    </div>
  );
}

const ConfirmContext = createContext(null);
export function ConfirmProvider({ children }) {
  const [pending, setPending] = useState(null);
  const settle = (value) => {
    pending?.resolve(value);
    setPending(null);
  };
  return (
    <ConfirmContext.Provider
      value={(message) =>
        new Promise((resolve) => setPending({ message, resolve }))
      }
    >
      {children}
      {pending && (
        <Modal title="确认操作" onClose={() => settle(false)}>
          <p>{pending.message}</p>
          <div className="modal-footer">
            <Button onClick={() => settle(false)}>取消</Button>
            <Button variant="primary" onClick={() => settle(true)}>
              确认继续
            </Button>
          </div>
        </Modal>
      )}
    </ConfirmContext.Provider>
  );
}
export const useConfirm = () => useContext(ConfirmContext);
export function Select({
  value,
  onChange,
  children,
  options,
  disabled = false,
  name,
  required = false,
  ...props
}) {
  const items = options
    ? options.map((v) => (typeof v === "object" ? v : { value: v, label: v }))
    : React.Children.toArray(children)
        .filter(React.isValidElement)
        .map((v) => ({
          value: v.props.value ?? String(v.props.children),
          label: v.props.children,
          disabled: v.props.disabled,
        }));
  const [open, setOpen] = useState(false),
    [position, setPosition] = useState({});
  const trigger = useRef(null),
    list = useRef(null),
    uid = useId();
  const current = items.find((i) => String(i.value) === String(value));
  const close = () => {
    setOpen(false);
    trigger.current?.focus({ preventScroll: true });
  };
  const choose = (i) => {
    onChange?.({ target: { value: String(i.value) } });
    close();
  };
  const show = () => {
    if (disabled) return;
    const r = trigger.current.getBoundingClientRect(),
      below = innerHeight - r.bottom - 12,
      above = r.top - 12,
      height = Math.min(280, Math.max(below, above)),
      width = Math.min(Math.max(r.width, 180), innerWidth - 24);
    setPosition({
      left: Math.min(Math.max(12, r.left), innerWidth - width - 12),
      width,
      maxHeight: height,
      ...(below >= Math.min(200, above)
        ? { top: r.bottom + 5 }
        : { bottom: innerHeight - r.top + 5 }),
    });
    setOpen(true);
  };
  useEffect(() => {
    if (!open) return;
    const selected =
      list.current?.querySelector("[aria-selected=true]") ||
      list.current?.querySelector("[role=option]");
    selected?.focus({ preventScroll: true });
    const outside = (e) => {
      if (
        !list.current?.contains(e.target) &&
        !trigger.current?.contains(e.target)
      )
        setOpen(false);
    };
    const scroll = (e) => {
      if (!list.current?.contains(e.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", scroll, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", scroll, true);
    };
  }, [open]);
  return (
    <span className="custom-select">
      <button
        {...props}
        ref={trigger}
        type="button"
        className="custom-select-trigger"
        role="combobox"
        aria-expanded={open}
        aria-controls={open ? uid : undefined}
        aria-haspopup="listbox"
        aria-required={required || undefined}
        disabled={disabled}
        onClick={() => (open ? close() : show())}
        onKeyDown={(e) => {
          if (["ArrowDown", "ArrowUp", "Enter", " "].includes(e.key)) {
            e.preventDefault();
            show();
          }
        }}
      >
        <span>{current?.label || "请选择"}</span>
        <Icon name="ChevronsUpDown" size={15} />
      </button>
      {name && <input type="hidden" name={name} value={value || ""} />}
      {open &&
        createPortal(
          <div
            ref={list}
            className="select-popover"
            id={uid}
            role="listbox"
            aria-label={props["aria-label"] || "可选项"}
            style={position}
            onKeyDown={(e) => {
              const all = [
                  ...list.current.querySelectorAll(
                    "[role=option]:not(:disabled)",
                  ),
                ],
                index = all.indexOf(document.activeElement);
              if (["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) {
                e.preventDefault();
                all[
                  e.key === "Home"
                    ? 0
                    : e.key === "End"
                      ? all.length - 1
                      : (index +
                          (e.key === "ArrowDown" ? 1 : -1) +
                          all.length) %
                        all.length
                ]?.focus();
              } else if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                close();
              } else if (e.key === "Tab") {
                e.preventDefault();
                close();
              } else if (e.key.length === 1) {
                all
                  .find((el) =>
                    el.textContent
                      .toLowerCase()
                      .startsWith(e.key.toLowerCase()),
                  )
                  ?.focus();
              }
            }}
          >
            {items.map((i) => (
              <button
                type="button"
                role="option"
                aria-selected={String(i.value) === String(value)}
                disabled={i.disabled}
                tabIndex={-1}
                key={i.value}
                onClick={() => choose(i)}
              >
                {i.label}
                {String(i.value) === String(value) && (
                  <Icon name="Check" size={15} />
                )}
              </button>
            ))}
          </div>,
          trigger.current?.closest("dialog") || document.body,
        )}
    </span>
  );
}
function validityText(input) {
  if (input.validity.valueMissing) return "请填写此项。";
  if (input.validity.typeMismatch)
    return input.type === "email"
      ? "请输入有效的电子邮件地址。"
      : "请输入有效格式。";
  if (input.validity.rangeUnderflow) return `不能小于 ${input.min}。`;
  if (input.validity.rangeOverflow) return `不能大于 ${input.max}。`;
  if (input.validity.patternMismatch) return "输入格式不符合要求。";
  if (input.validity.stepMismatch) return `请按 ${input.step} 的步长输入。`;
  return "请检查输入内容。";
}
export function Form({ onSubmit, children, ...props }) {
  const [error, setError] = useState("");
  return (
    <form
      {...props}
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        if (!e.currentTarget.checkValidity()) {
          setError("请修正标记的输入项后再提交。");
          e.currentTarget.querySelector(":invalid")?.focus();
          return;
        }
        setError("");
        onSubmit?.(e);
      }}
    >
      {children}
      {error && (
        <div className="validation-summary" role="alert">
          <Icon name="AlertCircle" size={16} />
          {error}
        </div>
      )}
    </form>
  );
}
