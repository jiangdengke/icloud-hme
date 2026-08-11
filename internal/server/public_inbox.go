package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const publicInboxContext = "icloud-hme-public-inbox-v1"

// normalizePublicLinkSecret 保证签名密钥长度足够。测试或旧调用方没有
// 显式配置时，使用管理密码派生兼容密钥；生产环境会传入持久化随机密钥。
func normalizePublicLinkSecret(secret []byte, adminPassword string) []byte {
	if len(secret) >= 32 {
		return append([]byte(nil), secret...)
	}
	sum := sha256.Sum256([]byte(publicInboxContext + "\x00" + adminPassword))
	return append([]byte(nil), sum[:]...)
}

// publicInboxToken 生成稳定的签名 token：同一账号+别名始终对应同一取件 URL。
// payload 只能被解码，不能被篡改；完整 URL 本身是读取该别名邮件的凭据。
func (s *Server) publicInboxToken(accountID, alias string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(accountID + "\x00" + alias))
	mac := hmac.New(sha256.New, s.publicLinkSecret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "~" + sig
}

func (s *Server) publicInboxPath(accountID, alias string) string {
	return "/mail/" + s.publicInboxToken(accountID, alias)
}

func (s *Server) parsePublicInboxToken(token string) (accountID, alias string, valid bool) {
	if token == "" || len(token) > 2048 {
		return "", "", false
	}
	payload, encodedSig, ok := strings.Cut(token, "~")
	if !ok || payload == "" || encodedSig == "" || strings.Contains(encodedSig, "~") {
		return "", "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(encodedSig)
	if err != nil {
		return "", "", false
	}
	mac := hmac.New(sha256.New, s.publicLinkSecret)
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", "", false
	}
	accountID, alias, ok = strings.Cut(string(raw), "\x00")
	if !ok || accountID == "" || alias == "" || len(accountID) > 128 || len(alias) > 320 {
		return "", "", false
	}
	if strings.TrimSpace(accountID) != accountID || strings.TrimSpace(alias) != alias || !strings.Contains(alias, "@") {
		return "", "", false
	}
	return accountID, alias, true
}

// publicInboxHandler 按签名 token 读取某一个隐藏邮箱最近的邮件摘要。
// 只返回该别名收到的邮件，不暴露账号 Cookie、App 密码或其他别名。
func (s *Server) publicInboxHandler(c *gin.Context) {
	c.Header("X-Robots-Tag", "noindex, nofollow, noarchive")
	accountID, alias, valid := s.parsePublicInboxToken(c.Param("token"))
	if !valid {
		failCode(c, http.StatusNotFound, "PUBLIC_LINK_INVALID", "取件链接无效")
		return
	}

	result, err := s.be.ListInbox(InboxQuery{
		AccountID: accountID,
		Alias:     alias,
		Limit:     20,
		Days:      30,
	})
	if err != nil {
		var backendErr *BackendError
		if errors.As(err, &backendErr) && backendErr.Code == "APP_PASSWORD_REQUIRED" {
			failCode(c, http.StatusServiceUnavailable, backendErr.Code, "邮箱取件需要先在后台设置 App 专用密码")
			return
		}
		// 公开接口不暴露账号凭据状态或上游详情。
		failCode(c, http.StatusBadGateway, "MAILBOX_UNAVAILABLE", "暂时无法读取邮件")
		return
	}
	ok(c, result)
}
