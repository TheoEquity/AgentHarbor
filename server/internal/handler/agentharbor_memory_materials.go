package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) SubmitAgentHarborMemoryMaterial(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.scopedAgentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	var body struct {
		RequestID       string `json:"request_id"`
		SourceText      string `json:"source_text"`
		Context         string `json:"context"`
		SessionID       string `json:"session_id"`
		TurnID          string `json:"turn_id"`
		RelatedEntryID  string `json:"related_entry_id"`
		RelatedRevision *int32 `json:"related_revision"`
	}
	if !decodeMemoryBody(w, r, &body) {
		return
	}
	if !validMemoryText(body.SourceText, 8192) || len(body.Context) > 4096 || len(body.SessionID) > 255 ||
		len(body.TurnID) > 255 || strings.ContainsRune(body.Context+body.SessionID+body.TurnID, 0) {
		writeError(w, http.StatusBadRequest, "invalid source text, context, or session reference")
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
	var revision pgtype.Int4
	if body.RelatedRevision != nil {
		if !relatedID.Valid || *body.RelatedRevision < 1 {
			writeError(w, http.StatusBadRequest, "related revision requires an entry and a positive revision")
			return
		}
		revision = pgtype.Int4{Int32: *body.RelatedRevision, Valid: true}
	}
	if relatedID.Valid {
		entry, ok := loadMemoryEntry(w, r.Context(), h.Queries, scope, relatedID)
		if !ok {
			return
		}
		if entry.Scope == "global" {
			settings, err := readMemorySettings(r.Context(), h.Queries, scope)
			if err != nil {
				memoryStoreError(w, err)
				return
			}
			if !settings.UseGlobal || !settings.UseMemory {
				writeError(w, http.StatusNotFound, "memory entry not found")
				return
			}
		}
	}
	ctx := r.Context()
	row, status, ok := h.storeMemoryMaterial(w, ctx, scope, db.CreateAgentHarborMemoryMaterialParams{
		WorkspaceID: scope.workspace, ProjectID: scope.project, SubmittedBy: scope.actor,
		RequestID: requestID, SourceText: body.SourceText, Context: body.Context,
		SessionID: body.SessionID, TurnID: body.TurnID, RelatedEntryID: relatedID, RelatedRevision: revision,
	})
	if ok {
		writeJSON(w, status, row)
	}
}

func (h *Handler) storeMemoryMaterial(w http.ResponseWriter, ctx context.Context, scope agentHarborMemoryScope, input db.CreateAgentHarborMemoryMaterialParams) (db.AgentharborMemoryMaterial, int, bool) {
	tx, q, ok := h.beginMemoryWrite(w, ctx, scope)
	if !ok {
		return db.AgentharborMemoryMaterial{}, 0, false
	}
	defer tx.Rollback(ctx)
	settings, err := readMemorySettings(ctx, q, scope)
	if err != nil {
		memoryStoreError(w, err)
		return db.AgentharborMemoryMaterial{}, 0, false
	}
	var row db.AgentharborMemoryMaterial
	status := http.StatusCreated
	if settings.RecordEnabled && settings.CloudEnabled {
		row, err = q.CreateAgentHarborMemoryMaterial(ctx, input)
	} else {
		err = pgx.ErrNoRows
	}
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = q.GetAgentHarborMemoryMaterialByRequest(ctx, db.GetAgentHarborMemoryMaterialByRequestParams{
			WorkspaceID: scope.workspace, ProjectID: scope.project, SubmittedBy: input.SubmittedBy, RequestID: input.RequestID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusForbidden, "memory recording is disabled for this project")
			return db.AgentharborMemoryMaterial{}, 0, false
		}
		if err == nil && (row.SourceText != input.SourceText || row.Context != input.Context || row.SessionID != input.SessionID ||
			row.TurnID != input.TurnID || row.RelatedEntryID != input.RelatedEntryID || row.RelatedRevision != input.RelatedRevision) {
			writeErrorCode(w, http.StatusConflict, "memory_request_conflict", "request id already belongs to different material")
			return db.AgentharborMemoryMaterial{}, 0, false
		}
		status = http.StatusOK
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		memoryStoreError(w, err)
		return db.AgentharborMemoryMaterial{}, 0, false
	}
	return row, status, true
}

func (h *Handler) ImportAgentHarborLegacyMemory(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	if h.AgentHarborMemoryCore == nil {
		writeError(w, http.StatusServiceUnavailable, "legacy MemoryCore is not configured")
		return
	}
	projectID := uuid.UUID(scope.project.Bytes).String()
	ctx := r.Context()
	snapshot, err := h.AgentHarborMemoryCore.ReadCore(ctx, projectID)
	if err != nil {
		slog.Error("AgentHarbor legacy memory read failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "legacy memory could not be read")
		return
	}
	if strings.TrimSpace(snapshot.Content) == "" {
		writeJSON(w, http.StatusOK, struct{}{})
		return
	}
	// The same legacy project/version is imported once regardless of reviewer.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("agentharbor-legacy-l3:"+projectID+":"+snapshot.Version))
	row, _, ok := h.storeMemoryMaterial(w, ctx, scope, db.CreateAgentHarborMemoryMaterialParams{
		WorkspaceID: scope.workspace, ProjectID: scope.project,
		SubmittedBy: scope.project, RequestID: pgtype.UUID{Bytes: id, Valid: true},
		SourceText: snapshot.Content, Context: "Imported MemoryCore L3, version " + snapshot.Version,
	})
	if ok {
		writeJSON(w, http.StatusOK, map[string]any{"material": row})
	}

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
