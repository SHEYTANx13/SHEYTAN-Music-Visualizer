/*
 * dom.js — tiny DOM utilities of the SHEYTAN web shell.
 *
 * Every dynamic value becomes text content — no HTML is ever built
 * from API data (XSS-safe by construction).
 */

export const $ = (id) => document.getElementById(id);

export function setText(node, text) {
    if (node) node.textContent = text;
}

export function clearNode(node) {
    while (node && node.firstChild) node.removeChild(node.firstChild);
}

/* Renders {key: value} rows into a <dl>. */
export function renderKV(dl, pairs) {
    if (!dl) return;
    clearNode(dl);
    for (const [key, value] of pairs) {
        const dt = document.createElement("dt");
        dt.textContent = key;
        const dd = document.createElement("dd");
        dd.textContent = value;
        dl.appendChild(dt);
        dl.appendChild(dd);
    }
}

/* Busy-state helper: one canonical visual + a11y treatment for any
 * button running a real backend operation. Never fakes completion:
 * the caller clears it when the API answers (or fails). */
export function setBusy(btn, busy, labelWhileBusy) {
    if (!btn) return;
    if (busy) {
        if (!btn.dataset.originalLabel) btn.dataset.originalLabel = btn.textContent;
        btn.classList.add("busy");
        btn.disabled = true;
        if (labelWhileBusy) btn.textContent = labelWhileBusy;
    } else {
        btn.classList.remove("busy");
        btn.disabled = false;
        if (btn.dataset.originalLabel) btn.textContent = btn.dataset.originalLabel;
    }
}

export function formatDuration(nanos) {
    const total = Math.max(0, Math.floor(nanos / 1e9));
    const m = Math.floor(total / 60);
    const s = total % 60;
    return `${m}:${String(s).padStart(2, "0")}`;
}

export function fmtBytes(n) {
    if (!Number.isFinite(n) || n <= 0) return "\u2014";
    const units = ["B", "KiB", "MiB", "GiB"];
    let v = n;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i += 1;
    }
    return (i === 0 ? v : v.toFixed(1)) + " " + units[i];
}

/* presetParamText renders the real scalar fields of a preset config as
 * one bounded text line ("key=value" pairs). Objects and arrays are
 * summarized as "(composed)" so the line never explodes. */
export function presetParamText(config) {
    if (!config || typeof config !== "object") return "";
    const parts = [];
    for (const [k, v] of Object.entries(config)) {
        if (k === "name") continue;
        if (v === null || v === undefined) continue;
        if (typeof v === "object") {
            parts.push(k + "=(composed)");
        } else {
            parts.push(k + "=" + String(v));
        }
        if (parts.length >= 6) break;
    }
    return parts.join("  ");
}

/* factText/quantityText render sysprofile facts with provenance:
 * unknown facts carry their exact reason instead of a guess. */
export function factText(f) {
    if (!f || typeof f !== "object") return String(f);
    if (f.value !== undefined && f.value !== "") return String(f.value);
    if (f.state === "failed") return "failed: " + (f.reason || "no reason given");
    return f.reason ? "unknown (" + f.reason + ")" : "unknown";
}

export function quantityText(q) {
    if (!q || typeof q !== "object") return String(q);
    if (q.value !== undefined && q.value !== 0) return String(q.value);
    return q.reason ? "unknown (" + q.reason + ")" : "unknown";
}
