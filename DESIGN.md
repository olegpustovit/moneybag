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
  c1: "#f2b300"
  c2: "#1fb5cc"
  c3: "#9b6bdf"
  c4: "#e8743b"
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
  c0-dark: "#5b8cff"
  c1-dark: "#f5c242"
  c2-dark: "#3fcbe0"
  c3-dark: "#b48cf0"
  c4-dark: "#f08a55"
  other-dark: "#6c727c"
  ok-dark: "#4fc98a"
  neg-dark: "#f07272"
  focus-dark: "#5b8cff"
  sel-dark: "#17223a"
  draft-border: "#a16f00"
  draft-text: "#946600"
typography:
  headline:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
    fontSize: "24px"
    fontWeight: 650
    lineHeight: 1.2
    letterSpacing: "-0.015em"
  title:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
    fontSize: "18px"
    fontWeight: 620
    lineHeight: 1.3
    letterSpacing: "-0.01em"
  body:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.6
  table:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
    fontSize: "13.5px"
    fontWeight: 400
    lineHeight: 1.6
  label:
    fontFamily: "ui-monospace,SFMono-Regular,Menlo,Consolas,monospace"
    fontSize: "11px"
    fontWeight: 600
    letterSpacing: "0.06em"
  identifier:
    fontFamily: "ui-monospace,SFMono-Regular,Menlo,Consolas,monospace"
    fontSize: "12px"
    fontWeight: 400
  data:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
    fontSize: "26px"
    fontWeight: 600
    lineHeight: 1.6
    letterSpacing: "-0.015em"
  button:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
    fontSize: "13px"
    fontWeight: 500
  input:
    fontFamily: "system-ui,-apple-system,\"Segoe UI\",Roboto,\"Helvetica Neue\",Arial,sans-serif"
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
    padding: "7px 14px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    typography: "{typography.button}"
    rounded: "{rounded.square}"
    padding: "7px 14px"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.text}"
    typography: "{typography.input}"
    rounded: "{rounded.square}"
    padding: "7px 10px"
  navigation:
    backgroundColor: "{colors.sunk}"
    textColor: "{colors.text2}"
    rounded: "{rounded.square}"
    padding: "5px 14px"
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

The workspace follows the system light or dark preference. Responsive layouts preserve task access with a scrolling workspace navigation, single-column panels and locally scrolling data. System sans serves interface text; system monospace distinguishes operational labels, identifiers and code. There is no separate display-type role or decorative image layer.

**Key Characteristics:**

- Square geometry and shared 1px borders.
- Compact system typography with tabular financial numbers.
- Blue chart data, green within budget, red over budget and amber drafts.
- Flat light/dark surfaces and visible keyboard focus.
- Readable mobile charts with expandable semantic data tables.

Evidence: `backend/cmd/api/handlers/web/style.css`, `app.html`, `workflows.html`, `app.js`, and `backend/internal/components/dashboard/render.go`; reviewed synthetic captures in `.impeccable/review/desktop.png`, `dark.png` and `mobile.png`. The frontmatter records the final CSS cascade, rather than superseded reference declarations. No previous DESIGN.md existed at this handoff.

## Colors

Neutral paper and recessed surfaces carry most of the interface; color identifies data, financial meaning and interaction state. Frontmatter names retain the source custom-property names without their leading `--`; `-dark` identifies the corresponding dark-theme value. The theme follows the operating system preference; there is no in-app switcher.

### Primary

- **Data blue** (`c0`): links, the workspace marker and the first chart series. **Focus blue** (`focus`) identifies keyboard focus and field focus; **selection blue** (`sel`) marks text selection and cells being saved.

### Secondary

- **Within-limit green** (`ok`): favorable KPI values, within-budget bars and published badges.
- **Over-limit red** (`neg`): over-budget bars, errors, invalid cells and adverse values.
- **Draft amber** (`draft-border`, `draft-text`): the actual light-theme badge uses darker border and text values for legibility. Its dark counterpart uses `c1-dark` for both. The chart amber (`c1`) remains a separate data-series color.

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

**Body Font:** the system sans stack recorded in `typography.body`.

