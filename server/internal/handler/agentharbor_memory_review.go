package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type agentHarborMemoryScope struct {
	workspace, project, actor pgtype.UUID
}

type agentHarborMemorySettings struct {
	RecordEnabled bool `json:"record_enabled"`
	CloudEnabled  bool `json:"cloud_enabled"`
	UseMemory     bool `json:"use_memory"`
	UseGlobal     bool `json:"use_global"`
}

func (h *Handler) browserMemoryScope(w http.ResponseWriter, r *http.Request) (agentHarborMemoryScope, bool) {
	project, ok := h.agentHarborMemoryProject(w, r)
	if !ok {
		return agentHarborMemoryScope{}, false
	}
	actor, ok := requireUserID(w, r)
	if !ok {
		return agentHarborMemoryScope{}, false
	}
	projectID, _ := util.ParseUUID(project)
	workspaceID, _ := util.ParseUUID(h.resolveWorkspaceID(r))
	actorID, ok := parseUUIDOrBadRequest(w, actor, "user id")
	return agentHarborMemoryScope{workspace: workspaceID, project: projectID, actor: actorID}, ok
}

func readMemorySettings(ctx context.Context, q *db.Queries, scope agentHarborMemoryScope) (agentHarborMemorySettings, error) {
	row, err := q.GetAgentHarborMemorySettings(ctx, db.GetAgentHarborMemorySettingsParams{WorkspaceID: scope.workspace, ProjectID: scope.project})
	if errors.Is(err, pgx.ErrNoRows) {
		return agentHarborMemorySettings{RecordEnabled: true, CloudEnabled: true, UseMemory: true}, nil
	}
	return agentHarborMemorySettings{
		RecordEnabled: row.RecordEnabled, CloudEnabled: row.CloudEnabled,
		UseMemory: row.UseMemory, UseGlobal: row.UseGlobal,
	}, err
}

func decodeMemoryBody(w http.ResponseWriter, r *http.Request, body any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid memory request")
		return false
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "expected one JSON object")
		return false
	}
	return true
}

func memoryStoreError(w http.ResponseWriter, err error) {
	slog.Error("AgentHarbor memory operation failed", "error", err)
	writeError(w, http.StatusInternalServerError, "memory operation failed")
}

func (h *Handler) GetAgentHarborMemorySettings(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	settings, err := readMemorySettings(r.Context(), h.Queries, scope)
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) PutAgentHarborMemorySettings(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	var body struct {
		RecordEnabled *bool `json:"record_enabled"`
		CloudEnabled  *bool `json:"cloud_enabled"`
		UseMemory     *bool `json:"use_memory"`
		UseGlobal     *bool `json:"use_global"`
	}
	if !decodeMemoryBody(w, r, &body) {
		return
	}
	if body.RecordEnabled == nil || body.CloudEnabled == nil || body.UseMemory == nil || body.UseGlobal == nil {
		writeError(w, http.StatusBadRequest, "all memory settings are required")
		return
	}
	ctx := r.Context()
	tx, q, ok := h.beginMemoryWrite(w, ctx, scope)
	if !ok {
		return
	}
	defer tx.Rollback(ctx)
	row, err := q.PutAgentHarborMemorySettings(ctx, db.PutAgentHarborMemorySettingsParams{
		ProjectID: scope.project, WorkspaceID: scope.workspace, RecordEnabled: *body.RecordEnabled,
		CloudEnabled: *body.CloudEnabled, UseMemory: *body.UseMemory, UseGlobal: *body.UseGlobal,
	})
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentHarborMemorySettings{
		RecordEnabled: row.RecordEnabled, CloudEnabled: row.CloudEnabled,
		UseMemory: row.UseMemory, UseGlobal: row.UseGlobal,
	})
}

func validMemoryText(s string, maxBytes int) bool {
	return len(s) <= maxBytes && strings.TrimSpace(s) != "" && !strings.ContainsRune(s, 0)
}

func validMemoryType(s string) bool {
	return s == "episodic" || s == "semantic" || s == "procedural"
}

func optionalMemoryID(w http.ResponseWriter, value, name string) (pgtype.UUID, bool) {
	if value == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, value, name)
}

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

