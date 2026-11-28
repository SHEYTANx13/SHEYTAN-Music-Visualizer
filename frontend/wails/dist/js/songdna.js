/*
 * songdna.js — the Song DNA pane (repair/run-46033044814).
 *
 * Renders the REAL per-track music DNA served by /api/songdna: the
 * measured DSP features (brightness, warmth, punchiness, rhythmic
 * drive, loudness) and the artistic defaults derived from them
 * (mode, palette, motion, pulse response, smoothing, particle
 * density, glow, tilt). States are honest: no-track, pending and
 * ready come verbatim from the facade; the bars render exactly those
 * 0..1 values and never synthesize data.
 */

import { fetchJSON, showAPIError } from "./api.js";
import { $, setText, clearNode } from "./dom.js";
import { state } from "./state.js";

const FEATURES = [
    ["brightness", "Brightness"],
    ["warmth", "Warmth"],
    ["punchiness", "Punch"],
    ["rhythmicDrive", "Drive"],
    ["loudness", "Loudness"],
];

export async function loadSongDNA() {
    try {
        const doc = await fetchJSON("/api/songdna");
        state.dna = doc;
        renderSongDNA(doc);
    } catch (err) {
        showAPIError(err.message);
    }
}

function renderSongDNA(doc) {
    const chip = $("dna-state");
    if (chip) {
        chip.textContent = String(doc.state || "unknown").replace(/-/g, " ");
        chip.dataset.state = String(doc.state || "unknown");
    }

    setText($("dna-note"), doc.note || "");
    setText($("dna-track"), doc.track && doc.track.name ? `track: ${doc.track.name}` : "");

    const grid = $("dna-features");
    if (!grid) return;
    clearNode(grid);

    const dna = doc.state === "ready" && doc.dna ? doc.dna : null;
    for (const [key, label] of FEATURES) {
        const value = dna ? Number(dna[key]) : null;
        grid.appendChild(featureCard(label, value));
    }

    if (dna) {
        renderKV($("dna-defaults"), [
            ["Default mode", String(dna.defaultMode)],
            ["Palette index", String(dna.paletteIndex)],
            ["Motion style", String(dna.motionStyle)],
            ["Pulse response", Number(dna.pulseResponse).toFixed(2)],
            ["Smoothing", Number(dna.smoothing).toFixed(2)],
            ["Particle density", Number(dna.particleDensity).toFixed(2)],
            ["Glow intensity", Number(dna.glowIntensity).toFixed(2)],
            ["Spectrum tilt", Number(dna.spectrumTilt).toFixed(2)],
        ]);
        renderKV($("dna-measured"), [
            ["Mean centroid", `${Number(doc.measured.meanCentroidHz).toFixed(0)} Hz`],
            ["Mean rolloff", `${Number(doc.measured.meanRolloffHz).toFixed(0)} Hz`],
            ["Mean RMS", Number(doc.measured.meanRMS).toFixed(4)],
            ["Peak RMS", Number(doc.measured.peakRMS).toFixed(4)],
            ["Onsets", String(doc.measured.onsetCount)],
            ["Tempo", doc.measured.tempoReliable
                ? `${Number(doc.measured.bpm).toFixed(1)} BPM (confidence ${Number(doc.measured.tempoConfidence).toFixed(2)})`
                : `${doc.measured.tempoNote || "tempo not reliable"} (confidence ${Number(doc.measured.tempoConfidence).toFixed(2)})`],
        ]);
        setText($("dna-descriptor"), `measured identity — ${dna.descriptor}`);
        setText($("dna-applied"), doc.appliedNote || "");
    } else {
        renderKV($("dna-defaults"), []);
        renderKV($("dna-measured"), []);
        setText($("dna-descriptor"), "");
        setText($("dna-applied"), "");
    }
}

/* featureCard renders one 0..1 measured feature with its honest
 * value: without a ready DNA the bar is 0% and the value is an
 * explicit placeholder, never a made-up number. */
function featureCard(label, value) {
    const card = document.createElement("div");
    card.className = "dna-feature";
    const name = document.createElement("div");
    name.className = "dna-feature-name";
    name.textContent = label;
    const val = document.createElement("div");
    val.className = "dna-feature-value";
    val.textContent = value === null ? "pending" : `${Math.round(value * 100)}%`;
    const bar = document.createElement("div");
    bar.className = "dna-bar";
    bar.setAttribute("role", "meter");
    bar.setAttribute("aria-label", label);
    bar.setAttribute("aria-valuemin", "0");
    bar.setAttribute("aria-valuemax", "100");
    bar.setAttribute("aria-valuenow", value === null ? "0" : String(Math.round(value * 100)));
    const fill = document.createElement("div");
    fill.className = "dna-bar-fill";
    fill.style.width = value === null ? "0%" : `${Math.max(2, Math.min(100, value * 100))}%`;
    bar.appendChild(fill);
    card.appendChild(name);
    card.appendChild(val);
    card.appendChild(bar);
    return card;
}

/* The 2 s tick refreshes the DNA pane only while an analysis can be
 * in flight (track loaded, DNA not ready) - bounded polling, no
 * request storm. */
export function dnaNeedsPolling() {
    return !!(state.library && state.library.trackLoaded
        && (!state.dna || state.dna.state !== "ready"));
}