**Label/Mono Font:** the system monospace stack recorded in `typography.label` and `typography.identifier`.

**Character:** compact, familiar application typography with operational metadata distinguished by monospace. Financial amounts use tabular numerals. There is no display-font role.

### Hierarchy

- **Headline:** page title (`24px`, weight `650`, line height `1.2`, tracking `-0.015em`); reduces to `22px` at the mobile breakpoint.
- **Title:** settings section headings (`18px`, weight `620`, line height `1.3`). The global section heading is `21px`; dashboard grouping headings deliberately reduce to `13px`, weight `500`.
- **Body:** interface prose (`15px`, line height `1.6`). Supporting paragraphs cap at `760px`; dashboard notes use `14px` and line height `1.7`.
- **Table:** rows (`13.5px`); numeric data uses tabular numerals, with explicit right alignment for amount columns.
- **Label:** panel headers (`11px`, weight `600`, tracking `0.06em`, uppercase). Table headers use `0.05em`; sidebar group labels use `10.5px` and `0.07em`. These name operational groups and data panels.
- **Identifier:** breadcrumbs (`12px` monospace), reduced to `10px` on mobile; component IDs (`9px`) are supplementary and hidden below the tablet breakpoint.
- **Data:** KPI values (`26px`, weight `600`, tracking `-0.015em`), with `12px` supporting detail.
- **Controls:** buttons (`13px`, weight `500`), inputs (`13px`, normal weight).

**The Data Before Decoration Rule.** Keep operational labels and identifiers compact and monospace; keep financial figures tabular and preserve a visible title-to-value hierarchy.

## Layout

The application shell fills the viewport with no header: a sticky full-height `248px` sidebar carries the workspace mark, navigation with glyph icons and the version footer, beside a flexible main column (padding `28px 40px 56px`, content capped at `1440px`). The dashboard has twelve columns with `12px` gaps and component-defined spans. Toolbar actions wrap, with `8px` gaps.

The brand establishes a 4px spacing rhythm. The shipped controls also use compact optical insets (`7px 14px` buttons, `7px 10px` fields, `9px 14px` table cells); do not round these measured values into a fabricated uniform scale. Panels use `14px 16px` padding, reducing to `12px` at `1100px`.

- At `1100px` and below, the main padding becomes `20px`; budget-bar values move to the second grid row.
- At `860px` and below, the sidebar becomes `190px`, all dashboard panels span twelve columns, two/three-column form layouts stack, and outer padding becomes `16px 16px 32px`. The console stacks its editor and output.
- At `620px` and below, the shell becomes a block layout with horizontal scrolling navigation above the main content. Outer padding is `12px 12px 28px`, main padding is `20px 14px`, and dashboard gaps are `10px`. Group labels, sidebar footer and supplementary component IDs are omitted; creation actions remain in the scrolling navigation.

Charts use a `560 × 250` SVG view box. At the mobile breakpoint the SVG retains at least `560px` width inside its local scrolling region. Tables also scroll inside their own region instead of forcing page overflow. Dashboard chart/table regions carry accessible names and `tabindex="0"`. Do not infer that every workflow table already has the same region semantics.

## Elevation & Depth

The workspace is flat: no decorative drop shadows. Paper, recessed surfaces and fine borders establish hierarchy. Modal dialogs use a strong ink border and a translucent backdrop (`rgb(0 0 0/.35)`). The rollback form is positioned above its toolbar with a plain bordered paper surface.

### State Indicators

- **Active desktop navigation:** inset ink stripe (`inset 3px 0 0 var(--ink)`). On mobile the stripe moves to the lower edge (`inset 0 -2px 0 var(--ink)`).
- **Editable-cell focus:** inset blue stroke (`inset 0 0 0 2px var(--focus)`). Invalid cells use a narrower inset error stroke (`1px`).
- **Keyboard focus:** a blue outline (`2px`, offset `3px`) for elements whose own focus rules do not suppress it. Inputs and textareas use a focus-border shift; editable cells use their inset stroke.

**The Flat Workspace Rule.** Separate surfaces with tone and 1px borders. Inset active and focus indicators communicate state; they do not lift cards above the page.

## Shapes

