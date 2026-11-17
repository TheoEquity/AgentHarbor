package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	"github.com/multica-ai/multica/server/pkg/remotemcp"
)

type agentHarborBarkRoundTripper func(*http.Request) (*http.Response, error)

func (f agentHarborBarkRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestAgentHarborBarkSettingsAndRetry(t *testing.T) {
	h := *testHandler
	box, err := secretbox.New(make([]byte, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	type push struct {
		Title     string `json:"title"`
		Body      string `json:"body"`
		DeviceKey string `json:"device_key"`
	}
	const deviceKey = "synthetic-device-key"
	const endpoint = "https://8.8.8.8/bark"
	var pushes []push
	failNext := false
	redirectNext := false
	h.agentHarborBark = &agentHarborBark{
		box: box,
		newHTTPClient: func(u *url.URL) *http.Client {
			client := remotemcp.NewStrictPublicHTTPSClient(u)
			client.Transport = agentHarborBarkRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.String() != endpoint+"/push" || r.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected Bark request %s %s", r.Method, r.URL)
				}
				var p push
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				pushes = append(pushes, p)
				if redirectNext {
					redirectNext = false
					return &http.Response{
						StatusCode: http.StatusTemporaryRedirect,
						Header:     http.Header{"Location": []string{"https://1.1.1.1/push"}},
						Body:       http.NoBody,
					}, nil
				}
				code := "200"
				if failNext {
					code = "500"
					failNext = false
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{\"code\":" + code + "}"))}, nil
			})
			return client
		},
	}

	projectID := dbfx.Project(t, "CIS synthetic")
	machineID := uuid.NewString()
	other := dbfx.User(t, "Other monitor user", "monitor-"+uuid.NewString()+"@example.invalid")
	dbfx.Member(t, testWorkspaceID, other, "member")
	t.Cleanup(func() {
		dbfx.Exec(t, "DELETE FROM agentharbor_bark_setting WHERE workspace_id = $1 AND user_id = $2", testWorkspaceID, testUserID)
		for _, table := range []string{"agentharbor_bark_delivery", "agentharbor_monitor_handled", "agentharbor_monitor_turn", "agentharbor_monitor_source"} {
			dbfx.Exec(t, "DELETE FROM "+table+" WHERE project_id = $1", projectID)
		}
	})
	settingsPath := "/api/agentharbor/monitor/bark"
	set := func(body map[string]any, configured, enabled bool) {
		t.Helper()
		got := testutil.Call(t, h.PutAgentHarborBark, newRequest(http.MethodPut, settingsPath, body)).Want(http.StatusOK).Map()
		if len(got) != 2 || got["configured"] != configured || got["enabled"] != enabled {
			t.Fatalf("Bark setting response = %+v", got)
		}
	}
	testutil.Call(t, h.PutAgentHarborBark,
		newRequest(http.MethodPut, settingsPath, map[string]any{"enabled": true, "server_url": endpoint})).Want(http.StatusBadRequest)
	testutil.Call(t, h.PutAgentHarborBark,
		newRequest(http.MethodPut, settingsPath, map[string]any{"enabled": true, "server_url": "https://127.0.0.1", "device_key": deviceKey})).Want(http.StatusBadRequest)

	set(map[string]any{"enabled": false, "server_url": endpoint, "device_key": deviceKey}, true, false)
	otherSetting := testutil.Call(t, h.GetAgentHarborBark, newRequestAs(other, http.MethodGet, settingsPath, nil)).Want(http.StatusOK).Map()
	if len(otherSetting) != 2 || otherSetting["configured"] != false || otherSetting["enabled"] != false {
		t.Fatalf("other user sees Bark settings: %+v", otherSetting)
	}
	var sealed []byte
	dbfx.QueryRow(t, "SELECT credentials FROM agentharbor_bark_setting WHERE workspace_id = $1 AND user_id = $2", testWorkspaceID, testUserID).Scan(&sealed)
	if bytes.Contains(sealed, []byte(endpoint)) || bytes.Contains(sealed, []byte(deviceKey)) || len(sealed) == 0 {
		t.Fatal("Bark credentials were not encrypted at rest")
	}
	testutil.Call(t, h.TestAgentHarborBark, newRequest(http.MethodPost, settingsPath+"/test", nil)).Want(http.StatusOK)
	if len(pushes) != 1 || pushes[0] != (push{"AgentHarbor", "Bark 测试提醒", deviceKey}) {
		t.Fatalf("disabled settings did not support a fixed test notification: count=%d", len(pushes))
	}

	set(map[string]any{"enabled": true}, true, true)
	now := time.Now().UTC()
	dbfx.Exec(t, "UPDATE agentharbor_bark_setting SET enabled_since = $1 WHERE workspace_id = $2 AND user_id = $3", now.Add(-time.Hour), testWorkspaceID, testUserID)
	snapshot := func(turn, status string, started, ended *time.Time, historical bool, handled any) map[string]any {
		return map[string]any{
			"session_id": "codex-session", "turn_id": turn, "title": "Task " + turn,
			"started_at": started, "ended_at": ended, "status": status, "historical": historical, "handled": handled,
		}
	}
	sync := func(turns ...map[string]any) {
		t.Helper()
		req := newRequest(http.MethodPost, "/api/agentharbor/monitor/sync", map[string]any{
			"project_id": projectID, "machine": map[string]any{"id": machineID, "name": "Synthetic machine"}, "scan_status": "ok", "turns": turns,
		})
		testutil.Call(t, h.SyncAgentHarborMonitor, req).Want(http.StatusOK)
	}
	beforeEnable := now.Add(-2 * time.Hour)
	sync(
		snapshot("older", "completed", nil, &beforeEnable, false, false),
		snapshot("historic-handled", "completed", nil, &now, true, true),
		snapshot("historic-unknown", "aborted", nil, &now, true, nil),
		snapshot("unknown-end", "aborted", nil, nil, false, false),
		snapshot("complete", "running", &now, nil, false, nil),
	)
	countDeliveries := func(want int) {
		t.Helper()
		var n int
		dbfx.QueryRow(t, "SELECT count(*) FROM agentharbor_bark_delivery WHERE project_id = $1 AND user_id = $2", projectID, testUserID).Scan(&n)
		if n != want {
			t.Fatalf("found %d deliveries, want %d", n, want)
		}
	}
	countDeliveries(0)

	done := snapshot("complete", "completed", &now, &now, false, false)
	sync(done)
	sync(done)
	countDeliveries(1)

	checkHandled := func(user, turn string, want any) {
		t.Helper()
		var listing struct {
			Tasks []map[string]any `json:"tasks"`
		}
		req := newRequestAs(user, http.MethodGet, "/api/agentharbor/monitor?view=history&project_id="+projectID, nil)
		testutil.Call(t, h.ListAgentHarborMonitor, req).Want(http.StatusOK).JSON(&listing)
		for _, row := range listing.Tasks {
			if row["turn_id"] == turn {
				if row["handled"] != want {
					t.Fatalf("handled(%s)=%v, want %v", turn, row["handled"], want)
				}
				return
			}
		}
		t.Fatalf("turn %s absent from history", turn)
	}
	checkHandled(testUserID, "historic-handled", true)
	checkHandled(other, "historic-handled", nil)
	checkHandled(testUserID, "historic-unknown", nil)

	failNext = true
	if _, err := h.processAgentHarborBarkDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var pending bool
	dbfx.QueryRow(t, "SELECT attempts, sent_at IS NULL AND available_at > now() FROM agentharbor_bark_delivery WHERE project_id = $1 AND turn_id = 'complete'", projectID).Scan(&attempts, &pending)
	if attempts != 1 || !pending {
		t.Fatalf("failed Bark response not rescheduled: attempts=%d, pending=%t", attempts, pending)
	}

	testutil.Call(t, h.HandleAgentHarborMonitorTurn, newRequest(http.MethodPost, "/api/agentharbor/monitor/handle", map[string]any{
		"project_id": projectID, "machine_id": machineID, "session_id": "codex-session", "turn_id": "complete",
	})).Want(http.StatusOK)
	checkHandled(testUserID, "complete", true)
	checkHandled(other, "complete", false)

	dbfx.Exec(t, "UPDATE agentharbor_bark_delivery SET available_at = now() WHERE project_id = $1 AND turn_id = 'complete'", projectID)
	sync(snapshot("abort", "aborted", nil, &now, false, false))
	countDeliveries(2)
	for range 3 {
		if _, err := h.processAgentHarborBarkDelivery(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var sent int
	dbfx.QueryRow(t, "SELECT count(*) FROM agentharbor_bark_delivery WHERE project_id = $1 AND sent_at IS NOT NULL", projectID).Scan(&sent)
	if sent != 2 || len(pushes) != 4 ||
		pushes[1] != (push{"Task complete", "CIS synthetic · 已完成", deviceKey}) ||
		pushes[2] != pushes[1] || pushes[3] != (push{"Task abort", "CIS synthetic · 已中止", deviceKey}) {
		t.Fatalf("Bark retry or terminal payload mismatch: sent=%d attempts=%d", sent, len(pushes))
	}
	set(map[string]any{"enabled": false}, true, false)
	sync(snapshot("disabled", "completed", nil, &now, false, false))
	countDeliveries(2)

	redirectNext = true
	result := testutil.Call(t, h.TestAgentHarborBark, newRequest(http.MethodPost, settingsPath+"/test", nil)).Want(http.StatusBadGateway).Map()
	if result["code"] != "bark_delivery_failed" || len(pushes) != 5 ||
		strings.Contains(fmt.Sprint(result), endpoint) || strings.Contains(fmt.Sprint(result), deviceKey) {
		t.Fatal("Bark redirect was followed or exposed credentials")
	}
}

func TestAgentHarborBarkRejectsPluginDevOrigin(t *testing.T) {
	t.Setenv(remotemcp.DevOriginsEnv, "https://127.0.0.1:9443")
	if _, err := remotemcp.ValidateStrictPublicHTTPSEndpoint(context.Background(), "https://127.0.0.1:9443"); err == nil {
		t.Fatal("Bark allowed a Plugin-only private dev origin")
	}
}
