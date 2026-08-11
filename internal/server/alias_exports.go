package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const maxAliasExportBatch = 1000

type aliasExportRecord struct {
	ExportedAt string `json:"exported_at"`
}

type aliasExportStore struct {
	mu       sync.RWMutex
	path     string
	accounts map[string]map[string]aliasExportRecord
	loadErr  error
}

func newAliasExportStore(path string) *aliasExportStore {
	s := &aliasExportStore{
		path:     path,
		accounts: make(map[string]map[string]aliasExportRecord),
	}
	if path == "" {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.loadErr = fmt.Errorf("读取别名导出状态: %w", err)
		}
		return s
	}
	var stored struct {
		Accounts map[string]map[string]aliasExportRecord `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		s.loadErr = fmt.Errorf("解析别名导出状态: %w", err)
		return s
	}
	if stored.Accounts != nil {
		s.accounts = stored.Accounts
	}
	return s
}

func normalizeExportEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *aliasExportStore) ExportedAt(accountID, email string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accounts[accountID][normalizeExportEmail(email)].ExportedAt
}

// Claim 首次认领一批待导出的邮箱。已经认领过的邮箱不会再次返回。
func (s *aliasExportStore) Claim(accountID string, emails []string, now time.Time) ([]aliasExportRecordPayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	account := s.accounts[accountID]
	if account == nil {
		account = make(map[string]aliasExportRecord)
		s.accounts[accountID] = account
	}

	exportedAt := now.UTC().Format(time.RFC3339)
	claimed := make([]aliasExportRecordPayload, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, raw := range emails {
		email := normalizeExportEmail(raw)
		if email == "" {
			continue
		}
		if _, exists := seen[email]; exists {
			continue
		}
		seen[email] = struct{}{}
		if account[email].ExportedAt != "" {
			continue
		}
		account[email] = aliasExportRecord{ExportedAt: exportedAt}
		claimed = append(claimed, aliasExportRecordPayload{Email: email, ExportedAt: exportedAt})
	}
	if len(claimed) == 0 {
		return claimed, nil
	}
	if err := s.persistLocked(now); err != nil {
		for _, item := range claimed {
			delete(account, item.Email)
		}
		return nil, err
	}
	return claimed, nil
}

func (s *aliasExportStore) persistLocked(now time.Time) error {
	if s.path == "" {
		return nil
	}
	stored := struct {
		Accounts  map[string]map[string]aliasExportRecord `json:"accounts"`
		UpdatedAt string                                  `json:"updated_at"`
	}{Accounts: s.accounts, UpdatedAt: now.UTC().Format(time.RFC3339)}
	raw, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化别名导出状态: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return fmt.Errorf("创建别名导出状态目录: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return fmt.Errorf("写入别名导出状态: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("替换别名导出状态: %w", err)
	}
	return nil
}

type aliasExportRequest struct {
	AccountID string   `json:"account_id"`
	Emails    []string `json:"emails"`
}

type aliasExportRecordPayload struct {
	Email      string `json:"email"`
	InboxURL   string `json:"inbox_url,omitempty"`
	ExportedAt string `json:"exported_at"`
}

func validAliasExportEmail(email string) bool {
	if len(email) > 254 || strings.ContainsAny(email, "\r\n\t ") {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	return at > 0 && at < len(email)-1
}

func (s *Server) exportAliasesHandler(c *gin.Context) {
	var req aliasExportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		failCode(c, 400, "VALIDATION_ERROR", "参数错误: account_id 和 emails 必填")
		return
	}
	req.AccountID = strings.TrimSpace(req.AccountID)
	if req.AccountID == "" || len(req.Emails) == 0 || len(req.Emails) > maxAliasExportBatch {
		failCode(c, 400, "VALIDATION_ERROR", "参数错误: 每次可导出 1 至 1000 个邮箱")
		return
	}
	if _, exists := s.findGenerationAccount(req.AccountID); !exists {
		failCode(c, 404, "ACCOUNT_NOT_FOUND", "账号不存在")
		return
	}
	for _, email := range req.Emails {
		if !validAliasExportEmail(normalizeExportEmail(email)) {
			failCode(c, 400, "VALIDATION_ERROR", "参数错误: 邮箱格式无效")
			return
		}
	}
	claimed, err := s.aliasExports.Claim(req.AccountID, req.Emails, time.Now())
	if err != nil {
		failCode(c, 500, "PERSISTENCE_FAILURE", "保存导出状态失败")
		return
	}
	for i := range claimed {
		claimed[i].InboxURL = s.publicInboxPath(req.AccountID, claimed[i].Email)
	}
	ok(c, gin.H{
		"account_id": req.AccountID,
		"count":      len(claimed),
		"items":      claimed,
	})
}
