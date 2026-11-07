package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentHarborSyncBodyLimit = 256 * 1024

type agentHarborMachine struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type agentHarborEventInput struct {
	EventID    string    `json:"event_id"`
	MachineID  string    `json:"machine_id"`
	ProjectID  string    `json:"project_id"`
	AgentType  string    `json:"agent_type"`
	SessionID  string    `json:"session_id"`
	TurnID     string    `json:"turn_id"`
	LocalSeq   int64     `json:"local_seq"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	Evidence   string    `json:"evidence"`
}

type agentHarborSyncRequest struct {
	Machine agentHarborMachine      `json:"machine"`
	Events  []agentHarborEventInput `json:"events"`
}

type agentHarborEventResponse struct {
	EventID    string    `json:"event_id"`
	MachineID  string    `json:"machine_id"`
	ProjectID  string    `json:"project_id"`
	AgentType  string    `json:"agent_type"`
	SessionID  string    `json:"session_id"`
	TurnID     string    `json:"turn_id"`
	LocalSeq   int64     `json:"local_seq"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	ReceivedAt time.Time `json:"received_at"`
	Evidence   string    `json:"evidence"`
}

type agentHarborTaskResponse struct {
	ProjectID      string                     `json:"project_id"`
	MachineID      string                     `json:"machine_id"`
	MachineName    string                     `json:"machine_name"`
	AgentType      string                     `json:"agent_type"`
	SessionID      string                     `json:"session_id"`
	Status         string                     `json:"status"`
	LastOccurredAt time.Time                  `json:"last_occurred_at"`
	LastReceivedAt time.Time                  `json:"last_received_at"`
	Events         []agentHarborEventResponse `json:"events"`
}

// SyncAgentHarborEvents stores passive local events and acknowledges repeated IDs.
func (h *Handler) SyncAgentHarborEvents(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentHarborSyncBodyLimit))
	var req agentHarborSyncRequest
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	machineID, ok := parseUUIDOrBadRequest(w, req.Machine.ID, "machine.id")
	if !ok {
		return
	}
	if strings.TrimSpace(req.Machine.Name) == "" || len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "machine.name and events are required")
		return
	}

	type validatedEvent struct {
		input     agentHarborEventInput
		eventID   pgtype.UUID
		projectID pgtype.UUID
	}
	validated := make([]validatedEvent, 0, len(req.Events))
	projects := make(map[string]pgtype.UUID)
	for _, event := range req.Events {
		eventID, valid := parseUUIDOrBadRequest(w, event.EventID, "event_id")
		if !valid {
			return
		}
		projectID, valid := parseUUIDOrBadRequest(w, event.ProjectID, "project_id")
		if !valid {
			return
		}
		eventMachineID, valid := parseUUIDOrBadRequest(w, event.MachineID, "machine_id")
		if !valid {
			return
		}
		if eventMachineID != machineID || event.AgentType != "codex" ||
			strings.TrimSpace(event.SessionID) == "" || event.LocalSeq <= 0 || event.OccurredAt.IsZero() ||
			utf8.RuneCountInString(event.Evidence) > 512 || !validAgentHarborEventType(event.EventType) {
			writeError(w, http.StatusBadRequest, "invalid agentharbor event")
			return
		}
		validated = append(validated, validatedEvent{input: event, eventID: eventID, projectID: projectID})
		projects[event.ProjectID] = projectID
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("agentharbor transaction failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to sync agentharbor events")
		return
	}
	defer tx.Rollback(r.Context())
	queries := h.Queries.WithTx(tx)
	// The event table has no FKs. Hold the same workspace and project locks as
	// chat-session creation so a concurrent delete cannot leave orphan events.
	if _, err := queries.LockWorkspaceForChatSessionCreate(r.Context(), workspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "workspace not found")
		} else {
			slog.Error("agentharbor workspace lock failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to sync agentharbor events")
		}
		return
	}
	projectKeys := make([]string, 0, len(projects))
	for id := range projects {
		projectKeys = append(projectKeys, id)
	}
	sort.Strings(projectKeys)
	for _, id := range projectKeys {
		if _, err := queries.LockProjectForChatSessionCreate(r.Context(), db.LockProjectForChatSessionCreateParams{
			ID: projects[id], WorkspaceID: workspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "project not found")
			} else {
				slog.Error("agentharbor project lock failed", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to sync agentharbor events")
			}
			return
		}
	}
	accepted := make([]string, 0, len(validated))
	for _, event := range validated {
		if err := queries.InsertAgentHarborEvent(r.Context(), db.InsertAgentHarborEventParams{
			EventID:     event.eventID,
			WorkspaceID: workspaceID,
			ProjectID:   event.projectID,
			MachineID:   machineID,
			MachineName: req.Machine.Name,
			AgentType:   event.input.AgentType,
			SessionID:   event.input.SessionID,
			TurnID:      event.input.TurnID,
			LocalSeq:    event.input.LocalSeq,
			EventType:   event.input.EventType,
			OccurredAt:  pgtype.Timestamptz{Time: event.input.OccurredAt, Valid: true},
			Evidence:    event.input.Evidence,
		}); err != nil {
			slog.Error("agentharbor event insert failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to sync agentharbor events")
			return
		}
		accepted = append(accepted, event.input.EventID)
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("agentharbor transaction commit failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to sync agentharbor events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted_event_ids": accepted})
}

