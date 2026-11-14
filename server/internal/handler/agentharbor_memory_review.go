package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
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
