# Prototype Web Interface Guidelines audit — 2026-10-13

Scope: read-only source review and temporary browser-local audit. Latest rules fetched from https://raw.githubusercontent.com/vercel-labs/web-interface-guidelines/main/command.md into /tmp/prototype-interface-guidelines.md. No claim that every UI state passes.

Source collection: all 18 files in docs/prototype/src (16,662 lines at inventory), index.html. Shared ui.jsx/main.jsx/styles.css/themes.css inspected by component/rule; all page families (admin, work, collab, calendar, flow, effort, qa, task-previews) covered by targeted source scans and rendered-route inventory. This is rule-oriented coverage, not a claim every source line underwent full domain review.

Evidence: /tmp/prototype-guidelines-{desktop,mobile}.json — 63 routes each at1440×1000 and375×812, seeded owner state; headings, names, overflow, accidental dialogs. /tmp/prototype-guidelines-sweep.js is reproducible. Themes file is valid only after UI-driven audit script asserts actual theme/dark state; first import-based attempt was discarded because Vite module instances did not drive current React state. See final parent summary for finished result.

## Confirmed findings / locations
- P2 ui.jsx:350: shared role=tab has no arrows/Home/End/roving tabIndex; actual ArrowRight on /worklogs stays on first tab.
- P2 styles.css mobile-bottom-nav active: NeoBrutalism light label contrast2.92 at375px; actual axe evidence. Preserve six themes/tokens; control foreground correction sufficient.
- P2 ui.jsx:392 / pages-admin.jsx goal editor: dirty Escape silently discards unsaved name. No navigation/dirty guard anywhere. Durable chat/workflow draft paths do not make all form editors safe.
- P3 pages-qa.jsx:26: testplan listing h1→h3.
- Other direct guideline gaps catalogued below: control name/autocomplete, local-only filter state, unbounded lists, Intl/translate, compositor-only transitions. Prioritize behavioral/accessibility defects over mechanical copy rewrites.

## Rule-by-rule matrix
SOURCE PASS means static evidence; SAMPLE PASS means stated runtime sample only. PARTIAL/UNVERIFIED are not passes. N/A states contextual nonapplicability.

