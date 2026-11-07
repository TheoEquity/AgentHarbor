package agentharbormemory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCoreRoundTripUsesProjectScopeAndContentHash(t *testing.T) {
	var saved string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer local-test" || r.Header.Get("x-tdai-service-id") != "default" {
			t.Errorf("unexpected MemoryCore request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["team_id"] != "ah-project-project-1" || body["agent_id"] != "ah-shared-project-1" || body["user_id"] != "ah-project" {
			t.Errorf("wrong project scope: %#v", body)
		}
		switch r.URL.Path {
		case "/v3/core/write":
			saved = body["content"]
			_, _ = w.Write([]byte(`{"code":0,"data":{"version":0}}`))
		case "/v3/core/read":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"content": saved, "version": 0}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "local-test", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteCore(context.Background(), "project-1", "First agreement"); err != nil {
		t.Fatal(err)
	}
	first, err := client.ReadCore(context.Background(), "project-1")
	if err != nil || first.Content != "First agreement" || first.Version == "" {
		t.Fatalf("first snapshot: %#v, %v", first, err)
	}
	if err := client.WriteCore(context.Background(), "project-1", "Updated agreement"); err != nil {
		t.Fatal(err)
	}
	second, err := client.ReadCore(context.Background(), "project-1")
	if err != nil || second.Content != "Updated agreement" || second.Version == first.Version {
		t.Fatalf("second snapshot should change despite upstream version 0: %#v, %v", second, err)
	}
}

func TestMissingCoreMemoryHasEmptyVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"content":null}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "default")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.ReadCore(context.Background(), "project-2")
	if err != nil || snapshot.Content != "" || snapshot.Version != "" {
		t.Fatalf("missing snapshot: %#v, %v", snapshot, err)
	}
}

func TestCoreErrorIsNotTreatedAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":403,"message":"scope rejected"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteCore(context.Background(), "project-1", "Agreement"); err == nil {
		t.Fatal("expected MemoryCore error")
	}
}
