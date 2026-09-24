# 028: pf-ui — platform design tokens + component kit

## Motivation

Five hand-rolled UI surfaces in three tech shapes (Vue SPA ×2, single-file HTML ×2, Python-inlined ×1) each carry private styles: no central management, drifting details, no upgrade path. A platform needs one UI contract.

## Design

### Component form: native Web Components + token CSS (explicitly NOT a Vue library)

The estate spans Vue / static HTML / inlined HTML, and third-party plugin consoles can be any stack. A Vue component lib would either drag zero-build surfaces into a build pipeline or a 200KB in-browser compiler. Custom elements (light DOM, zero deps, zero build) work identically in all of them — Vue templates use `<pf-button>` natively (`isCustomElement` config). Token layer is plain CSS custom properties with a `data-pf-theme="dark"` override.

### Distribution: files-as-truth, gateway same-origin

- Source of truth: `platform/ui/v1/` (pf-ui.css / pf-ui.js / demo.html) in the platfarm repo.
- Served by the console container (`pctl serve` statically serves `$ROOT/platform/ui` with 1h cache) behind a public gateway route `/platform/ui` (generated in kong.yml).
- Upgrade = new versioned dir (`v2/`); surfaces migrate at their own pace; contract changes require a spec.

### Contract

Tokens, class set, element/API inventory: [docs/ui-spec.md](../../../docs/ui-spec.md). `demo.html` is the living visual acceptance standard.

### Migration (this change)

All five first-party surfaces adopt pf-ui and delete their private global styles: pctl console shell (dark), svc-users console, svc-openapi console (Vue), account center, svc-demo console.

## Blast radius

`platform/ui/` (new), pctl (serve static route + kong route), the five surfaces' markup/styles. No API/backend behavior changes anywhere.

## Acceptance

- `/platform/ui/v1/demo.html` renders all components in light + dark.
- Each migrated surface loads pf-ui from `/platform/ui/v1/`, retains full functionality (browser-verified), and contains no hard-coded palette values.
- `pctl check --e2e` green (no backend regression).