| # | Latest rule | Status | Evidence / gap |
|---|---|---|---|
| 1 | Icon-only buttons need `aria-label` | SAMPLE PASS | ui.jsx:230–245 shared primitives; 63 routes × 2 widths: zero visible unnamed buttons/controls; conditional dialogs not exhaustive. |
| 2 | Form controls need `<label>` or `aria-label` | SAMPLE PASS | ui.jsx:230–245 shared primitives; 63 routes × 2 widths: zero visible unnamed buttons/controls; conditional dialogs not exhaustive. |
| 3 | Interactive elements need keyboard handlers (`onKeyDown`/`onKeyUp`) | FAIL | ui.jsx:350 Tabs role=tab lacks Arrow/Home/End/roving semantics; actual ArrowRight on /worklogs leaves focus/selection unchanged. Native buttons otherwise keyboard-operable. |
| 4 | `<button>` for actions, `<a>`/`<Link>` for navigation (not `<div onClick>`) | PARTIAL | Link emits anchor ui.jsx:369; PanelButton:375 uses button for application panel selection (user requires sidebar URL/history unchanged). pages-flow.jsx:284 latest-definition navigation uses button; action-then-navigation is not flagged. |
| 5 | Images need `alt` (or `alt=""` if decorative) | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 6 | Decorative icons need `aria-hidden="true"` | SOURCE PASS | ui.jsx:230 Icon defaults aria-hidden=true; Avatar has named role=img. |
| 7 | Async updates (toasts, validation) need `aria-live="polite"` | PARTIAL | main.jsx:776 toast role=status aria-live=polite; Field/Form errors role=alert (assertive), not literal polite; terminal output polite. Screen-reader announcement timing not exercised. |
| 8 | Use semantic HTML (`<button>`, `<a>`, `<label>`, `<table>`) before ARIA | FAIL | ui.jsx:350 Tabs role=tab lacks Arrow/Home/End/roving semantics; actual ArrowRight on /worklogs leaves focus/selection unchanged. Native buttons otherwise keyboard-operable. |
| 9 | Headings hierarchical `<h1>`–`<h6>`; include skip link for main content | FAIL | main.jsx:493 skip link; 63-route heading scan finds /testplans [h1,h3], pages-qa.jsx:26. Other scanned default pages no jumps. |
| 10 | `scroll-margin-top` on heading anchors | N/A | No heading-anchor navigation in app; skip link targets main with focus handler, not heading. No scroll-margin-top declaration. |
| 11 | Meaningful media needs captions, transcripts, or descriptions as applicable | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 12 | Media controls need keyboard support; decorative media needs assistive-tech hiding | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 13 | Interactive elements need visible focus: `focus-visible:ring-*` or equivalent | SOURCE PASS | styles.css:73 global focus-visible 2px outline. Composer outline removal replaced by focus-within:1414. All widget/theme focus contrast not fully measured. |
| 14 | Never `outline-none` / `outline: none` without focus replacement | SOURCE PASS | styles.css:73 global focus-visible 2px outline. Composer outline removal replaced by focus-within:1414. All widget/theme focus contrast not fully measured. |
| 15 | Use `:focus-visible` over `:focus` (avoid focus ring on click) | PARTIAL | Global focus-visible; styles.css:3947 listbox option uses :focus and composer textarea :focus exception. |
| 16 | Group focus with `:focus-within` for compound controls | SOURCE PASS | styles.css:1414 composer and :4750 org search focus-within. |
| 17 | Sticky headers/footers/overlays must not cover the focused element | UNVERIFIED | Sticky modal header, app shell and mobile footer; full keyboard traversal at zoom/landscape not done. |
| 18 | Inputs need `autocomplete` and meaningful `name` | FAIL | main.jsx:957 search, pages-collab.jsx:507 composer, pages-work.jsx:326 search, pages-admin.jsx:825 search lack name/autocomplete; Field input supplies defaults, textarea does not. |
| 19 | Use correct `type` (`email`, `tel`, `url`, `number`) and `inputmode` | PARTIAL | Field uses passed types; number/date/email callsites present. No general inputmode default; full semantic field inventory not runtime-validated. |
| 20 | Never block paste (`onPaste` + `preventDefault`) | SOURCE PASS | No onPaste handler or clipboard prevention found across src. |
| 21 | Labels clickable (`htmlFor` or wrapping control) | SOURCE PASS | Field htmlFor; switches/check labels wrap control; QA case picker and team checkboxes wrapped. Default routes have no unnamed controls. |
| 22 | Disable spellcheck on emails, codes, usernames (`spellCheck={false}`) | FAIL | Preview path/terminal explicitly spellCheck=false; shared Field email/code/system-prompt fields do not default false. |
| 23 | Checkboxes/radios: label + control share single hit target (no dead zones) | SOURCE PASS | Field htmlFor; switches/check labels wrap control; QA case picker and team checkboxes wrapped. Default routes have no unnamed controls. |
| 24 | Submit button stays enabled until request starts; spinner during request | PARTIAL | ActionForm awaits submit but no pending/disabled/spinner state. Current store operations synchronous local simulation; real async lifecycle not implemented. |
| 25 | Errors inline next to fields; focus first error on submit | PARTIAL | Field inline error and Form first :invalid focus. Domain exceptions summarized at ActionForm bottom; Effort manual handlers do not focus specific field. |
| 26 | Placeholders end with `…` and show example pattern | FAIL | task-previews.jsx:161 placeholder help; pages-effort.jsx:71 Asia/Singapore; calendar:253 YYYY-MM-DD lack ellipsis; mostly cosmetic. |
| 27 | `autocomplete="off"` on non-auth fields to avoid password manager triggers | FAIL | main.jsx:957 search, pages-collab.jsx:507 composer, pages-work.jsx:326 search, pages-admin.jsx:825 search lack name/autocomplete; Field input supplies defaults, textarea does not. |
| 28 | Warn before navigation with unsaved changes (`beforeunload` or router guard) | FAIL | No beforeunload/router dirty guard. Actual goal editor changed name then Escape closes without confirmation; reopening restores old name, losing draft. |
| 29 | Honor `prefers-reduced-motion` (provide reduced variant or disable) | SOURCE PASS | styles.css:3515 disables animation/transition and smooth scroll under reduced motion; needs actual OS/browser emulation verification. |
| 30 | Animate `transform`/`opacity` only (compositor-friendly) | FAIL | styles.css:182 animates background/color/border; :729 border-color/box-shadow. Pulse only opacity. Low-impact paint transitions violate strict compositor-only wording. |
| 31 | Never `transition: all`—list properties explicitly | SOURCE PASS | No transition:all; explicit property lists. |
| 32 | Set correct `transform-origin` | N/A | No animated SVG transforms or scale animation found; ReactFlow canvas transformation delegated to library. |
| 33 | SVG: transforms on `<g>` wrapper with `transform-box: fill-box; transform-origin: center` | N/A | No animated SVG transforms or scale animation found; ReactFlow canvas transformation delegated to library. |
| 34 | Animations interruptible—respond to user input mid-animation | UNVERIFIED | Short CSS hover transitions inherently interruptible; no high-load mid-animation responsiveness measurements. |
| 35 | Autoplay motion >5 seconds alongside other content needs pause, stop, or hide controls | PARTIAL | Loading skeleton pulse is infinite with no pause control, normal loading brief; persistent loading simulation can remain >5s. Reduced motion disables it. |
| 36 | Muted decorative loops must stop under `prefers-reduced-motion` | SOURCE PASS | styles.css:3515 disables animation/transition and smooth scroll under reduced motion; needs actual OS/browser emulation verification. |
| 37 | `…` not `...` | PARTIAL | UI copy predominantly Chinese ellipsis/curly quotes; code literals/JSON/spread excluded; no exhaustive content corpus typography proof. |
| 38 | Curly quotes `“` `”` not straight `"` | PARTIAL | UI copy predominantly Chinese ellipsis/curly quotes; code literals/JSON/spread excluded; no exhaustive content corpus typography proof. |
| 39 | Non-breaking spaces: `10&nbsp;MB`, `⌘&nbsp;K`, brand names | PARTIAL | No explicit NBSP convention; shortcut and units use ordinary spaces. No actual wrapping failure reproduced. |
| 40 | Loading states end with `…`: `"Loading…"`, `"Saving…"` | FAIL | main.jsx:454 skeleton aria-label 加载中 has no ellipsis; low-impact copy mismatch. |
| 41 | `font-variant-numeric: tabular-nums` for number columns/comparisons | PARTIAL | styles.css:636,2348,4727,4742 tabular-nums present; not a universal numeric-cell rule. |
| 42 | Use `text-wrap: balance` or `text-pretty` on headings (prevents widows) | SOURCE PASS | styles.css:83 h1/h2/h3 text-wrap:balance. |
| 43 | Text containers handle long content: `truncate`, `line-clamp-*`, or `break-words` | SOURCE PASS | styles.css:108,117 overflow-wrap:anywhere; many flex min-width:0 and ellipsis rules. Default 63-route responsive sweep no overflow. |
| 44 | Flex children need `min-w-0` to allow text truncation | SOURCE PASS | styles.css:108,117 overflow-wrap:anywhere; many flex min-width:0 and ellipsis rules. Default 63-route responsive sweep no overflow. |
| 45 | Handle empty states—don't render broken UI for empty strings/arrays | PARTIAL | Empty reused in resource/work/QA branches. Audit filtered empty array yields empty section (pages-admin.jsx:1908) without explanatory state. |
| 46 | User-generated content: anticipate short, average, and very long inputs | UNVERIFIED | Default data only; adversarial long CJK/URL/name input at 200% zoom across every screen not run. |
| 47 | `<img>` needs explicit `width` and `height` (prevents CLS) | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 48 | Below-fold images: `loading="lazy"` | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 49 | Above-fold critical images: `priority` or `fetchpriority="high"` | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 50 | Large lists (>50 items): virtualize (`virtua`, `content-visibility: auto`) | FAIL | pages-admin.jsx:1908 unbounded audit events map; messages and resource lists also unvirtualized. No content-visibility. >50-item performance load not run. |
| 51 | No layout reads in render (`getBoundingClientRect`, `offsetHeight`, `offsetWidth`, `scrollTop`) | SOURCE PASS | Layout reads only DatePicker/Select show handlers and task-previews effects; none found in JSX render path. |
| 52 | Batch DOM reads/writes; avoid interleaving | PARTIAL | Popover measurements precede setState; preview effect reads rects then writes scroll. No trace proving no layout thrashing in ReactFlow. |
| 53 | Prefer uncontrolled inputs; controlled inputs must be cheap per keystroke | UNVERIFIED | Controlled forms and search filter synchronously; no keystroke CPU trace under large data. |
| 54 | Add `<link rel="preconnect">` for CDN/asset domains | N/A | No remote asset/font requests in index.html; CSS uses local/system fallback stacks, no critical downloaded font. |
| 55 | Critical fonts: `<link rel="preload" as="font">` with `font-display: swap` | N/A | No remote asset/font requests in index.html; CSS uses local/system fallback stacks, no critical downloaded font. |
| 56 | Prefer `<video autoplay muted loop playsinline>` over animated GIF; provide a still alternative | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 57 | Short non-essential loops: Safari H.264 MP4 `<picture>` source, `prefers-reduced-motion` media condition, and still fallback | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 58 | URL reflects state—filters, tabs, pagination, expanded panels in query params | FAIL | Work query/status at pages-work.jsx:301; Resource query at pages-admin.jsx:132; Team tab/query at :747; Calendar mode/anchor/person at :25 remain local state. Explicit sidebar URL/history preservation is user-approved exception, not defect. |
| 59 | Links use `<a>`/`<Link>` (Cmd/Ctrl+click, middle-click support) | PARTIAL | Link emits anchor ui.jsx:369; PanelButton:375 uses button for application panel selection (user requires sidebar URL/history unchanged). pages-flow.jsx:284 latest-definition navigation uses button; action-then-navigation is not flagged. |
| 60 | Deep-link all stateful UI (if uses `useState`, consider URL sync via nuqs or similar) | FAIL | Work query/status at pages-work.jsx:301; Resource query at pages-admin.jsx:132; Team tab/query at :747; Calendar mode/anchor/person at :25 remain local state. Explicit sidebar URL/history preservation is user-approved exception, not defect. |
| 61 | Destructive actions need confirmation modal or undo window—never immediate | PARTIAL | Workflow delete confirm, work cancel reason modal, resource transitions reason modal, org revoke editor. Settings simulate revoke button immediate (pages-admin.jsx:2025) is local simulation, does not revoke real sessions. |
| 62 | `touch-action: manipulation` (prevents double-tap zoom delay) | SOURCE PASS | styles.css:50 manipulation; :54 tap highlight; dialog:2471, select/date:3923/4831 contain overscroll. |
| 63 | `-webkit-tap-highlight-color` set intentionally | SOURCE PASS | styles.css:50 manipulation; :54 tap highlight; dialog:2471, select/date:3923/4831 contain overscroll. |
| 64 | `overscroll-behavior: contain` in modals/drawers/sheets | SOURCE PASS | styles.css:50 manipulation; :54 tap highlight; dialog:2471, select/date:3923/4831 contain overscroll. |
| 65 | Page rubber-band suppression: `overscroll-behavior: none` on `<html>` inside `@media (pointer: fine)`; keep coarse-pointer page defaults for pull-to-refresh. Test hybrid touch devices: `pointer` describes the primary input, and `none` also disables scroll chaining and native swipe navigation | PARTIAL | styles.css:3524 pointer:fine html overscroll none. Hybrid Windows/touch and native swipe navigation not physically tested. |
| 66 | During drag: disable text selection, `inert` on dragged elements | UNVERIFIED | ReactFlow handles drag; app does not explicitly set inert on dragged elements; inspect library behavior and real drag keyboard interaction required. |
| 67 | Drag/swipe/pinch/path gestures need tap/click and keyboard alternatives unless essential | PARTIAL | Workflow has node list, inspector x/y and edge selectors as gesture alternatives; all edge-create/delete/reorder keyboard paths not fully exercised. |
| 68 | `autoFocus` sparingly—desktop only, single primary input; avoid on mobile | SOURCE PASS | No autoFocus attribute in src. Dialog showModal native focus is intentional; does not forcibly select mobile input. |
| 69 | Full-bleed layouts need `env(safe-area-inset-*)` for notches | PARTIAL | styles.css:2829 top and :3481 bottom safe-area; landscape left/right insets not configured/tested. |
| 70 | Avoid unwanted scrollbars: `overflow-x-hidden` on containers, fix content overflow | SAMPLE PASS | 63 routes at1440×1000 and375×812 have document.scrollWidth<=innerWidth; every modal and extreme content not covered. |
| 71 | Flex/grid over JS measurement for layout | SOURCE PASS | Flex/grid app layout; JS measurement restricted to popover viewport placement and tab scrolling. |
| 72 | `color-scheme: dark` on `<html>` for dark themes (fixes scrollbar, inputs) | SOURCE PASS | styles.css:23 html.dark color-scheme:dark; main.jsx:294–297 sets theme data/dark and derives meta theme-color from --bg. |
| 73 | `<meta name="theme-color">` matches page background | SOURCE PASS | styles.css:23 html.dark color-scheme:dark; main.jsx:294–297 sets theme data/dark and derives meta theme-color from --bg. |
| 74 | Native `<select>`: explicit `background-color` and `color` (Windows dark mode) | N/A | Shared Select is custom combobox; no native select rendered in src. |
| 75 | Dates/times: use `Intl.DateTimeFormat` not hardcoded formats | FAIL | pages-calendar.jsx:105 manual year/month; pages-admin.jsx:1762/1869 and pages-work.jsx:126/970 currency toFixed; timestamps displayed raw in new QA/effort/goal history. Intl used in some log/report paths, not consistently. |
| 76 | Numbers/currency: use `Intl.NumberFormat` not hardcoded formats | FAIL | pages-calendar.jsx:105 manual year/month; pages-admin.jsx:1762/1869 and pages-work.jsx:126/970 currency toFixed; timestamps displayed raw in new QA/effort/goal history. Intl used in some log/report paths, not consistently. |
| 77 | Detect language via `Accept-Language` / `navigator.languages`, not IP | N/A | Fixed Chinese prototype; no locale negotiation promised; no IP geolocation. |
| 78 | Brand names, code tokens, identifiers: wrap with `translate="no"` to prevent garbled auto-translation | FAIL | No translate=no attributes for brand, code/IDs across src. |
| 79 | Inputs with `value` need `onChange` (or use `defaultValue` for uncontrolled) | SOURCE PASS | Controlled visible inputs checked have onChange; hidden Select input intentionally no change handler. |
| 80 | Date/time rendering: guard against hydration mismatch (server vs client) | N/A | Client-only Vite createRoot; no SSR hydration or suppressHydrationWarning. |
| 81 | `suppressHydrationWarning` only where truly needed | N/A | Client-only Vite createRoot; no SSR hydration or suppressHydrationWarning. |
| 82 | Buttons/links need `hover:` state (visual feedback) | SOURCE PASS | styles.css:66 links and :69 buttons global hover; specialized button/card hover rules. |
| 83 | Interactive states increase contrast: hover/active/focus more prominent than rest | FAIL | Actual375px NeoBrutalism light bottom-nav active label contrast2.92 #ff006e/#ffd6e8. Gradient themes need manual contrast; full state matrix pending. |
| 84 | Active voice: "Install the CLI" not "The CLI will be installed" | PARTIAL | Predominantly direct Chinese action labels, numeric counts and corrective error strings; all dynamic domain errors not reviewed with screen readers. |
| 85 | Title Case for headings/buttons (Chicago style) | N/A | Chinese product copy; English title case/and→& rule not broadly applicable to IDs, model names and intentional mixed terms. |
| 86 | Numerals for counts: "8 deployments" not "eight" | PARTIAL | Predominantly direct Chinese action labels, numeric counts and corrective error strings; all dynamic domain errors not reviewed with screen readers. |
| 87 | Specific button labels: "Save API Key" not "Continue" | PARTIAL | Domain buttons mostly specific; generic modal 保存/取消 contextual. No critical ambiguous action reproduced. |
| 88 | Error messages include fix/next step, not just problem | PARTIAL | Predominantly direct Chinese action labels, numeric counts and corrective error strings; all dynamic domain errors not reviewed with screen readers. |
| 89 | Second person; avoid first person | N/A | First-person 我的/本人 intentionally expresses owner-specific authorization/records, should not be blindly rewritten. |
| 90 | `&` over "and" where space-constrained | N/A | Chinese product copy; English title case/and→& rule not broadly applicable to IDs, model names and intentional mixed terms. |
| 91 | `user-scalable=no` or `maximum-scale=1` disabling zoom | SOURCE PASS | index.html viewport allows zoom; no maximum-scale/user-scalable=no. |
| 92 | `onPaste` with `preventDefault` | SOURCE PASS | No onPaste handler or clipboard prevention found across src. |
| 93 | `transition: all` | SOURCE PASS | No transition:all; explicit property lists. |
| 94 | `outline-none` without focus-visible replacement | SOURCE PASS | styles.css:73 global focus-visible 2px outline. Composer outline removal replaced by focus-within:1414. All widget/theme focus contrast not fully measured. |
| 95 | Inline `onClick` navigation without `<a>` | PARTIAL | Link emits anchor ui.jsx:369; PanelButton:375 uses button for application panel selection (user requires sidebar URL/history unchanged). pages-flow.jsx:284 latest-definition navigation uses button; action-then-navigation is not flagged. |
| 96 | `<div>` or `<span>` with click handlers (should be `<button>`) | SOURCE PASS | No div/span action navigation found; dialog backdrop/listbox event handlers are container mechanics with inner native controls. Generic onclick seen by browser reflects React delegation, not actionable div. |
| 97 | Images without dimensions | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 98 | Large arrays `.map()` without virtualization | FAIL | pages-admin.jsx:1908 unbounded audit events map; messages and resource lists also unvirtualized. No content-visibility. >50-item performance load not run. |
| 99 | Form inputs without labels | SAMPLE PASS | ui.jsx:230–245 shared primitives; 63 routes × 2 widths: zero visible unnamed buttons/controls; conditional dialogs not exhaustive. |
| 100 | Icon buttons without `aria-label` | SAMPLE PASS | ui.jsx:230–245 shared primitives; 63 routes × 2 widths: zero visible unnamed buttons/controls; conditional dialogs not exhaustive. |
| 101 | Hardcoded date/number formats (use `Intl.*`) | FAIL | pages-calendar.jsx:105 manual year/month; pages-admin.jsx:1762/1869 and pages-work.jsx:126/970 currency toFixed; timestamps displayed raw in new QA/effort/goal history. Intl used in some log/report paths, not consistently. |
| 102 | `autoFocus` without clear justification | SOURCE PASS | No autoFocus attribute in src. Dialog showModal native focus is intentional; does not forcibly select mobile input. |
| 103 | Animated GIF when compressed video is suitable | N/A | No img/video/audio/GIF in current JSX collection; media previews are explicitly local simulated HTML/SVG, not real playback. |
| 104 | Gesture-only action without tap/click and keyboard alternative | PARTIAL | Workflow has node list, inspector x/y and edge selectors as gesture alternatives; all edge-create/delete/reorder keyboard paths not fully exercised. |

