import React, {
  useEffect,
  useRef,
  useState,
  useId,
  createContext,
  useContext,
} from "react";
import { createPortal } from "react-dom";
import { SectionLink, ViewContext, useViewState } from "./view-state";
import {
  Palette,
  Maximize2,
  Info,
  AlertTriangle,
  Copy,
  Trash2,
  CalendarDays,
  ChevronLeft,
  ChevronDown,
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
  Upload,
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
  ListFilter,
  Smile,
  Mic,

  Table2,
  Presentation,
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
  Maximize2,
  Info,
  AlertTriangle,
  Copy,
  Trash2,
  CalendarDays,
  ChevronLeft,
  ChevronDown,
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
  Upload,
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
  ListFilter,
  Smile,
  Mic,

  Table2,
  Presentation,
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
    <button type="button" className={`btn ${variant} ${className}`} {...props}>
      {icon && <Icon name={icon} />} {children}
    </button>
  );
}
export function IconButton({ name, label, ...props }) {
  return (
    <button type="button" className="icon-button" aria-label={label} title={label} {...props}>
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
        {eyebrow && <div className="eyebrow">{eyebrow}</div>}
        <h1>{title}</h1>
        {subtitle && <p>{subtitle}</p>}
      </div>
      <div className="actions">{children}</div>
    </header>
  );
}
export function Section({ title, extra, children, className = "" }) {
  return (
    <section className={`section ${className}`} data-segment={typeof title === "string" ? title : undefined}>
      <div className="section-title">
        <h2>{title}<SectionLink title={title} /></h2>
        {extra}
      </div>
      {children}
    </section>
  );
}
export function Empty({
  title = "这里还没有记录",
  heading = "h2",
  text = "开始第一项工作，让协作从这里发生。",
  children,
}) {
  return (
    <div className="empty">
      <span className="empty-icon">
        <Icon name="Inbox" size={30} />
      </span>
      {React.createElement(heading, null, title)}
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
export function Tabs({ items, value, onChange, label = "内容视图", className = "", restore = true }) {
  const context=useContext(ViewContext);
  const [remembered,remember]=useViewState(`tab.${label}`,value);
  const ref=useRef(null), callback=useRef(onChange); callback.current=onChange;
  useEffect(()=>{
    if(!restore || ref.current?.closest("dialog"))return;
    if(items.some(([key])=>key===remembered) && remembered!==value)callback.current(remembered);
  },[context?.key,context?.params.get(`state.tab.${label}`),remembered,restore]);
  const select=key=>{if(!ref.current?.closest("dialog"))remember(key);onChange(key);};
  const active = items.some(([key]) => key === value) ? value : items[0]?.[0];
  return (
    <div ref={ref} className={`tabs ${className}`} role="tablist" aria-label={label}>
      {items.map(([key, text], index) => (
        <button
          type="button"
          key={key}
          role="tab"
          aria-selected={active === key}
          tabIndex={active === key ? 0 : -1}
          className={active === key ? "selected" : ""}
          onClick={() => select(key)}
          onKeyDown={(event) => {
            if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
            event.preventDefault();
            const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
              : (index + (event.key === "ArrowRight" ? 1 : -1) + items.length) % items.length;
            event.currentTarget.parentElement.querySelectorAll('[role="tab"]')[next]?.focus();
            select(items[next][0]);
          }}
        >
          {text}
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
  onClick,
  ...props
}) {
  return (
    <a
      href={`#${to}`}
      className={`panel-button ${className}`}
      {...props}
      onClick={event => {
        onClick?.(event);
        if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.currentTarget.target === "_blank") return;
        event.preventDefault();
        onView(to);
      }}
    >
      {children}
    </a>
  );
}
const ModalContext = createContext(null);
function draftSnapshot(value) {
  if (Array.isArray(value)) return value.map(draftSnapshot);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(key => [key, draftSnapshot(value[key])]));
  return value;
}
const formDrafts = new Map();
// Callers key the editor component with the same tenant/entity key. Only dirty
// drafts live in this tab's memory; cached values retain their original revision.
export function useRecoverableDraft(key, initialize, normalize = value => value) {
  const [original] = useState(() => {
    const cached = key && formDrafts.get(key);
    return cached ? { ...structuredClone(cached), recovered: true } : { baseline: initialize(), recovered: false };
  });
  const [value, setValue] = useState(() => structuredClone(original.recovered ? original.value : original.baseline));
  const draftValue = normalize(value);
  const draftBaseline = normalize(original.baseline);
  const dirty = JSON.stringify(draftSnapshot(draftValue)) !== JSON.stringify(draftSnapshot(draftBaseline));
  useEffect(() => {
    if (!key) return;
    if (dirty) formDrafts.set(key, { baseline: structuredClone(original.baseline), value: structuredClone(value) });
    else formDrafts.delete(key);
  }, [key, value, dirty, original]);
  return [value, setValue, {
    recovered: original.recovered,
    modalProps: { draftValue, draftBaseline, onDraftClear: () => { if (key) formDrafts.delete(key); } },
  }];
}
export function useDraftGuard({ draftValue, draftBaseline, onDraftClear }, onClose) {
  const confirm = useContext(ConfirmContext);
  const snapshot = JSON.stringify(draftSnapshot(draftValue));
  const initial = useRef(draftBaseline === undefined ? snapshot : JSON.stringify(draftSnapshot(draftBaseline)));
  const closing = useRef(false);
  const mounted = useRef(true);
  const dismiss = (done = onClose) => { onDraftClear?.(); done?.(); };
  const requestClose = async (done = onClose) => {
    if (closing.current) return;
    if (draftValue !== undefined && snapshot !== initial.current) {
      closing.current = true;
      const discard = await confirm("未保存的修改将被放弃。", { title: "放弃未保存的修改？", cancelLabel: "继续编辑", confirmLabel: "放弃修改" });
      closing.current = false;
      if (!discard) return;
      if (!mounted.current) { onDraftClear?.(); return; }
    }
    dismiss(done);
  };
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  return { requestClose, dismiss };
}
export function Modal({ title, children, headerActions, onClose, wide = false, returnFocusRef, className = "", draftValue, draftBaseline, onDraftClear }) {
  const ref = useRef();
  const { requestClose, dismiss } = useDraftGuard({ draftValue, draftBaseline, onDraftClear }, onClose);
  useEffect(() => {
    const last = document.activeElement;
    const dialog = ref.current;
    dialog.showModal();
    // Native keyboard scrolling must leave focused fields below the sticky
    // heading, including when its height changes with wrapping or resizing.
    const heading = dialog.querySelector('.modal-heading');
    const reserveHeading = () => { dialog.style.scrollPaddingTop = `${heading.getBoundingClientRect().height + 8}px`; };
    const headingObserver = new ResizeObserver(reserveHeading);
    headingObserver.observe(heading);
    reserveHeading();
    return () => {
      headingObserver.disconnect();
      const target = returnFocusRef?.current || last;
      const top = [...document.querySelectorAll("dialog[open]")].filter(item => item !== dialog).at(-1);
      if (target?.isConnected && (!top || top.contains(target))) target.focus();
    };
  }, []);
  return (
    <ModalContext.Provider value={{ requestClose, dismiss }}>
      <dialog
        ref={ref}
        aria-label={title}
        className={`${wide ? "wide" : ""} ${className}`}
        onCancel={(event) => { event.preventDefault(); event.stopPropagation(); requestClose(); }}
        onClick={(event) => { if (event.target === ref.current) requestClose(); }}
      >
        <div className="modal-heading">
          <h2>{title}</h2>
          <div className="modal-heading-actions">
            {headerActions}
            <IconButton name="X" label="关闭弹窗" onClick={() => requestClose()} />
          </div>
        </div>
        <div className="modal-body"><ViewContext.Provider value={null}>{typeof children === "function" ? children({ requestClose, dismiss }) : children}</ViewContext.Provider></div>
      </dialog>
    </ModalContext.Provider>
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
    autoComplete: type === "email" ? "email" : "off",
    ...(type === "email" ? { spellCheck: false } : {}),
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
      ) : type === "date" ? (
        <DatePicker {...shared} label={label} />
      ) : type === "textarea" ? (
        <textarea {...shared} rows={4} />
      ) : (
        <input {...shared} type={type} />
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
export function FormError({ error }) {
  const errorRef = useRef(null);
  useEffect(() => {
    if (error) {
      errorRef.current?.focus({ preventScroll: true });
      errorRef.current?.scrollIntoView({ block: "center" });
    }
  }, [error]);
  return error ? <p className="error" role="alert" ref={errorRef} tabIndex={-1}>{typeof error === "string" ? error : error.message}</p> : null;
}
export function ActionForm({ onSubmit, children, label = "保存", onCancel }) {
  const [error, setError] = useState("");
  const modal = useContext(ModalContext);
  return (
    <Form
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          setError("");
          await onSubmit({ dismiss: modal?.dismiss || ((done = onCancel) => done?.()) });
        } catch (err) {
          setError({ message: err.message });
        }
      }}
    >
      {children}
      <FormError error={error} />
      <div className="modal-footer">
        <Button type="button" onClick={() => modal ? modal.requestClose(onCancel) : onCancel?.()}>
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
  const current = useRef(null);
  const settle = (value) => {
    const request = current.current;
    current.current = null;
    setPending(null);
    // Restore the parent dialog before a confirmed action can unmount it.
    if (request) setTimeout(() => request.resolve(value), 0);
  };
  return (
    <ConfirmContext.Provider value={(message, options = {}) => {
      if (current.current) return Promise.resolve(false);
      return new Promise((resolve) => {
        current.current = { message, resolve, ...options };
        setPending(current.current);
      });
    }}>
      {children}
      {pending && (
        <Modal title={pending.title || "确认操作"} onClose={() => settle(false)}>
          <p>{pending.message}</p>
          <div className="modal-footer">
            <Button onClick={() => settle(false)}>{pending.cancelLabel || "取消"}</Button>
            <Button variant="primary" onClick={() => settle(true)}>{pending.confirmLabel || "确认继续"}</Button>
          </div>
        </Modal>
      )}
    </ConfirmContext.Provider>
  );
}
export const useConfirm = () => useContext(ConfirmContext);
const dateDay = 86400000;
function parseCalendarDate(value) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value || "") || value.startsWith("0000"))
    return null;
  const date = new Date(`${value}T00:00:00Z`);
  return Number.isFinite(date.getTime()) &&
    date.toISOString().slice(0, 10) === value
    ? date
    : null;
}
function calendarDate(date) {
  return date.toISOString().slice(0, 10);
}
function DatePicker({
  label,
  value,
  onChange,
  disabled,
  readOnly,
  min,
  max,
  step,
  ...props
}) {
  const input = useRef(null),
    trigger = useRef(null),
    panel = useRef(null),
    initialValue = useRef(value);
  const uid = useId();
  const today = new Date(),
    todayValue = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}-${String(today.getDate()).padStart(2, "0")}`;
  const [open, setOpen] = useState(false),
    [position, setPosition] = useState({}),
    [active, setActive] = useState(value || todayValue);
  const focusDay = useRef(false);
  const lower = parseCalendarDate(min),
    upper = parseCalendarDate(max);
  const valid = (candidate) => {
    if (!candidate) return "";
    const date = parseCalendarDate(candidate);
    if (!date) return "请输入有效日期，格式为 YYYY-MM-DD。";
    if (lower && date < lower) return `日期不能早于 ${min}。`;
    if (upper && date > upper) return `日期不能晚于 ${max}。`;
    const interval = Number(step || 1),
      base =
        lower ||
        parseCalendarDate(initialValue.current) ||
        new Date("1970-01-01T00:00:00Z");
    if (
      step !== "any" &&
      interval > 0 &&
      Math.abs(
        (date - base) / dateDay / interval -
          Math.round((date - base) / dateDay / interval),
      ) > 1e-8
    )
      return `请按 ${interval} 天的间隔选择日期。`;
    return "";
  };
  useEffect(() => {
    input.current?.setCustomValidity(valid(value));
  }, [value, min, max, step]);
  const close = (restore = true) => {
    setOpen(false);
    if (restore) trigger.current?.focus({ preventScroll: true });
  };
  const reposition = () => {
    const rect = trigger.current.getBoundingClientRect(),
      width = Math.min(338, innerWidth - 24),
      height = Math.min(432, innerHeight - 24);
    setPosition({
      left: Math.min(Math.max(12, rect.right - width), innerWidth - width - 12),
      top: Math.max(12, Math.min(rect.bottom + 6, innerHeight - height - 12)),
      width,
      maxHeight: height,
    });
  };
  const show = () => {
    if (disabled || readOnly) return;
    reposition();
    let date = parseCalendarDate(value) || parseCalendarDate(todayValue);
    if (lower && date < lower) date = lower;
    if (upper && date > upper) date = upper;
    setActive(calendarDate(date));
    focusDay.current = true;
    setOpen(true);
  };
  useEffect(() => {
    if (!open) return;
    if (focusDay.current) {
      panel.current
        ?.querySelector(`[data-date="${active}"]`)
        ?.focus({ preventScroll: true });
      focusDay.current = false;
    }
  }, [open, active]);
  useEffect(() => {
    if (!open) return;
    const outside = (event) => {
      if (
        !panel.current?.contains(event.target) &&
        !trigger.current?.contains(event.target)
      )
        close(false);
    };
    const scroll = (event) => {
      if (!panel.current?.contains(event.target)) reposition();
    };
    const resize = () => close();
    document.addEventListener("pointerdown", outside);
    window.addEventListener("resize", resize);
    window.addEventListener("scroll", scroll, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("resize", resize);
      window.removeEventListener("scroll", scroll, true);
    };
  }, [open]);
  useEffect(() => {
    if (disabled || readOnly) setOpen(false);
  }, [disabled, readOnly]);
  const move = (days, months = 0, focus = true) => {
    const date = parseCalendarDate(active) || parseCalendarDate(todayValue);
    if (months) {
      const day = date.getUTCDate();
      date.setUTCDate(1);
      date.setUTCMonth(date.getUTCMonth() + months);
      const last = new Date(date);
      last.setUTCMonth(last.getUTCMonth() + 1, 0);
      date.setUTCDate(Math.min(day, last.getUTCDate()));
    } else date.setUTCDate(date.getUTCDate() + days);
    if (date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999) return;
    focusDay.current = focus;
    setActive(calendarDate(date));
  };
  const choose = (candidate) => {
    if (valid(candidate)) return;
    input.current.setCustomValidity("");
    onChange({ target: { value: candidate } });
    close();
  };
  const cursor = parseCalendarDate(active) || parseCalendarDate(todayValue),
    first = new Date(cursor);
  first.setUTCDate(1);
  first.setUTCDate(1 - ((first.getUTCDay() + 6) % 7));
  const days = Array.from(
    { length: 42 },
    (_, index) => new Date(first.getTime() + index * dateDay),
  );
  return (
    <span className="custom-date">
      <input
        {...props}
        ref={input}
        type="text"
        value={value || ""}
        disabled={disabled}
        readOnly={readOnly}
        placeholder="YYYY-MM-DD"
        autoComplete="off"
        inputMode="text"
        min={min}
        max={max}
        step={step}
        onChange={(event) => {
          event.target.setCustomValidity(valid(event.target.value));
          onChange(event);
        }}
        onKeyDown={(event) => {
          props.onKeyDown?.(event);
          if (!event.defaultPrevented && event.key === "ArrowDown") {
            event.preventDefault();
            show();
          }
        }}
      />
      <button
        ref={trigger}
        className="date-trigger"
        type="button"
        aria-label={`选择${label}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? uid : undefined}
        disabled={disabled || readOnly}
        onClick={() => (open ? close() : show())}
      >
        <Icon name="CalendarDays" />
      </button>
      {open &&
        createPortal(
          <div
            ref={panel}
            id={uid}
            className="date-popover"
            role="dialog"
            aria-label={`${label}日历`}
            aria-describedby={`${uid}-keys`}
            style={position}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                close();
                return;
              }
              if (event.key === "Tab") {
                const controls = [
                  ...panel.current.querySelectorAll("button:not(:disabled)"),
                ].filter((node) => node.tabIndex >= 0);
                if (
                  (event.shiftKey && event.target === controls[0]) ||
                  (!event.shiftKey && event.target === controls.at(-1))
                )
                  close();
                return;
              }
              if (!event.target.dataset.date) return;
              const offsets = {
                ArrowLeft: -1,
                ArrowRight: 1,
                ArrowUp: -7,
                ArrowDown: 7,
              };
              if (event.key in offsets) {
                event.preventDefault();
                move(offsets[event.key]);
              } else if (["PageUp", "PageDown"].includes(event.key)) {
                event.preventDefault();
                move(
                  0,
                  (event.key === "PageUp" ? -1 : 1) * (event.shiftKey ? 12 : 1),
                );
              } else if (["Home", "End"].includes(event.key)) {
                event.preventDefault();
                const weekday = (cursor.getUTCDay() + 6) % 7;
                move(event.key === "Home" ? -weekday : 6 - weekday);
              }
            }}
          >
            <div className="date-heading">
              <button
                type="button"
                aria-label="上个月"
                onClick={() => move(0, -1, false)}
              >
                <Icon name="ChevronLeft" />
              </button>
              <strong aria-live="polite">
                {cursor.getUTCFullYear()} 年 {cursor.getUTCMonth() + 1} 月
              </strong>
              <button
                type="button"
                aria-label="下个月"
                onClick={() => move(0, 1, false)}
              >
                <Icon name="ChevronRight" />
              </button>
            </div>
            <div role="grid" aria-label="选择日期">
              <div className="date-week" role="row">
                {["一", "二", "三", "四", "五", "六", "日"].map((day) => (
                  <span key={day} role="columnheader">
                    {day}
                  </span>
                ))}
              </div>
              {Array.from({ length: 6 }, (_, week) => (
                <div className="date-week" role="row" key={week}>
                  {days.slice(week * 7, week * 7 + 7).map((date) => {
                    const key = calendarDate(date),
                      unavailable = !!valid(key);
                    return (
                      <div
                        role="gridcell"
                        key={key}
                        aria-selected={key === value}
                      >
                        <button
                          type="button"
                          data-date={key}
                          aria-label={key}
                          aria-current={key === todayValue ? "date" : undefined}
                          aria-disabled={unavailable || undefined}
                          tabIndex={key === active ? 0 : -1}
                          className={`${date.getUTCMonth() !== cursor.getUTCMonth() ? "outside-month" : ""} ${key === value ? "selected" : ""}`}
                          onClick={() => choose(key)}
                        >
                          {date.getUTCDate()}
                        </button>
                      </div>
                    );
                  })}
                </div>
              ))}
            </div>
            <div className="date-footer">
              <button
                type="button"
                onClick={() => choose(todayValue)}
                disabled={!!valid(todayValue)}
              >
                今天
              </button>
              <button type="button" onClick={() => choose("")}>
                清空
              </button>
              <button type="button" onClick={() => close()}>
                关闭日历
              </button>
            </div>
            <small id={`${uid}-keys`}>
              方向键选择日期 · PageUp / PageDown 切月 · Escape 关闭
            </small>
          </div>,
          trigger.current?.closest("dialog") || document.body,
        )}
    </span>
  );
}
export function Select({
  value,
  onChange,
  children,
  options,
  disabled = false,
  name,
  required = false,
  searchable = false,
  indicator = "ChevronsUpDown",
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
  const [query, setQuery] = useState("");
  const filteredItems = searchable ? items.filter(i => String(i.searchText || i.label).toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())) : items;
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
    setQuery("");
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
      list.current?.querySelector("[role=option][aria-selected=true]:not(:disabled)") ||
      list.current?.querySelector("[role=option]:not(:disabled)");
    (searchable ? list.current?.querySelector("input[type=search]") : selected)?.focus({ preventScroll: true });
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
        aria-controls={open ? searchable ? `${uid}-options` : uid : undefined}
        aria-haspopup="listbox"
        aria-required={required || undefined}
        disabled={disabled}
        onClick={() => (open ? close() : show())}
        onKeyDown={(e) => {
          if (e.nativeEvent?.isComposing || e.nativeEvent?.keyCode === 229) return;
          if (["ArrowDown", "ArrowUp", "Enter", " "].includes(e.key)) {
            e.preventDefault();
            show();
          }
        }}
      >
        <span>{current?.label || "请选择"}</span>
        <Icon name={indicator} size={15} />
      </button>
      {name && <input type="hidden" name={name} value={value || ""} />}
      {open &&
        createPortal(
          <div
            ref={list}
            className="select-popover"
            id={uid}
            role={searchable ? undefined : "listbox"}
            aria-label={searchable ? undefined : props["aria-label"] || "可选项"}
            style={position}
            onKeyDown={(e) => {
              if (e.nativeEvent?.isComposing || e.nativeEvent?.keyCode === 229) return;
              const all = [
                  ...list.current.querySelectorAll(
                    "[role=option]:not(:disabled)",
                  ),
                ],
                index = all.indexOf(document.activeElement);
              if (searchable && e.target.tagName === "INPUT" && e.key === "Enter") {
                e.preventDefault();
                e.stopPropagation();
                all.find(option => option.dataset.value)?.click();
                return;
              }
              if (["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key) && !(searchable && e.target.tagName === "INPUT" && ["Home", "End"].includes(e.key))) {
                e.preventDefault();
                all[
                  e.key === "Home"
                    ? 0
                    : e.key === "End" || index === -1 && e.key === "ArrowUp"
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
                close();
              } else if (e.key.length === 1 && !(searchable && e.target.tagName === "INPUT")) {
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
            {searchable && <div className="select-search"><Icon name="Search" size={16} /><input type="search" aria-label={`搜索${props["aria-label"] || "选项"}`} name={`${name || "select"}-search`} autoComplete="off" placeholder="搜索姓名、身份或岗位…" value={query} onChange={event => setQuery(event.target.value)} /></div>}
            <div role={searchable ? "listbox" : undefined} id={searchable ? `${uid}-options` : undefined} aria-label={searchable ? props["aria-label"] || "可选项" : undefined}>
            {filteredItems.map((i) => (
              <button
                type="button"
                role="option"
                data-value={String(i.value)}
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
            </div>
            {searchable && !filteredItems.length && <p className="select-search-empty" role="status">没有匹配的联系人</p>}
          </div>,
          trigger.current?.closest("dialog") || document.body,
        )}
    </span>
  );
}
function validityText(input) {
  if (input.validity.customError) return input.validationMessage;
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
