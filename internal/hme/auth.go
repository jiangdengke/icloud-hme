// Package hme - iCloud 认证模块
//
// 基于 Go-iClient 项目实现完整的 SRP (Secure Remote Password) 登录流程,
// 支持双重认证 (2FA),登录成功后提取 session token Cookie。
package hme

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/pbkdf2"

	http "github.com/bogdanfinn/fhttp"
	"icloud-hme/internal/srp"
)

// OAuthClientID 是 iCloud Web 登录使用的一方 OAuth 客户端标识。
const OAuthClientID = "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d"

const appleFDClientInfo = `{"U":"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36","L":"zh-CN","Z":"GMT+08:00","V":"1.1","F":".ta44j1e3NlY5BNlY5BSs5uQ32SCVgdI.AqWJ4EKKw0fVD_DJhCizgzH_y3EjNklY_ia4WFL264HRe4FSr_JzC1zJ6rgNNlY5BNp55BNlan0Os5Apw.BS1"}`

// OTPProvider 双重认证回调函数,返回 2FA 验证码
type OTPProvider func() (string, error)

// ErrOTPRequired indicates that Apple has created a 2FA challenge and the
// caller should collect a code before continuing the same login session.
var ErrOTPRequired = errors.New("账号启用了双重认证,需要提供 OTP")

// LoginSession contains the state Apple issued between SRP authentication and
// OTP verification. It must be continued instead of starting Login again.
type LoginSession struct {
	client *Client
	state  *authState
}

// Client returns the client associated with this login session.
func (s *LoginSession) Client() *Client {
	if s == nil {
		return nil
	}
	return s.client
}

// Finish completes a login that did not require OTP and stores its cookies.
func (s *LoginSession) Finish() error {
	if s == nil || s.client == nil || s.state == nil {
		return fmt.Errorf("登录会话无效")
	}

	c := s.client
	state := s.state
	c.log("认证步骤 5/6: trust")
	if err := c.getTrust(state); err != nil {
		c.log("认证失败: trust: %v", err)
		return fmt.Errorf("get trust: %w", err)
	}

	c.log("认证步骤 6/6: account-login")
	if err := c.authenticateWeb(state); err != nil {
		c.log("认证失败: account-login: %v", err)
		return fmt.Errorf("authenticate web: %w", err)
	}

	cookies := c.extractSessionCookies()
	c.Cookies = cookies
	c.log("登录成功,获取到 %d 个 Cookie", len(cookies))
	return nil
}

// ContinueOTP verifies a code against the existing Apple 2FA challenge and
// then finishes the login session.
func (s *LoginSession) ContinueOTP(otp string) error {
	if s == nil || s.client == nil || s.state == nil {
		return fmt.Errorf("登录会话无效")
	}
	if err := s.client.verifyTwoFactor(s.state, otp); err != nil {
		return err
	}
	return s.Finish()
}

// ContinueOTPProvider obtains a code and continues the existing session.
func (s *LoginSession) ContinueOTPProvider(provider OTPProvider) error {
	if provider == nil {
		return ErrOTPRequired
	}
	otp, err := provider()
	if err != nil {
		return fmt.Errorf("获取 2FA 验证码失败: %w", err)
	}
	return s.ContinueOTP(otp)
}

// authState 保存认证过程中的状态
type authState struct {
	username       string
	frameId        string
	clientId       string
	authAttr       string
	sessionID      string
	scnt           string
	authToken      string
	trustToken     string
	accountCountry string
	dsid           string
}

// authOrigin 按账号区域返回 Apple 身份认证域名。
func (c *Client) authOrigin() string {
	if c.Host == "icloud.com.cn" {
		return "https://idmsa.apple.com.cn"
	}
	return "https://idmsa.apple.com"
}

func (c *Client) authURL(path string) string {
	return c.authOrigin() + "/appleauth/auth" + path
}

