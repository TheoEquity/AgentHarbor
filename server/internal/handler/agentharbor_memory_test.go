package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentharbormemory"
	"github.com/multica-ai/multica/server/internal/testutil"
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

func TestAgentHarborMemoryReviewLifecycle(t *testing.T) {
	project := dbfx.Project(t, "CIS 5480 memory acceptance")
	other := dbfx.Project(t, "Other memory project")
	requestID := uuid.NewString()
	var materialID string
	defer func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, "DELETE FROM agentharbor_memory_revision WHERE entry_id IN (SELECT id FROM agentharbor_memory_entry WHERE workspace_id = $1 AND project_id = $2)", testWorkspaceID, project)
		_, _ = testPool.Exec(ctx, "DELETE FROM agentharbor_memory_entry WHERE workspace_id = $1 AND project_id = $2", testWorkspaceID, project)
		_, _ = testPool.Exec(ctx, "DELETE FROM agentharbor_memory_material WHERE workspace_id = $1 AND project_id = $2", testWorkspaceID, project)
		_, _ = testPool.Exec(ctx, "DELETE FROM agentharbor_memory_settings WHERE workspace_id = $1 AND project_id = $2", testWorkspaceID, project)
	}()
	scoped := func(method, id string, body any) *http.Request {
		token, err := signAgentHarborMemoryToken(agentHarborMemoryTokenClaims{
			UserID: testUserID, WorkspaceID: testWorkspaceID, ProjectID: id,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		r := newRequest(method, "/api/agentharbor/memory/materials", body)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}
	web := func(method, material string, body any) *http.Request {
		r := withURLParam(newRequest(method, "/", body), "id", project)
		chi.RouteContext(r.Context()).URLParams.Add("materialId", material)
		return r
	}
	call := func(handler http.HandlerFunc, r *http.Request, want int) json.RawMessage {
		t.Helper()
		var raw json.RawMessage
		testutil.Call(t, handler, r).Want(want).JSON(&raw)
		return raw
	}
	assertEntries := func(project string, want int) {
		t.Helper()
		var got struct {
			Entries []struct {
				Content  string
				Revision int
			}
		}
		if err := json.Unmarshal(call(testHandler.ListScopedAgentHarborMemoryEntries, scoped(http.MethodGet, project, nil), http.StatusOK), &got); err != nil || len(got.Entries) != want {
			t.Fatalf("entries: %+v, err %v; want %d", got, err, want)
		}
		if want == 1 && (got.Entries[0].Content != "Check callers before changing public types." || got.Entries[0].Revision != 1) {
			t.Fatalf("wrong approved entry: %+v", got.Entries[0])
		}
	}
	body := map[string]string{"request_id": requestID, "source_text": "Check callers first", "session_id": "cis5480-test"}
	var material struct{ ID string }
	if err := json.Unmarshal(call(testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, body), http.StatusCreated), &material); err != nil || material.ID == "" {
		t.Fatalf("submit: %+v, %v", material, err)
	}
	materialID = material.ID
	call(testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, body), http.StatusOK)
	body["source_text"] = "changed"
	call(testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, body), http.StatusConflict)
	assertEntries(project, 0)
	var listed struct{ Materials []struct{ ID, Status string } }
	if err := json.Unmarshal(call(testHandler.ListAgentHarborMemoryMaterials, web(http.MethodGet, "", nil), http.StatusOK), &listed); err != nil || len(listed.Materials) != 1 || listed.Materials[0].ID != materialID || listed.Materials[0].Status != "pending" {
		t.Fatalf("pending materials: %+v, %v", listed, err)
	}

	review := map[string]any{"action": "approve", "scope": "project", "type": "procedural", "title": "Public types", "content": "Check callers before changing public types."}
	var result struct{ Entry struct{ ID string } }
	if err := json.Unmarshal(call(testHandler.ReviewAgentHarborMemoryMaterial, web(http.MethodPatch, materialID, review), http.StatusOK), &result); err != nil || result.Entry.ID == "" {
		t.Fatalf("approve: %+v, %v", result, err)
	}
	call(testHandler.ReviewAgentHarborMemoryMaterial, web(http.MethodPatch, materialID, review), http.StatusConflict)
	assertEntries(project, 1)
	assertEntries(other, 0)
	correction := map[string]any{"request_id": uuid.NewString(), "source_text": "Only exported types require this check", "related_entry_id": result.Entry.ID, "related_revision": 1}
	var reported struct{ ID string }
	testutil.Call(t, testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, correction)).Want(http.StatusCreated).JSON(&reported)
	snapshot := testutil.Call(t, testHandler.GetScopedAgentHarborMemoryContext, scoped(http.MethodGet, project, nil)).Want(http.StatusOK).Map()
	if snapshot["content"] != "" {
		t.Fatalf("disputed memory in startup: %+v", snapshot)
	}
	var read struct {
		Entries  []json.RawMessage
		Disputes []struct {
			Entry  struct{ ID string }
			Report struct {
				SourceText string `json:"source_text"`
			}
		}
	}
	testutil.Call(t, testHandler.ListScopedAgentHarborMemoryEntries, scoped(http.MethodGet, project, nil)).Want(http.StatusOK).JSON(&read)
	if len(read.Entries) != 0 || len(read.Disputes) != 1 || read.Disputes[0].Entry.ID != result.Entry.ID || read.Disputes[0].Report.SourceText != correction["source_text"] {
		t.Fatalf("missing dispute: %+v", read)
	}
	history := web(http.MethodGet, "", nil)
	chi.RouteContext(history.Context()).URLParams.Add("entryId", result.Entry.ID)
	var record struct {
		Revisions []struct{ Revision int }
		Materials []struct{ ID string }
		Disputes  []struct{ ID string }
	}
	testutil.Call(t, testHandler.GetAgentHarborMemoryHistory, history).Want(http.StatusOK).JSON(&record)
	if len(record.Revisions) != 1 || record.Revisions[0].Revision != 1 || len(record.Materials) != 1 || record.Materials[0].ID != materialID || len(record.Disputes) != 1 || record.Disputes[0].ID != reported.ID {
		t.Fatalf("incomplete memory history: %+v", record)
	}
	edit := web(http.MethodPatch, "", map[string]any{
		"action": "revise", "expected_revision": 1, "type": "procedural",
		"title": "Check public types", "content": "Check callers before changing public types.",
	})
	chi.RouteContext(edit.Context()).URLParams.Add("entryId", result.Entry.ID)
	call(testHandler.UpdateAgentHarborMemoryEntry, edit, http.StatusOK)
	assertEntries(project, 0)
	call(testHandler.ReviewAgentHarborMemoryMaterial, web(http.MethodPatch, reported.ID, map[string]any{"action": "discard"}), http.StatusOK)
	after := testutil.Call(t, testHandler.GetScopedAgentHarborMemoryContext, scoped(http.MethodGet, project, nil)).Want(http.StatusOK).Map()
	if after["content"] == "" {
		t.Fatal("memory should become readable after resolving the report")
	}

	revision := func(value int) *http.Request {
		r := web(http.MethodPatch, "", map[string]any{"action": "disable", "expected_revision": value})
		chi.RouteContext(r.Context()).URLParams.Add("entryId", result.Entry.ID)
		return r
	}
	call(testHandler.UpdateAgentHarborMemoryEntry, revision(1), http.StatusConflict)
	call(testHandler.UpdateAgentHarborMemoryEntry, revision(2), http.StatusOK)
	assertEntries(project, 0)
	call(testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, correction), http.StatusOK)
	correction["request_id"] = uuid.NewString()
	call(testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, correction), http.StatusNotFound)

	call(testHandler.PutAgentHarborMemorySettings, web(http.MethodPut, "", map[string]bool{
		"record_enabled": true, "cloud_enabled": true, "use_memory": false, "use_global": false,
	}), http.StatusOK)
	call(testHandler.SubmitAgentHarborMemoryMaterial, scoped(http.MethodPost, project, map[string]string{
		"request_id": uuid.NewString(), "source_text": "Recording can continue independently of retrieval.",
	}), http.StatusCreated)
}

