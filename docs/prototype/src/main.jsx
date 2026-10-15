import { ProjectPlanningPage } from "./project-planning.jsx";
import { PerformancePage } from "./pages-performance";
import { ViewContext, CurrentViewLink, Breadcrumbs, captureScroll, restoreScroll, viewKey, savedView, saveViewSnapshot, syncViewURL } from "./view-state";
import { visibleGoals } from "./goals";
import { channelReadProblem } from "./channels";
import { activeProjects, projectIsArchived, recordProjectId } from "./project-lifecycle";
import { PlansPage } from "./pages-plans";
import React, { useEffect, useLayoutEffect, useState, useRef } from "react";
import { createRoot } from "react-dom/client";
import { catalog, themeNames } from "./data";
import {
  useStore,
  visibleKnowledge,
  update,
  visibleWork,
  sharedVersions,
  sharedArtifactPath,
  switchTenant,
  createTenant,
  joinTenant,
  declineTenantInvitation,
} from "./store";
import {
  Icon,
  Select,
  ConfirmProvider,
  Form,
  IconButton,
  Avatar,
  Button,
  Link,
  Modal,
  Empty,
  Notice,
  PageHead,
  PanelButton,
  Field,
  ActionForm,
} from "./ui";
import {
  Dashboard,
  ProjectList,
  WorkList,
  Project,
  WorkDetail, DefectList, LegacyDefectRoute,
  NewWork,
  workPath,
} from "./pages-work";
import { Flow } from "./pages-flow";
import { WorkflowList, TenantWorkflows } from "./pages-workflows";
import { Chat, Artifact } from "./pages-collab";
import { EffortPage } from "./pages-effort";
import {
  ResourcePage,
  Team,
  Agents,
  Machines,
  Cost,
  Audit,
  Settings,
} from "./pages-admin";
import "./styles.css";
const navGroups = {
  home: [
    ["/overview", "LayoutDashboard", "总览"],
    ["/projects", "Folders", "全部项目"],
    ["/work", "ListTodo", "我的工作"],
    ["/plans/me", "CalendarDays", "我的周期计划"],
    ["/approvals", "Stamp", "待审批"],
    ["/reports", "NotebookPen", "周期报告"],
  ],
  chat: [
    ["/chat?channel_id=team", "MessagesSquare", "协作总频道"],
    ["/chat?channel_id=launch", "Hash", "新品发布"],
    ["/chat?channel_id=external", "Globe2", "客户共创"],
    ["/chat?channel_id=private", "LockKeyhole", "私人会话"],
  ],
  collab: [
    ["/requirements", "FileText", "需求"],
    ["/goals", "Target", "目标"],
    ["/iterations", "CalendarDays", "迭代"],
    ["/collaboration", "ListTodo", "任务"],
    ["/p/launch/work-items", "ListTodo", "任务"],
    ["/workflows", "Workflow", "工作流"],
    ["/p/launch/flow", "Workflow", "工作流"],
    ["/repositories", "GitBranch", "源码连接"],
    ["/testplans", "ClipboardCheck", "测试计划"],
    ["/tests", "FlaskConical", "测试用例"],
    ["/defects", "Bug", "缺陷"],
    ["/materials", "Folders", "文件"],
  ],
  team: [
    ["/members", "UsersRound", "组织与成员"],
    ["/agents", "Bot", "Agent 名册"],
    ["/machines", "Server", "执行节点"],
    ["/tools", "Wrench", "工具与 Skills"],
    ["/templates", "Workflow", "工作流模板"],
  ],
  governance: [
    ["/knowledge", "Library", "知识库"],
    ["/performance", "ChartNoAxesCombined", "绩效考核"],
    ["/cost", "Wallet", "用量与预算"],
    ["/policies", "ShieldCheck", "权限与策略"],
    ["/models", "SlidersHorizontal", "模型配置"],
    ["/schedules", "CalendarClock", "定时与触发"],
    ["/health", "Activity", "健康与告警"],
    ["/connectors", "Plug", "外部连接"],
    ["/governance/audit", "Fingerprint", "决定与审计"],
    ["/settings", "Settings2", "个人设置"],
  ],
};
const primary = [
  ["home", "工作台", "/overview", "LayoutDashboard"],
  ["chat", "会话", "/chat?channel_id=launch", "MessagesSquare"],
  ["collab", "协作", "/collaboration", "Layers"],
  ["team", "团队", "/members", "UsersRound"],
  ["governance", "治理", "/cost", "ShieldCheck"],
];
function getLocation() {
  const hash = location.hash.slice(1) || "/overview";
  const [path, query = ""] = hash.split("?");
  return { path, params: new URLSearchParams(query), full: hash };
}
function groupFor(path) {
  if (path === "/chat") return "chat";
  if (path === "/projects" || /^\/p\/[^/]+$/.test(path)) return "home";
  if (
    path.startsWith("/p/") ||
    [
      "/collaboration",
      "/workflows",
      "/requirements",
      "/iterations",
      "/goals",
      "/materials",
      "/deliveries",
      "/repositories",
      "/tests",
      "/testplans",
      "/testreports",
      "/defects",
    ].some((v) => path === v || path.startsWith(v + "/"))
  )
    return "collab";
  if (
    [
      "/members",
      "/invitations",
      "/agents",
      "/machines",
      "/tools",
      "/templates",
    ].some((v) => path === v || path.startsWith(v + "/"))
  )
    return "team";
  if (
    [
      "/performance",
      "/knowledge",
      "/cost",
      "/policies",
      "/models",
      "/schedules",
      "/health",
      "/connectors",
      "/governance",
      "/settings",
    ].some((v) => path === v || path.startsWith(v + "/"))
  )
    return "governance";
  return "home";
}
function App() {
  const s = useStore();
  const [route, setRoute] = useState(() => {
    const saved=history.state?.prototypeReturn;
    if(saved?.url===location.href && saved.tenantId===s.activeTenant && saved.role===s.role) {
      const [path,query=""]=saved.full.split("?");return {path,params:new URLSearchParams(query),full:saved.full};
    }
    return getLocation();
  });
  const [locationLink,setLocationLink]=useState(null);
  const currentKey=viewKey(route.full,s.activeTenant,s.role,s.project);
  const rememberCurrent=()=>saveViewSnapshot(currentKey,{scrolls:captureScroll()});
  const coordinates=useRef(null);
  coordinates.current={key:currentKey,full:route.full,params:route.params,tenantId:s.activeTenant,role:s.role,project:s.project};
  const [coordinateError,setCoordinateError]=useState("");
  const restoring=useRef(false);
  useEffect(()=>{
    let timer;
    const save=event=>{
      if(restoring.current || event.target?.closest?.("dialog") || (event.type==="scroll" && event.target!==document && !document.querySelector("main")?.contains(event.target)))return;
      clearTimeout(timer);timer=setTimeout(()=>{
        const context=coordinates.current;saveViewSnapshot(context.key,{scrolls:captureScroll()});syncViewURL(context);
      },160);
    };
    const leave=()=>{const context=coordinates.current;saveViewSnapshot(context.key,{scrolls:captureScroll()});syncViewURL(context);};
    document.addEventListener("scroll",save,true);window.addEventListener("pagehide",leave);
    return ()=>{clearTimeout(timer);document.removeEventListener("scroll",save,true);window.removeEventListener("pagehide",leave);};
  },[]);
  useLayoutEffect(()=>{
    const tenantId=route.params.get("tenant");
    const projectPage=/^\/p\/([^/]+)(?:\/(?:flow|work-items)\/?|\/?)$/.exec(route.path)?.[1];
    const scope=route.path === "/projects" ? "all" : projectPage || route.params.get("scope");
    try {
      if(tenantId && tenantId!==s.activeTenant) {switchTenant(tenantId);return;}
      if(scope && scope!==s.project) {
        if(scope!=="all" && !s.projects.some(p=>p.id===scope))throw Error("位置链接的项目不存在于当前租户。");
        update(x=>{x.project=scope;});
      }
      if (scope && route.params.get("scope") !== scope) {
        const next=new URLSearchParams(route.params);next.set("scope",scope);
        const full=`${route.path}?${next}`;
        history.replaceState(history.state,"",`#${full}`);
        setRoute({path:route.path,params:next,full});
      }
      setCoordinateError("");
    } catch(error) {setCoordinateError(error.message);}
  },[route.full,s.activeTenant]);
  useLayoutEffect(()=>{
    restoring.current=true;let second;
    const frame=requestAnimationFrame(()=>{second=requestAnimationFrame(()=>{
      const segment=route.params.get("segment");
      let positions;
      try {positions=route.params.has("scroll")?JSON.parse(route.params.get("scroll")):null;}catch {setCoordinateError("位置链接的滚动坐标无效，未应用。");}
      if(positions && typeof positions==="object")restoreScroll(positions);
      if(segment) {
        const target=[...document.querySelectorAll("main [data-segment]")].find(el=>el.dataset.segment===segment);
        if(target) {target.scrollIntoView({block:"start"});target.tabIndex=-1;target.focus({preventScroll:true});}
        else setCoordinateError("此段落不存在于当前视图，未跳转到其他内容。");
      }
      restoring.current=false;
    });});
    return ()=>{cancelAnimationFrame(frame);cancelAnimationFrame(second);restoring.current=false;};
  },[route.full,s.activeTenant,s.project]);
  const pendingReturn = useRef(null);
  useLayoutEffect(() => {
    const entry = pendingReturn.current;
    if (!entry) return;
    pendingReturn.current = null;
    restoreScroll(entry.scrolls);
    window.scrollTo(entry.windowX || 0,entry.windowY || 0);
  }, [route.full]);
  const [newWork, setNewWork] = useState(false);
  const [search, setSearch] = useState(false);
  const [drawer, setDrawer] = useState(false);
  const [notice, setNotice] = useState(null);
  const [group, setGroup] = useState(() => groupFor(getLocation().path));
  const [themePicker, setThemePicker] = useState(false);
  const [tenantMenu, setTenantMenu] = useState(false);
  const [tenantAction, setTenantAction] = useState(null);
  const tenantTrigger = useRef(null);
  const [tenantInput, setTenantInput] = useState("");
  const [tenantFilter, setTenantFilter] = useState("");
  useEffect(() => {
    if (!drawer) return;
    const sidebar = document.querySelector(".sidebar");
    const trigger = document.querySelector(".mobile-menu");
    const focusable = () => [...sidebar.querySelectorAll('button:not([disabled]), a[href], input:not([disabled]), [tabindex="0"]')]
      .filter((el) => el.getClientRects().length);
    focusable()[0]?.focus();
    const key = (event) => {
      if (event.defaultPrevented || document.querySelector('dialog[open]')) return;
      if (event.key === "Escape") {
        event.preventDefault();
        setDrawer(false);
      } else if (event.key === "Tab") {
        const items = focusable(), first = items[0], last = items.at(-1);
        if (event.shiftKey && (document.activeElement === first || !sidebar.contains(document.activeElement))) {
          event.preventDefault(); last?.focus();
        } else if (!event.shiftKey && (document.activeElement === last || !sidebar.contains(document.activeElement))) {
          event.preventDefault(); first?.focus();
        }
      }
    };
    const compact = matchMedia("(max-width: 1023px)");
    const resize = () => { if (!compact.matches) setDrawer(false); };
    compact.addEventListener("change", resize);
    document.addEventListener("keydown", key);
    return () => {
      compact.removeEventListener("change", resize);
      document.removeEventListener("keydown", key);
      if (trigger?.getClientRects().length) trigger.focus();
    };
  }, [drawer]);
  useEffect(() => {
    if (!tenantMenu) return;
    const key = (event) => {
      if (event.key === "Escape" && !event.defaultPrevented) {
        event.preventDefault();
        setTenantMenu(false);
        document.querySelector(".tenant-trigger")?.focus();
      }
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [tenantMenu]);
  const onView = (full) => {
    rememberCurrent();
    syncViewURL(coordinates.current);
    if(location.hash.split("?")[0] !== `#${full.split("?")[0]}` || location.hash !== `#${full}`)history.pushState(null,"",`#${full}`);
    const [path, query = ""] = full.split("?");
    pendingReturn.current=savedView(viewKey(full,s.activeTenant,s.role,s.project));
    setRoute({ path, params: new URLSearchParams(query), full });
    setGroup(groupFor(path));
    setDrawer(false);
  };
  const areaPath = (key, fallback) =>
    key === "collab"
      ? s.project === "all"
        ? "/collaboration"
        : `/p/${s.project}/work-items`
      : fallback;
  const areaItems = navGroups[group]
    .flatMap(item => item[0] === "/chat?channel_id=launch"
      ? activeProjects(s).filter(project => project.id !== "private" && (s.project === "all" || s.project === project.id))
        .map(project => [`/chat?channel_id=${encodeURIComponent(project.id)}`, "Hash", project.name])
      : [item])
    .filter(([path]) => {
      if (path === "/performance") return s.role === "owner";
      if (group === "home" && path === "/projects") return s.project === "all";
      if (group === "collab")
        return s.project === "all"
          ? !path.startsWith("/p/")
          : path !== "/collaboration" && path!=="/workflows";
      return true;
    })
    .map(([path, icon, label]) => [
      path.replace("/p/launch", `/p/${s.project}`),
      icon,
      path === "/overview"
        ? s.project === "all"
          ? "租户总览"
          : "项目概览"
        : label,
    ]);
  const workDetail = /^\/p\/[^/]+\/(work-items|artifacts)(\/|$)/.test(route.path);
  const detailIsDefect=workDetail && s.work.find(work=>work.id===route.path.split("/")[4])?.workKind==="BUG";
  const activeAreaPath = detailIsDefect ? "/defects" : workDetail
    ? s.project === "all" ? "/collaboration" : `/p/${s.project}/work-items`
    : route.path.startsWith("/testreports") ? "/testplans" : route.path.startsWith("/deliveries") ? s.project==="all"?"/workflows":`/p/${s.project}/flow` : route.path === "/collaboration" && s.project!=="all" ? `/p/${s.project}/work-items` : route.path.startsWith("/invitations") ? "/members" : route.path.startsWith("/worklogs") ? "/work" : /^\/p\/[^/]+\/?$/.test(route.path) ? "/overview" : route.path;
  const areaIsActive = (to) => {
    const [path, query] = to.split("?");
    if (detailIsDefect) return path === "/defects";
    if (query) return route.path === path && [...new URLSearchParams(query)].every(([key, value]) => route.params.get(key) === value);
    return activeAreaPath === path || route.path === path || route.path.startsWith(`${path}/`);
  };
  const tenant = s.tenants.find((t) => t.id === s.activeTenant);
  const pendingTenantInvitations = s.tenantInvitations.filter((item) => item.recipient === "林予" && item.status === "待接受");
  const openArea = (key, to) => {
    setGroup(key);
    onView(to);
    setDrawer(false);
  };
  const closeTenantAction = () => {
    setTenantAction(null);
    setTenantInput("");
  };
  const finishTenant = () => {
    setTenantMenu(false);
    closeTenantAction();
    setTenantInput("");
    setSearch(false);
    setNewWork(false);
    openArea("home", "/overview");
    location.hash = "/overview";
  };
  const toast = (text, error = false) => {
    setNotice({ text, error });
  };
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => setNotice(null), 5500);
    return () => clearTimeout(timer);
  }, [notice]);
  useEffect(() => {
    const remember = event => {
      const anchor=event.target.closest?.('a[href^="#/"]');
      if (!anchor || anchor.target === "_blank" || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const scrolls=captureScroll();
      rememberCurrent();
      history.replaceState({...history.state,prototypeReturn:{url:location.href,full:getLocation().full,tenantId:s.activeTenant,role:s.role,scrolls,windowX:scrollX,windowY:scrollY}}, "");
    };
    const change = () => {
      const saved=history.state?.prototypeReturn;
      const returning=saved?.url===location.href && saved.tenantId===s.activeTenant && saved.role===s.role;
      const [path,query=""]=returning ? saved.full.split("?") : [null];
      const next=returning ? {path,params:new URLSearchParams(query),full:saved.full} : getLocation();
      pendingReturn.current=returning ? saved : null;
      setRoute(next);
      setGroup(groupFor(next.path));
      setDrawer(false);
      if (!returning) document.querySelector("main")?.scrollTo(0, 0);
    };
    document.addEventListener("click",remember,true);
    window.addEventListener("hashchange", change);
    window.addEventListener("popstate", change);
    const key = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setSearch((v) => !v);
      }
    };
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("hashchange", change);
      window.removeEventListener("popstate", change);
      document.removeEventListener("click",remember,true);
      window.removeEventListener("keydown", key);
    };
  }, [route.full,s.activeTenant,s.role]);
  useEffect(() => {
    document.documentElement.dataset.theme = s.theme;
    document.documentElement.classList.toggle("dark", s.dark);
    document.querySelector("meta[name=theme-color]").content =
      getComputedStyle(document.documentElement).getPropertyValue("--bg").trim();
  }, [s.theme, s.dark]);
  const shared = {
    toast,
    onView,
    params: route.params,
    viewPath: route.path,
    openNew: () => setNewWork(true),
  };
  let content;
  const parts = route.path.split("/").filter(Boolean);
  const breadcrumbProject=s.projects.find(project=>project.id===(parts[0]==="p" ? parts[1] : s.project));
  const breadcrumbMenu=areaItems.find(([path])=>areaIsActive(path));
  const breadcrumbObject=parts[0]==="defects" ? visibleWork({...s,project:"all"},{includeArchived:true}).find(work=>work.id===parts[1]&&work.workKind==="BUG") : parts[0]==="p" ? visibleWork({...s,project:"all"},{includeArchived:true}).find(work=>work.id===parts[3]) : parts[0]==="agents" ? s.agents.find(agent=>agent.id===parts[1]) : parts[0]==="machines" ? s.nodes.find(node=>node.id===parts[1]) : ["knowledge","materials"].includes(parts[0]) ? visibleKnowledge(s).find(record=>record.id===parts[1]) : parts[0]==="goals" ? visibleGoals(s).find(record=>record.id===parts[1]) : catalog[parts[0]] && s.role!=="external" ? s.resources[parts[0]]?.find(record=>record.id===parts[1]) : null;
  const breadcrumbBase=detailIsDefect ? "/defects" : parts[0]==="p" ? parts[3] ? `/p/${parts[1]}/work-items` : route.path : parts[0]==="invitations" ? "/members" : `/${parts[0]}`;
  const breadcrumbItems=[
    {label:tenant?.name || "当前租户",to:"/overview?scope=all"},
    breadcrumbProject && {label:breadcrumbProject.name,to:`/p/${breadcrumbProject.id}?scope=${encodeURIComponent(breadcrumbProject.id)}`},
    {label:breadcrumbMenu?.[2] || (parts[0]==="performance"?"绩效考核":route.path==="/chat"?"会话":"工作台"),to:breadcrumbBase},
    breadcrumbObject && {label:breadcrumbObject.title || breadcrumbObject.name || breadcrumbObject.id,to:route.path},
    route.params.get("tab") && {label:({brief:"工作简报",plan:"计划与契约",results:"成果与版本",review:"检查与接受",activity:"活动记录",analysis:"投入与运行分析",status:"状态与维护",files:"工作区文件",terminal:"实时终端",preview:"预览与隧道",periods:"绩效周期",policies:"考核方案",human:"Human 工作量",agent:"Agent 运行指标"})[route.params.get("tab")] || route.params.get("tab"),to:route.full},
    route.params.get("segment") && {label:route.params.get("segment"),to:route.full},
  ];
  if (route.path === "/overview")
    content =
      route.params.get("view") === "attention" ? (
        <WorkList {...shared} />
      ) : s.project === "all" ? (
        <Dashboard {...shared} />
      ) : (
        <Project {...shared} projectId={s.project} />
      );
  else if (route.path === "/work" || route.path === "/collaboration")
    content = <WorkList {...shared} />;
  else if (route.path === "/chat") content = <Chat {...shared} />;
  else if (route.path === "/projects")
    content =
      s.project === "all" ? (
        <ProjectList {...shared} />
      ) : (
        <Project {...shared} projectId={s.project} />
      );
  else if (route.path === "/workflows") content = s.project==="all"?<TenantWorkflows {...shared}/>:<WorkflowList {...shared} projectId={s.project}/>;
  else if (parts[0] === "p") {
    const projectId = parts[1];
    if (parts[2] === "work-items")
      content = parts[3] ? (
        <WorkDetail key={parts[3]} {...shared} workId={parts[3]} />
      ) : (
        <WorkList {...shared} projectId={projectId} />
      );
    else if (parts[2] === "flow")
      content = route.params.get("view") === "runs" || (!route.params.has("workflow_id") && !route.params.has("version"))
        ? <WorkflowList key={projectId} {...shared} projectId={projectId} />
        : <Flow key={`${projectId}:${route.params.get("workflow_id") || "default"}`} {...shared} projectId={projectId} />;
    else if (parts[2] === "artifacts")
      content = <Artifact key={parts[3]} {...shared} workId={parts[3]} />;
    else content = <Project {...shared} projectId={projectId} />;
  } else if (route.path === "/members" || parts[0] === "invitations") content = <Team key={route.path} {...shared} initialTab={parts[0] === "invitations" ? "invitations" : "members"} invitationId={parts[0] === "invitations" ? parts[1] : undefined} />;
  else if (["requirements","iterations"].includes(parts[0])) content = <ProjectPlanningPage {...shared} kind={parts[0]} recordId={parts[1]}/>;
  else if (parts[0] === "agents")
    content = <Agents {...shared} agentId={parts[1]} />;
  else if (parts[0] === "machines")
    content = <Machines {...shared} nodeId={parts[1]} />;
  else if (parts[0] === "performance") content = s.role === "owner" ? <PerformancePage {...shared} /> : <Empty title="仅租户 owner 可管理绩效" text="考核报告保留主体与范围权限。" />;
  else if (route.path === "/cost") content = <Cost {...shared} />;
  else if (route.path === "/settings") content = <Settings {...shared} />;
  else if (route.path === "/governance/audit") content = <Audit />;
  else if (parts[0] === "plans") content = <PlansPage key={s.activeTenant + (parts[1] || "me")} {...shared} planId={parts[1] === "me" ? null : parts[1]} />;
  else if (parts[0] === "worklogs") content = <EffortPage key={parts[1] || "list"} {...shared} recordId={parts[1]} />;
  else if (parts[0] === "defects") {
    const defect = s.work.find(work => work.id === parts[1] && work.workKind === "BUG");
    content = parts[1] ? (defect ? <LegacyDefectRoute key={defect.id} {...shared} work={defect} /> : <Empty title="缺陷待修复迁移或不存在"><Link to="/defects">查看缺陷工作与迁移记录</Link></Empty>) : <DefectList {...shared} />;
  }
  else if (catalog[parts[0]] || parts[0] === "materials")
    content = (
      <ResourcePage
        key={parts[0] + parts[1]}
        {...shared}
        kind={parts[0] === "materials" ? "knowledge" : parts[0]}
        collection={parts[0] === "materials" ? "MATERIAL" : undefined}
        recordId={parts[1]}
      />
    );
  else
    content = (
      <Empty title="这个地址不存在" text="请返回工作台或通过搜索找到正确对象。">
        <Link className="btn primary" to="/overview">
          返回工作台
        </Link>
      </Empty>
    );
  if (s.role === "external") {
    const sharedArtifact =
      parts[0] === "p" &&
      parts[2] === "artifacts" &&
      s.work.some(
        (w) =>
          w.id === parts[3] &&
          w.project === parts[1] &&
          sharedVersions(s, w.id).length > 0,
      );
    if (route.path === "/overview")
      content = (
        <>
          <PageHead
            title="你的共享工作台"
            subtitle="只看与你相关的成果、反馈和交付。"
          />
          <div className="link-tiles">
            <Link to="/chat?channel_id=external">
              <Icon name="MessagesSquare" />
              <h3>客户共创频道</h3>
              <p>讨论已开放的资料与交付</p>
            </Link>
            <Link to="/deliveries">
              <Icon name="PackageCheck" />
              <h3>交付与回执</h3>
              <p>查看固定版本，逐项确认</p>
            </Link>
            {s.work
              .filter((w) => sharedVersions(s, w.id).length > 0)
              .map((w) => (
                <Link key={w.id} to={sharedArtifactPath(s, w)}>
                  <Icon name="FileCheck" />
                  <h3>{w.title}</h3>
                  <p>共享版本 v{sharedVersions(s, w.id).at(-1)}</p>
                </Link>
              ))}
          </div>
        </>
      );
    else if (
      !sharedArtifact &&
      route.path !== "/settings" &&
      !(
        route.path === "/chat" && !channelReadProblem(s, route.params.get("channel_id")) && !route.params.get("execution_id")
      ) &&
      parts[0] !== "deliveries"
    )
      content = (
        <Empty
          title="此内容不在共享范围"
          text="当前角色只能访问已共享会话、成果和交付回执。"
        >
          <Link to="/overview" className="btn">
            返回共享工作台
          </Link>
        </Empty>
      );
  }
  if (
    parts[0] === "p" &&
    ["work-items", "artifacts"].includes(parts[2]) &&
    parts[3] &&
    s.work.find((w) => w.id === parts[3])?.project !== parts[1]
  )
    content = (
      <Empty title="对象与项目不匹配" text="请从原项目打开正确的对象地址。" />
    );
  if (route.path === "/overview" && !s.projects.length && s.role !== "external")
    content = (
      <Empty
        title="从第一个项目开始"
        text="这个租户还没有项目。创建一个项目，再在其中组织工作、成员与交付。"
      >
        <Button
          variant="primary"
          onClick={() => {
            setGroup("collab");
            onView("/projects");
          }}
        >
          管理项目
        </Button>
      </Empty>
    );
  const state = route.params.get("state");
  if (state === "loading")
    content = (
      <div className="skeleton" role="status" aria-label="加载中…">
        <i />
        <i />
        <i />
        <i />
        <p>正在加载工作信息…</p>
      </div>
    );
  if (state === "error")
    content = (
      <Empty
        title="暂时无法获取信息"
        text="已保存的数据不会丢失。请重试或稍后继续。"
      >
        <Link className="btn primary" to={route.path}>
          重新加载
        </Link>
      </Empty>
    );
  if (state === "empty")
    content = (
      <Empty>
        <Button variant="primary" onClick={() => setNewWork(true)}>
          发起第一件工作
        </Button>
      </Empty>
    );
  if (state === "denied")
    content = (
      <Empty title="没有访问权限" text="请联系对象负责人获取适当范围的授权。">
        <Link className="btn" to="/overview">
          返回工作台
        </Link>
      </Empty>
    );
  return (
    <>
      <a
        className="skip-link"
        href="#main-content"
        onClick={(e) => {
          e.preventDefault();
          document.getElementById("main-content").focus();
        }}
      >
        跳到主要内容
      </a>
      <header className="topbar">
        <IconButton
          name="Menu"
          label="展开导航"
          className="mobile-menu icon-button"
          aria-expanded={drawer}
          aria-controls="app-sidebar"
          onClick={() => setDrawer(!drawer)}
        />
        <Link className="brand" to="/overview" aria-label="返回工作台">
          <span className="brand-mark">
            <Icon name="Asterisk" size={25} />
          </span>
          <b>
            Myrmidon<span>WORK TOGETHER.</span>
          </b>
        </Link>
        <div className="tenant-control">
          <button
            ref={tenantTrigger}
            className="tenant-trigger"
            aria-label={`租户：${tenant?.name}`}
            aria-expanded={tenantMenu}
            aria-controls="tenant-switcher"
            onClick={() => setTenantMenu(!tenantMenu)}
          >
            <span className="tenant-monogram">{tenant?.name.slice(0, 1)}</span>
            <span className="tenant-name">{tenant?.name}</span>
            <Icon name="ChevronsUpDown" size={14} />
          </button>
          {tenantMenu && (
            <>
              <button
                className="tenant-dismiss"
                aria-label="关闭租户选择"
                onClick={() => setTenantMenu(false)}
              />
              <section
                id="tenant-switcher"
                className="tenant-popover"
                aria-label="租户切换器"
              >
                <small>已加入的租户 · {s.tenants.length}</small>
                {s.tenants.length > 4 && <Field label="查找租户" value={tenantFilter} onChange={setTenantFilter} />}
                {s.tenants.filter((t) => t.name.toLowerCase().includes(tenantFilter.toLowerCase())).map((t) => (
                  <button
                    key={t.id}
                    className={t.id === s.activeTenant ? "selected" : ""}
                    onClick={() => {
                      try {
                        switchTenant(t.id);
                        finishTenant();
                      } catch (e) {
                        toast(e.message, true);
                      }
                    }}
                  >
                    <span className="tenant-monogram">
                      {t.name.slice(0, 1)}
                    </span>
                    <span>
                      <b>{t.name}</b>
                      <small>{t.role === "owner" ? "所有者" : "成员"}</small>
                    </span>
                    {t.id === s.activeTenant && <Icon name="Check" size={16} />}
                  </button>
                ))}
                {pendingTenantInvitations.length > 0 && <Button icon="Mail" onClick={() => {
                  setTenantMenu(false); setTenantAction("invitations");
                }}>待处理邀请 · {pendingTenantInvitations.length}</Button>}
                <div className="tenant-actions">
                  <Button
                    icon="Plus"
                    onClick={() => {
                      setTenantMenu(false);
                      setTenantAction("create");
                      setTenantInput("");
                    }}
                  >
                    创建租户
                  </Button>
                  <Button
                    icon="UserPlus"
                    onClick={() => {
                      setTenantMenu(false);
                      setTenantAction("join");
                      setTenantInput("");
                    }}
                  >
                    加入租户
                  </Button>
                </div>
                <p>项目属于当前租户，切换不会混合资料。</p>
              </section>
            </>
          )}
        </div>
        <div className="top-divider" />
        <nav className="primary-nav" aria-label="主导航">
          {primary.map(([key, label, to, icon]) => (
            <Link
              key={key}
              to={areaPath(key, to)}
              onClick={event => {if(event.metaKey || event.ctrlKey || event.shiftKey || event.altKey)return;event.preventDefault();openArea(key, areaPath(key, to));}}
              className={group === key ? "active" : ""}
              aria-current={group === key ? "page" : undefined}
            >
              <Icon name={icon} />
              {label}
              {key === "chat" && <span className="nav-dot" />}
            </Link>
          ))}
        </nav>
        <div className="top-actions">
          <button
            className="search-button"
            onClick={() => setSearch(true)}
            aria-label="全局搜索"
          >
            <Icon name="Search" />
            <span>搜索任何工作</span>
            <kbd>⌘ K</kbd>
          </button>
          <IconButton
            name="Palette"
            label="切换主题"
            className="icon-button theme-button"
            onClick={() => setThemePicker(true)}
          />
          <IconButton
            name={s.dark ? "Sun" : "Moon"}
            label="切换明暗主题"
            onClick={() => update((x) => (x.dark = !x.dark))}
          />
          <Link
            to="/approvals"
            className="icon-button"
            aria-label="通知与待审批"
          >
            <Icon name="Bell" />
          </Link>
          <Link to="/settings" aria-label="个人设置">
            <Avatar name={s.role === "external" ? "陈曦" : "林予"} />
          </Link>
        </div>
      </header>
      <div className="app-shell" data-area={group}>
        {drawer && (
          <button
            className="nav-scrim"
            aria-label="关闭导航"
            onClick={() => setDrawer(false)}
          />
        )}
        <aside id="app-sidebar" className={`sidebar ${drawer ? "open" : ""}`} role={drawer ? "dialog" : undefined} aria-modal={drawer ? true : undefined} aria-label="工作空间导航">
          <div className="workspace">
            <Icon name="Boxes" />
            <div>
              <strong>项目范围</strong>
              <small>当前租户内的协作与交付</small>
            </div>
          </div>
          <label className="scope-select">
            <Icon name="Folders" />
            <Select
              aria-label="项目"
              value={s.project}
              onChange={(e) => {
                const project = e.target.value;
                rememberCurrent();
                update((x) => (x.project = project));
                const params=new URLSearchParams(route.params);params.set("scope",project);
                for(const key of [...params.keys()])if(key.startsWith("state.")||["scroll","segment","quick_work","work_draft","workflow_id","version"].includes(key))params.delete(key);
                setNewWork(false);
                let destination=route.path;
                if(/^\/p\/[^/]+\/(?:work-items|artifacts)(?:\/|$)/.test(destination)||destination==="/collaboration")destination=project==="all"?"/collaboration":`/p/${project}/work-items`;
                else if(/^\/p\/[^/]+\/flow/.test(destination))destination=project==="all"?"/workflows":`/p/${project}/flow`;
                else if(destination==="/overview"||destination==="/projects"||/^\/p\/[^/]+$/.test(destination))destination="/overview";
                else if(/^\/(materials|requirements|iterations|tests|testplans|testreports|defects|repositories)\//.test(destination))destination=`/${destination.split("/")[1]==="testreports"?"testplans":destination.split("/")[1]}`;
                onView(`${destination}?${params}`);
              }}
            >
              <option value="all">全部项目</option>
              {activeProjects(s)
                .filter(
                  (p) =>
                    s.role !== "external" ||
                    s.work.some(
                      (w) =>
                        w.project === p.id &&
                        sharedVersions(s, w.id).length > 0,
                    ),
                )
                .map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
            </Select>
          </label>
          <div className="sidebar-label">
            {primary.find((p) => p[0] === group)?.[1]}空间
          </div>
          <nav aria-label="区域导航">
            {(s.role === "external"
              ? [
                  ["/overview", "LayoutDashboard", "共享工作台"],
                  ["/chat?channel_id=external", "MessagesSquare", "客户共创"],
                  ["/deliveries", "PackageCheck", "交付与回执"],
                  ["/settings", "Settings2", "个人设置"],
                ]
              : areaItems
            ).map(([to, icon, label]) => (
              <PanelButton
                onView={onView}
                key={to}
                to={to}
                className={areaIsActive(to) ? "active" : ""}
                aria-current={areaIsActive(to) ? "page" : undefined}
              >
                <Icon name={icon} />
                <span>{label}</span>
                {to === "/approvals" && (
                  <span className="count">
                    {
                      s.resources.approvals.filter((a) => a.status === "待审核")
                        .length
                    }
                  </span>
                )}
              </PanelButton>
            ))}
          </nav>
          <div className="sidebar-bottom">
            <PanelButton onView={onView} to="/health" className="connection">
              <span className={s.offline ? "offline-dot" : "online-dot"} />
              {s.offline ? "当前离线 · 草稿保留" : "工作状态已同步"}
              <Icon name="ChevronRight" size={14} />
            </PanelButton>
            <small>交互原型 · 本地模拟数据</small>
          </div>
        </aside>
        <main
          id="main-content"
          tabIndex={-1}
          className={route.path === "/chat" ? "main chat-main" : "main"}
        >
          {s.offline && (
            <Notice tone="warn">
              当前离线，保留草稿与已知状态；正式写操作等待重连后确认。
            </Notice>
          )}
          {s.role === "external" && (
            <Notice>
              当前为外部客户视角。仅显示共享会话、交付成果与回执。
            </Notice>
          )}
          <ViewContext.Provider value={{key:currentKey,full:route.full,params:route.params,tenantId:s.activeTenant,project:s.project,role:s.role,showLink:setLocationLink,sync:()=>syncViewURL(coordinates.current)}}>
            <div className="view-location-bar"><Breadcrumbs items={breadcrumbItems}/><CurrentViewLink toast={toast}/></div>
            {coordinateError && <Notice tone="warn">{coordinateError}</Notice>}
            <React.Fragment key={s.activeTenant}>{content?.type === Empty ? React.cloneElement(content, { heading: "h1" }) : content}</React.Fragment>
          </ViewContext.Provider>
          {locationLink && <Modal title="当前位置链接" onClose={()=>setLocationLink(null)}><Notice>浏览器未允许自动复制，可选择下方链接复制，或在新标签页打开。</Notice><Field label="直接位置链接" value={locationLink} readOnly/><a className="btn" href={locationLink} target="_blank" rel="noopener noreferrer">新标签页打开</a></Modal>}
        </main>
      </div>
      <nav className="bottom-nav" aria-label="手机主导航">
        {primary.map(([key, label, to, icon]) => (
          <Link
            key={key}
            to={areaPath(key, to)}
            onClick={event => {if(event.metaKey || event.ctrlKey || event.shiftKey || event.altKey)return;event.preventDefault();openArea(key, areaPath(key, to));}}
            className={group === key ? "active" : ""}
              aria-current={group === key ? "page" : undefined}
          >
            <Icon name={icon} />
            <span>{label}</span>
          </Link>
        ))}
      </nav>
      {notice && (
        <div
          className={`toast ${notice.error ? "error" : ""}`}
          role="status"
          aria-live="polite"
        >
          <Icon name={notice.error ? "CircleAlert" : "CircleCheck"} />
          <span>{notice.text}</span>
          <IconButton
            name="X"
            label="关闭提示"
            onClick={() => setNotice(null)}
          />
        </div>
      )}
      {themePicker && (
        <Modal title="选择你的主题" onClose={() => setThemePicker(false)}>
          <p className="muted">
            选择主题后立即应用，也可搭配深色模式。
          </p>
          <div className="theme-grid">
            {[
              [
                "aurora",
                themeNames.aurora,
                "#F7F6FB",
                "#7C3AED",
                "#0891B2",
                "#F0EEF7",
              ],
              [
                "modern",
                themeNames.modern,
                "#fafafa",
                "#171717",
                "#0e7490",
                "#e5e5e5",
              ],
              [
                "business",
                themeNames.business,
                "#f8fafc",
                "#6366f1",
                "#0284c7",
                "#e2e8f0",
              ],
              [
                "eyeCare",
                themeNames.eyeCare,
                "#f4f1e8",
                "#5a7c5a",
                "#4f7a8a",
                "#ece7d8",
              ],
              [
                "pinkKawaii",
                themeNames.pinkKawaii,
                "#fff5fa",
                "#e8388a",
                "#a855f7",
                "#ffe8f3",
              ],
              [
                "neoBrutalism",
                themeNames.neoBrutalism,
                "#fffdf5",
                "#ff006e",
                "#3a86ff",
                "#ffe066",
              ],
            ].map(([key, name, bg, primary, info, shell]) => (
              <button
                key={key}
                className="theme-card"
                aria-pressed={s.theme === key}
                onClick={() => update((x) => (x.theme = key))}
              >
                <span
                  className="theme-preview"
                  style={{
                    "--preview-bg": bg,
                    "--preview-primary": primary,
                    "--preview-info": info,
                    "--preview-shell": shell,
                    "--preview-border": shell,
                  }}
                >
                  <i />
                  <i />
                  <i />
                </span>
                <b>{name}</b>
              </button>
            ))}
          </div>
          <label className="switch-label">
            <input
              type="checkbox"
              checked={s.dark}
              onChange={(e) => update((x) => (x.dark = e.target.checked))}
            />
            深色模式
          </label>
        </Modal>
      )}
      {tenantAction && (
        <Modal
          title={tenantAction === "create" ? "创建租户" : tenantAction === "invitations" ? "待处理租户邀请" : "加入租户"}
          key={tenantAction}
          draftValue={tenantAction === "invitations" ? undefined : tenantInput}
          returnFocusRef={tenantTrigger}
          onClose={closeTenantAction}
        >
          {tenantAction === "invitations" ? <div className="stack">
            <Notice>这里是当前账号收到的租户邀请。接受后才会加入租户；项目成员邀请在组织与成员中管理。均为本地演示。</Notice>
            {pendingTenantInvitations.map((item) => <section className="tenant-invitation" key={item.id}>
              <h3>{item.tenantName}</h3><p>{item.inviter} 邀请你作为{item.role === "owner" ? "负责人" : "成员"}加入</p>
              <p className="muted">有效至 {Number.isFinite(Date.parse(item.expires)) ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium" }).format(new Date(item.expires)) : "日期无效，请联系邀请人"}</p>
              <div className="button-row"><Button variant="primary" disabled={!Number.isFinite(Date.parse(item.expires)) || Date.parse(item.expires) <= Date.now()} onClick={() => {
                try { joinTenant(item.code); finishTenant(); toast("已接受邀请并进入租户。"); } catch (error) { toast(error.message, true); }
              }}>接受并进入</Button><Button onClick={() => {
                try { declineTenantInvitation(item.id); toast("已拒绝邀请。"); } catch (error) { toast(error.message, true); }
              }}>拒绝邀请</Button></div>
            </section>)}
            {pendingTenantInvitations.length === 0 && <Empty title="没有待处理邀请" text="收到新邀请后会显示在这里。" />}
          </div> : <ActionForm
            label={tenantAction === "create" ? "创建并进入" : "验证邀请并加入"}
            onCancel={closeTenantAction}
            onSubmit={({ dismiss }) => {
              try {
                if (tenantAction === "create") createTenant(tenantInput);
                else joinTenant(tenantInput);
                dismiss(finishTenant);
                toast("已切换租户，项目与数据已分开。");
              } catch (e) {
                toast(e.message, true);
              }
            }}
          >
            <Field
              label={tenantAction === "create" ? "租户名称" : "邀请码"}
              value={tenantInput}
              onChange={setTenantInput}
              required
            />
            {tenantAction === "join" && (
              <Notice>
                本地邀请示例：DEMO-JOIN。这里只接受发给当前演示账号且未过期、未撤销的邀请；真实邮件和账号验证尚未接入。
              </Notice>
            )}
          </ActionForm>}
        </Modal>
      )}
      {(newWork||route.params.has("quick_work")) && <NewWork key={`${s.activeTenant}:${route.params.get("quick_work")||"new"}`} draftId={route.params.get("quick_work")} toast={toast} onClose={() => setNewWork(false)} />}
      {search && <Search onClose={() => setSearch(false)} />}
    </>
  );
}
function Search({ onClose }) {
  const s = useStore();
  const [q, setQ] = useState("");
  const [includeArchived,setIncludeArchived] = useState(false);
  const works = visibleWork(s,{includeArchived});
  const records =
    s.role === "external"
      ? []
      : Object.entries(s.resources).flatMap(([kind, rows]) =>
          (kind === "knowledge" ? visibleKnowledge(s,{includeArchived}) : kind === "goals" ? visibleGoals(s) : rows).filter(r => includeArchived || !projectIsArchived(s,recordProjectId(s,r))).map((r) => ({
            title: `${r.name}${projectIsArchived(s,recordProjectId(s,r)) ? " · 已归档项目" : ""}`,
            kind: catalog[kind]?.[0],
            path: `/${kind === "knowledge" && (r.library?.category === "MATERIAL" || ["资料","素材","文件"].includes(r.kind)) ? "materials" : kind}/${r.id}`,
          })),
        );
  const all = [
    ...works.map((w) => ({ title: `${w.title}${projectIsArchived(s,w.project) ? " · 已归档项目" : ""}`, kind: "工作", path: workPath(w) })),
    ...records,
  ];
  const filtered = all
    .filter(
      (x) =>
        !q || `${x.title}${x.kind}`.toLowerCase().includes(q.toLowerCase()),
    )
    .slice(0, 25);
  return (
    <Modal title="找到任何工作、资料或决定" onClose={onClose}>
      <label className="search-field">
        <Icon name="Search" />
        <input
          aria-label="全局搜索关键词" name="global-search" autoComplete="off"
          placeholder="搜索工作、知识、报告、审批…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      </label>
      <label className="switch-label"><input type="checkbox" checked={includeArchived} onChange={event=>setIncludeArchived(event.target.checked)} />包含归档项目</label>
      <div className="search-results">
        {filtered.map((r, i) => (
          <Link key={r.path + i} to={r.path} onClick={onClose}>
            <Icon name="Search" />
            <span>
              {r.title}
              <small>{r.kind}</small>
            </span>
            <Icon name="ArrowUpRight" size={15} />
          </Link>
        ))}
        {!filtered.length && (
          <Empty title="没有匹配的内容" text="搜索只显示当前有权访问的记录。" />
        )}
      </div>
    </Modal>
  );
}
class ErrorBoundary extends React.Component {
  state = { error: null };
  static getDerivedStateFromError(error) {
    return { error };
  }
  render() {
    return this.state.error ? (
      <div className="padded">
        <h1>页面暂时无法显示</h1>
        <p>{this.state.error.message}</p>
        <Button onClick={() => location.reload()}>重新加载</Button>
      </div>
    ) : (
      this.props.children
    );
  }
}
createRoot(document.getElementById("root")).render(
  <ErrorBoundary>
    <ConfirmProvider>
      <App />
    </ConfirmProvider>
  </ErrorBoundary>,
);
