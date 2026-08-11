package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
)

func TestGenerationTaskStartCreatesPersistsAndStops(t *testing.T) {
	statePath := t.TempDir() + "/generation_tasks.json"
	f := &fakeBackend{
		accounts: []account.Summary{{
			ID:         "acc_1",
			Name:       "ready",
			Status:     "active",
			HasCookies: true,
		}},
		createSeq: []hme.CreateResult{{
			Email:     "continuous-1@icloud.com",
			Label:     "continuous-1",
			CreatedAt: "2026-08-11T10:00:00Z",
		}},
	}
	s := newWithBackend(f, Config{
		AdminPassword:           "admin-pass-2026-strong",
		SessionTTL:              time.Hour,
		PublicLinkSecret:        []byte("0123456789abcdef0123456789abcdef"),
		GenerationTaskStatePath: statePath,
		generationSuccessDelay:  time.Hour,
		generationFailureDelay:  []time.Duration{time.Hour},
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	start := authedReq(t, ts, http.MethodPost, "/api/generation-task/start", `{"account_id":"acc_1","label_prefix":"持续测试"}`)
	start.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	start.Header.Set("X-CSRF-Token", csrf)
	status, body, _ := do(t, start)
	if status != http.StatusOK {
		t.Fatalf("启动失败: %d %s", status, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	var payload struct {
		Data struct {
			Running bool `json:"running"`
			Created int  `json:"created"`
			Aliases []struct {
				Email    string `json:"email"`
				InboxURL string `json:"inbox_url"`
			} `json:"aliases"`
		} `json:"data"`
	}
	for time.Now().Before(deadline) {
		req := authedReq(t, ts, http.MethodGet, "/api/generation-task?account_id=acc_1", "")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		status, body, _ = do(t, req)
		if status == http.StatusOK && json.Unmarshal([]byte(body), &payload) == nil && payload.Data.Created >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if payload.Data.Created != 1 || len(payload.Data.Aliases) != 1 {
		t.Fatalf("任务没有创建邮箱: %s", body)
	}
	if payload.Data.Aliases[0].Email != "continuous-1@icloud.com" || !strings.HasPrefix(payload.Data.Aliases[0].InboxURL, "/mail/") {
		t.Fatalf("创建结果缺少取件链接: %+v", payload.Data.Aliases[0])
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("任务状态未持久化: %v", err)
	}

	stop := authedReq(t, ts, http.MethodPost, "/api/generation-task/stop", `{"account_id":"acc_1"}`)
	stop.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	stop.Header.Set("X-CSRF-Token", csrf)
	status, body, _ = do(t, stop)
	if status != http.StatusOK {
		t.Fatalf("停止失败: %d %s", status, body)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := s.generationTasks.Status("acc_1")
		if !state.Running && state.State == "stopped" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务未停止: %+v", s.generationTasks.Status("acc_1"))
}

func TestGenerationTaskUsesIncreasingFailureCooldown(t *testing.T) {
	f := &fakeBackend{
		createErrs: []error{
			&BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "上游限流"},
			&BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "上游限流"},
		},
	}
	m := newGenerationTaskManager(f, generationTaskOptions{
		successDelay:  time.Hour,
		failureDelays: []time.Duration{20 * time.Millisecond, 80 * time.Millisecond},
	})
	if _, err := m.Start("acc_1", "cooldown"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := m.Status("acc_1")
		if state.ConsecutiveFailures >= 2 {
			if state.State != "cooldown" || state.NextRunAt == "" {
				t.Fatalf("失败后应进入冷却: %+v", state)
			}
			m.Stop("acc_1")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("没有执行递增冷却，calls=%d state=%+v", f.generationCreateCalls(), m.Status("acc_1"))
}
