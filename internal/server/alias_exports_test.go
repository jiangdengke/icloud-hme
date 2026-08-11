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

func TestAliasExportClaimPersistsAndSkipsDuplicates(t *testing.T) {
	path := t.TempDir() + "/alias_exports.json"
	store := newAliasExportStore(path)
	first, err := store.Claim("acc_1", []string{"Alpha@icloud.com", "alpha@icloud.com"}, time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Email != "alpha@icloud.com" {
		t.Fatalf("首次导出应去重: %+v", first)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("导出状态未落盘: %v", err)
	}

	reloaded := newAliasExportStore(path)
	if reloaded.loadErr != nil {
		t.Fatal(reloaded.loadErr)
	}
	if got := reloaded.ExportedAt("acc_1", "ALPHA@ICLOUD.COM"); got != "2026-08-11T08:00:00Z" {
		t.Fatalf("重启后导出时间错误: %q", got)
	}
	second, err := reloaded.Claim("acc_1", []string{"alpha@icloud.com"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("重复导出应返回空列表: %+v", second)
	}
}

func TestAliasExportEndpointMarksListAndPreventsDuplicateExport(t *testing.T) {
	statePath := t.TempDir() + "/alias_exports.json"
	f := &fakeBackend{
		accounts: []account.Summary{{ID: "acc_1", Name: "test", Status: "active"}},
		aliases:  []hme.Alias{{Email: "alpha@icloud.com", AnonymousID: "anon_1", Active: true}},
	}
	s := newWithBackend(f, Config{
		AdminPassword:        "admin-pass-2026-strong",
		SessionTTL:           time.Hour,
		PublicLinkSecret:     []byte("0123456789abcdef0123456789abcdef"),
		AliasExportStatePath: statePath,
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	export := func() (int, string) {
		req := authedReq(t, ts, http.MethodPost, "/api/aliases/export", `{"account_id":"acc_1","emails":["alpha@icloud.com"]}`)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		status, body, _ := do(t, req)
		return status, body
	}

	status, body := export()
	if status != http.StatusOK || !strings.Contains(body, `"count":1`) || !strings.Contains(body, `"inbox_url":"/mail/`) {
		t.Fatalf("首次导出响应错误: %d %s", status, body)
	}
	status, body = export()
	if status != http.StatusOK || !strings.Contains(body, `"count":0`) {
		t.Fatalf("重复导出应为空: %d %s", status, body)
	}

	req := authedReq(t, ts, http.MethodGet, "/api/aliases?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	status, body, _ = do(t, req)
	if status != http.StatusOK {
		t.Fatalf("别名列表失败: %d %s", status, body)
	}
	var response struct {
		Data struct {
			Aliases []hme.Alias `json:"aliases"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Aliases) != 1 || !response.Data.Aliases[0].Exported || response.Data.Aliases[0].ExportedAt == "" {
		t.Fatalf("别名列表缺少导出状态: %s", body)
	}
}
