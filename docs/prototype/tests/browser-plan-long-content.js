// run-browser.py generates this local draft through store.generatePeriodicReport.
// Only source work text/identity is synthetic; no Plan, runtime or approval is forged.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 180));
  const check = (value, message) => { if (!value) throw Error(message); };
  const state = () => JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const fixture = state().testPlanLongContent;
  check(fixture, "Generated long-content fixture missing");
  const plan = state().resources.plans.find(item => item.id === fixture.planId);
  const report = state().resources.reports.find(item => item.id === fixture.reportId);
  const item = plan?.items.find(item => item.workId === fixture.workId);
  check(plan?.draftedBy?.kind === "System" && plan.status === "草稿" && !plan.confirmedSnapshot && !plan.submission,
    "Long-content Plan must be a genuine unconfirmed system draft");
  check(item?.title === fixture.title && plan.draftSources.some(source => source.itemId === item.id && source.kind === "CURRENT_ASSIGNMENT" && source.workId === fixture.workId && source.workVersion === fixture.workVersion),
    "Generated item lost its actual assigned-work provenance");
  check(report?.nextPlanBinding?.planId === plan.id && report.content.next_plan.some(entry => entry.plan_item_id === item.id),
    "Report did not project the automatically generated Plan");
  const original = JSON.stringify({ plan, report });
  const root = document.documentElement, originalTheme = root.dataset.theme, originalDark = root.classList.contains("dark");
  const results = [], failures = [];
  try {
    location.hash = `/plans/${plan.id}`;
    await wait();
    root.dataset.theme = "aurora";
    for (const dark of [false, true]) {
      root.classList.toggle("dark", dark);
      await wait();
      const source = [...document.querySelectorAll("main section")].find(section => section.querySelector("h2")?.childNodes[0]?.textContent === "系统起草来源");
      const article = [...document.querySelectorAll("main article")].find(article => article.querySelector("h3")?.textContent === fixture.title);
      const link = article && [...article.querySelectorAll("a")].find(link => link.textContent === `查看工作与责任团队 · ${fixture.workId}`);
      const goal = article && [...article.querySelectorAll("a")].find(link => link.textContent === fixture.goalTitle);
      check(source?.textContent.includes(fixture.title) && source.textContent.includes(fixture.workId) && source.textContent.includes("待核对草稿") && !source.querySelector("pre"), "Source prose lost full identity or honest provenance");
      check(article && link, "Full title or work link was truncated/replaced");
      check(goal?.getAttribute("href") === `#/goals/${fixture.goalId}`, "Full goal title or destination was truncated/replaced");
      check(link.getAttribute("href") === `#/p/${fixture.project}/work-items/${fixture.workId}`, "Work link points to a different source");
      article.scrollIntoView({ block: "center" });
      const box = article.getBoundingClientRect();
      const geometry = element => {
        const range = document.createRange(); range.selectNodeContents(element);
        const rects = [...range.getClientRects()];
        return { text: element.textContent, clientWidth: element.clientWidth, scrollWidth: element.scrollWidth,
          rects: rects.map(rect => ({ left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom })),
          fits: rects.every(rect => rect.left >= box.left - 1 && rect.right <= box.right + 1 && rect.top >= box.top - 1 && rect.bottom <= box.bottom + 1) };
      };
      const title = geometry(article.querySelector("h3")), destination = geometry(link), goalTitle = geometry(goal);
      const result = { dark, width: innerWidth, pageWidth: root.scrollWidth, sourceWidth: source.clientWidth, sourceScrollWidth: source.scrollWidth,
        articleWidth: article.clientWidth, articleScrollWidth: article.scrollWidth, title, goalTitle, destination };
      results.push(result);
      if (root.scrollWidth > innerWidth + 2 || source.scrollWidth > source.clientWidth + 2 || article.scrollWidth > article.clientWidth + 2 || !title.fits || !goalTitle.fits || !destination.fits)
        failures.push(`Aurora ${dark ? "dark" : "light"}: viewport ${innerWidth}, document ${root.scrollWidth}, article ${article.clientWidth}/${article.scrollWidth}, title fits ${title.fits}, goal fits ${goalTitle.fits}, work link fits ${destination.fits}`);
    }
    const workLink = [...document.querySelectorAll("main article a")].find(link => link.getAttribute("href") === `#/p/${fixture.project}/work-items/${fixture.workId}`);
    check(workLink.target === "_blank", "Source link hijacks the report view");
    location.hash = workLink.getAttribute("href").slice(1); await wait();
    check(location.hash.split("?")[0] === `#/p/${fixture.project}/work-items/${fixture.workId}` && document.querySelector("main")?.textContent.includes(fixture.title), "Work link did not open the actual source work");
    const destinationResults = [];
    for (const dark of [false, true]) {
      root.classList.toggle("dark", dark); await wait();
      const header = document.querySelector("main .page-head"), heading = header?.querySelector("h1");
      check(heading?.textContent === fixture.title, "Actual destination lost the complete title");
      const bounds = header.getBoundingClientRect(), range = document.createRange(); range.selectNodeContents(heading);
      const rects = [...range.getClientRects()];
      const fits = rects.every(rect => rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1);
      const result = { dark, width: innerWidth, pageWidth: root.scrollWidth, title: heading.textContent,
        headingWidth: heading.clientWidth, headingScrollWidth: heading.scrollWidth, fits,
        rects: rects.map(rect => ({ left: rect.left, right: rect.right })) };
      destinationResults.push(result);
      if (root.scrollWidth > innerWidth + 2 || heading.scrollWidth > heading.clientWidth + 2 || !fits)
        failures.push(`Actual work destination ${dark ? "dark" : "light"}: viewport ${innerWidth}, document ${root.scrollWidth}, title fits ${fits}`);
    }
    check(JSON.stringify({ plan: state().resources.plans.find(item => item.id === plan.id), report: state().resources.reports.find(item => item.id === report.id) }) === original, "Reading or following source changed the generated Plan/report");
    return { pass: failures.length === 0, width: innerWidth, failures, results, destinationResults,
      checks: ["actual report-generated system draft", "assigned work provenance", "full unbroken title and identifier", "text bounds without clipping", "working source destination and complete title geometry", "Aurora light/dark", "Plan/report unchanged"] };
  } finally {
    root.dataset.theme = originalTheme;
    root.classList.toggle("dark", originalDark);
  }
})()
