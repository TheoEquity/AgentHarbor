package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAgentHarborPATRouteSmoke(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}
	fx := testutil.New(testPool, testWorkspaceID, testUserID)
	projectID := fx.Project(t, "AgentHarbor route smoke")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agentharbor_event WHERE project_id = $1`, projectID)
	})
	pat, err := auth.GeneratePATToken()
	if err != nil {
		t.Fatalf("generate PAT: %v", err)
	}
	fx.Insert(t, "personal_access_token", testutil.Cols{
		"user_id": testUserID, "name": "agentharbor-route-smoke",
		"token_hash": auth.HashToken(pat), "token_prefix": pat[:12],
	})
	machineID := uuid.NewString()
	eventID := uuid.NewString()
	call := func(method, path, bearer string, body any, workspaceOverride ...string) *http.Response {
		t.Helper()
		var payload []byte
		if body != nil {
			var err error
			payload, err = json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
		}
		req, err := http.NewRequest(method, testServer.URL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		workspaceID := testWorkspaceID
		if len(workspaceOverride) > 0 {
			workspaceID = workspaceOverride[0]
		}
		req.Header.Set("X-Workspace-ID", workspaceID)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		return resp
	}
	requestBody := map[string]any{
		"machine": map[string]any{"id": machineID, "name": "Laptop A"},
		"events": []map[string]any{{
			"event_id": eventID, "machine_id": machineID, "project_id": projectID,
			"agent_type": "codex", "session_id": "session-1", "local_seq": 1,
			"event_type": "prompt_started", "occurred_at": "2026-01-02T03:04:05Z",
		}},
	}
	unauthorized := call(http.MethodPost, "/api/agentharbor/sync", "", requestBody)
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST status = %d, want 401", unauthorized.StatusCode)
	}
	post := call(http.MethodPost, "/api/agentharbor/sync", pat, requestBody)
	if post.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(post.Body)
		post.Body.Close()
		t.Fatalf("PAT POST status = %d: %s", post.StatusCode, body)
	}
	var acknowledged struct {
		AcceptedEventIDs []string `json:"accepted_event_ids"`
	}
	readJSON(t, post, &acknowledged)
	if len(acknowledged.AcceptedEventIDs) != 1 || acknowledged.AcceptedEventIDs[0] != eventID {
		t.Fatalf("acknowledged IDs = %v", acknowledged.AcceptedEventIDs)
	}
	get := call(http.MethodGet, "/api/agentharbor/tasks?project_id="+projectID, pat, nil)
	if get.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(get.Body)
		get.Body.Close()
		t.Fatalf("PAT GET status = %d: %s", get.StatusCode, body)
	}
	var listed struct {
		Tasks []struct {
			MachineID string `json:"machine_id"`
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		} `json:"tasks"`
	}
	readJSON(t, get, &listed)
	if len(listed.Tasks) != 1 || listed.Tasks[0].MachineID != machineID ||
		listed.Tasks[0].SessionID != "session-1" || listed.Tasks[0].Status != "running" {
		t.Fatalf("PAT GET tasks = %+v", listed.Tasks)
	}
	foreignWorkspaceID := fx.Workspace(t, "AgentHarbor foreign", "agentharbor-"+uuid.NewString())
	foreignProjectID := fx.Project(t, "Foreign project", testutil.Cols{"workspace_id": foreignWorkspaceID})
	foreign := call(http.MethodGet, "/api/agentharbor/tasks?project_id="+foreignProjectID, pat, nil, foreignWorkspaceID)
	foreign.Body.Close()
	if foreign.StatusCode != http.StatusNotFound {
		t.Fatalf("nonmember GET status = %d, want 404", foreign.StatusCode)
	}

	runtimeID := fx.Runtime(t, "AgentHarbor route smoke")
	agentID := fx.Agent(t, "AgentHarbor route smoke", runtimeID)
	taskID := fx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	taskToken := mintAgentTaskToken(t, agentID, taskID, testUserID)
	snapshotPath := "/api/projects/" + projectID + "/memory-snapshot"
	for _, tc := range []struct {
		bearer string
		want   int
	}{
		{pat, http.StatusOK},
		{taskToken, http.StatusForbidden},
	} {
		response := call(http.MethodGet, snapshotPath, tc.bearer, nil)
		response.Body.Close()
		if response.StatusCode != tc.want {
			t.Errorf("memory snapshot status = %d, want %d", response.StatusCode, tc.want)
		}
	}

	monitorPath := "/api/agentharbor/monitor"
	syncMonitor := func(scan string, turns ...map[string]any) {
		t.Helper()
		if turns == nil {
			turns = []map[string]any{}
		}
		response := call(http.MethodPost, monitorPath+"/sync", pat, map[string]any{
			"project_id": projectID, "machine": requestBody["machine"], "scan_status": scan, "turns": turns,
		})
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("monitor sync status = %d: %s", response.StatusCode, body)
		}
		var ack struct {
			AcceptedCount int `json:"accepted_count"`
		}
		readJSON(t, response, &ack)
		if ack.AcceptedCount != len(turns) {
			t.Fatalf("monitor accepted %d turns, want %d", ack.AcceptedCount, len(turns))
		}
	}
	const latest = "2026-01-03T03:04:05Z"
	old := map[string]any{"session_id": "same-session", "turn_id": "old", "title": "Old turn", "started_at": "2026-01-02T03:04:05Z", "ended_at": latest, "status": "completed", "historical": true}
	newRunning := map[string]any{"session_id": "same-session", "turn_id": "new", "title": "New turn", "started_at": latest, "status": "running"}
	syncMonitor("ok", newRunning)
	syncMonitor("ok", old)
	syncMonitor("ok", map[string]any{"session_id": "same-session", "turn_id": "older-no-start", "title": "Old interrupted turn", "ended_at": "2026-01-01T00:00:00Z", "status": "aborted", "historical": true})
	syncMonitor("degraded")
	type turn struct {
		TurnID  string `json:"turn_id"`
		Status  string `json:"status"`
		Handled *bool  `json:"handled"`
	}
	getMonitor := func(view string) ([]turn, bool) {
		t.Helper()
		response := call(http.MethodGet, monitorPath+"?project_id="+projectID+"&view="+view, pat, nil)
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("monitor list status = %d: %s", response.StatusCode, body)
		}
		var listing struct {
			Tasks   []turn `json:"tasks"`
			Sources []struct {
				Stale bool `json:"stale"`
			} `json:"sources"`
		}
		readJSON(t, response, &listing)
		if len(listing.Sources) != 1 {
			t.Fatalf("monitor sources = %+v, want one", listing.Sources)
		}
		return listing.Tasks, listing.Sources[0].Stale
	}
	if active, stale := getMonitor("active"); len(active) != 1 || active[0].TurnID != "new" || active[0].Status != "running" || !stale {
		t.Fatalf("late old completion and degraded scan changed latest running turn: %+v, stale=%t", active, stale)
	}
	key := map[string]any{"project_id": projectID, "machine_id": machineID, "session_id": "same-session", "turn_id": "new"}
	handle := func(want int) {
		t.Helper()
		response := call(http.MethodPost, monitorPath+"/handle", pat, key)
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("handle status = %d, want %d", response.StatusCode, want)
		}
	}
	handle(http.StatusConflict)
	syncMonitor("ok", map[string]any{"session_id": "same-session", "turn_id": "new", "title": "New turn", "started_at": latest, "ended_at": "2026-01-03T03:05:05Z", "status": "aborted"})
	syncMonitor("ok", newRunning)
	if active, stale := getMonitor("active"); len(active) != 1 || active[0].Status != "aborted" || active[0].Handled == nil || *active[0].Handled || stale {
		t.Fatalf("replayed start changed terminal turn: %+v, stale=%t", active, stale)
	}
	handle(http.StatusOK)
	handle(http.StatusOK)
	if active, _ := getMonitor("active"); len(active) != 0 {
		t.Fatalf("handled terminal turn still active: %+v", active)
	}
	if history, _ := getMonitor("history"); len(history) != 3 || history[0].TurnID != "new" || history[0].Handled == nil || !*history[0].Handled || history[1].Handled != nil || history[2].TurnID != "older-no-start" {
		t.Fatalf("monitor history lost turns or handling distinction: %+v", history)
	}
	syncMonitor("ok", map[string]any{"session_id": "same-session", "turn_id": "next", "title": "Next turn", "started_at": "2026-01-04T00:00:00Z", "ended_at": "2026-01-04T00:01:00Z", "status": "completed"})
	if active, _ := getMonitor("active"); len(active) != 1 || active[0].TurnID != "next" || active[0].Handled == nil || *active[0].Handled {
		t.Fatalf("handling a terminal turn carried over to a later turn: %+v", active)
	}
	for _, item := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, monitorPath + "/sync", map[string]any{"project_id": foreignProjectID, "machine": requestBody["machine"], "scan_status": "ok", "turns": []any{}}},
		{http.MethodGet, monitorPath + "?project_id=" + foreignProjectID, nil},
		{http.MethodPost, monitorPath + "/handle", map[string]any{"project_id": foreignProjectID, "machine_id": machineID, "session_id": "same-session", "turn_id": "new"}},
	} {
		response := call(item.method, item.path, pat, item.body)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("foreign project %s %s status = %d, want 404", item.method, item.path, response.StatusCode)
		}
	}
	deleted := call(http.MethodDelete, "/api/projects/"+projectID, pat, nil)
	deleted.Body.Close()
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("project deletion status = %d, want 204", deleted.StatusCode)
	}

}
