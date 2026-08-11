// Package mail - iCloud Web 邮件客户端
//
// 使用 Cookie 认证通过 iCloud Web API 读取邮件，
// 无需 App Password。基于 mccgateway 服务。
package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

// WebClientBuildNumber 是与浏览器一致的 mccgateway 邮件接口构建号。
const WebClientBuildNumber = "2624Build13"

// ErrRecipientFilterUnavailable 表示 iCloud Web 的 THREAD_DIGEST 响应没有
// 真实收件人字段，不能据此安全地把邮件归属到某一个隐藏邮箱别名。
var ErrRecipientFilterUnavailable = errors.New("iCloud Web 邮件摘要不支持可靠的收件人筛选")

// WebClient 是 iCloud Web 邮件客户端。
type WebClient struct {
	cookies       map[string]string
	dsid          string
	clientID      string
	mccGatewayURL string
	host          string // "icloud.com" 或 "icloud.com.cn"
	httpc         tls_client.HttpClient
}

// NewWebClient 创建一个 Web 邮件客户端。
func NewWebClient(cookies map[string]string, dsid, host string) *WebClient {
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	}

	httpc, _ := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)

	if host == "" {
		host = "icloud.com"
	}

	c := &WebClient{
		cookies:  cookies,
		dsid:     dsid,
		clientID: uuid.New().String(),
		host:     host,
		httpc:    httpc,
	}

	// 设置 Cookie 到所有相关域名(确保跨域请求能传递 Cookie)
	if len(cookies) > 0 {
		suffix := "icloud.com"
		if host == "icloud.com.cn" {
			suffix = "icloud.com.cn"
		}
		domains := []string{
			"https://setup." + suffix,
			"https://www." + suffix,
			"https://p217-mccgateway." + suffix,
			"https://p217-maildomainws." + suffix,
		}
		for _, domain := range domains {
			u, _ := url.Parse(domain)
			httpCookies := make([]*http.Cookie, 0, len(cookies))
			for k, v := range cookies {
				httpCookies = append(httpCookies, &http.Cookie{
					Name:  k,
					Value: v,
					Path:  "/",
				})
			}
			jar.SetCookies(u, httpCookies)
		}
	}

	return c
}

// origin 返回当前账号对应的 Web Origin。
func (c *WebClient) origin() string {
	return "https://www." + c.host
}

// setCommonHeaders 设置与浏览器一致的通用请求头。
func (c *WebClient) setCommonHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin())
	req.Header.Set("Referer", c.origin()+"/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("sec-ch-ua", `"Google Chrome";v="147", "Not.A/Brand";v="8", "Chromium";v="147"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	if cookieHeader := c.cookieHeader(); cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
}

// cookieHeader 保留浏览器导出 Cookie 的引号形式并在每个动态分区域请求上
// 显式携带。iCloud 的 validate/mccgateway 会返回动态主机，只预先向固定
// p217 CookieJar 写入 Cookie 会在实际 pXX 主机上丢失会话。
func (c *WebClient) cookieHeader() string {
	if len(c.cookies) == 0 {
		return ""
	}
	keys := make([]string, 0, len(c.cookies))
	for key := range c.cookies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		// 直接沿用浏览器导出的 Cookie 值：只有值本身带双引号时才
		// 保留双引号，不能把普通值统一改成带引号格式。
		parts = append(parts, key+"="+c.cookies[key])
	}
	return strings.Join(parts, "; ")
}

// absorbResponseCookies 保存 Apple 在 validate/accountLogin 中刷新的会话
// Cookie，后续跨动态 mccgateway 主机的显式 Cookie 头也会使用这些新值。
func (c *WebClient) absorbResponseCookies(resp *http.Response) {
	if c.cookies == nil {
		c.cookies = make(map[string]string)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "" && cookie.Value != "" {
			c.cookies[cookie.Name] = cookie.Value
		}
	}
}

// withParams 给 URL 追加 iCloud Web 必需查询参数。首次 validate 时
// Apple 会根据 Cookie 解析 DSID，提前附加 dsid 会在中国区返回 HTTP 421。
func (c *WebClient) withParams(rawURL string, includeDSID bool) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	result := fmt.Sprintf("%s%sclientBuildNumber=%s&clientMasteringNumber=%s&clientId=%s",
		rawURL, sep, WebClientBuildNumber, WebClientBuildNumber, c.clientID)
	if includeDSID && c.dsid != "" {
		result += "&dsid=" + url.QueryEscape(c.dsid)
	}
	return result
}

type setupResponse struct {
	DsInfo struct {
		Dsid string `json:"dsid"`
	} `json:"dsInfo"`
	Webservices struct {
		Mccgateway struct {
			URL string `json:"url"`
		} `json:"mccgateway"`
	} `json:"webservices"`
}

