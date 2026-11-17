package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type agentHarborMemorySubmission struct {
	RequestID       string `json:"request_id"`
	SourceText      string `json:"source_text"`
	Context         string `json:"context,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	RelatedEntryID  string `json:"related_entry_id,omitempty"`
	RelatedRevision *int32 `json:"related_revision,omitempty"`
}

func (h *Handler) SubmitAgentHarborMemoryMaterial(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.scopedAgentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	var body agentHarborMemorySubmission
	if !decodeMemoryBody(w, r, &body) {
		return
	}
	requestID, ok := parseUUIDOrBadRequest(w, body.RequestID, "request id")
	if !ok {
		return
	}
	relatedID, ok := optionalMemoryID(w, body.RelatedEntryID, "related entry id")
	if !ok {
		return
	}
	row, status, failure := h.submitMemoryMaterial(r.Context(), scope, body, requestID, relatedID)
	if failure != nil {
		failure.writeHTTP(w)
		return
	}
	writeJSON(w, status, row)
}

func (h *Handler) submitMemoryMaterial(ctx context.Context, scope agentHarborMemoryScope, body agentHarborMemorySubmission, requestID, relatedID pgtype.UUID) (db.AgentharborMemoryMaterial, int, *agentHarborMemoryFailure) {
	if !validMemoryText(body.SourceText, 8192) || len(body.Context) > 4096 || len(body.SessionID) > 255 ||
		len(body.TurnID) > 255 || strings.ContainsRune(body.Context+body.SessionID+body.TurnID, 0) {
		return db.AgentharborMemoryMaterial{}, 0, &agentHarborMemoryFailure{status: http.StatusBadRequest, message: "invalid source text, context, or session reference"}
	}
	var revision pgtype.Int4
	if body.RelatedRevision != nil {
		if !relatedID.Valid || *body.RelatedRevision < 1 {
			return db.AgentharborMemoryMaterial{}, 0, &agentHarborMemoryFailure{status: http.StatusBadRequest, message: "related revision requires an entry and a positive revision"}
		}
		revision = pgtype.Int4{Int32: *body.RelatedRevision, Valid: true}
	}
	return h.storeMemoryMaterial(ctx, scope, db.CreateAgentHarborMemoryMaterialParams{
		WorkspaceID: scope.workspace, ProjectID: scope.project, SubmittedBy: scope.actor,
		RequestID: requestID, SourceText: body.SourceText, Context: body.Context,
		SessionID: body.SessionID, TurnID: body.TurnID, RelatedEntryID: relatedID, RelatedRevision: revision,
	})
}

func (h *Handler) storeMemoryMaterial(ctx context.Context, scope agentHarborMemoryScope, input db.CreateAgentHarborMemoryMaterialParams) (db.AgentharborMemoryMaterial, int, *agentHarborMemoryFailure) {
	tx, q, failure := h.beginMemoryWrite(ctx, scope)
	if failure != nil {
		return db.AgentharborMemoryMaterial{}, 0, failure
	}
	defer tx.Rollback(ctx)
	settings, err := readMemorySettings(ctx, q, scope)
	if err != nil {
		return db.AgentharborMemoryMaterial{}, 0, memoryStoreFailure(err)
	}
	request := db.GetAgentHarborMemoryMaterialByRequestParams{
		WorkspaceID: scope.workspace, ProjectID: scope.project, SubmittedBy: input.SubmittedBy, RequestID: input.RequestID,
	}
	row, err := q.GetAgentHarborMemoryMaterialByRequest(ctx, request)
	status := http.StatusOK
	if errors.Is(err, pgx.ErrNoRows) {
		if !settings.RecordEnabled || !settings.CloudEnabled {
			return db.AgentharborMemoryMaterial{}, 0, &agentHarborMemoryFailure{status: http.StatusForbidden, message: "memory recording is disabled for this project"}
		}
		if input.RelatedEntryID.Valid {
			if failure := readableRelatedMemoryEntry(ctx, q, scope, settings, input.RelatedEntryID); failure != nil {
				return db.AgentharborMemoryMaterial{}, 0, failure
			}
		}
		row, err = q.CreateAgentHarborMemoryMaterial(ctx, input)
		status = http.StatusCreated
		if errors.Is(err, pgx.ErrNoRows) {
			row, err = q.GetAgentHarborMemoryMaterialByRequest(ctx, request)
			status = http.StatusOK
		}
	}
	if err == nil && status == http.StatusOK && (row.SourceText != input.SourceText || row.Context != input.Context ||
		row.SessionID != input.SessionID || row.TurnID != input.TurnID || row.RelatedEntryID != input.RelatedEntryID ||
		row.RelatedRevision != input.RelatedRevision) {
		return db.AgentharborMemoryMaterial{}, 0, &agentHarborMemoryFailure{
			status: http.StatusConflict, code: "memory_request_conflict", message: "request id already belongs to different material",
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return db.AgentharborMemoryMaterial{}, 0, memoryStoreFailure(err)
	}
	return row, status, nil
}

func readableRelatedMemoryEntry(ctx context.Context, q *db.Queries, scope agentHarborMemoryScope, settings agentHarborMemorySettings, id pgtype.UUID) *agentHarborMemoryFailure {
	if !settings.UseMemory {
		return &agentHarborMemoryFailure{status: http.StatusNotFound, message: "memory entry not found"}
	}
	entry, failure := loadMemoryEntry(ctx, q, scope, id)
	if failure != nil {
		return failure
	}
	if entry.Status != "active" || (entry.Scope == "global" && !settings.UseGlobal) {
		return &agentHarborMemoryFailure{status: http.StatusNotFound, message: "memory entry not found"}
	}
	return nil
}

func (h *Handler) ListAgentHarborMemoryMaterials(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListAgentHarborMemoryMaterials(r.Context(), db.ListAgentHarborMemoryMaterialsParams{
		WorkspaceID: scope.workspace, ProjectID: scope.project,
	})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"materials": rows})
}
