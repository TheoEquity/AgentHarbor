package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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