## Applicable design-taste audit
Skill explicitly excludes dashboards/data tables/multi-step product UI, so landing-page hero/AIDA/asymmetry/font/package mandates are N/A. Applicable preservation protocol: retain six theme definitions; existing Chinese copy voice/IA; shared tokens, spacing, semantic hierarchy and focus. Existing --font-body/--font-head/--font-mono, --radius and theme colors remain established system; no new library or full visual redesign warranted. Fixed title/brand are deployment concerns outside this read-only guideline task. SEO migration is N/A for local hash prototype. Need actual visual assessment of all12 theme modes at key dense/modal/error states; one sampled report route cannot certify them.

## Remaining real gates
1. Fix confirmed tab keyboard, bottom-nav contrast and QA heading defects; real keyboard+12-mode mobile contrast retest.
2. Decide desired dirty-form retention/confirmation and URL-synced filters under explicit sidebar URL/history exception; do not silently change product navigation.
3. Keyboard traversal at200% zoom/mobile landscape, focus under sticky header/footer, all popover/dialog drawers; browser-native Tab/ShiftTab/Escape, not synthetic events only.
4. Manual contrast for gradient Aurora/Pink backgrounds; hover/disabled/error/focus states in six themes; screen-reader announcement verification.
5. Long content and >50/500-row data tests with performance/keystroke evidence; virtualization scope if needed.
6. Safari/Firefox and real Windows/hybrid-touch/native swipe, landscape safe insets; not run here.
7. New QA/rework UI is changing concurrently: rerun affected route evidence after integration freeze; do not treat this snapshot as final acceptance.

