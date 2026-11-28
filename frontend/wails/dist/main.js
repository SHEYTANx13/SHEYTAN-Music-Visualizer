/*
 * main.js — SHEYTAN Music Visualizer BETA web shell entry module.
 *
 * Vanilla ES modules, offline-first: no frameworks, no bundler, no CDN
 * assets. All dynamic data comes from the Go-side /api/* endpoints
 * served by the shell facade (internal/transport/wails); design tokens
 * come from the byte-identical tokens.json copy next to this file.
 *
 * The implementation is split one file per element under js/ (and one
 * stylesheet per element under css/ — see index.html for the cascade
 * order). This file is ONLY the entry point: it boots the shell.
 *
 * Honesty rules implemented across the modules:
 *   - When the API is unreachable, every affected surface shows a
 *     visible error state (never a fake "ok").
 *   - Unavailable destinations render their catalog emptyState text.
 *   - Unavailable commands render as disabled with their reason.
 *   - Renderer evidence is displayed verbatim; no state is upgraded.
 *   - Song DNA and export progress are real backend measurements;
 *     pending states say pending, never invented numbers.
 */

import { boot } from "./js/boot.js";

document.addEventListener("DOMContentLoaded", () => {
    boot();
});
