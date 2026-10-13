import React, { useState } from "react";
import { useStore, scheduleWork } from "./store";
import {
  Button,
  Icon,
  Select,
  Modal,
  ActionForm,
  Field,
  Link,
  Badge,
  Empty,
} from "./ui";
const TODAY = "2026-10-13";
const date = (s) => new Date(`${s}T12:00:00Z`);
const key = (d) => d.toISOString().slice(0, 10);
const shift = (d, n) => {
  const copy = new Date(d);
  copy.setUTCDate(copy.getUTCDate() + n);
  return copy;
};
export function WorkCalendar({ work, toast, projectId }) {
  const s = useStore(),
    [mode, setMode] = useState("month"),
    [anchor, setAnchor] = useState(TODAY),
    [person, setPerson] = useState("all"),
    [selected, setSelected] = useState(null),
    [start, setStart] = useState(""),
    [end, setEnd] = useState(""),
    [reason, setReason] = useState("");
  const people = [
    ...new Set(work.flatMap((w) => [w.owner, w.agent]).filter(Boolean)),
  ];
  const items = work.filter(
    (w) => person === "all" || w.owner === person || w.agent === person,
  );
  const center = date(anchor),
    first =
      mode === "month"
        ? new Date(
            Date.UTC(center.getUTCFullYear(), center.getUTCMonth(), 1, 12),
          )
        : center;
  const begin = shift(first, -((first.getUTCDay() + 6) % 7));
  const days = Array.from({ length: mode === "month" ? 42 : 7 }, (_, i) =>
    shift(begin, i),
  );
  const active = items.filter((w) => w.plannedStart && w.plannedEnd),
    unscheduled = items.filter((w) => !w.plannedStart || !w.plannedEnd);
  const milestones = (d) =>
    s.projects.filter(
      (p) =>
        (!projectId || p.id === projectId) &&
        (s.project === "all" || p.id === s.project) &&
        (person === "all" || p.owner === person) &&
        p.deadline === key(d),
    );
  const milestone = (p) => (
    <div className="calendar-milestone" key={`milestone-${p.id}`}>
      <Icon name="Target" size={12} />
      <span>项目截止 · {p.name}</span>
    </div>
  );
  const onDay = (d) =>
    active.filter((w) => w.plannedStart <= key(d) && w.plannedEnd >= key(d));
  const open = (w) => {
    setSelected({ ...w });
    setStart(w.plannedStart || "");
    setEnd(w.plannedEnd || "");
    setReason("");
  };
  const move = (delta) =>
    setAnchor(
      mode === "month"
        ? key(
            new Date(
              Date.UTC(
                center.getUTCFullYear(),
                center.getUTCMonth() + delta,
                1,
                12,
              ),
            ),
          )
        : key(shift(center, delta * 7)),
    );
  const tile = (w) => (
    <button
      className={`calendar-task ${w.type === "视频" ? "teal" : ""}`}
      key={w.id}
      onClick={() => open(w)}
    >
      <span>{w.title}</span>
      <small>
        {w.owner} · {w.agent || "Human 执行"}
      </small>
    </button>
  );
  return (
    <>
      <div className="calendar-toolbar">
        <div className="calendar-period">
          <Button
            aria-label="上一周期"
            onClick={() => move(-1)}
            icon="ChevronLeft"
          />
          <h2>
            {center.getUTCFullYear()} 年 {center.getUTCMonth() + 1} 月
            {mode === "week"
              ? ` · ${key(begin).slice(5)}—${key(days.at(-1)).slice(5)}`
              : ""}
          </h2>
          <Button
            aria-label="下一周期"
            onClick={() => move(1)}
            icon="ChevronRight"
          />
          <Button onClick={() => setAnchor(TODAY)}>今天</Button>
        </div>
        <Select
          aria-label="排期成员"
          value={person}
          onChange={(e) => setPerson(e.target.value)}
          options={[
            { value: "all", label: "所有 Human / Agent" },
            ...people.map((p) => ({
              value: p,
              label: p === "林予" ? "我的排期 · 林予" : p,
            })),
          ]}
        />
        <div className="segmented">
          <Button
            className={mode === "month" ? "selected" : ""}
            onClick={() => setMode("month")}
          >
            月
          </Button>
          <Button
            className={mode === "week" ? "selected" : ""}
            onClick={() => setMode("week")}
          >
            周
          </Button>
        </div>
      </div>
      <div className="calendar-legend">
        <span>
          <i className="tiny-dot violet" />
          计划周期 · 点击任务调整
        </span>
        <span>当前项目范围内 · 按日排期</span>
        <small>演示当前日期：{TODAY}</small>
      </div>
      <div
        className="calendar-grid"
        role="region"
        aria-label={`${mode === "month" ? "月" : "周"}排期日历`}
      >
        {["周一", "周二", "周三", "周四", "周五", "周六", "周日"].map((d) => (
          <div className="calendar-weekday" key={d}>
            {d}
          </div>
        ))}
        {days.map((d) => (
          <section
            key={key(d)}
            className={`calendar-day ${key(d) === TODAY ? "today" : ""} ${d.getUTCMonth() !== center.getUTCMonth() ? "outside" : ""}`}
            aria-label={key(d)}
          >
            <header>
              <time dateTime={key(d)}>{d.getUTCDate()}</time>
              {onDay(d).length > 1 && <small>{onDay(d).length} 项并行</small>}
            </header>
            {milestones(d).map(milestone)}
            {onDay(d).map(tile)}
          </section>
        ))}
      </div>
      <div className="calendar-agenda">
        <p className="muted">
          按日期展示当前{mode === "month" ? "月" : "周"}的排期
        </p>
        {days
          .filter(
            (d) => mode === "week" || d.getUTCMonth() === center.getUTCMonth(),
          )
          .filter((d) => onDay(d).length || milestones(d).length)
          .map((d) => (
            <section key={key(d)}>
              <h3>
                {key(d)} {key(d) === TODAY ? "· 今天" : ""}
              </h3>
              {milestones(d).map(milestone)}
              {onDay(d).map(tile)}
            </section>
          ))}
        {!days.some((d) => onDay(d).length) && (
          <Empty title="这一周期没有排期" />
        )}
      </div>
      <section className="unscheduled">
        <div className="section-title">
          <h2>未排期</h2>
          <Badge>{String(unscheduled.length)}</Badge>
        </div>
        {unscheduled.length ? (
          <div className="unscheduled-list">
            {unscheduled.map((w) => (
              <button key={w.id} onClick={() => open(w)}>
                <Icon name="CalendarDays" />
                <span>
                  <b>{w.title}</b>
                  <small>{w.owner} 负责</small>
                </span>
                <Icon name="Plus" size={16} />
              </button>
            ))}
          </div>
        ) : (
          <p className="muted">当前筛选范围内的工作都已安排时间。</p>
        )}
      </section>
      {selected && (
        <Modal title="调整任务排期" onClose={() => setSelected(null)}>
          <p>
            <b>{selected.title}</b>
          </p>
          <p className="muted">
            {selected.owner} 负责 · {selected.agent || "Human"} 执行
          </p>
          <ActionForm
            label="保存排期"
            onCancel={() => setSelected(null)}
            onSubmit={() => {
              scheduleWork(
                selected.id,
                start,
                end,
                reason,
                selected.scheduleRevision || 0,
              );
              setSelected(null);
              toast("排期已更新，项目与个人日历同步显示。");
            }}
          >
            <div className="form-grid">
              <Field
                label="开始日期"
                value={start}
                onChange={setStart}
                placeholder="YYYY-MM-DD"
                pattern="[0-9]{4}-[0-9]{2}-[0-9]{2}"
              />
              <Field
                label="结束日期"
                value={end}
                onChange={setEnd}
                placeholder="YYYY-MM-DD"
                pattern="[0-9]{4}-[0-9]{2}-[0-9]{2}"
              />
            </div>
            <Field
              label="调整说明"
              type="textarea"
              value={reason}
              onChange={setReason}
              required
            />
            <p className="muted">
              清空两个日期可移回未排期。并行提示仅表示日期重叠，不代表工时超载。
            </p>
          </ActionForm>
          <Link
            className="text-link"
            to={`/p/${selected.project}/work-items/${selected.id}`}
          >
            查看工作详情 <Icon name="ArrowUpRight" size={14} />
          </Link>
        </Modal>
      )}
    </>
  );
}
