package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) agentHarborMemoryProject(w http.ResponseWriter, r *http.Request) (string, bool) {
	projectID := chi.URLParam(r, "id")
	projectUUID, ok := parseUUIDOrBadRequest(w, projectID, "project id")
	if !ok {
		return "", false
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return "", false
	}
	_, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectUUID, WorkspaceID: workspaceUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return "", false
	}
	if err != nil {
		slog.Error("AgentHarbor memory project lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "project lookup failed")
		return "", false
	}
	if h.AgentHarborMemoryCore == nil {
		writeError(w, http.StatusServiceUnavailable, "MemoryCore is not configured")
		return "", false
	}
	return projectID, true
}

// GetAgentHarborMemorySnapshot returns the actual L3 document stored in MemoryCore.
func (h *Handler) GetAgentHarborMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.agentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	snapshot, err := h.AgentHarborMemoryCore.ReadCore(r.Context(), projectID)
	if err != nil {
		slog.Error("AgentHarbor MemoryCore read failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore read failed")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// PutAgentHarborCoreMemory stores a manually confirmed project agreement as L3.
func (h *Handler) PutAgentHarborCoreMemory(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.agentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Content) == "" || len(body.Content) > 16<<10 {
		writeError(w, http.StatusBadRequest, "content must be 1-16384 bytes")
		return
	}
	if err := h.AgentHarborMemoryCore.WriteCore(r.Context(), projectID, body.Content); err != nil {
		slog.Error("AgentHarbor MemoryCore write failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore write failed")
		return
	}
	snapshot, err := h.AgentHarborMemoryCore.ReadCore(r.Context(), projectID)
	if err != nil {
		slog.Error("AgentHarbor MemoryCore read after write failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore read after write failed")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
