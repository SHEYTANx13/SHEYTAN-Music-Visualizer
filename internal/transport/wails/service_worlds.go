package wails

// service_worlds.go — the Worlds tab facade (run 46168717789 task:
// "add worlds full tab ... complete it in AAA manner").
//
// The Worlds tab renders the REAL per-track multi-scene world plan
// (internal/mvplan) that the analysis pipeline already derives from the
// measured Song DNA: chapters (scene spans), world elements (kind,
// motion, energy, lifetime), the camera plan, the lighting/palette
// mapping, the long-audio policy and the plan notes. The surface is
// honest in every state: no-track, analysis-pending, ready and the
// legacy-only case where no plan could be built — no invented worlds.

import (
	"encoding/json"
	"fmt"
)

// worldsDoc is the /api/worlds response document.
type worldsDoc struct {
	// State: "no-track" | "pending" | "ready" | "unavailable".
	State string `json:"state"`
	// Note explains the state in one honest sentence.
	Note string `json:"note,omitempty"`
	// TrackName is the loaded track's display name (never its path).
	TrackName string `json:"trackName,omitempty"`
	// PlanPath is the on-disk world-plan JSON (world-plans/<fp>.json).
	PlanPath string `json:"planPath,omitempty"`
	// Plan is the mvplan.Plan document (ready state only).
	Plan map[string]any `json:"plan,omitempty"`
}

// GetWorlds serves the world-plan surface for the Worlds tab.
func (s *SheytanService) GetWorlds() worldsDoc {
	track := s.studio.Track()
	if track == nil {
		return worldsDoc{
			State: "no-track",
			Note:  "Import music in the Studio tab: the analysis derives the Song DNA and the per-track world plan automatically.",
		}
	}
	doc := worldsDoc{TrackName: track.SourceName}
	if s.studio.WorldPlan() == nil {
		// Either the analysis is still running (pending) or it finished
		// without a plan (legacy-only analysis: honest unavailability).
		if s.studio.Analysis() == nil {
			doc.State = "pending"
			doc.Note = "Analyzing the track — the world plan lands the moment the Song DNA is measured."
			return doc
		}
		doc.State = "unavailable"
		doc.Note = "This analysis could not derive a world plan (legacy-only analysis document); the measured Song DNA tab still shows the real features."
		return doc
	}
	plan := s.studio.WorldPlan()
	doc.State = "ready"
	doc.Note = fmt.Sprintf("%d chapters, %d world elements, camera %q.", len(plan.Chapters), len(plan.Elements), plan.Camera.Style)
	doc.PlanPath = s.studio.WorldPlanPath()
	doc.Plan = planToMap(plan)
	return doc
}

// planToMap serializes the plan through its JSON tags without importing
// mvplan's types into the web contract (the document shape is the mvplan
// schema, served verbatim).
func planToMap(p any) map[string]any {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}