func (h *Handler) beginMemoryWrite(w http.ResponseWriter, ctx context.Context, scope agentHarborMemoryScope) (pgx.Tx, *db.Queries, bool) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		memoryStoreError(w, err)
		return nil, nil, false
	}
	q := h.Queries.WithTx(tx)
	_, err = q.LockWorkspaceForChatSessionCreate(ctx, scope.workspace)
	if err == nil {
		_, err = q.LockProjectForChatSessionCreate(ctx, db.LockProjectForChatSessionCreateParams{ID: scope.project, WorkspaceID: scope.workspace})
	}
	if err == nil {
		return tx, q, true
	}
	_ = tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "memory project not found")
	} else {
		memoryStoreError(w, err)
	}
	return nil, nil, false
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

type agentHarborMemoryReview struct {
	Action                string   `json:"action"`
	Scope                 string   `json:"scope"`
	Type                  string   `json:"type"`
	Title                 string   `json:"title"`
	Content               string   `json:"content"`
	EntryID               string   `json:"entry_id"`
	ExpectedRevision      int32    `json:"expected_revision"`
	AdditionalMaterialIDs []string `json:"additional_material_ids"`
}

func (h *Handler) ReviewAgentHarborMemoryMaterial(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	materialID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "materialId"), "material id")
	if !ok {
		return
	}
	var body agentHarborMemoryReview
	if !decodeMemoryBody(w, r, &body) {
		return
	}
	if body.Action != "approve" && body.Action != "discard" {
		writeError(w, http.StatusBadRequest, "invalid review action")
		return
	}
	targetID, ok := optionalMemoryID(w, body.EntryID, "entry id")
	if !ok {
		return
	}
	if body.Action == "approve" && ((body.Scope != "project" && body.Scope != "global") ||
		!validMemoryType(body.Type) || !validMemoryText(body.Title, 240) || !validMemoryText(body.Content, 8192) ||
		(targetID.Valid && body.ExpectedRevision < 1)) {
		writeError(w, http.StatusBadRequest, "invalid approved memory or expected revision")
		return
	}
	if len(body.AdditionalMaterialIDs) > 49 {
		writeError(w, http.StatusBadRequest, "at most 50 materials can be reviewed together")
		return
	}
	ids := []pgtype.UUID{materialID}
	seen := map[pgtype.UUID]bool{materialID: true}
	for _, value := range body.AdditionalMaterialIDs {
		id, ok := parseUUIDOrBadRequest(w, value, "additional material id")
		if !ok {
			return
		}
		if seen[id] {
			writeError(w, http.StatusBadRequest, "duplicate material id")
			return
		}
		ids = append(ids, id)
		seen[id] = true
	}

	ctx := r.Context()
	tx, q, ok := h.beginMemoryWrite(w, ctx, scope)
	if !ok {
		return
	}
	defer tx.Rollback(ctx)
	if !allowMemoryCloudWrite(w, ctx, q, scope) {
		return
	}
	rows, err := q.LockAgentHarborMemoryMaterials(ctx, db.LockAgentHarborMemoryMaterialsParams{
		WorkspaceID: scope.workspace, ProjectID: scope.project, Ids: ids,
	})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	if len(rows) != len(ids) {
		writeError(w, http.StatusNotFound, "memory material not found")
		return
	}
	for _, row := range rows {
		if row.Status != "pending" {
			writeErrorCode(w, http.StatusConflict, "memory_already_reviewed", "memory material has already been reviewed")
			return
		}
	}

	response := struct {
		Material db.AgentharborMemoryMaterial `json:"material"`
		Entry    *db.AgentharborMemoryEntry   `json:"entry,omitempty"`
	}{}
	status := "discarded"
	if body.Action == "approve" {
		entry, ok := saveReviewedMemoryEntry(w, ctx, q, scope, body, targetID, ids)
		if !ok {
			return
		}
		response.Entry = &entry
		status = "processed"
	}
	finished, err := q.FinishAgentHarborMemoryMaterials(ctx, db.FinishAgentHarborMemoryMaterialsParams{
		Status: status, ReviewedBy: scope.actor, WorkspaceID: scope.workspace, ProjectID: scope.project, Ids: ids,
	})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	for _, row := range finished {
		if row.ID == materialID {
			response.Material = row
		}
	}
	if err := tx.Commit(ctx); err != nil {
		memoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func allowMemoryCloudWrite(w http.ResponseWriter, ctx context.Context, q *db.Queries, scope agentHarborMemoryScope) bool {
	settings, err := readMemorySettings(ctx, q, scope)
	if err != nil {
		memoryStoreError(w, err)
		return false
	}
	if !settings.CloudEnabled {
		writeError(w, http.StatusForbidden, "cloud memory updates are disabled for this project")
		return false
	}
	return true
}

func loadMemoryEntry(w http.ResponseWriter, ctx context.Context, q *db.Queries, scope agentHarborMemoryScope, id pgtype.UUID) (db.AgentharborMemoryEntry, bool) {
	entry, err := q.GetAgentHarborMemoryEntry(ctx, db.GetAgentHarborMemoryEntryParams{
		WorkspaceID: scope.workspace, ID: id, BoundProjectID: scope.project, ActorID: scope.actor,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "memory entry not found")
	} else if err != nil {
		memoryStoreError(w, err)
	}
	return entry, err == nil
}

func saveReviewedMemoryEntry(w http.ResponseWriter, ctx context.Context, q *db.Queries, scope agentHarborMemoryScope, body agentHarborMemoryReview, targetID pgtype.UUID, ids []pgtype.UUID) (db.AgentharborMemoryEntry, bool) {
	title, content := strings.TrimSpace(body.Title), strings.TrimSpace(body.Content)
	if targetID.Valid {
		old, ok := loadMemoryEntry(w, ctx, q, scope, targetID)
		if !ok {
			return db.AgentharborMemoryEntry{}, false
		}
		if old.Scope != body.Scope || old.Status != "active" {
			writeErrorCode(w, http.StatusConflict, "memory_entry_conflict", "entry is inactive or has a different scope")
			return db.AgentharborMemoryEntry{}, false
		}
		return updateMemoryEntry(w, ctx, q, scope, old.ID, body.ExpectedRevision, body.Type, title, content, old.Status, append(old.SourceMaterialIds, ids...))
	}
	project := scope.project
	if body.Scope == "global" {
		project = pgtype.UUID{}
	}
	row, err := q.CreateAgentHarborMemoryEntry(ctx, db.CreateAgentHarborMemoryEntryParams{
		WorkspaceID: scope.workspace, ProjectID: project, OwnerID: scope.actor, Scope: body.Scope,
		Type: body.Type, Title: title, Content: content, SourceMaterialIds: ids,
	})
	if err != nil {
		memoryStoreError(w, err)
		return db.AgentharborMemoryEntry{}, false
	}
	return row, recordMemoryRevision(w, ctx, q, scope, row.ID)
}

func recordMemoryRevision(w http.ResponseWriter, ctx context.Context, q *db.Queries, scope agentHarborMemoryScope, id pgtype.UUID) bool {
	err := q.RecordAgentHarborMemoryRevision(ctx, db.RecordAgentHarborMemoryRevisionParams{
		WorkspaceID: scope.workspace, ID: id, ChangedBy: scope.actor,
	})
	if err != nil {
		memoryStoreError(w, err)
		return false
	}
	return true
}

func updateMemoryEntry(w http.ResponseWriter, ctx context.Context, q *db.Queries, scope agentHarborMemoryScope, id pgtype.UUID, revision int32, kind, title, content, status string, sources []pgtype.UUID) (db.AgentharborMemoryEntry, bool) {
	row, err := q.UpdateAgentHarborMemoryEntry(ctx, db.UpdateAgentHarborMemoryEntryParams{
		Type: kind, Title: title, Content: content, Status: status, SourceMaterialIds: sources,
		WorkspaceID: scope.workspace, ID: id, ExpectedRevision: revision, BoundProjectID: scope.project, ActorID: scope.actor,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErrorCode(w, http.StatusConflict, "memory_revision_conflict", "memory entry changed; reload and review the latest revision")
		return db.AgentharborMemoryEntry{}, false
	}
	if err != nil {
		memoryStoreError(w, err)
		return db.AgentharborMemoryEntry{}, false
	}
	return row, recordMemoryRevision(w, ctx, q, scope, row.ID)
}

func (h *Handler) UpdateAgentHarborMemoryEntry(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "entryId"), "entry id")
	if !ok {
		return
	}
	var body struct {
		Action           string `json:"action"`
		ExpectedRevision int32  `json:"expected_revision"`
		Title            string `json:"title"`
		Content          string `json:"content"`
		Type             string `json:"type"`
	}
	if !decodeMemoryBody(w, r, &body) {
		return
	}
	if body.ExpectedRevision < 1 || (body.Action != "revise" && body.Action != "disable") ||
		(body.Action == "revise" && (!validMemoryType(body.Type) || !validMemoryText(body.Title, 240) || !validMemoryText(body.Content, 8192))) {
		writeError(w, http.StatusBadRequest, "invalid memory update or expected revision")
		return
	}
	ctx := r.Context()
	tx, q, ok := h.beginMemoryWrite(w, ctx, scope)
	if !ok {
		return
	}
	defer tx.Rollback(ctx)
	if !allowMemoryCloudWrite(w, ctx, q, scope) {
		return
	}
	old, ok := loadMemoryEntry(w, ctx, q, scope, id)
	if !ok {
		return
	}
	status := old.Status
	if body.Action == "disable" {
		status = "disabled"
		body.Title, body.Content, body.Type = old.Title, old.Content, old.Type
	}
	row, ok := updateMemoryEntry(w, ctx, q, scope, id, body.ExpectedRevision, body.Type,
		strings.TrimSpace(body.Title), strings.TrimSpace(body.Content), status, old.SourceMaterialIds)
	if !ok {
		return
	}
	if err := tx.Commit(ctx); err != nil {
		memoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) ListAgentHarborMemoryEntries(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	filter := r.URL.Query().Get("scope")
	if filter != "project" && filter != "global" {
		writeError(w, http.StatusBadRequest, "scope must be project or global")
		return
	}
	rows, err := h.Queries.ListAgentHarborMemoryEntries(r.Context(), db.ListAgentHarborMemoryEntriesParams{
		WorkspaceID: scope.workspace, BoundProjectID: scope.project, ActorID: scope.actor,
		ScopeFilter: filter, IncludeGlobal: filter == "global", MaxResults: 500,
	})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": rows})
}

func (h *Handler) readableMemoryEntries(ctx context.Context, scope agentHarborMemoryScope, kind, query string, limit int32, excludeDisputed bool) ([]db.AgentharborMemoryEntry, error) {
	settings, err := readMemorySettings(ctx, h.Queries, scope)
	if err != nil || !settings.UseMemory {
		return []db.AgentharborMemoryEntry{}, err
	}
	return h.Queries.ListAgentHarborMemoryEntries(ctx, db.ListAgentHarborMemoryEntriesParams{
		WorkspaceID: scope.workspace, BoundProjectID: scope.project, ActorID: scope.actor,
		IncludeGlobal: settings.UseGlobal, ActiveOnly: true, ExcludeDisputed: excludeDisputed, TypeFilter: kind, SearchText: query, MaxResults: limit,
	})
}

type agentHarborMemoryDispute struct {
	Entry  db.AgentharborMemoryEntry           `json:"entry"`
	Report db.ListAgentHarborMemoryDisputesRow `json:"report"`
}

func (h *Handler) memoryDisputes(ctx context.Context, scope agentHarborMemoryScope, entries []db.AgentharborMemoryEntry) ([]agentHarborMemoryDispute, error) {
	disputes := make([]agentHarborMemoryDispute, 0)
	if len(entries) == 0 {
		return disputes, nil
	}
	byID := make(map[pgtype.UUID]db.AgentharborMemoryEntry, len(entries))
	ids := make([]pgtype.UUID, 0, len(entries))
	for _, entry := range entries {
		if entry.Status == "active" {
			ids = append(ids, entry.ID)
			byID[entry.ID] = entry
		}
	}
	reports, err := h.Queries.ListAgentHarborMemoryDisputes(ctx, db.ListAgentHarborMemoryDisputesParams{
		WorkspaceID: scope.workspace, EntryIds: ids, BoundProjectID: scope.project, ActorID: scope.actor,
	})
	if err != nil {
		return nil, err
	}
	for _, report := range reports {
		entry, exists := byID[report.RelatedEntryID]
		if exists {
			disputes = append(disputes, agentHarborMemoryDispute{Entry: entry, Report: report})
		}
	}
	return disputes, nil
}

func (h *Handler) ListScopedAgentHarborMemoryEntries(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.scopedAgentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	kind, query := r.URL.Query().Get("type"), strings.TrimSpace(r.URL.Query().Get("query"))
	if (kind != "" && !validMemoryType(kind)) || len(query) > 512 || strings.ContainsRune(query, 0) {
		writeError(w, http.StatusBadRequest, "invalid memory search filters")
		return
	}
	rows, err := h.readableMemoryEntries(r.Context(), scope, kind, query, 25, false)
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	disputes, err := h.memoryDisputes(r.Context(), scope, rows)
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	disputed := make(map[pgtype.UUID]bool, len(disputes))
	for _, dispute := range disputes {
		disputed[dispute.Entry.ID] = true
	}
	entries := rows[:0]
	for _, row := range rows {
		if !disputed[row.ID] {
			entries = append(entries, row)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "disputes": disputes})
}

func (h *Handler) GetAgentHarborMemoryHistory(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "entryId"), "entry id")
	if !ok {
		return
	}
	ctx := r.Context()
	entry, ok := loadMemoryEntry(w, ctx, h.Queries, scope, id)
	if !ok {
		return
	}
	revisions, err := h.Queries.ListAgentHarborMemoryRevisions(ctx, db.ListAgentHarborMemoryRevisionsParams{
		WorkspaceID: scope.workspace, EntryID: entry.ID,
	})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	seen := make(map[pgtype.UUID]bool)
	var ids []pgtype.UUID
	for _, revision := range revisions {
		for _, id := range revision.SourceMaterialIds {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	materials, err := h.Queries.ListAgentHarborMemoryHistorySources(ctx, db.ListAgentHarborMemoryHistorySourcesParams{
		WorkspaceID: scope.workspace, Ids: ids,
	})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	disputes, err := h.memoryDisputes(ctx, scope, []db.AgentharborMemoryEntry{entry})
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	reports := make([]db.ListAgentHarborMemoryDisputesRow, 0, len(disputes))
	for _, dispute := range disputes {
		reports = append(reports, dispute.Report)
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revisions, "materials": materials, "disputes": reports})
}

