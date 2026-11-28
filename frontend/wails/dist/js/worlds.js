/*
 * worlds.js — the Worlds tab loader (run 46168717789).
 *
 * Renders the REAL per-track multi-scene world plan served by
 * /api/worlds (internal/mvplan): the world identity, camera plan,
 * lighting plan, long-audio policy, chapters (scene spans) and world
 * elements. Every state is honest — no-track, analysis-pending, ready
 * and the legacy-only unavailability come verbatim from the facade;
 * nothing here invents a world, a scene or a measurement.
 */

import { fetchJSON, showAPIError } from "./api.js";
import { $, setText, clearNode, renderKV, formatDuration } from "./dom.js";
import { state } from "./state.js";

const MOTION_LABELS = {
    pulse: "pulse",
    orbit: "orbit",
    rise: "rise",
    drift: "drift",
    shatter: "shatter",
};

export async function loadWorlds() {
    try {
        const doc = await fetchJSON("/api/worlds");
        state.worlds = doc;
        renderWorlds(doc);
    } catch (err) {
        showAPIError(err.message);
    }
}

/* The 2 s tick refreshes the Worlds pane only while an analysis can be
 * in flight (track loaded, plan not ready yet) — bounded polling. */
export function worldNeedsPolling() {
    return !!(state.library && state.library.trackLoaded
        && (!state.worlds || state.worlds.state === "pending"));
}

function renderWorlds(doc) {
    const chip = $("worlds-state");
    const content = $("worlds-content");
    if (!chip) return;

    setText($("worlds-desc"), doc.trackName ? `World plan for ${doc.trackName}` : "");

    if (doc.state !== "ready") {
        chip.hidden = false;
        chip.textContent = String(doc.note || "No world plan available.");
        if (content) content.hidden = true;
        return;
    }

    chip.hidden = true;
    if (content) content.hidden = false;
    const plan = doc.plan || {};

    renderKV($("worlds-identity"), [
        ["Descriptor", String(plan.descriptor || "—")],
        ["Duration", plan.duration ? formatDuration(Number(plan.duration)) : "—"],
        ["Tempo", Number(plan.tempoBpm) > 0 ? `${Number(plan.tempoBpm).toFixed(1)} BPM` : "not measured"],
        ["Chapters", String((plan.chapters || []).length)],
        ["Elements", String((plan.elements || []).length)],
        ["Seed", String(plan.seed ?? "—")],
        ["Plan saved", doc.planPath ? "yes (world-plans)" : "not saved"],
    ]);

    const cam = plan.camera || {};
    renderKV($("worlds-camera"), [
        ["Style", String(cam.style || "—")],
        ["Beat synced", cam.beatSynced ? "yes (cuts quantized to measured tempo)" : "energy-synced only"],
        ["Cut cadence", cam.cutCadenceSec ? `${Number(cam.cutCadenceSec).toFixed(1)} s between cuts` : "—"],
        ["Beat period", Number(cam.beatPeriodSec) > 0 ? `${Number(cam.beatPeriodSec).toFixed(2)} s` : "unknown"],
    ]);

    const light = plan.lighting || {};
    renderKV($("worlds-lighting"), [
        ["Palette index", String(light.paletteIndex ?? "—")],
        ["Base hue", light.baseHue !== undefined ? Number(light.baseHue).toFixed(2) : "—"],
        ["Accent", String(light.accent || "—")],
        ["Glow", light.glow !== undefined ? Number(light.glow).toFixed(2) : "—"],
    ]);

    const long = plan.longAudio || {};
    renderKV($("worlds-longaudio"), [
        ["Strategy", String(long.strategy || "—")],
        ["Estimated frames", String(long.estimatedFrames ?? "—")],
        ["Memory bound", String(long.analysisMemoryBound || "—")],
        ["Within export bound", long.withinExportBound ? "yes" : "no"],
    ]);

    renderChapters($("worlds-chapters"), plan.chapters || []);
    renderElements($("worlds-elements"), plan.elements || []);

    const notes = plan.notes || [];
    const noteEl = $("worlds-note");
    if (noteEl) {
        noteEl.textContent = notes.length
            ? `Plan notes: ${notes.join(" ")}`
            : "The plan is deterministic for this track: the same analysis always rebuilds the same world.";
    }
}

function renderChapters(list, chapters) {
    if (!list) return;
    clearNode(list);
    if (!chapters.length) {
        const li = document.createElement("li");
        li.className = "worlds-empty";
        li.textContent = "No chapters were detected (single-scene fallback).";
        list.appendChild(li);
        return;
    }
    for (const c of chapters) {
        const li = document.createElement("li");
        li.className = "worlds-chapter";
        const head = document.createElement("div");
        head.className = "worlds-chapter-head";
        const name = document.createElement("span");
        name.className = "worlds-chapter-name";
        name.textContent = `${Number(c.index) + 1}. ${String(c.name || "scene")}`;
        const span = document.createElement("span");
        span.className = "worlds-chapter-span";
        span.textContent = `${formatTime(Number(c.start))} – ${formatTime(Number(c.end))}`;
        head.appendChild(name);
        head.appendChild(span);
        const meta = document.createElement("div");
        meta.className = "worlds-chapter-meta";
        const bits = [
            String(c.environment || "environment unknown"),
            String(c.energy || "energy unknown"),
            Number.isFinite(Number(c.confidence)) ? `confidence ${Math.round(Number(c.confidence) * 100)}%` : "",
            c.floor ? `floor ${c.floor}` : "",
        ].filter(Boolean);
        meta.textContent = bits.join(" · ");
        li.appendChild(head);
        li.appendChild(meta);
        list.appendChild(li);
    }
}

function renderElements(list, elements) {
    if (!list) return;
    clearNode(list);
    const note = $("worlds-elements-note");
    if (!elements.length) {
        const li = document.createElement("li");
        li.className = "worlds-empty";
        li.textContent = "No world elements were placed.";
        list.appendChild(li);
        if (note) note.textContent = "";
        return;
    }
    // Bounded display: the plan caps elements per chapter and total;
    // the UI shows them all up to the plan's own bound and says so.
    const shown = elements.slice(0, 120);
    for (const e of shown) {
        const li = document.createElement("li");
        li.className = "worlds-element";
        const kind = document.createElement("span");
        kind.className = `worlds-element-kind energy-${e.energy || "calm"}`;
        kind.textContent = String(e.kind || "element");
        const desc = document.createElement("span");
        desc.className = "worlds-element-desc";
        const bits = [
            MOTION_LABELS[e.motion] || String(e.motion || ""),
            String(e.energy || ""),
            e.reactive ? "audio-reactive" : "ambient",
            `chapter ${(Number(e.chapter) || 0) + 1}`,
        ].filter(Boolean);
        desc.textContent = bits.join(" · ");
        li.appendChild(kind);
        li.appendChild(desc);
        list.appendChild(li);
    }
    if (note) {
        note.textContent = shown.length < elements.length
            ? `Showing ${shown.length} of ${elements.length} elements (bounded display).`
            : `${elements.length} elements placed by the deterministic plan.`;
    }
}

function formatTime(sec) {
    const s = Math.max(0, Math.round(Number(sec) || 0));
    const m = Math.floor(s / 60);
    return `${m}:${String(s % 60).padStart(2, "0")}`;
}
