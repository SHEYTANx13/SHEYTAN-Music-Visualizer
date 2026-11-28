/*
 * tokens.js — canonical design-token application.
 *
 * Injects the canonical tokens (tokens.json, byte-identical to
 * design/tokens.json and guarded by a Go test) as CSS custom
 * properties, and seeds the intensity-dependent variables from the
 * canonical presets. Fallbacks in css/base.css keep the page readable
 * when the token fetch fails.
 */

import { showAPIError } from "./api.js";
import { state } from "./state.js";

let tokenDoc = null;

export function tokenDocOrNull() {
    return tokenDoc;
}

export async function applyTokens() {
    let doc;
    try {
        const res = await fetch("tokens.json");
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        doc = await res.json();
    } catch (err) {
        showAPIError(`design tokens: ${err.message}`);
        return;
    }
    tokenDoc = doc;
    const root = document.documentElement.style;
    const c = doc.color || {};
    const set = (name, value) => {
        if (value) root.setProperty(name, value);
    };
    for (const [k, v] of Object.entries(c.surface || {})) set(`--c-surface-${k}`, v);
    for (const [k, v] of Object.entries(c.accent || {})) set(`--c-accent-${k}`, v);
    for (const [k, v] of Object.entries(c.secondary || {})) set(`--c-secondary-${k}`, v);
    for (const [k, v] of Object.entries(c.text || {})) set(`--c-text-${k}`, v);
    for (const [k, v] of Object.entries(c.line || {})) set(`--c-line-${k}`, v);
    for (const [k, v] of Object.entries(c.status || {})) set(`--c-status-${k}`, v);
    const motion = doc.motion || {};
    for (const [k, v] of Object.entries(motion.durationMs || {})) set(`--motion-${k}`, `${v}ms`);
    for (const [k, v] of Object.entries(motion.easing || {})) set(`--motion-easing-${k}`, v);
    const focus = (doc.accessibility || {}).focusRing || {};
    set("--focus-ring-color", focus.color);
    set("--focus-ring-width", focus.widthDp ? `${focus.widthDp}px` : null);
    set("--focus-ring-offset", focus.offsetDp ? `${focus.offsetDp}px` : null);
    const radius = doc.radius || {};
    for (const [k, v] of Object.entries(radius)) set(`--radius-${k}`, `${v}px`);
    const bp = doc.breakpoints || {};
    if (bp.tablet && bp.tablet.minDp) set("--bp-tablet", `${bp.tablet.minDp}px`);
    if (bp.desktop && bp.desktop.minDp) set("--bp-desktop", `${bp.desktop.minDp}px`);
    if (bp.wide && bp.wide.minDp) set("--bp-wide", `${bp.wide.minDp}px`);
    window.__sheytanTokens = doc;
    applyIntensityDefaults(doc);
}

/* Seeds intensity-dependent CSS variables from the canonical presets. */
export function applyIntensityDefaults(doc) {
    const d = doc || window.__sheytanTokens;
    if (!d || !d.intensityPresets) return;
    const intensity = (state.settings && state.settings.intensity) || d.defaultIntensity;
    const preset = d.intensityPresets[intensity];
    if (!preset) return;
    const root = document.documentElement.style;
    root.setProperty("--decoration-opacity", String(preset.decorationOpacity));
    root.setProperty("--glow-strength", String(preset.glowStrength));
    document.body.classList.toggle("no-embers", preset.embers === false);
    document.body.classList.toggle("reduced-motion", !!(state.settings && state.settings.reducedMotion));
    document.body.classList.toggle("reduced-glow", !!(state.settings && state.settings.reducedGlow));
}
