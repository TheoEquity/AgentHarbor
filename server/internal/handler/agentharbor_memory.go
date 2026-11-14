package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

const agentHarborMemoryTokenTTL = 30 * 24 * time.Hour

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

	return projectID, true
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

func (h *Handler) scopedAgentHarborMemoryProject(w http.ResponseWriter, r *http.Request) (agentHarborMemoryScope, bool) {
	if r.URL.Query().Has("project_id") {
		writeError(w, http.StatusBadRequest, "project selector is not allowed")
		return agentHarborMemoryScope{}, false
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "invalid memory token")
		return agentHarborMemoryScope{}, false
	}
	claims, ok := verifyAgentHarborMemoryToken(strings.TrimPrefix(authorization, "Bearer "), time.Now())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid memory token")
		return agentHarborMemoryScope{}, false
	}
	if auth.IsTemporarilyDisabledUser(claims.UserID, "") {
		writeError(w, http.StatusForbidden, "memory token owner is disabled")
		return agentHarborMemoryScope{}, false
	}
	workspaceID, _ := util.ParseUUID(claims.WorkspaceID)
	userID, _ := util.ParseUUID(claims.UserID)
	projectID, _ := util.ParseUUID(claims.ProjectID)
	if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID: userID, WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusForbidden, "memory token owner is not a workspace member")
		return agentHarborMemoryScope{}, false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return agentHarborMemoryScope{}, false
	}
	return agentHarborMemoryScope{workspace: workspaceID, project: projectID, actor: userID}, true
}