func TestAgentHarborLegacyImportStaysPendingAndDeduplicates(t *testing.T) {
	project := dbfx.Project(t, "CIS 5480 legacy import")
	source := "Legacy CIS 5480 notes"
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.URL.Path != "/v3/core/read" || json.NewDecoder(r.Body).Decode(&body) != nil || body["team_id"] != "ah-project-"+project {
			t.Error("expected read-only legacy import scoped to its project")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]string{"content": source}})
	}))
	defer legacy.Close()
	client, err := agentharbormemory.NewClient(legacy.URL, "", "default")
	if err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.AgentHarborMemoryCore = client
	t.Cleanup(func() {
		dbfx.Exec(t, "DELETE FROM agentharbor_memory_material WHERE workspace_id = $1 AND project_id = $2", testWorkspaceID, project)
	})
	var imported struct {
		Material struct {
			ID, Status string
			SourceText string `json:"source_text"`
		}
	}
	importRequest := func() *http.Request {
		return withURLParam(newRequest(http.MethodPost, "/", nil), "id", project)
	}
	testutil.Call(t, h.ImportAgentHarborLegacyMemory, importRequest()).Want(http.StatusOK).JSON(&imported)
	id := imported.Material.ID
	if id == "" || imported.Material.Status != "pending" || imported.Material.SourceText != source {
		t.Fatalf("unexpected legacy import: %+v", imported)
	}
	testutil.Call(t, h.ImportAgentHarborLegacyMemory, importRequest()).Want(http.StatusOK).JSON(&imported)
	if imported.Material.ID != id {
		t.Fatalf("repeat import created a new material: %+v", imported)
	}
}
