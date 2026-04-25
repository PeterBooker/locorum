---
name: add-ui-component
description: Add a new Gio UI component to Locorum — use when the user asks to add a new panel, modal, button group, or UI screen inside internal/ui/
user-invocable: true
---

# Add a UI Component

Locorum's UI is in `internal/ui/`. Each file is one component. Components follow a consistent pattern.

## Pattern

### 1. Create the file

`internal/ui/mycomponent.go` (lowercase, no hyphens).

### 2. Define the component struct

Persistent widget state lives in the struct — it must outlive individual `Layout` calls.

```go
package ui

import (
    "gioui.org/layout"
    "gioui.org/widget"
    "gioui.org/widget/material"

    "github.com/PeterBooker/locorum/internal/sites"
)

type MyComponent struct {
    state  *UIState
    sm     *sites.SiteManager
    toasts *ToastManager

    // Persistent widget state — never re-allocate these per frame.
    saveBtn  widget.Clickable
    input    widget.Editor
    list     widget.List
}

func NewMyComponent(state *UIState, sm *sites.SiteManager, toasts *ToastManager) *MyComponent {
    c := &MyComponent{state: state, sm: sm, toasts: toasts}
    c.input.SingleLine = true
    c.list.List.Axis = layout.Vertical
    return c
}
```

Only take the dependencies you actually use. Most components need `*UIState` and `*sites.SiteManager`. Take `*ToastManager` if you want to surface success/error toasts.

### 3. Implement `Layout`

```go
func (c *MyComponent) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
    // Handle events once per frame.
    if c.saveBtn.Clicked(gtx) {
        c.handleSave()
    }

    return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
        layout.Rigid(func(gtx layout.Context) layout.Dimensions {
            return LabeledInput(gtx, th, "Name", &c.input, "Enter a name")
        }),
        layout.Rigid(func(gtx layout.Context) layout.Dimensions {
            return layout.Inset{Top: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
                return PrimaryButton(gtx, th, &c.saveBtn, "Save")
            })
        }),
    )
}
```

### 4. Use project widget helpers

Don't call `material.Button`/`material.Editor` directly with custom colors. Use the helpers in `internal/ui/widgets.go`:

| Need | Helper |
|---|---|
| Primary action button (cyan) | `PrimaryButton` |
| Secondary button | `SecondaryButton` |
| Destructive button (pink) | `DangerButton` |
| Success button (green) | `SuccessButton` |
| Small inline button | `SmallButton` |
| Labeled text input | `LabeledInput` |
| Bordered editor alone | `BorderedEditor` |
| Dropdown | `NewDropdown([]string)` + `d.Layout(gtx, th, label)` |
| Tab bar | `TabBar(gtx, th, labels, active, clicks)` |
| Status badge | `StatusBadge(gtx, th, started)` |
| Section with title | `Section(gtx, th, "Title", contentFn)` |
| Key/value rows | `KVRows(gtx, th, []KV{...})` |
| Output / log panel | `OutputArea(gtx, th, &list, text, placeholder, maxHeight)` |
| Loading spinner | `Loader(gtx, th, LoaderSize)` |
| Background fill | `FillBackground(gtx, col, contentFn)` |
| Horizontal divider | `Divider(gtx, ColorBorder, SpaceSM)` |
| Modal shell | `ModalOverlay(gtx, contentFn)` (from `modal.go`) |
| Confirm dialog | `ConfirmDialog` struct + `cd.Layout(gtx, th, ConfirmDialogStyle{...})` |
| Copy text to clipboard | `CopyToClipboard(gtx, text)` |
| Selectable text | `SelectableLabel(gtx, th, &sel, text, size, col)` |

### 5. Use theme constants

From `internal/ui/theme.go`:

- Spacing: `SpaceXS`, `SpaceSM`, `SpaceMD`, `SpaceLG`, `SpaceXL`, `Space2XL`
- Text sizes (all ≥ 18sp for accessibility): `TextXS`, `TextSM`, `TextBase`, `TextLG`
- Radii: `RadiusSM`, `RadiusMD`, `RadiusLG`
- Layout: `SidebarWidth`, `ModalWidth`, `LoaderSize`, `LabelColWidth`, `OutputAreaMax`
- Colors: `ColorBlue600` (primary), `ColorRed600` (danger), `ColorGreen600` (success), `ColorGold` (brand), `ColorGray*` (surfaces/text), `ColorSidebarBg`, `ColorContentBg`, `ColorModalBg`

Don't invent raw colors or `unit.Dp(12)` values — use the named constants so the app stays visually consistent.

### 6. Wire into the UI root

`internal/ui/ui.go`:

```go
type UI struct {
    ...
    MyComponent *MyComponent
    ...
}

func New(sm *sites.SiteManager) *UI {
    ...
    ui.MyComponent = NewMyComponent(state, sm, ui.Toasts)
    ...
}

func (ui *UI) Layout(gtx layout.Context) layout.Dimensions {
    // Call from wherever it fits — inside the content panel, in a modal stack, etc.
    ui.MyComponent.Layout(gtx, ui.Theme)
}
```

If it's a modal, return it from the modal overlay `layout.Stacked` in `ui.Layout`, conditional on a flag in `UIState`.

### 7. Background ops (if any)

Never call `SiteManager` methods from inside `Layout` — they can block. Spawn a goroutine:

```go
func (c *MyComponent) handleSave() {
    name := c.input.Text()
    c.state.SetSiteToggling(id, true)
    go func() {
        err := c.sm.DoSomething(name)
        c.state.SetSiteToggling(id, false)
        if err != nil {
            c.toasts.ShowError("Save failed: " + err.Error())
            return
        }
        c.toasts.ShowSuccess("Saved")
    }()
}
```

`UIState` setters already lock internally and call `Invalidate()`. Don't touch `state.mu` from outside `state.go`.

### 8. Backend hook-up

If the component needs new business logic, add a method on `SiteManager` in `internal/sites/sites.go` — don't call `storage` or `docker` packages from the UI directly. That boundary exists so future non-UI frontends (CLI, API) can reuse the same logic.

### 9. Verify

```bash
go vet ./...
go test ./...
go run .
```

You can't unit-test Gio layouts meaningfully — verify by running the app and interacting with the UI. If you can't run it (headless environment, no display), **say so** instead of claiming the component works.

## Checklist

- [ ] Struct holds persistent widget state (`widget.*` fields in the struct, not locals)
- [ ] `Layout(gtx layout.Context, th *material.Theme) layout.Dimensions` signature matches other components
- [ ] Uses widget helpers from `widgets.go`, not raw `material.*` with custom colors
- [ ] Uses theme constants, not raw `unit.Dp()` / `unit.Sp()` / `color.NRGBA{}`
- [ ] Background ops run in goroutines, surface results via `UIState` + `toasts`
- [ ] Wired into `ui.go` (`UI` struct field, constructor call, `Layout` call)
- [ ] New business logic (if any) lives in `SiteManager`, not in the UI package
- [ ] Minimum 18sp text for accessibility
- [ ] `go vet ./...` passes
