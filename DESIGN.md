---
name: "Moneybag"
description: "A square, quiet, dense personal finance workspace for humans and agents."
colors:
  bg: "#ffffff"
  paper: "#ffffff"
  sunk: "#f6f6f7"
  text: "#111214"
  text2: "#55595f"
  text3: "#646a73"
  bd: "#e4e5e8"
  bd2: "#efeff1"
  hover: "#f1f2f4"
  ink: "#111214"
  c0: "#2f6bff"
  c1: "#c48a00"
  c2: "#0f97ad"
  c3: "#8b5ad6"
  c4: "#dc6327"
  other: "#a1a5ad"
  ok: "#17783f"
  neg: "#bf3030"
  focus: "#2f6bff"
  sel: "#e6eeff"
  bg-dark: "#0e0f11"
  paper-dark: "#0e0f11"
  sunk-dark: "#17191c"
  text-dark: "#eef0f3"
  text2-dark: "#aab0b9"
  text3-dark: "#9ba2ae"
  bd-dark: "#2a2d33"
  bd2-dark: "#1f2226"
  hover-dark: "#1c1f23"
  ink-dark: "#eef0f3"
  c0-dark: "#4d7ff5"
  c1-dark: "#b88a10"
  c2-dark: "#1fa6bb"
  c3-dark: "#9a72e0"
  c4-dark: "#d0682f"
  other-dark: "#6c727c"
  ok-dark: "#4fc98a"
  neg-dark: "#f07272"
  focus-dark: "#5b8cff"
  sel-dark: "#17223a"
  draft-border: "#a16f00"
  draft-text: "#946600"
typography:
  headline:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "24px"
    fontWeight: 650
    lineHeight: 1.2
    letterSpacing: "-0.015em"
  title:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "16px"
    fontWeight: 620
  body:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.5
  table:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Geist Mono,ui-monospace,monospace"
    fontSize: "10px"
    fontWeight: 600
    letterSpacing: "0.07em"
  identifier:
    fontFamily: "Geist Mono,ui-monospace,monospace"
    fontSize: "10px"
    fontWeight: 600
    letterSpacing: "0.07em"
  data:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "26px"
    fontWeight: 600
    lineHeight: 1.5
    letterSpacing: "-0.015em"
  button:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "13px"
    fontWeight: 500
  input:
    fontFamily: "Geist,\"Geist Fallback\",system-ui,sans-serif"
    fontSize: "13px"
    fontWeight: 400
rounded:
  square: "0"
spacing:
  space-4: "4px"
  space-8: "8px"
  space-12: "12px"
  space-16: "16px"
  space-20: "20px"
  space-24: "24px"
  space-28: "28px"
  space-32: "32px"
components:
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.bg}"
    typography: "{typography.button}"
    rounded: "{rounded.square}"
    padding: "0 10px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    typography: "{typography.button}"
    rounded: "{rounded.square}"
    padding: "0 10px"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text}"
    typography: "{typography.input}"
    rounded: "{rounded.square}"
    padding: "4px 10px"
  navigation:
    backgroundColor: "{colors.sunk}"
    textColor: "{colors.text2}"
    rounded: "{rounded.square}"
    padding: "6px 16px"
  chip:
    textColor: "{colors.text2}"
    rounded: "{rounded.square}"
    padding: "1px 6px"
  dashboard-panel:
    backgroundColor: "{colors.paper}"
    rounded: "{rounded.square}"
    padding: "14px 16px"
  badge-draft:
    textColor: "{colors.draft-text}"
    rounded: "{rounded.square}"
    padding: "1px 6px"
  badge-published:
    textColor: "{colors.ok}"
    rounded: "{rounded.square}"
    padding: "1px 6px"
  data-grid:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text}"
    typography: "{typography.table}"
    rounded: "{rounded.square}"
  chart:
    textColor: "{colors.text3}"
    rounded: "{rounded.square}"
---

# Design System: Moneybag

## Overview

**Creative North Star: "The Moneybag workspace"**

The Moneybag workspace is square, quiet and dense: a practical finance interface for repeated reading, editing and comparison. Its visual authority is the supplied Moneybag artifact and the durable brand commitments in PRODUCT.md. The implementation uses flat paper and recessed surfaces, fine shared borders, compact controls and restrained semantic color.