func (h *Handler) GetScopedAgentHarborMemoryContext(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.scopedAgentHarborMemoryProject(w, r)
	if ok {
		h.writeMemoryContext(w, r.Context(), scope)
	}
}

func (h *Handler) GetAgentHarborMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.browserMemoryScope(w, r)
	if ok {
		h.writeMemoryContext(w, r.Context(), scope)
	}
}

func (h *Handler) writeMemoryContext(w http.ResponseWriter, ctx context.Context, scope agentHarborMemoryScope) {
	rows, err := h.readableMemoryEntries(ctx, scope, "", "", 100, true)
	if err != nil {
		memoryStoreError(w, err)
		return
	}
	var content strings.Builder
	entries := make([]db.AgentharborMemoryEntry, 0)
	for _, row := range rows {
		if row.Type == "episodic" {
			continue
		}
		block := fmt.Sprintf("## %s (%s)\nEntry %s, revision %d\n%s\n\n",
			row.Title, row.Scope, uuid.UUID(row.ID.Bytes).String(), row.Revision, row.Content)
		if content.Len()+len(block) > 2000 {
			continue
		}
		content.WriteString(block)
		entries = append(entries, row)
	}
	version := ""
	if content.Len() > 0 {
		payload, _ := json.Marshal(entries)
		digest := sha256.Sum256(payload)
		version = hex.EncodeToString(digest[:])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id": scope.project, "entries": entries, "content": strings.TrimSpace(content.String()), "version": version,
	})
}

