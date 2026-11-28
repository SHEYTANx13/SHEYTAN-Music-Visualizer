/*
 * tabs.js — the workspace destination catalog and tab strip.
 *
 * Tabs render from /api/destinations (the canonical DestinationCatalog,
 * including honest empty states for unavailable destinations). Empty-
 * state text comes verbatim from the catalog — never invented here.
 */

import { fetchJSON, setStatus } from "./api.js";
import { $, clearNode, setText } from "./dom.js";
import { state } from "./state.js";

export async function loadDestinations() {
    const data = await fetchJSON("/api/destinations");
    state.destinations = data.destinations || [];
    renderTabs();
}

export function renderTabs() {
    const strip = $("tabstrip");
    if (!strip) return;
    clearNode(strip);
    for (const dest of state.destinations) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "tab";
        btn.dataset.dest = String(dest.id);
        btn.textContent = String(dest.title);
        if (!dest.available) btn.classList.add("tab-unavailable");
        btn.title = dest.available
            ? String(dest.description || "")
            : String(dest.emptyState || dest.description || "");
        btn.addEventListener("click", () => activateTab(String(dest.id)));
        strip.appendChild(btn);
    }
    // Empty-state text for unavailable destinations comes verbatim from
    // the catalog — never invented by the frontend.
    for (const dest of state.destinations) {
        if (dest.available) continue;
        const empty = $(`empty-${String(dest.id).replace(/[^a-z-]/g, "")}`);
        if (empty) empty.textContent = String(dest.emptyState || "Not available in this build.");
    }
    // Keep the visible pane consistent with the (re)rendered strip.
    const active = strip.querySelector(".tab.active");
    if (active) {
        activateTab(active.dataset.dest, { keepActive: true });
    } else {
        const first = state.destinations.find((d) => d.available);
        if (first) activateTab(String(first.id));
    }
}

export function activateTab(destID, options) {
    const strip = $("tabstrip");
    if (!strip) return;
    for (const btn of strip.querySelectorAll(".tab")) {
        btn.classList.toggle("active", btn.dataset.dest === destID);
    }
    for (const pane of document.querySelectorAll(".pane")) {
        pane.hidden = pane.dataset.dest !== destID;
    }
    if (!options || !options.keepActive) {
        setStatus(`viewing ${destID}`);
    }
}

/* Renders the canonical per-destination descriptions into the pane
 * description paragraphs. */
export function renderDescriptions(destinations) {
    const byID = new Map(destinations.map((d) => [String(d.id), d]));
    const pairs = [
        ["home-desc", "home"],
        ["studio-desc", "studio"],
        ["templates-desc", "templates"],
        ["library-desc", "library"],
        ["dna-desc", "songdna"],
        ["export-desc", "export"],
        ["requirements-desc", "requirements"],
        ["systemfiles-desc", "system-files"],
        ["settings-desc", "settings"],
        ["diagnostics-desc", "diagnostics"],
    ];
    for (const [nodeID, destID] of pairs) {
        const dest = byID.get(destID);
        if (dest) setText($(nodeID), String(dest.description || ""));
    }
}
