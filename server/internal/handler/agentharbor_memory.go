package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentHarborMemoryTokenTTL = 30 * 24 * time.Hour

// MemoryCore L3 replaces one document, so append operations on this server must
// serialize their read-modify-write cycle.
var agentHarborMemoryWriteMu sync.Mutex

type agentHarborMemoryTokenClaims struct {
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	ExpiresAt   int64  `json:"expires_at"`
}

func agentHarborMemoryTokenSignature(payload string) []byte {
	key := sha256.Sum256(append([]byte("agentharbor-project-memory-v1:"), auth.JWTSecret()...))
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("ahm_" + payload))
	return mac.Sum(nil)
}

func signAgentHarborMemoryToken(claims agentHarborMemoryTokenClaims) (string, error) {
	data, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	return "ahm_" + payload + "." + base64.RawURLEncoding.EncodeToString(agentHarborMemoryTokenSignature(payload)), nil
}

func verifyAgentHarborMemoryToken(raw string, now time.Time) (agentHarborMemoryTokenClaims, bool) {
	var claims agentHarborMemoryTokenClaims
	if !strings.HasPrefix(raw, "ahm_") || len(raw) > 1024 {
		return claims, false
	}
	payload, signature, ok := strings.Cut(strings.TrimPrefix(raw, "ahm_"), ".")
	if !ok || payload == "" {
		return claims, false
	}
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(got, agentHarborMemoryTokenSignature(payload)) {
		return claims, false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(data, &claims) != nil || claims.ExpiresAt <= now.Unix() {
		return claims, false
	}
	if _, err = util.ParseUUID(claims.UserID); err != nil {
		return claims, false
	}
	if _, err = util.ParseUUID(claims.WorkspaceID); err != nil {
		return claims, false
	}
	if _, err = util.ParseUUID(claims.ProjectID); err != nil {
		return claims, false
	}
	return claims, true
}

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
	agentHarborMemoryWriteMu.Lock()
	defer agentHarborMemoryWriteMu.Unlock()
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

// IssueAgentHarborMemoryToken gives a human-owned project a memory-only credential.
func (h *Handler) IssueAgentHarborMemoryToken(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
		return
	}
	projectID, ok := h.agentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	expiresAt := time.Now().Add(agentHarborMemoryTokenTTL).Unix()
	token, err := signAgentHarborMemoryToken(agentHarborMemoryTokenClaims{
		UserID: userID, WorkspaceID: h.resolveWorkspaceID(r), ProjectID: projectID, ExpiresAt: expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue memory token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"token": token, "project_id": projectID, "expires_at": time.Unix(expiresAt, 0).UTC().Format(time.RFC3339),
	})
}

func (h *Handler) scopedAgentHarborMemoryProject(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.URL.Query().Has("project_id") {
		writeError(w, http.StatusBadRequest, "project selector is not allowed")
		return "", false
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "invalid memory token")
		return "", false
	}
	claims, ok := verifyAgentHarborMemoryToken(strings.TrimPrefix(authorization, "Bearer "), time.Now())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid memory token")
		return "", false
	}
	if auth.IsTemporarilyDisabledUser(claims.UserID, "") {
		writeError(w, http.StatusForbidden, "memory token owner is disabled")
		return "", false
	}
	workspaceID, _ := util.ParseUUID(claims.WorkspaceID)
	userID, _ := util.ParseUUID(claims.UserID)
	projectID, _ := util.ParseUUID(claims.ProjectID)
	if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID: userID, WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusForbidden, "memory token owner is not a workspace member")
		return "", false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return "", false
	}
	if h.AgentHarborMemoryCore == nil {
		writeError(w, http.StatusServiceUnavailable, "MemoryCore is not configured")
		return "", false
	}
	return claims.ProjectID, true
}

// GetScopedAgentHarborMemory reads only the project signed into the bearer token.
func (h *Handler) GetScopedAgentHarborMemory(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.scopedAgentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	snapshot, err := h.AgentHarborMemoryCore.ReadCore(r.Context(), projectID)
	if err != nil {
		slog.Error("AgentHarbor MemoryCore scoped read failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore read failed")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// AppendScopedAgentHarborMemory adds one explicit session note without replacing prior L3 content.
func (h *Handler) AppendScopedAgentHarborMemory(w http.ResponseWriter, r *http.Request) {
	projectID, ok := h.scopedAgentHarborMemoryProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Content) == "" || len(body.Content) > 2<<10 {
		writeError(w, http.StatusBadRequest, "content must be 1-2048 bytes and cannot select a project")
		return
	}
	agentHarborMemoryWriteMu.Lock()
	defer agentHarborMemoryWriteMu.Unlock()
	before, err := h.AgentHarborMemoryCore.ReadCore(r.Context(), projectID)
	if err != nil {
		slog.Error("AgentHarbor MemoryCore read before append failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore read failed")
		return
	}
	note := "## Session note " + time.Now().UTC().Format(time.RFC3339) + "\n" + strings.TrimSpace(body.Content)
	content := note
	if strings.TrimSpace(before.Content) != "" {
		content = strings.TrimRight(before.Content, "\n") + "\n\n" + note
	}
	if len(content) > 16<<10 {
		writeError(w, http.StatusConflict, "core memory document is full")
		return
	}
	if err := h.AgentHarborMemoryCore.WriteCore(r.Context(), projectID, content); err != nil {
		slog.Error("AgentHarbor MemoryCore scoped append failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore write failed")
		return
	}
	snapshot, err := h.AgentHarborMemoryCore.ReadCore(r.Context(), projectID)
	if err != nil {
		slog.Error("AgentHarbor MemoryCore read after append failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "MemoryCore read after write failed")
		return
	}
	if snapshot.Content != content {
		writeError(w, http.StatusServiceUnavailable, "MemoryCore write was not confirmed")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