func (c *Client) authStartURL(state *authState) string {
	return fmt.Sprintf(
		"%s?frame_id=auth-%s&language=zh_CN&skVersion=7&iframeId=auth-%s&client_id=%s&redirect_uri=%s&response_type=code&response_mode=web_message&state=auth-%s&authVersion=latest",
		c.authURL("/authorize/signin"), state.frameId, state.frameId, state.clientId,
		url.QueryEscape(c.Origin()), state.frameId,
	)
}

func (c *Client) authWebURL() string {
	return "https://setup." + c.Host + "/setup/ws/1/accountLogin"
}

// Login 使用 iCloud 账号密码登录,获取 session token Cookie。
//
// 登录成功后,可以通过 client.GetCookies() 获取 Cookie。
// 启用 2FA 时,会调用 otpProvider 获取验证码。
func (c *Client) Login(username, password string, otpProvider OTPProvider) error {
	session, err := c.StartLogin(username, password)
	if err != nil {
		if errors.Is(err, ErrOTPRequired) && otpProvider != nil {
			if continueErr := session.ContinueOTPProvider(otpProvider); continueErr != nil {
				return fmt.Errorf("auth complete: %w", continueErr)
			}
			return nil
		}
		return err
	}
	return session.Finish()
}

// StartLogin performs the password/SRP portion of login once. When 2FA is
// enabled it returns the live session together with ErrOTPRequired.
func (c *Client) StartLogin(username, password string) (*LoginSession, error) {
	state := &authState{
		username: username,
	}
	session := &LoginSession{client: c, state: state}

	// 1. 初始化 frameId 和 clientId
	c.log("认证步骤 1/6: auth-start (%s)", c.Host)
	if err := c.authStart(state); err != nil {
		c.log("认证失败: auth-start: %v", err)
		return nil, fmt.Errorf("auth start: %w", err)
	}

	// 2. 提交用户名
	c.log("认证步骤 2/6: auth-federate")
	if err := c.authFederate(state); err != nil {
		c.log("认证失败: auth-federate: %v", err)
		return nil, fmt.Errorf("auth federate: %w", err)
	}

	// 3. SRP 协议初始化
	params := srp.GetParams(2048)
	params.NoUserNameInX = true
	srpClient := srp.NewSRPClient(params, nil)

	// 4. 获取 salt 和 B
	c.log("认证步骤 3/6: auth-init")
	authInitResp, err := c.authInit(state, base64.StdEncoding.EncodeToString(srpClient.GetABytes()))
	if err != nil {
		c.log("认证失败: auth-init: %v", err)
		return nil, fmt.Errorf("auth init: %w", err)
	}

	// 5. 解码 salt 和 B
	bDec, err := base64.StdEncoding.DecodeString(authInitResp.B)
	if err != nil {
		return nil, fmt.Errorf("decode B: %w", err)
	}
	saltDec, err := base64.StdEncoding.DecodeString(authInitResp.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}

	// 6. 生成密码密钥
	passHash := sha256.Sum256([]byte(password))
	passKey := pbkdf2.Key(passHash[:], saltDec, authInitResp.Iteration, 32, sha256.New)

	// 7. 处理挑战
	srpClient.ProcessClientChanllenge([]byte(username), passKey, saltDec, bDec)

	// 8. 提交 SRP 响应 (可能触发 2FA)
	c.log("认证步骤 4/6: auth-complete")
	if err := c.authComplete(state, authInitResp.C, base64.StdEncoding.EncodeToString(srpClient.M1), base64.StdEncoding.EncodeToString(srpClient.M2), nil); err != nil {
		c.log("认证失败: auth-complete: %v", err)
		return session, fmt.Errorf("auth complete: %w", err)
	}
	return session, nil
}

// --- 认证流程的各步骤 ---