The workspace follows the system light or dark preference. Responsive layouts preserve task access with a scrolling workspace navigation, single-column panels and locally scrolling data. Geist serves interface text, with a build-generated metric-adjusted Arial fallback; Geist Mono distinguishes operational labels, identifiers and code. There is no separate display-type role or decorative image layer.

**Key Characteristics:**

- Square geometry and shared 1px borders.
- Compact Geist typography with tabular financial numbers.
- Blue chart data, green within budget, red over budget and amber drafts.
- Flat light/dark surfaces and visible keyboard focus.
- Readable mobile charts with expandable semantic data tables.

Evidence: the SPA styles in `ui/src/index.css`, the shared controls in `ui/src/components/ui`, and chart rendering in `ui/src/features/dashboard`. Authentication and generated previews still use their server-side styles.

## Colors

Neutral paper and recessed surfaces carry most of the interface; color identifies data, financial meaning and interaction state. Frontmatter names retain the source custom-property names without their leading `--`; `-dark` identifies the corresponding dark-theme value. The theme follows the operating system preference by default; Settings can select light, dark or system.

### Primary

- **Data blue** (`c0`): links, the workspace marker and the first chart series. **Focus blue** (`focus`) identifies keyboard focus and field focus; **selection blue** (`sel`) marks text selection and cells being saved.

### Secondary

- **Within-limit green** (`ok`): favorable KPI values, within-budget bars and published badges.
- **Over-limit red** (`neg`): over-budget bars, errors, invalid cells and adverse values.
- **Draft amber** (`draft-border`, `draft-text`): the actual light-theme badge uses darker border and text values for legibility. In dark mode the badge text is `#e3bd6e`. The chart amber (`c1`) remains a separate data-series color.

### Tertiary

- **Cyan, violet and orange** (`c2`, `c3`, `c4`): subsequent chart series. The renderer cycles through `c0` to `c4`; legends preserve the series names.

### Neutral

- **Paper** (`bg`, `paper`): page, panels and editable cells. The two tokens share a value in each theme but keep separate source roles.
- **Recessed surface** (`sunk`): sidebar, table headers, calculated cells, formula bar and code surfaces.
- **Ink** (`text`, `ink`): primary text, filled controls, strong borders and active-navigation markers.
- **Supporting text** (`text2`) and **muted text** (`text3`): explanatory copy, metadata, captions and chart labels.
- **Frame border** (`bd`) and **cell divider** (`bd2`): panel boundaries and quieter internal rules.
- **Hover surface** (`hover`) and **scrollbar gray** (`other`): local interaction feedback without additional elevation.

**The Financial Meaning Rule.** Use green for within-limit or favorable values, red for over-limit or error values, and amber for drafts. Keep chart-series colors distinct from these status meanings.

## Typography

**Body Font:** Geist Variable, with metric-adjusted Arial and system sans fallbacks, recorded in `typography.body`.

**Label/Mono Font:** Geist Mono Variable with a system monospace fallback, recorded in `typography.label` and `typography.identifier`.

**Character:** compact, familiar application typography with operational metadata distinguished by monospace. Financial amounts use tabular numerals. There is no display-font role.

### Hierarchy

- **Headline:** page titles use 24px, weight 650, line-height 1.2 and -0.015em tracking.
- **Title:** section headings use 16px and weight 620; dashboard panel headings use 12px and weight 600.
- **Body and tables:** the root size is 15px; tables use 12px. Amounts and table values use tabular figures.
- **Labels and identifiers:** navigation groups and breadcrumbs use 10px Geist Mono at weight 600 and 0.07em tracking.
- **Data:** KPI values use 26px, weight 600 and -0.015em tracking; supporting text is 12px.
- **Controls:** shared buttons and inputs use the Geist interface font; sizes vary with the component variant and viewport.

**The Data Before Decoration Rule.** Keep operational labels and identifiers compact and monospace; keep financial figures tabular and preserve a visible title-to-value hierarchy.

## Layout

The desktop shell uses a sticky 248px sidebar and a flexible main column capped at 1440px, with `28px 40px 56px` content padding. Dashboard panels occupy a twelve-column grid with shared borders. Toolbars and legends wrap.

Between 761px and 1100px the sidebar becomes a 56px icon rail. At 760px and below a header opens the navigation in a sheet, and main padding becomes `24px 18px 48px`. Data tables and charts scroll locally instead of forcing page overflow. Charts are 240px high with a 360px minimum canvas width.