## Authorized repair and verified checkpoint
The three confirmed scoped UI defects are now repaired: ui.jsx:350 shared Tabs has arrows/Home/End/wrap and single roving tabstop, no mount/useEffect focus; artifact and workflow-node hand-written button tabs reuse it. Existing TaskPreviews tablist retained and tested. URL/navigation link groups remain links (not recast as tabs). pages-qa.jsx:26 list heading h2; styles.css:3496 mobile active text color mixes existing primary/fg without changing six theme tokens.

`bun run build`: PASS. `git diff --check`: PASS. `python3 tests/browser-guidelines.py http://127.0.0.1:4175`: PASS on stable built preview (no HMR). Four tab groups exercised using actual Chromium keyboard presses: Worklogs, artifact views, workflow node inspector, task preview. Home/End/arrows/wrap/single tabstop/Tab exit verified; no mount focus. Twelve theme × light/dark combinations at375px: bottom navigation axe zero violations in all12;11 have zero incomplete, Pink Kawaii light retains one translucent-gradient contrast incomplete. This is a scoped navigation result, not whole-page/whole-theme certification. Evidence `/tmp/prototype-guidelines-regression.log`. Earlier full-page theme experiments on live HMR produced transient results and were discarded; do not use `/tmp/prototype-guidelines-themes.json` as final evidence.

