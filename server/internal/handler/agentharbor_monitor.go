package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentHarborMonitorPageSize = 50

type agentHarborMonitorScope struct {
	workspace pgtype.UUID
	user      pgtype.UUID
}

type agentHarborMonitorTurnInput struct {
	SessionID  string     `json:"session_id"`
	TurnID     string     `json:"turn_id"`
	Title      string     `json:"title"`
	StartedAt  *time.Time `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Status     string     `json:"status"`
	Historical bool       `json:"historical"`
	Handled    *bool      `json:"handled"`
}

type agentHarborMonitorTask struct {
	ProjectID   string     `json:"project_id"`
	ProjectName string     `json:"project_name"`
	MachineID   string     `json:"machine_id"`
	MachineName string     `json:"machine_name"`
	SessionID   string     `json:"session_id"`
	TurnID      string     `json:"turn_id"`
	Title       string     `json:"title"`
	StartedAt   *time.Time `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at"`
	Status      string     `json:"status"`
	Handled     *bool      `json:"handled"`
	Historical  bool       `json:"historical"`
}

type agentHarborMonitorSource struct {
	ProjectID    string    `json:"project_id"`
	MachineID    string    `json:"machine_id"`
	MachineName  string    `json:"machine_name"`
	LastSyncedAt time.Time `json:"last_synced_at"`
	Stale        bool      `json:"stale"`
}

func (h *Handler) agentHarborMonitorScope(w http.ResponseWriter, r *http.Request) (agentHarborMonitorScope, bool) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
		return agentHarborMonitorScope{}, false
	}
	workspace, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return agentHarborMonitorScope{}, false
	}
	member, ok := h.workspaceMember(w, r, uuidToString(workspace))
	return agentHarborMonitorScope{workspace: workspace, user: member.UserID}, ok
}

func (h *Handler) beginAgentHarborMonitorWrite(w http.ResponseWriter, r *http.Request, scope agentHarborMonitorScope, project pgtype.UUID) (pgx.Tx, *db.Queries, bool) {
	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return nil, nil, false
	}
	q := h.Queries.WithTx(tx)
	// The monitor tables have no FKs. Take parent locks before writing so
	// project, workspace and membership deletion can remove all dependents.
	if _, err = q.LockWorkspaceForChatSessionCreate(ctx, scope.workspace); err == nil && project.Valid {
		_, err = q.LockProjectForChatSessionCreate(ctx, db.LockProjectForChatSessionCreateParams{WorkspaceID: scope.workspace, ID: project})
	}
	if err == nil {
		_, err = q.LockAgentHarborMonitorMember(ctx, db.LockAgentHarborMonitorMemberParams{WorkspaceID: scope.workspace, UserID: scope.user})
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		agentHarborMonitorDatabaseError(w, err)
		return nil, nil, false
	}
	return tx, q, true
}

func agentHarborMonitorDatabaseError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor resource not found")
		return
	}
	slog.Error("AgentHarbor monitor database operation failed")
	writeError(w, http.StatusInternalServerError, "monitor operation failed")
}

func decodeAgentHarborMonitorBody(w http.ResponseWriter, r *http.Request, dest any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func validAgentHarborMonitorText(s string, maxRunes int) bool {
	return strings.TrimSpace(s) != "" && strings.IndexByte(s, 0) < 0 && utf8.RuneCountInString(s) <= maxRunes
}

func agentHarborMonitorTimestamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func agentHarborMonitorTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time.UTC()
	return &value
}