// authStart 初始化 frameId 和 clientId
func (c *Client) authStart(state *authState) error {
	state.frameId = strings.ToLower(uuid.New().String())
	state.clientId = OAuthClientID

	req, err := http.NewRequest("GET", c.authStartURL(state), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	state.authAttr = resp.Header.Get("X-Apple-Auth-Attributes")
	return nil
}

// authFederate 提交用户名
func (c *Client) authFederate(state *authState) error {
	data := `{"accountName":"` + state.username + `","rememberMe":true}`
	req, err := http.NewRequest("POST", c.authURL("/federate?isRememberMeEnabled=true"), bytes.NewReader([]byte(data)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// authInitResp authInit 响应
type authInitResp struct {
	Iteration int    `json:"iteration"`
	Salt      string `json:"salt"`
	Protocol  string `json:"protocol"`
	B         string `json:"b"`
	C         string `json:"c"`
}

// authInit 初始化 SRP 认证
func (c *Client) authInit(state *authState, a string) (*authInitResp, error) {
	reqBody := map[string]interface{}{
		"a":           a,
		"accountName": state.username,
		"protocols":   []string{"s2k", "s2k_fo"},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.authURL("/signin/init"), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result authInitResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// authComplete 提交 SRP 响应
func (c *Client) authComplete(state *authState, challenge, m1, m2 string, otpProvider OTPProvider) error {
	reqBody := map[string]interface{}{
		"accountName": state.username,
		"rememberMe":  true,
		"trustTokens": []string{},
		"m1":          m1,
		"c":           challenge,
		"m2":          m2,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.authURL("/signin/complete?isRememberMeEnabled=true"), bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if country := resp.Header.Get("X-Apple-ID-Account-Country"); country != "" {
		state.accountCountry = country
	}

	switch resp.StatusCode {
	case 200:
		return nil
	case 409:
		// 需要 2FA
		return c.handleTwoFactor(state, resp, otpProvider)
	case 403:
		return fmt.Errorf("用户名或密码错误")
	case 412:
		return fmt.Errorf("需要先在 appleid.apple.com 同意隐私条款")
	default:
		return fmt.Errorf("auth complete 失败: HTTP %d", resp.StatusCode)
	}
}

// handleTwoFactor 处理双重认证
func (c *Client) handleTwoFactor(state *authState, signinResp *http.Response, otpProvider OTPProvider) error {
	state.sessionID = signinResp.Header.Get("X-Apple-ID-Session-Id")
	state.scnt = signinResp.Header.Get("scnt")

	// Apple 要求先读取双重认证选项。这个请求会刷新 scnt，并且在
	// 受信任设备上触发“Apple 账户登录请求”弹窗。先前直接返回
	// OTP_REQUIRED，会导致网页已显示验证码输入框，手机却没有收到提示。
	optionsReq, err := http.NewRequest("GET", c.authURL(""), nil)
	if err != nil {
		return err
	}
	optionsReq.Header = c.updateAuthHeaders(optionsReq.Header, state)
	optionsResp, err := c.httpc.Do(optionsReq)
	if err != nil {
		return err
	}
	defer optionsResp.Body.Close()
	if optionsResp.StatusCode < 200 || optionsResp.StatusCode >= 300 {
		return fmt.Errorf("获取双重认证选项失败: HTTP %d", optionsResp.StatusCode)
	}
	if newScnt := optionsResp.Header.Get("scnt"); newScnt != "" {
		state.scnt = newScnt
	}

	if otpProvider == nil {
		return ErrOTPRequired
	}

	otp, err := otpProvider()
	if err != nil {
		return fmt.Errorf("获取 2FA 验证码失败: %w", err)
	}

	return c.verifyTwoFactor(state, otp)
}

// verifyTwoFactor submits a code to the already prepared Apple challenge.
func (c *Client) verifyTwoFactor(state *authState, otp string) error {
	reqBody := map[string]interface{}{
		"securityCode": map[string]string{"code": otp},
	}

	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", fmt.Sprintf(c.authURL("/verify/%s/securitycode"), "trusteddevice"), bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		return fmt.Errorf("2FA 验证失败: HTTP %d", resp.StatusCode)
	}

	if newScnt := resp.Header.Get("scnt"); newScnt != "" {
		state.scnt = newScnt
	}
	return nil
}

// getTrust 获取 trust token
func (c *Client) getTrust(state *authState) error {
	req, err := http.NewRequest("GET", c.authURL("/2sv/trust"), nil)
	if err != nil {
		return err
	}

	req.Header = c.updateAuthHeaders(req.Header, state)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		return fmt.Errorf("trust 失败: HTTP %d", resp.StatusCode)
	}

	state.authToken = resp.Header.Get("X-Apple-Session-Token")
	state.trustToken = resp.Header.Get("X-Apple-TwoSV-Trust-Token")
	return nil
}

// authenticateWeb 认证 iCloud Web 服务
func (c *Client) authenticateWeb(state *authState) error {
	country := state.accountCountry
	if country == "" {
		country = "USA"
		if c.Host == "icloud.com.cn" {
			country = "CHN"
		}
	}
	body := fmt.Sprintf(`{"dsWebAuthToken":"%s","accountCountryCode":"%s","extended_login":true,"trustToken":"%s"}`,
		state.authToken, country, state.trustToken)

	req, err := http.NewRequest("POST", c.authWebURL(), bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.Origin())
	req.Header.Set("Accept", "*/*")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("auth web 失败: HTTP %d", resp.StatusCode)
	}

	var result struct {
		DsInfo struct {
			Dsid string `json:"dsid"`
		} `json:"dsInfo"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	state.dsid = result.DsInfo.Dsid

	// 复制 Apple 身份域 Cookie 到当前 iCloud 区域。
	u1, _ := url.Parse(c.authOrigin())
	u2, _ := url.Parse(c.Origin())
	cookies := c.httpc.GetCookies(u1)
	c.httpc.SetCookies(u2, cookies)

	return nil
}

// extractSessionCookies 提取 session token Cookie
func (c *Client) extractSessionCookies() map[string]string {
	cookies := make(map[string]string)
	u, _ := url.Parse(c.Origin())
	for _, cookie := range c.httpc.GetCookies(u) {
		cookies[cookie.Name] = cookie.Value
	}
	return cookies
}

// updateAuthHeaders 更新认证请求所需的头部
func (c *Client) updateAuthHeaders(header http.Header, state *authState) http.Header {
	if state.scnt != "" {
		header.Set("scnt", state.scnt)
	}
	if state.sessionID != "" {
		header.Set("X-Apple-ID-Session-Id", state.sessionID)
	}

	header.Set("X-Requested-With", "XMLHttpRequest")
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("Referer", c.authOrigin()+"/")
	header.Set("Origin", c.authOrigin())
	header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	header.Set("X-Apple-I-Require-UE", "true")
	header.Set("X-Apple-Auth-Attributes", state.authAttr)
	header.Set("X-Apple-Widget-Key", state.clientId)
	header.Set("X-Apple-Mandate-Security-Upgrade", "0")
	header.Set("X-Apple-Oauth-Client-Id", state.clientId)
	header.Set("X-Apple-I-FD-Client-Info", appleFDClientInfo)
	header.Set("X-Apple-Oauth-Client-Type", "firstPartyAuth")
	header.Set("X-Apple-Oauth-Redirect-URI", c.Origin())
	header.Set("X-Apple-Oauth-Require-Grant-Code", "true")
	header.Set("X-Apple-Oauth-Response-Mode", "web_message")
	header.Set("X-Apple-Oauth-Response-Type", "code")
	header.Set("X-Apple-Oauth-State", "auth-"+state.frameId)
	header.Set("X-Apple-Offer-Security-Upgrade", "1")
	header.Set("X-Apple-Frame-Id", "auth-"+state.frameId)

	return header
}

// Validate 验证当前 Cookie 是否有效
func (c *Client) Validate() (bool, error) {
	if len(c.Cookies) == 0 {
		return false, fmt.Errorf("无 Cookie")
	}
	// 简单实现：尝试调用 validate 端点
	err := c.ValidateSession()
	if err != nil {
		return false, err
	}
	return true, nil
}