Report rule statuses describe the initial audited snapshot; rules3/8/9/83 confirmed defects above are now repaired within this sample. All other PARTIAL/UNVERIFIED gates remain open. Parent is independently changing preview splitter; it is outside this verified build and needs its own acceptance.


## 2026-10-13 — Display and audit-list follow-up

Current evidence supersedes only the specific initial findings below; untested rules remain open.

- Rule 98, audit list: render bounded to 50 rows/page; search includes event ID and target; empty state and page reset are explicit. Actual 501-record fixture at 1440/375 initially reproduced long-title horizontal overflow, then passed after wrapping. `tests/browser-audit-list.js`, `/tmp/audit-list-after-desktop.json` and `/tmp/audit-list-after-mobile.json` both PASS with unchanged source. Other unbounded message/resource lists remain unverified.
- Rules 75/76/101: shared `formatCurrency` now formats monetary totals; calendar month headings use Intl. QA report/run, report generation/snapshot/history, work decisions/history, effort confirmation/history and conversation timestamp displays now reuse `formatTimestamp`. Legacy HH:mm is retained without inventing a date. This closes the named display instances, not every arbitrary date field or locale/platform matrix.
- Absolute audit time: independently reproduced `log()` returning HH:mm with `Date.parse` invalid. New events now retain ISO UTC timestamps; old events remain intact. New domain regression passes and an isolated restoration of the old HH:mm implementation fails that regression (`/tmp/audit-time-mutation-result.txt`). Persistent timestamps and presentation formats are deliberately separate.
- Frozen display build `/tmp/prototype-display-review` (4188): QA, QA plans, effort, reports, collaboration and audit-list browser suites pass at 1440/375. Defect suite initially failed because the unified runner reset its required QA closure; runner now executes and records the real QA browser prerequisite. Targeted defect reruns at both sizes PASS (`/tmp/display-defects-desktop.json`, `/tmp/display-defects-mobile.json`). No green state is injected. This snapshot predates the audit ISO storage change and the ongoing new OKR/dirty-form integration.
- Rules 3/8/9/83 retain the earlier scoped keyboard and mobile-navigation repairs; gradient contrast and complete twelve-theme dense/form/error coverage are still not certified. Rules 60/69 and physical platform checks remain unresolved.

Full PRD/two-complete-round acceptance: NOT READY, consecutive full clean rounds 0.