func validAgentHarborEventType(eventType string) bool {
	switch eventType {
	case "prompt_started", "turn_stopped", "interrupted":
		return true
	default:
		return false
	}
}

func agentHarborStatus(eventType string) string {
	switch eventType {
	case "prompt_started":
		return "running"
	case "interrupted":
		return "interrupted"
	default:
		return "turn_ended_unknown"
	}
}

// ListAgentHarborTasks groups persisted events by machine and local session.
func (h *Handler) ListAgentHarborTasks(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("project_id"), "project_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
		} else {
			slog.Error("agentharbor project lookup failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list agentharbor tasks")
		}
		return
	}
	rows, err := h.Queries.ListAgentHarborEvents(r.Context(), db.ListAgentHarborEventsParams{
		WorkspaceID: workspaceID, ProjectID: projectID,
	})
	if err != nil {
		slog.Error("agentharbor event list failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agentharbor tasks")
		return
	}
	type taskKey struct{ machineID, agentType, sessionID string }
	tasks := make([]agentHarborTaskResponse, 0)
	indexes := make(map[taskKey]int)
	latestSeq := make(map[taskKey]int64)
	for _, row := range rows {
		machineID := uuidToString(row.MachineID)
		key := taskKey{machineID: machineID, agentType: row.AgentType, sessionID: row.SessionID}
		index, exists := indexes[key]
		if !exists {
			index = len(tasks)
			indexes[key] = index
			tasks = append(tasks, agentHarborTaskResponse{
				ProjectID: uuidToString(row.ProjectID), MachineID: machineID,
				MachineName: row.MachineName, AgentType: row.AgentType, SessionID: row.SessionID,
				Events: make([]agentHarborEventResponse, 0),
			})
		}
		task := &tasks[index]
		task.Events = append(task.Events, agentHarborEventResponse{
			EventID: uuidToString(row.EventID), MachineID: machineID,
			ProjectID: uuidToString(row.ProjectID), AgentType: row.AgentType,
			SessionID: row.SessionID, TurnID: row.TurnID, LocalSeq: row.LocalSeq,
			EventType: row.EventType, OccurredAt: row.OccurredAt.Time,
			ReceivedAt: row.ReceivedAt.Time, Evidence: row.Evidence,
		})
		if row.LocalSeq > latestSeq[key] {
			latestSeq[key] = row.LocalSeq
			task.MachineName = row.MachineName
			task.Status = agentHarborStatus(row.EventType)
			task.LastOccurredAt = row.OccurredAt.Time
			task.LastReceivedAt = row.ReceivedAt.Time
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}
