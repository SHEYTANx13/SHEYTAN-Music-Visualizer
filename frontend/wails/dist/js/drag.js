/*
 * drag.js — pointer-based drag-to-scroll for every pane.
 *
 * Every section pans with finger, pen and mouse drag. Vertical finger
 * scrolling stays native (touch-action: pan-y); this adds mouse-drag
 * panning and pointer capture so a drag that leaves the pane keeps
 * scrolling until release.
 */

export function wireDragScroll() {
    for (const el of document.querySelectorAll(".pane")) {
        let dragging = false;
        let startY = 0;
        let startScroll = 0;
        let moved = false;
        el.addEventListener("pointerdown", (ev) => {
            if (ev.pointerType === "touch") return; // native scroll for touch
            if (ev.button !== 0) return;
            dragging = true;
            moved = false;
            startY = ev.clientY;
            startScroll = el.scrollTop;
        });
        el.addEventListener("pointermove", (ev) => {
            if (!dragging) return;
            const dy = ev.clientY - startY;
            if (!moved && Math.abs(dy) > 4) {
                moved = true;
                el.classList.add("drag-scrolling");
                try { el.setPointerCapture(ev.pointerId); } catch (err) { /* pane may be gone */ }
            }
            if (moved) {
                el.scrollTop = startScroll - dy;
                ev.preventDefault();
            }
        });
        const endDrag = (ev) => {
            if (!dragging) return;
            dragging = false;
            el.classList.remove("drag-scrolling");
            if (moved && ev.pointerId !== undefined) {
                try { el.releasePointerCapture(ev.pointerId); } catch (err) { /* already released */ }
            }
        };
        el.addEventListener("pointerup", endDrag);
        el.addEventListener("pointercancel", endDrag);
    }
}
