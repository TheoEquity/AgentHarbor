package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/remotemcp"
)

const (
	agentHarborBarkKeyEnv  = "AGENTHARBOR_BARK_SECRET_KEY"
	agentHarborBarkTimeout = 10 * time.Second
)

type agentHarborBark struct {
	box           *secretbox.Box
	newHTTPClient func(*url.URL) *http.Client
}

type agentHarborBarkCredentials struct {
	ServerURL string `json:"server_url"`
	DeviceKey string `json:"device_key"`
}

type agentHarborBarkStatus struct {
	Configured bool `json:"configured"`
	Enabled    bool `json:"enabled"`
}

func (h *Handler) ConfigureAgentHarborBark() {
	if os.Getenv(agentHarborBarkKeyEnv) == "" {
		return
	}
	key, err := secretbox.LoadKey(agentHarborBarkKeyEnv)
	if err != nil {
		slog.Warn("AgentHarbor Bark disabled: AGENTHARBOR_BARK_SECRET_KEY must be 32 bytes encoded as base64")
		return
	}
	box, err := secretbox.New(key)
	if err != nil {
		slog.Warn("AgentHarbor Bark disabled: unable to initialize encryption")
		return
	}
	h.agentHarborBark = &agentHarborBark{box: box, newHTTPClient: remotemcp.NewStrictPublicHTTPSClient}
}

func (h *Handler) GetAgentHarborBark(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.agentHarborMonitorScope(w, r)
	if !ok {
		return
	}
	row, err := h.Queries.GetAgentHarborBarkSetting(r.Context(), db.GetAgentHarborBarkSettingParams{
		WorkspaceID: scope.workspace, UserID: scope.user,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, agentHarborBarkStatus{})
		return
	}
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentHarborBarkStatus{Configured: true, Enabled: row.EnabledSince.Valid})
}