## Elevation & Depth

Paper, recessed surfaces and fine borders establish hierarchy. Overlays use ink borders without drop shadows; dialogs and sheets use a translucent backdrop.

### State Indicators

- **Active navigation:** inset ink stripe (`inset 3px 0 var(--ink)`) in the sidebar and mobile sheet.
- **Editable-cell focus:** inset blue stroke (`inset 0 0 0 2px var(--focus)`). Invalid cells use a narrower inset error stroke (`1px`).
- **Keyboard focus:** a blue outline (`2px`, offset `2px`) for elements whose own focus rules do not suppress it. Inputs and textareas use a focus-border shift; editable cells use their inset stroke.

**The Flat Workspace Rule.** Separate surfaces with tone and 1px borders. Inset active and focus indicators communicate state; they do not lift cards above the page.

## Shapes

All UI corners are square (`0`), enforced by the global radius rule, including native fields and dialogs. Shared `1px` borders form the dominant geometry. The workspace marker is a solid `24px` ink square; chart legends use `9px` swatches. Circles remain valid inside chart geometry, including line points and donut marks; the square rule governs interface containers and controls.

## Components

### Buttons

Compact, direct actions. Primary buttons use ink fill and page-color text. Ghost buttons have a transparent background and gain a muted background on hover. Disabled buttons reduce opacity to `.5`; keyboard focus uses a visible ring. Shared controls use restrained state transitions and honor reduced-motion preferences.

### Chips and Status Badges

Status labels use thin, square borders and retain status words alongside color. The draft badge uses 10px Geist Mono, `2px 5px` padding and the theme-specific amber treatment described in Colors.

### Cards / Containers

Dashboard panels use a paper surface, frame border and the observed panel padding. Panel headings are compact and muted. KPI values remain the strongest text inside the panel. No decorative shadow or rounded shell is used.

### Inputs / Fields

Paper fields with a frame border, square corners and compact insets. Labels remain visibly associated with each field. Text inputs and textareas switch border color on focus; text caret uses data blue. Spreadsheet cells save on Enter or blur; Escape cancels. Calculated cells use the recessed surface and stay read-only. Saving uses selection blue; validation uses the inset error stroke. Preserve native select/checkbox behavior rather than claiming a custom selected or invalid treatment that is absent.

### Navigation

The recessed sidebar uses 13px links and 10px monospace group labels. Hover uses the hover surface; the active link uses paper and an inset ink stripe. The tablet rail keeps labels accessible while visually hiding them. Mobile opens the same navigation in a sheet. Settings offers light, dark and system appearance.

### Data Grid

A collapsed-border table with quiet internal rules, recessed uppercase headers and right-aligned numeric cells. Editable cells use paper; calculated cells are recessed. The formula bar uses a monospace value field. Normal table padding is reduced to `7px 10px` inside the editing grid. Long values wrap; local horizontal overflow preserves useful column widths.

### Charts and Budget Bars

Charts use the five-color series cycle, faint horizontal grid lines, stable legend colors and direct labels for small series sets. A **Table** button switches to a labelled, keyboard-accessible data table. Charts scroll locally on narrow screens. Budget bars use green within limit and red over limit, with actual and limit values written alongside.

### Dialogs and Console

Dialogs and sheets use square paper surfaces and viewport-constrained content. The MCP console provides structured tool inputs and displays results in the workspace.

## Do's and Don'ts

### Do:

- **Do** use the final application theme overrides recorded in the frontmatter, including the stronger muted-text and financial-status colors.
- **Do** preserve square corners, fine shared borders and the observed compact spacing rhythm.
- **Do** keep amounts tabular and align numeric table columns to the right.
- **Do** preserve all workspace tasks on mobile through the navigation sheet and single-column panels.
- **Do** pair charts with labelled, keyboard-focusable scroll regions and the chart table toggle.
- **Do** use synthetic records in public examples and screenshots.

### Don't:

- **Don't** add rounded cards, pill controls or decorative drop shadows to this workspace.
- **Don't** replace the pinned Geist body and operational type with an unrelated display identity.
- **Don't** shrink mobile charts until their labels become unreadable; retain local chart scrolling.
- **Don't** communicate a financial status through color alone; retain values, labels and legends.
- **Don't** copy financial data or private reference material into public documentation.