All UI corners are square (`0`), enforced by the global radius rule, including native fields and dialogs. Shared `1px` borders form the dominant geometry. The logo is a solid `28px` ink square; the workspace marker is `8px`, navigation markers are `10px` outlined squares, and chart legends use `10px` swatches. Circles remain valid inside chart geometry, including line points and donut marks; the square rule governs interface containers and controls.

## Components

### Buttons

Compact, direct actions. Primary buttons use ink fill, page-color text, an ink border and the frontmatter padding. Ghost buttons preserve the border with a transparent fill and normal text. Hover reduces opacity to `.88`; disabled buttons reduce opacity to `.5` and use the waiting cursor. Buttons retain the shared keyboard focus outline. No movement or hover animation is added.

### Chips and Status Badges

Thin outlined, square monospace labels (`11px`, weight `500`, padding `1px 6px`). Chips use muted text and a frame border; permission variants use semantic border/text colors. Published badges use within-limit green; draft badges use the theme-specific amber treatment described in Colors. Status words remain present alongside color.

### Cards / Containers

Dashboard panels use a paper surface, frame border and the observed panel padding. Their header puts the compact uppercase data title opposite supplementary component metadata, which wraps and disappears at narrower widths. KPI values remain the strongest text inside the panel. No decorative shadow or rounded shell is used.

### Inputs / Fields

Paper fields with a frame border, square corners and compact insets. Labels remain visibly associated with each field. Text inputs and textareas switch border color on focus; text caret uses data blue. Spreadsheet cells save on Enter or blur; Escape cancels. Calculated cells use the recessed surface and stay read-only. Saving uses selection blue; validation uses the inset error stroke. Preserve native select/checkbox behavior rather than claiming a custom selected or invalid treatment that is absent.

### Navigation

The recessed sidebar has compact sans links (`13.5px`) and monospace group labels. Hover uses the hover surface and stronger text; the active link uses paper and an inset ink stripe, with `aria-current="page"`. Mobile presents the same links in a horizontally scrolling strip and moves the active stripe to the bottom. The theme switch is a three-part segmented control with an ink-filled selection and `aria-pressed` state.

### Data Grid

A collapsed-border table with quiet internal rules, recessed uppercase headers and right-aligned numeric cells. Editable cells use paper; calculated cells are recessed. The formula bar uses a monospace value field. Normal table padding is reduced to `7px 10px` inside the editing grid. Long values wrap; local horizontal overflow preserves useful column widths.

### Charts and Budget Bars

Charts use the five-color series cycle, subtle grid lines and compact monospace labels. Every rendered chart includes a native **View chart data** disclosure with a captioned semantic table containing its values; rules appear in that data as well. The chart SVG has its own accessible title, and the chart plus data-table scroll regions are labelled and keyboard-focusable. Preserve the mobile canvas width and scrolling behavior. Budget bars use green within limit, red over limit, a narrow ink limit marker, written actual/limit values and a legend.

### Dialogs and Console

Dialogs retain square paper geometry, ink borders and `28px` padding, with a viewport-constrained scrolling body and native modal behavior. The console is a bordered disclosure: compact operational header, recessed monospace editor, paper output, and wrapping footer actions. Its two-column body stacks at the tablet breakpoint.

## Do's and Don'ts

### Do:

- **Do** use the final application theme overrides recorded in the frontmatter, including the stronger muted-text and financial-status colors.
- **Do** preserve square corners, fine shared borders and the observed compact spacing rhythm.
- **Do** keep amounts tabular and align numeric table columns to the right.
- **Do** preserve all workspace tasks on mobile through scrolling navigation and single-column panels.
- **Do** pair charts with labelled, keyboard-focusable scroll regions and expandable semantic chart-data tables.
- **Do** use synthetic records in public examples and screenshots.

### Don't:

- **Don't** add rounded cards, pill controls or decorative drop shadows to this workspace.
- **Don't** replace the pinned system body and operational type with an unrelated display identity.
- **Don't** shrink mobile charts until their labels become unreadable; retain the scrolling chart canvas.
- **Don't** communicate a financial status through color alone; retain values, labels and legends.
- **Don't** copy financial data or private reference material into public documentation.
