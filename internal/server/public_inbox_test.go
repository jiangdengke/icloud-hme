package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icloud-hme/internal/hme"
	"icloud-hme/internal/mail"
)

func newPublicInboxTestServer(f *fakeBackend) *Server {
	return newWithBackend(f, Config{
		AdminPassword:    "admin-pass-2026-strong",
		PublicLinkSecret: []byte("0123456789abcdef0123456789abcdef"),
	})
}

func TestPublicInboxTokenIsStableUniqueAndTamperProof(t *testing.T) {
	s := newPublicInboxTestServer(&fakeBackend{})
	a := s.publicInboxToken("acc_1", "alpha@icloud.com")
	if a != s.publicInboxToken("acc_1", "alpha@icloud.com") {
		t.Fatal("同一别名的 token 应稳定")
	}
	if a == s.publicInboxToken("acc_1", "beta@icloud.com") {
		t.Fatal("不同别名应生成不同 token")
	}
	accountID, alias, ok := s.parsePublicInboxToken(a)
	if !ok || accountID != "acc_1" || alias != "alpha@icloud.com" {
		t.Fatalf("token 解析错误: %q %q %v", accountID, alias, ok)
	}
	replacement := byte('A')
	if a[len(a)-1] == replacement {
		replacement = 'B'
	}
	tampered := a[:len(a)-1] + string(replacement)
	if _, _, ok := s.parsePublicInboxToken(tampered); ok {
		t.Fatal("被篡改的 token 应被拒绝")
	}
}

func TestPublicInboxRouteOnlyQueriesTokenAlias(t *testing.T) {
	f := &fakeBackend{inbox: InboxResult{
		AccountID: "acc_1",
		Alias:     "alpha@icloud.com",
		Count:     1,
		Messages:  []mail.Message{{ID: "m1", To: "alpha@icloud.com", Subject: "123456"}},
		Method:    "imap",
	}}
	s := newPublicInboxTestServer(f)
	token := s.publicInboxToken("acc_1", "alpha@icloud.com")
	req := httptest.NewRequest(http.MethodGet, "/api/public/mail/"+token, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200,得到 %d: %s", rec.Code, rec.Body.String())
	}
	if f.listInboxQuery.AccountID != "acc_1" || f.listInboxQuery.Alias != "alpha@icloud.com" {
		t.Fatalf("查询没有锁定 token 中的别名: %+v", f.listInboxQuery)
	}
	if f.listInboxQuery.Limit != 20 || f.listInboxQuery.Days != 30 {
		t.Fatalf("公开取件范围错误: %+v", f.listInboxQuery)
	}
	if got := rec.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Fatalf("取件页应禁止搜索引擎收录: %q", got)
	}
}

func TestInvalidPublicInboxTokenReturns404(t *testing.T) {
	s := newPublicInboxTestServer(&fakeBackend{})
	req := httptest.NewRequest(http.MethodGet, "/api/public/mail/not-a-token", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404,得到 %d", rec.Code)
	}
}

func TestPublicInboxExplainsAppPasswordRequirement(t *testing.T) {
	f := &fakeBackend{inboxErr: &BackendError{
		Status:  http.StatusPreconditionFailed,
		Code:    "APP_PASSWORD_REQUIRED",
		Message: "fixture",
	}}
	s := newPublicInboxTestServer(f)
	token := s.publicInboxToken("acc_1", "alpha@icloud.com")
	req := httptest.NewRequest(http.MethodGet, "/api/public/mail/"+token, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "APP_PASSWORD_REQUIRED") {
		t.Fatalf("应返回可操作的取件配置提示，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAliasResponseContainsPublicInboxURL(t *testing.T) {
	f := &fakeBackend{created: &hme.CreateResult{
		Email:     "alpha@icloud.com",
		Label:     "alpha",
		CreatedAt: "2026-08-10T10:00:00Z",
	}}
	s, ts := newTestServer(f)
	s.publicLinkSecret = []byte("0123456789abcdef0123456789abcdef")
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, http.MethodPost, "/api/create", `{"account_id":"acc_1","label":"alpha"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	status, body, _ := do(t, req)
	if status != http.StatusOK {
		t.Fatalf("期望 200,得到 %d: %s", status, body)
	}
	var out struct {
		Data struct {
			InboxURL string `json:"inbox_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Data.InboxURL, "/mail/") {
		t.Fatalf("缺少取件链接: %s", body)
	}
}

func TestCreateAliasesBatchDefaultsToTwentyAndReturnsLinks(t *testing.T) {
	created := make([]hme.CreateResult, 20)
	for i := range created {
		created[i] = hme.CreateResult{
			Email:     fmt.Sprintf("batch-%02d@icloud.com", i+1),
			Label:     fmt.Sprintf("batch-%02d", i+1),
			CreatedAt: "2026-08-10T10:00:00Z",
		}
	}
	f := &fakeBackend{batchCreated: created}
	s, ts := newTestServer(f)
	s.publicLinkSecret = []byte("0123456789abcdef0123456789abcdef")
	defer ts.Close()
	session, csrf := login(t, ts, "admin-pass-2026-strong")
	req := authedReq(t, ts, http.MethodPost, "/api/create-batch", `{"account_id":"acc_1"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	status, body, _ := do(t, req)
	if status != http.StatusOK {
		t.Fatalf("期望 200,得到 %d: %s", status, body)
	}
	var out struct {
		Data struct {
			Requested int  `json:"requested"`
			Created   int  `json:"created"`
			Complete  bool `json:"complete"`
			Aliases   []struct {
				Email    string `json:"email"`
				InboxURL string `json:"inbox_url"`
			} `json:"aliases"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if f.batchAccount != "acc_1" || f.batchCount != 20 || f.batchPrefix == "" {
		t.Fatalf("批量参数错误: account=%q count=%d prefix=%q", f.batchAccount, f.batchCount, f.batchPrefix)
	}
	if out.Data.Requested != 20 || out.Data.Created != 20 || !out.Data.Complete || len(out.Data.Aliases) != 20 {
		t.Fatalf("批量响应错误: %+v", out.Data)
	}
	seen := make(map[string]bool)
	for _, alias := range out.Data.Aliases {
		if !strings.HasPrefix(alias.InboxURL, "/mail/") || seen[alias.InboxURL] {
			t.Fatalf("取件链接缺失或重复: %+v", alias)
		}
		seen[alias.InboxURL] = true
	}
}
