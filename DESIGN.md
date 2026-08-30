# Design Reference — Linear-derived PC Builder Workspace

## Source reference

- Confirmed primary system: Linear design analysis.
- Source DESIGN.md:https://github.com/VoltAgent/awesome-design-md/blob/main/design-md/linear.app/DESIGN.md
- Preview:https://getdesign.md/design-md/linear.app/preview
- Match decision:91% for an information-dense, precise, two-pane AI configuration workspace.

This file preserves the confirmed reference and the traits selected from it. It is an adapted project design system, not a copy of Linear's brand. Do not use Linear's name, logo, product copy, screenshots, custom fonts, or proprietary assets in the product UI.

## Visual thesis

A calm, technical dark workspace built from near-black surfaces, crisp typography, hairline separation, and one restrained violet-blue action color. Hardware configuration, price truth, validation status, and version changes carry the visual hierarchy; decoration never competes with them.

## Selected reference traits

- Dense but readable product workspace.
- Near-black canvas with a small surface ladder instead of shadows.
- One primary chromatic accent, used sparingly.
- Compact controls with 8px radii.
- Strong typography and negative tracking only on large display text.
- Hairline borders and whitespace instead of heavy containers.
- Product content is the protagonist.

## Intentional adaptations

- The source's almost pure black canvas is lifted to #0b0c0f for long Chinese reading sessions.
- Validation needs success, review, error, and unknown semantic colors in addition to the primary accent.
- Geist replaces unavailable brand fonts; Chinese uses system sans fallbacks.
- The product is an application, so no marketing hero or screenshot-led landing composition is used.
- Chat is visually quiet and integrated into the workspace rather than rendered as oversized bubbles.
- Hardware product images are omitted until a licensed, stable data source exists.

## Tokens

### Color

| Token | Value | Role |
|---|---|---|
| canvas | #0b0c0f | Page and app background |
| surface-1 | #111318 | Primary panels |
| surface-2 | #171a21 | Raised/selected panels |
| surface-3 | #1d2028 | Menus and active nested surfaces |
| hairline | #272b35 | Default 1px separation |
| hairline-strong | #3a4050 | Focused/selected borders |
| ink | #f2f3f5 | Primary text |
| ink-muted | #a6abb6 | Secondary text |
| ink-subtle | #747b89 | Metadata and disabled text |
| primary | #737de8 | Primary action, selection, focus |
| primary-hover | #8992f0 | Hovered primary action |
| primary-pressed | #626bd2 | Pressed primary action |
| success | #45a66b | pass |
| review | #d39a45 | warning/review |
| error | #d85c66 | fail/error |
| unknown | #8f96a3 | missing-data/unknown |

Semantic colors never replace text or icons. Large tinted status backgrounds are prohibited; use compact indicators, left rules, or subtle 8–12% tints.

浅色主题使用独立表面阶梯：canvas `#f6f7f9`、surface-1 `#ffffff`、surface-2 `#eef0f4`、surface-3 `#ffffff`、hairline `#d8dce5`、ink `#17191f`、ink-subtle `#626b7b`、review `#7d4c00`。浅色语义色必须达到正文对比度，不允许把深色 token 机械反相。默认偏好为「系统」，显式「深色／浅色」选择保存在浏览器本地。

### Typography

- UI/display:Geist Sans, Inter, system-ui, -apple-system, Segoe UI, PingFang SC, Microsoft YaHei, sans-serif.
- Mono:Geist Mono, JetBrains Mono, ui-monospace, SFMono-Regular, Consolas, monospace.
- Use mono only for SKU, IDs when intentionally exposed, version tokens, and technical code—not ordinary prices or body copy.
- Default body:14px/1.55 on desktop,15px/1.6 on mobile.
- UI label:12px/1.4 or 13px/1.4,weight 500.
- Section title:18px/1.3,weight 600,tracking -0.2px.
- Workspace title:24px/1.2,weight 600,tracking -0.5px.
- Marketing-scale 56–80px headings from the source reference are not used in the app.

### Spacing and shape

- Base unit:4px.
- Common spacing:4,8,12,16,24,32,48.
- Control radius:8px.
- Interactive panel radius:12px.
- Oversized radius and pill buttons are prohibited except status tags and compact filters.
- Default border:1px hairline.
- Shadows:none by default; modal overlay may use one restrained shadow.

## Surface model

| Level | Treatment | Use |
|---|---|---|
| Canvas | canvas, no border | App floor |
| Panel | surface-1 + hairline edge | Chat and inspector regions |
| Selected | surface-2 + primary/strong edge | Active version, tab, focused row |
| Overlay | surface-3 + restrained shadow | Dialog, menu, drawer |

Never create depth by stacking multiple bordered cards inside bordered cards. Prefer sections, dividers, rows, and definition lists.

## Signature components

### Workspace top bar

56px high, canvas/surface-1, one bottom hairline. Brand and session title left; health, export, and share right. The brand is text-only until an original identity is created.

### Chat stream

Messages are grouped by spacing and subtle surface changes. No colorful avatars, giant speech bubbles, or per-message drop shadows. Agent progress occupies one stable row.

### Requirement confirmation

The requirement object is a real interaction surface and may use a 12px bordered panel. Group fields by core/use case/preference/constraint, keep Save and Confirm distinct, and surface schema errors inline.

### Part list

Use eight compact rows in canonical category order. Each row contains category, product name/SKU, price, rationale, and optional change action. No placeholder image boxes.

### Validation list

Twelve ordered rows, using icon + text + semantic token. Unknown and warning must be visually distinguishable. The disclaimer stays visible beneath the list.

### Version timeline and diff

Version tokens may use compact rounded tags. Changed rows get a one-time tint and stronger left rule; unchanged rows stay visible at lower emphasis.

## Motion

- Agent progress enter/update:160–200ms opacity with at most 4px translation.
- Inspector version transition:160–220ms opacity/translation.
- Diff highlight:one 300ms tint settle,never looping.
- Standard hover/press:100–160ms.
- Disable nonessential transitions for prefers-reduced-motion.

## Responsive model

- ≥1280px:42/58 chat-inspector workspace.
- 1024–1279px:two panes,session navigation in drawer.
- 768–1023px:single visible pane selected by Chat/Build tabs.
- <768px:mobile single column,bottom composer,build subtabs.
- Touch targets are at least 44×44px on touch layouts.

## Do

- Make price date, validation status, version, and budget delta easy to scan.
- Use the primary color only for actions, selection, links, and focus.
- Use semantic colors only for actual semantic state.
- Preserve all eight part categories and all twelve validation rows.
- Keep utility copy short, factual, and actionable.
- Prefer rows, dividers, and whitespace over card grids.

## Do not

- Do not use Linear logos, screenshots, custom fonts, or product copy.
- Do not use a pure-black canvas or low-contrast gray body text.
- Do not add decorative gradients, glassmorphism, glow, or spotlight backgrounds.
- Do not turn every section into a card.
- Do not use color alone for pass/review/fail/unknown.
- Do not invent Agent stages or display hidden reasoning.
- Do not hide snapshot dates or disclaimers behind collapsed UI.
