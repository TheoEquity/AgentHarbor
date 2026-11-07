package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAgentHarborSyncAndTaskReadback(t *testing.T) {
	projectID := dbfx.Project(t, "AgentHarbor sync")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agentharbor_event WHERE project_id = $1`, projectID)
	})
	machineID := uuid.NewString()
	sessionID := "codex-session-1"
	firstID := uuid.NewString()
	secondID := uuid.NewString()
	occurredAt := "2026-01-02T03:04:05Z"
	event := func(id string, seq int64, eventType string) map[string]any {
		return map[string]any{
			"event_id": id, "machine_id": machineID, "project_id": projectID,
			"agent_type": "codex", "session_id": sessionID, "turn_id": "turn-1",
			"local_seq": seq, "event_type": eventType, "occurred_at": occurredAt,
			"evidence": "prompt_chars=23",
		}
	}
	syncEvents := func(events ...map[string]any) {
		t.Helper()
		var response struct {
			AcceptedEventIDs []string `json:"accepted_event_ids"`
		}
		testutil.Call(t, testHandler.SyncAgentHarborEvents,
			newRequest(http.MethodPost, "/api/agentharbor/sync", map[string]any{
				"machine": map[string]any{"id": machineID, "name": "Laptop A"},
				"events":  events,
			})).Want(http.StatusOK).JSON(&response)
		if len(response.AcceptedEventIDs) != len(events) {
			t.Fatalf("accepted %d events, want %d", len(response.AcceptedEventIDs), len(events))
		}
	}

	// Delayed lower sequence and a retried ID remain visible without changing status.
	syncEvents(event(secondID, 2, "turn_stopped"))
	syncEvents(event(firstID, 1, "prompt_started"))
	syncEvents(event(secondID, 2, "turn_stopped"))
	var response struct {
		Tasks []agentHarborTaskResponse `json:"tasks"`
	}
	testutil.Call(t, testHandler.ListAgentHarborTasks,
		newRequest(http.MethodGet, "/api/agentharbor/tasks?project_id="+projectID, nil)).
		Want(http.StatusOK).JSON(&response)
	if len(response.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(response.Tasks))
	}
	task := response.Tasks[0]
	if task.ProjectID != projectID || task.MachineID != machineID || task.MachineName != "Laptop A" ||
		task.AgentType != "codex" || task.SessionID != sessionID || task.Status != "turn_ended_unknown" {
		t.Fatalf("unexpected task identity/status: %+v", task)
	}
	if len(task.Events) != 2 || task.Events[0].LocalSeq != 1 || task.Events[1].LocalSeq != 2 {
		t.Fatalf("events = %+v, want one event per sequence", task.Events)
	}
	wantOccurred, _ := time.Parse(time.RFC3339, occurredAt)
	if !task.LastOccurredAt.Equal(wantOccurred) || !task.Events[1].OccurredAt.Equal(wantOccurred) {
		t.Fatalf("occurred_at changed: task=%s event=%s", task.LastOccurredAt, task.Events[1].OccurredAt)
	}
	if task.Events[1].ReceivedAt.IsZero() || task.Events[1].ReceivedAt.Equal(wantOccurred) ||
		!task.LastReceivedAt.Equal(task.Events[1].ReceivedAt) {
		t.Fatalf("received_at not distinguished from occurred_at: %+v", task)
	}

	secondMachineID := uuid.NewString()
	testutil.Call(t, testHandler.SyncAgentHarborEvents,
		newRequest(http.MethodPost, "/api/agentharbor/sync", map[string]any{
			"machine": map[string]any{"id": secondMachineID, "name": "Laptop B"},
			"events": []map[string]any{{
				"event_id": uuid.NewString(), "machine_id": secondMachineID, "project_id": projectID,
				"agent_type": "codex", "session_id": sessionID, "local_seq": 1,
				"event_type": "prompt_started", "occurred_at": occurredAt,
			}},
		})).Want(http.StatusOK)
	testutil.Call(t, testHandler.ListAgentHarborTasks,
		newRequest(http.MethodGet, "/api/agentharbor/tasks?project_id="+projectID, nil)).
		Want(http.StatusOK).JSON(&response)
	if len(response.Tasks) != 2 || response.Tasks[0].MachineID == response.Tasks[1].MachineID {
		t.Fatalf("same session on two machines must yield distinct tasks: %+v", response.Tasks)
	}
	testutil.Call(t, testHandler.DeleteProject,
		withURLParam(newRequest(http.MethodDelete, "/api/projects/"+projectID, nil), "id", projectID)).
		Want(http.StatusNoContent)
	var remaining int
	dbfx.QueryRow(t, `SELECT count(*) FROM agentharbor_event WHERE project_id = $1`, projectID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("project deletion left %d agentharbor events", remaining)
	}
}

func TestAgentHarborSyncRejectsOtherWorkspaceProject(t *testing.T) {
	foreignWorkspaceID := dbfx.Workspace(t, "AgentHarbor foreign", "agentharbor-"+uuid.NewString())
	foreignProjectID := dbfx.Project(t, "Foreign project", testutil.Cols{"workspace_id": foreignWorkspaceID})
	machineID := uuid.NewString()
	eventID := uuid.NewString()
	testutil.Call(t, testHandler.SyncAgentHarborEvents,
		newRequest(http.MethodPost, "/api/agentharbor/sync", map[string]any{
			"machine": map[string]any{"id": machineID, "name": "Laptop A"},
			"events": []map[string]any{{
				"event_id": eventID, "machine_id": machineID, "project_id": foreignProjectID,
				"agent_type": "codex", "session_id": "session-1", "local_seq": 1,
				"event_type": "prompt_started", "occurred_at": "2026-01-02T03:04:05Z",
			}},
		})).Want(http.StatusNotFound)
	var count int
	dbfx.QueryRow(t, `SELECT count(*) FROM agentharbor_event WHERE event_id = $1`, eventID).Scan(&count)
	if count != 0 {
		t.Fatalf("foreign event was stored: count=%d", count)
	}
	testutil.Call(t, testHandler.ListAgentHarborTasks,
		newRequest(http.MethodGet, "/api/agentharbor/tasks?project_id="+foreignProjectID, nil)).Want(http.StatusNotFound)
}