func (h *Handler) PutAgentHarborBark(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.agentHarborMonitorScope(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled   *bool   `json:"enabled"`
		ServerURL *string `json:"server_url"`
		DeviceKey *string `json:"device_key"`
	}
	if !decodeAgentHarborMonitorBody(w, r, &req, 8192) {
		return
	}
	if req.Enabled == nil || (req.ServerURL == nil) != (req.DeviceKey == nil) {
		writeErrorCode(w, http.StatusBadRequest, "bark_invalid_settings", "enabled is required and Bark credentials must be supplied together")
		return
	}
	if h.agentHarborBark == nil && (*req.Enabled || req.ServerURL != nil) {
		writeErrorCode(w, http.StatusServiceUnavailable, "bark_unavailable", "Bark encryption is unavailable")
		return
	}
	var sealed []byte
	if req.ServerURL != nil {
		rawURL, key := strings.TrimSpace(*req.ServerURL), strings.TrimSpace(*req.DeviceKey)
		if len(rawURL) > 2048 || !validAgentHarborMonitorText(key, 512) {
			writeErrorCode(w, http.StatusBadRequest, "bark_invalid_settings", "invalid Bark server URL or device key")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), agentHarborBarkTimeout)
		endpoint, err := remotemcp.ValidateStrictPublicHTTPSEndpoint(ctx, rawURL)
		cancel()
		if err != nil {
			writeErrorCode(w, http.StatusBadRequest, "bark_invalid_settings", "Bark server URL must be publicly reachable over HTTPS")
			return
		}
		// Both fields are strings, so JSON marshaling cannot fail.
		credentials, _ := json.Marshal(agentHarborBarkCredentials{ServerURL: endpoint.String(), DeviceKey: key})
		sealed, err = h.agentHarborBark.box.Seal(credentials)
		if err != nil {
			writeErrorCode(w, http.StatusServiceUnavailable, "bark_unavailable", "Bark encryption is unavailable")
			return
		}
	}
	tx, q, ok := h.beginAgentHarborMonitorWrite(w, r, scope, pgtype.UUID{})
	if !ok {
		return
	}
	ctx := r.Context()
	defer tx.Rollback(ctx)
	var row db.AgentharborBarkSetting
	var err error
	if sealed != nil {
		row, err = q.UpsertAgentHarborBarkSetting(ctx, db.UpsertAgentHarborBarkSettingParams{
			WorkspaceID: scope.workspace, UserID: scope.user, Credentials: sealed, Enabled: *req.Enabled,
		})
	} else {
		row, err = q.SetAgentHarborBarkEnabled(ctx, db.SetAgentHarborBarkEnabledParams{
			WorkspaceID: scope.workspace, UserID: scope.user, Enabled: *req.Enabled,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			if *req.Enabled {
				writeErrorCode(w, http.StatusConflict, "bark_not_configured", "Bark settings are not configured")
			} else {
				writeJSON(w, http.StatusOK, agentHarborBarkStatus{})
			}
			return
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentHarborBarkStatus{Configured: true, Enabled: row.EnabledSince.Valid})
}

func (h *Handler) TestAgentHarborBark(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.agentHarborMonitorScope(w, r)
	if !ok {
		return
	}
	if h.agentHarborBark == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "bark_unavailable", "Bark encryption is unavailable")
		return
	}
	ctx := r.Context()
	row, err := h.Queries.GetAgentHarborBarkSetting(ctx, db.GetAgentHarborBarkSettingParams{
		WorkspaceID: scope.workspace, UserID: scope.user,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErrorCode(w, http.StatusConflict, "bark_not_configured", "Bark settings are not configured")
		return
	}
	if err != nil {
		agentHarborMonitorDatabaseError(w, err)
		return
	}
	if !h.agentHarborBark.send(ctx, row.Credentials, "AgentHarbor", "Bark 测试提醒") {
		writeErrorCode(w, http.StatusBadGateway, "bark_delivery_failed", "Bark test notification failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// send uses the Bark V2 API. Only its result crosses this boundary because
// transport and upstream errors can contain the server URL or device key.
func (b *agentHarborBark) send(ctx context.Context, sealed []byte, title, body string) bool {
	clear, err := b.box.Open(sealed)
	if err != nil {
		return false
	}
	var credentials agentHarborBarkCredentials
	if err := json.Unmarshal(clear, &credentials); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, agentHarborBarkTimeout)
	defer cancel()
	serverURL, err := remotemcp.ValidateStrictPublicHTTPSEndpoint(ctx, credentials.ServerURL)
	if err != nil {
		return false
	}
	payload, err := json.Marshal(map[string]string{"title": title, "body": body, "device_key": credentials.DeviceKey})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL.JoinPath("push").String(), bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	client := b.newHTTPClient(serverURL)
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return false
	}
	var result struct {
		Code int `json:"code"`
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&result) == nil && result.Code == http.StatusOK
}

func (h *Handler) RunAgentHarborBark(ctx context.Context) {
	if h.agentHarborBark == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for range 20 {
			claimed, err := h.processAgentHarborBarkDelivery(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Error("AgentHarbor Bark delivery queue operation failed")
			}
			if !claimed || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) processAgentHarborBarkDelivery(ctx context.Context) (bool, error) {
	delivery, err := h.Queries.ClaimAgentHarborBarkDelivery(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	setting, err := h.Queries.GetAgentHarborBarkSetting(ctx, db.GetAgentHarborBarkSettingParams{
		WorkspaceID: delivery.WorkspaceID, UserID: delivery.UserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if !setting.EnabledSince.Valid {
		return true, nil
	}
	sent := h.agentHarborBark.send(ctx, setting.Credentials, delivery.Title, delivery.Body)
	delay := min(900, 15<<min(delivery.Attempts-1, 6))
	err = h.Queries.FinishAgentHarborBarkDelivery(ctx, db.FinishAgentHarborBarkDeliveryParams{
		WorkspaceID: delivery.WorkspaceID, ProjectID: delivery.ProjectID, MachineID: delivery.MachineID,
		SessionID: delivery.SessionID, TurnID: delivery.TurnID, UserID: delivery.UserID,
		Attempts: delivery.Attempts, Sent: sent, RetrySeconds: int32(delay),
	})
	if err == nil && !sent && ctx.Err() == nil {
		slog.Warn("AgentHarbor Bark delivery failed; retry scheduled", "attempt", delivery.Attempts)
	}
	return true, err
}
