package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentharbormemory"
)

func TestAgentHarborMemoryTokenRejectsTamperingAndExpiry(t *testing.T) {
	claims := agentHarborMemoryTokenClaims{
		UserID: testUserID, WorkspaceID: testWorkspaceID,
		ProjectID: "21d3ce52-9690-4f22-b387-59ed9c3617e0",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	token, err := signAgentHarborMemoryToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, ok := verifyAgentHarborMemoryToken(token, time.Now())
	if !ok || verified != claims {
		t.Fatalf("valid token not accepted: %+v, %v", verified, ok)
	}
	if _, ok := verifyAgentHarborMemoryToken(token+"x", time.Now()); ok {
		t.Fatal("tampered token accepted")
	}
	if _, ok := verifyAgentHarborMemoryToken(token, time.Unix(claims.ExpiresAt, 0)); ok {
		t.Fatal("expired token accepted")
	}
}

func TestAgentHarborScopedMemoryWritesOnlyTokenProject(t *testing.T) {
	projectA := dbfx.Project(t, "Scoped memory A")
	projectB := dbfx.Project(t, "Scoped memory B")
	contents := map[string]string{projectA: "Existing A", projectB: "Existing B"}
	memoryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode MemoryCore request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		projectID := strings.TrimPrefix(body["team_id"], "ah-project-")
		if body["agent_id"] != "ah-shared-"+projectID || body["user_id"] != "ah-project" {
			t.Errorf("wrong MemoryCore scope: %+v", body)
		}
		if _, ok := contents[projectID]; !ok {
			t.Errorf("unexpected project %q", projectID)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v3/core/read":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"content": contents[projectID]}})
		case "/v3/core/write":
			contents[projectID] = body["content"]
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"version": 0}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer memoryServer.Close()
	client, err := agentharbormemory.NewClient(memoryServer.URL, "", "default")
	if err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.AgentHarborMemoryCore = client
	issue := httptest.NewRecorder()
	h.IssueAgentHarborMemoryToken(issue, withURLParam(newRequest(http.MethodPost, "/api/projects/"+projectA+"/memory/access-token", nil), "id", projectA))
	if issue.Code != http.StatusCreated {
		t.Fatalf("issue status %d: %s", issue.Code, issue.Body.String())
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &issued); err != nil || issued.Token == "" {
		t.Fatalf("invalid issued token: %v", err)
	}
	request := func(method string, body any) *http.Request {
		r := newRequest(method, "/api/agentharbor/project-memory", body)
		r.Header.Set("Authorization", "Bearer "+issued.Token)
		return r
	}
	wrongQuery := request(http.MethodGet, nil)
	wrongQuery.URL.RawQuery = "project_id=" + projectB
	queryResult := httptest.NewRecorder()
	h.GetScopedAgentHarborMemory(queryResult, wrongQuery)
	if queryResult.Code != http.StatusBadRequest {
		t.Fatalf("client-selected project query accepted: status=%d", queryResult.Code)
	}
	read := httptest.NewRecorder()
	h.GetScopedAgentHarborMemory(read, request(http.MethodGet, nil))
	var snapshot agentharbormemory.Snapshot
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &snapshot) != nil || snapshot.ProjectID != projectA || snapshot.Content != "Existing A" {
		t.Fatalf("scoped read exposed wrong project: status=%d, body=%s", read.Code, read.Body.String())
	}
	appendResult := httptest.NewRecorder()
	h.AppendScopedAgentHarborMemory(appendResult, request(http.MethodPost, map[string]string{"content": "Verified session note"}))
	if appendResult.Code != http.StatusOK || !strings.Contains(contents[projectA], "Verified session note") || !strings.HasPrefix(contents[projectA], "Existing A") {
		t.Fatalf("append failed: status=%d, body=%s", appendResult.Code, appendResult.Body.String())
	}
	if contents[projectB] != "Existing B" {
		t.Fatalf("other project changed: %q", contents[projectB])
	}
	redirect := httptest.NewRecorder()
	h.AppendScopedAgentHarborMemory(redirect, request(http.MethodPost, map[string]string{"content": "wrong", "project_id": projectB}))
	if redirect.Code != http.StatusBadRequest || contents[projectB] != "Existing B" {
		t.Fatalf("client-selected project accepted: status=%d", redirect.Code)
	}
	invalid := httptest.NewRecorder()
	badRequest := request(http.MethodGet, nil)
	badRequest.Header.Set("Authorization", "Bearer mul_unscoped")
	h.GetScopedAgentHarborMemory(invalid, badRequest)
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("unscoped token accepted: status=%d", invalid.Code)
	}
}