func (h *Handler) SyncAgentHarborMonitor(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.agentHarborMonitorScope(w, r)
	if !ok {
		return
	}
	var req struct {
		ProjectID  string                        `json:"project_id"`
		Machine    agentHarborMachine            `json:"machine"`
		ScanStatus string                        `json:"scan_status"`
		Turns      []agentHarborMonitorTurnInput `json:"turns"`
	}
	if !decodeAgentHarborMonitorBody(w, r, &req, agentHarborSyncBodyLimit) {
		return
	}
	project, ok := parseUUIDOrBadRequest(w, req.ProjectID, "project_id")
	if !ok {
		return
	}
	machine, ok := parseUUIDOrBadRequest(w, req.Machine.ID, "machine.id")
	if !ok {
		return
	}
	if req.Turns == nil || len(req.Turns) > 100 ||
		(req.ScanStatus != "ok" && req.ScanStatus != "degraded") ||
		!validAgentHarborMonitorText(req.Machine.Name, 256) {
		writeError(w, http.StatusBadRequest, "invalid monitor sync batch")
		return
	}
	for _, turn := range req.Turns {
		if !validAgentHarborMonitorText(turn.SessionID, 256) || !validAgentHarborMonitorText(turn.TurnID, 256) ||
			!validAgentHarborMonitorText(turn.Title, 512) ||
			(turn.Status != "running" && turn.Status != "completed" && turn.Status != "aborted") ||
			(turn.Status == "running" && turn.EndedAt != nil) {
			writeError(w, http.StatusBadRequest, "invalid monitor turn")
			return
		}
	}
	tx, q, ok := h.beginAgentHarborMonitorWrite(w, r, scope, project)
	if !ok {
		return
	}
	ctx := r.Context()
	defer tx.Rollback(ctx)
	if err := q.UpsertAgentHarborMonitorSource(ctx, db.UpsertAgentHarborMonitorSourceParams{
		WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine,
		MachineName: req.Machine.Name, ScanStatus: req.ScanStatus,
	}); err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	for _, turn := range req.Turns {
		row, err := q.UpsertAgentHarborMonitorTurn(ctx, db.UpsertAgentHarborMonitorTurnParams{
			WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine,
			SessionID: turn.SessionID, TurnID: turn.TurnID, Title: turn.Title,
			StartedAt: agentHarborMonitorTimestamp(turn.StartedAt), EndedAt: agentHarborMonitorTimestamp(turn.EndedAt),
			Status: turn.Status, Historical: turn.Historical,
		})
		if err != nil {
			agentHarborMonitorDatabaseError(w, err)
			return
		}
		if turn.Status == "running" || turn.Status != row.Status {
			continue
		}
		if turn.Historical && row.Historical && turn.Handled != nil && *turn.Handled {
			err = q.MarkAgentHarborMonitorHandled(ctx, db.MarkAgentHarborMonitorHandledParams{
				WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine,
				SessionID: row.SessionID, TurnID: row.TurnID, UserID: scope.user,
			})
		} else if !turn.Historical && !row.Historical && row.EndedAt.Valid {
			outcome := "已完成"
			if row.Status == "aborted" {
				outcome = "已中止"
			}
			err = q.EnqueueAgentHarborBarkDelivery(ctx, db.EnqueueAgentHarborBarkDeliveryParams{
				WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine,
				SessionID: row.SessionID, TurnID: row.TurnID, Title: row.Title,
				UserID: scope.user, EndedAt: row.EndedAt, Outcome: outcome,
			})
		}
		if err != nil {
			agentHarborMonitorDatabaseError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted_count": len(req.Turns)})
}

func (h *Handler) ListAgentHarborMonitor(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.agentHarborMonitorScope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	args := r.URL.Query()
	view := args.Get("view")
	if view != "" && view != "active" && view != "history" {
		writeError(w, http.StatusBadRequest, "invalid monitor view")
		return
	}
	var project, machine pgtype.UUID
	if raw := args.Get("project_id"); raw != "" {
		project, ok = parseUUIDOrBadRequest(w, raw, "project_id")
		if !ok {
			return
		}
		if _, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: project, WorkspaceID: scope.workspace}); err != nil {
			agentHarborMonitorDatabaseError(w, err)
			return
		}
	}
	if raw := args.Get("machine_id"); raw != "" {
		machine, ok = parseUUIDOrBadRequest(w, raw, "machine_id")
		if !ok {
			return
		}
	}
	page := int64(1)
	if raw := args.Get("page"); raw != "" {
		var err error
		page, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || page < 1 {
			writeError(w, http.StatusBadRequest, "invalid monitor page")
			return
		}
	}
	params := db.ListAgentHarborMonitorTurnsParams{
		WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine, UserID: scope.user,
		History: view == "history",
	}
	if params.History {
		params.PageLimit = pgtype.Int8{Int64: agentHarborMonitorPageSize + 1, Valid: true}
		params.PageOffset = (page - 1) * agentHarborMonitorPageSize
	}
	rows, err := h.Queries.ListAgentHarborMonitorTurns(ctx, params)
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	hasMore := params.History && len(rows) > agentHarborMonitorPageSize
	if hasMore {
		rows = rows[:agentHarborMonitorPageSize]
	}
	tasks := make([]agentHarborMonitorTask, 0, len(rows))
	for _, row := range rows {
		var handled *bool
		if row.HasHandling || (row.Status != "running" && !row.Historical) {
			handled = &row.HasHandling
		}
		tasks = append(tasks, agentHarborMonitorTask{
			ProjectID: uuidToString(row.ProjectID), ProjectName: row.ProjectName,
			MachineID: uuidToString(row.MachineID), MachineName: row.MachineName,
			SessionID: row.SessionID, TurnID: row.TurnID, Title: row.Title, Status: row.Status,
			StartedAt: agentHarborMonitorTime(row.StartedAt), EndedAt: agentHarborMonitorTime(row.EndedAt),
			Handled: handled, Historical: row.Historical,
		})
	}
	sourceRows, err := h.Queries.ListAgentHarborMonitorSources(ctx, db.ListAgentHarborMonitorSourcesParams{
		WorkspaceID: scope.workspace, ProjectID: project,
	})
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	sources := make([]agentHarborMonitorSource, 0, len(sourceRows))
	for _, row := range sourceRows {
		sources = append(sources, agentHarborMonitorSource{
			ProjectID: uuidToString(row.ProjectID), MachineID: uuidToString(row.MachineID),
			MachineName: row.MachineName, LastSyncedAt: row.LastSyncedAt.Time.UTC(), Stale: row.Stale.Bool,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks, "sources": sources, "has_more": hasMore})
}

func (h *Handler) HandleAgentHarborMonitorTurn(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.agentHarborMonitorScope(w, r)
	if !ok {
		return
	}
	var req struct {
		ProjectID string `json:"project_id"`
		MachineID string `json:"machine_id"`
		SessionID string `json:"session_id"`
		TurnID    string `json:"turn_id"`
	}
	if !decodeAgentHarborMonitorBody(w, r, &req, 8192) {
		return
	}
	project, ok := parseUUIDOrBadRequest(w, req.ProjectID, "project_id")
	if !ok {
		return
	}
	machine, ok := parseUUIDOrBadRequest(w, req.MachineID, "machine_id")
	if !ok {
		return
	}
	if !validAgentHarborMonitorText(req.SessionID, 256) || !validAgentHarborMonitorText(req.TurnID, 256) {
		writeError(w, http.StatusBadRequest, "session_id and turn_id are required")
		return
	}
	tx, q, ok := h.beginAgentHarborMonitorWrite(w, r, scope, project)
	if !ok {
		return
	}
	ctx := r.Context()
	defer tx.Rollback(ctx)
	status, err := q.GetAgentHarborMonitorTurnStatus(ctx, db.GetAgentHarborMonitorTurnStatusParams{
		WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine, SessionID: req.SessionID, TurnID: req.TurnID,
	})
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	if status == "running" {
		writeErrorCode(w, http.StatusConflict, "monitor_turn_running", "running turns cannot be handled")
		return
	}
	if err := q.MarkAgentHarborMonitorHandled(ctx, db.MarkAgentHarborMonitorHandledParams{
		WorkspaceID: scope.workspace, ProjectID: project, MachineID: machine,
		SessionID: req.SessionID, TurnID: req.TurnID, UserID: scope.user,
	}); err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"handled": true})
}
