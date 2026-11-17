package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAgentHarborMonitorHistoryFiltersAndPagination(t *testing.T) {
	project := dbfx.Project(t, "Monitor history")
	otherProject := dbfx.Project(t, "Other monitor project")
	firstMachine, secondMachine := uuid.NewString(), uuid.NewString()
	for _, id := range []string{project, otherProject} {
		t.Cleanup(func() {
			req := withURLParam(newRequest(http.MethodDelete, "/api/projects/"+id, nil), "id", id)
			testutil.Call(t, testHandler.DeleteProject, req).Want(http.StatusNoContent)
		})
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	turn := func(id string, offset time.Duration) map[string]any {
		return map[string]any{
			"session_id": "same-codex-session", "turn_id": id, "title": "Synthetic turn",
			"started_at": base.Add(offset), "ended_at": base.Add(offset + time.Second),
			"status": "completed", "historical": true, "handled": nil,
		}
	}
	sync := func(projectID, machineID string, wantStatus int, turns ...map[string]any) {
		t.Helper()
		req := newRequest(http.MethodPost, "/api/agentharbor/monitor/sync", map[string]any{
			"project_id": projectID, "machine": map[string]any{"id": machineID, "name": "Synthetic machine"},
			"scan_status": "ok", "turns": turns,
		})
		testutil.Call(t, testHandler.SyncAgentHarborMonitor, req).Want(wantStatus)
	}
	batch := make([]map[string]any, 0, 51)
	for i := range 51 {
		batch = append(batch, turn(fmt.Sprintf("first-%02d", i), time.Duration(i)*time.Minute))
	}
	sync(project, firstMachine, http.StatusOK, batch...)
	sync(project, secondMachine, http.StatusOK, turn("second", 2*time.Hour))
	sync(otherProject, firstMachine, http.StatusOK, turn("other", 3*time.Hour))

	invalid := turn("invalid", 4*time.Hour)
	invalid["raw_prompt"] = "fixture text excluded by monitor contract"
	sync(project, firstMachine, http.StatusBadRequest, turn("partial", 3*time.Hour), invalid)
	sync(project, firstMachine, http.StatusBadRequest, append(append([]map[string]any{}, batch...), batch...)...)

	type page struct {
		Tasks   []agentHarborMonitorTask   `json:"tasks"`
		Sources []agentHarborMonitorSource `json:"sources"`
		HasMore bool                       `json:"has_more"`
	}
	get := func(projectID, machineID string, pageNumber int) page {
		t.Helper()
		var result page
		path := fmt.Sprintf("/api/agentharbor/monitor?view=history&project_id=%s&machine_id=%s&page=%d", projectID, machineID, pageNumber)
		testutil.Call(t, testHandler.ListAgentHarborMonitor, newRequest(http.MethodGet, path, nil)).Want(http.StatusOK).JSON(&result)
		return result
	}
	first, second := get(project, "", 1), get(project, "", 2)
	if len(first.Tasks) != 50 || !first.HasMore || len(second.Tasks) != 2 || second.HasMore {
		t.Fatalf("unexpected history pagination: first=%d more=%t, second=%d more=%t", len(first.Tasks), first.HasMore, len(second.Tasks), second.HasMore)
	}
	if first.Tasks[0].TurnID != "second" || second.Tasks[0].TurnID != "first-01" || second.Tasks[1].TurnID != "first-00" {
		t.Fatalf("wrong task order across pages")
	}
	unique := make(map[string]bool)
	for _, task := range append(first.Tasks, second.Tasks...) {
		if task.ProjectID != project || task.ProjectName != "Monitor history" || unique[task.TurnID] {
			t.Fatalf("wrong project or duplicate turn: %+v", task)
		}
		unique[task.TurnID] = true
	}

	filtered := get(project, secondMachine, 1)
	if len(filtered.Tasks) != 1 || filtered.Tasks[0].MachineID != secondMachine || len(filtered.Sources) != 2 {
		t.Fatalf("machine filtering lost task or visible sources: %+v", filtered)
	}
	for _, source := range filtered.Sources {
		if source.ProjectID != project || source.LastSyncedAt.IsZero() || source.Stale {
			t.Fatalf("wrong source: %+v", source)
		}
	}
	if other := get(otherProject, firstMachine, 1); len(other.Tasks) != 1 || other.Tasks[0].TurnID != "other" || len(other.Sources) != 1 {
		t.Fatalf("other project did not preserve its own machine turn: %+v", other)
	}
}