func (h *Handler) RejectAgentHarborLegacyMemoryWrite(w http.ResponseWriter, _ *http.Request) {
	writeErrorCode(w, http.StatusGone, "memory_review_required", "submit a memory material and approve it in AgentHarbor")
}

func deleteAgentHarborProjectMemory(ctx context.Context, q *db.Queries, workspace, project pgtype.UUID) error {
	if err := q.DeleteAgentHarborMemoryRevisionsByProject(ctx, db.DeleteAgentHarborMemoryRevisionsByProjectParams{WorkspaceID: workspace, ProjectID: project}); err != nil {
		return err
	}
	if err := q.DeleteAgentHarborMemoryEntriesByProject(ctx, db.DeleteAgentHarborMemoryEntriesByProjectParams{WorkspaceID: workspace, ProjectID: project}); err != nil {
		return err
	}
	if err := q.DeleteAgentHarborMemoryMaterialsByProject(ctx, db.DeleteAgentHarborMemoryMaterialsByProjectParams{WorkspaceID: workspace, ProjectID: project}); err != nil {
		return err
	}
	return q.DeleteAgentHarborMemorySettingsByProject(ctx, db.DeleteAgentHarborMemorySettingsByProjectParams{WorkspaceID: workspace, ProjectID: project})
}

func deleteAgentHarborWorkspaceMemory(ctx context.Context, q *db.Queries, workspace pgtype.UUID) error {
	for _, remove := range []func(context.Context, pgtype.UUID) error{
		q.DeleteAgentHarborMemoryRevisionsByWorkspace, q.DeleteAgentHarborMemoryEntriesByWorkspace,
		q.DeleteAgentHarborMemoryMaterialsByWorkspace, q.DeleteAgentHarborMemorySettingsByWorkspace,
	} {
		if err := remove(ctx, workspace); err != nil {
			return err
		}
	}
	return nil
}
