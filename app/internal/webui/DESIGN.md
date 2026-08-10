---
version: alpha
name: sennit-web
description: shadcn-aligned neutral design system for the sennit session web UI
colors:
  background: "#ffffff"
  foreground: "#0a0a0a"
  card: "#ffffff"
  card-foreground: "#0a0a0a"
  primary: "#171717"
  primary-foreground: "#fafafa"
  secondary: "#f5f5f5"
  secondary-foreground: "#171717"
  muted: "#f5f5f5"
  muted-foreground: "#737373"
  accent: "#f5f5f5"
  accent-foreground: "#171717"
  destructive: "#dc2626"
  destructive-foreground: "#fafafa"
  border: "#e5e5e5"
  input: "#e5e5e5"
  ring: "#a1a1a1"
typography:
  body:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.5
  heading:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: 18px
    fontWeight: 600
    lineHeight: 1.4
  label:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: 12px
    fontWeight: 500
    lineHeight: 1.3
rounded:
  sm: 6px
  md: 8px
  lg: 10px
  full: 9999px
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: 12px
  card:
    backgroundColor: "{colors.card}"
    textColor: "{colors.card-foreground}"
    rounded: "{rounded.lg}"
    padding: 12px
  badge:
    typography: "{typography.label}"
    rounded: "{rounded.full}"
    padding: 4px
---

## Overview

A neutral, shadcn-aligned system for a single-user control plane. Optimized for
clarity and quick scanning of session state on both desktop and mobile. Depth is
expressed through borders and tonal layers rather than heavy shadows.

## Colors

- **background / foreground** — page surface and primary text.
- **card** — session rows and panels sit on `card` with a `border`.
- **primary** — primary actions (e.g. create/up). `primary-foreground` for its text.
- **muted / muted-foreground** — secondary metadata (branch, GitHub status).
- **destructive** — destroy and other irreversible actions.
- **border / input / ring** — separators, field borders, focus ring.

Session status badges reuse Tailwind's built-in palette (green/amber/red/gray)
and are defined in code (`statusClass`), not as semantic tokens.

## Typography

A single system sans stack (no web-font download). `heading` for the page title,
`body` for content, `label` for badges and small metadata.

## Layout

Single column on mobile (≤640px); rows expand to a space-between flex layout from
`sm` up. Generous tap targets for phone use over a private network.

## Elevation & Depth

Prefer 1px `border` + `card` background over shadows. Reserve shadow for true
overlays (dialogs) only.

## Shapes

`rounded.md` (8px) is the default for buttons/inputs; `rounded.lg` for cards;
`rounded.full` for badges.

## Components

- **button-primary** — `primary` background, `primary-foreground` text, `md` radius.
- **card** — `card` background, `border`, `lg` radius.
- **badge** — `label` typography, `full` radius, status color from code.

## Do's and Don'ts

- Do keep WCAG AA contrast (≥4.5:1 for normal text); `muted-foreground` is the floor.
- Do rely on native elements (`<dialog>`, `<select>`) for interactive a11y.
- Don't introduce shadows for non-overlay surfaces.
- Don't hardcode hex in templates — reference tokens via Tailwind utilities.
