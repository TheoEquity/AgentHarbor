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
}