func normalizeServiceURL(rawURL string) string {
	serviceURL := strings.TrimSpace(rawURL)
	if serviceURL == "" {
		return ""
	}
	if !strings.HasPrefix(serviceURL, "https://") {
		serviceURL = "https://" + serviceURL
	}
	// Apple 有时返回 :443。去掉默认端口，避免 CookieJar 按不同 host
	// 处理；显式 Cookie 头虽已兜底，但统一 URL 也便于后续请求。
	if u, err := url.Parse(serviceURL); err == nil && u.Host != "" {
		u.Host = u.Hostname()
		serviceURL = u.String()
	}
	return strings.TrimRight(serviceURL, "/")
}

func (c *WebClient) applySetupResponse(body []byte) error {
	var parsed setupResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("解析 iCloud setup 响应失败: %w", err)
	}
	if parsed.DsInfo.Dsid != "" {
		c.dsid = parsed.DsInfo.Dsid
	}
	c.mccGatewayURL = normalizeServiceURL(parsed.Webservices.Mccgateway.URL)
	if c.mccGatewayURL == "" {
		return fmt.Errorf("iCloud setup 响应缺少 mccgateway URL")
	}
	return nil
}

// resolveMccGateway 从 validate 响应中获取 mccgateway URL。
func (c *WebClient) resolveMccGateway() error {
	if c.mccGatewayURL != "" {
		return nil
	}

	setupURL := "https://setup." + c.host + "/setup/ws/1/validate"
	req, err := http.NewRequest("POST", c.withParams(setupURL, false), nil)
	if err != nil {
		return err
	}
	c.setCommonHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	c.absorbResponseCookies(resp)
	if resp.StatusCode == http.StatusMisdirectedRequest {
		return fmt.Errorf("iCloud Web Mail 会话已失效,请更新 Cookie 或配置 App 专用密码")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("validate 失败: HTTP %d", resp.StatusCode)
	}
	return c.applySetupResponse(body)
}

// threadSearchResp 是 thread/search 接口的响应结构。
type threadSearchResp struct {
	TotalThreadsReturned int `json:"totalThreadsReturned"`
	ThreadList           []struct {
		ThreadID  string   `json:"threadId"`
		Subject   string   `json:"subject"`
		Senders   []string `json:"senders"`
		Preview   string   `json:"preview"`
		Timestamp int64    `json:"timestamp"`
	} `json:"threadList"`
}

// search 执行 thread/search 请求,返回解析后的邮件列表。
func (c *WebClient) search(payload string) ([]Message, error) {
	if err := c.resolveMccGateway(); err != nil {
		return nil, err
	}

	searchURL := c.withParams(c.mccGatewayURL+"/mailws2/v1/thread/search", true)
	req, err := http.NewRequest("POST", searchURL, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setCommonHeaders(req)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取邮件失败: HTTP %d - %s", resp.StatusCode, truncate(string(body), 300))
	}
	if strings.Contains(string(body), `"success":false`) {
		return nil, fmt.Errorf("获取邮件失败: %s", truncate(string(body), 300))
	}

	var result threadSearchResp
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析邮件响应失败: %w", err)
	}

	messages := make([]Message, 0, len(result.ThreadList))
	for _, t := range result.ThreadList {
		from := ""
		if len(t.Senders) > 0 {
			from = t.Senders[0]
		}
		date := ""
		if t.Timestamp > 0 {
			date = time.UnixMilli(t.Timestamp).Format(time.RFC3339)
		}
		messages = append(messages, Message{
			ID:      t.ThreadID,
			From:    from,
			Subject: t.Subject,
			Preview: t.Preview,
			Date:    date,
		})
	}
	return messages, nil
}

// ListInbox 列出收件箱邮件。
func (c *WebClient) ListInbox(limit int) ([]Message, error) {
	payload := fmt.Sprintf(`{"responseType":"THREAD_DIGEST","includeFolderStatus":true,"maxResults":%d,"sessionHeaders":{"folder":"INBOX","modseq":null,"threadmodseq":null,"condstore":1,"qresync":1,"threadmode":1}}`, limit)
	return c.search(payload)
}

// SearchMails 搜索邮件。query 为空时等价于 ListInbox。
func (c *WebClient) SearchMails(query string, limit int) ([]Message, error) {
	if query == "" {
		return c.ListInbox(limit)
	}
	payload := fmt.Sprintf(`{"responseType":"THREAD_DIGEST","includeFolderStatus":false,"maxResults":%d,"query":%q,"sessionHeaders":{"folder":"INBOX","condstore":1,"qresync":1,"threadmode":1}}`, limit, query)
	return c.search(payload)
}

// FindByAlias 不使用 THREAD_DIGEST 做别名取件。该响应没有 To 字段，而且
// query 参数可能被上游忽略；把搜索结果标记成请求别名会混入其他邮箱邮件。
func (c *WebClient) FindByAlias(alias string, limit int) ([]Message, error) {
	return nil, ErrRecipientFilterUnavailable
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
