package mail

import (
	"net/mail"
	"strings"
	"testing"
)

func TestReadBodyDecodesMultipartQuotedPrintable(t *testing.T) {
	raw := strings.Join([]string{
		`Content-Type: multipart/alternative; boundary="part-123"`,
		`MIME-Version: 1.0`,
		``,
		`--part-123`,
		`Content-Type: text/plain; charset=UTF-8`,
		`Content-Transfer-Encoding: quoted-printable`,
		``,
		`=E9=AA=8C=E8=AF=81=E7=A0=81=EF=BC=9A123456`,
		`--part-123`,
		`Content-Type: text/html; charset=UTF-8`,
		`Content-Transfer-Encoding: quoted-printable`,
		``,
		`<p>=E9=AA=8C=E8=AF=81=E7=A0=81 <strong>123456</strong></p>`,
		`--part-123--`,
		``,
	}, "\r\n")
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := readBody(msg)
	if err != nil {
		t.Fatal(err)
	}
	if body != "验证码：123456" {
		t.Fatalf("multipart 正文解析错误: %q", body)
	}
	if strings.Contains(body, "Content-Type") || strings.Contains(body, "part-123") || strings.Contains(body, "=E9") {
		t.Fatalf("摘要仍包含 MIME 原文: %q", body)
	}
}

func TestReadBodyUsesBase64HTMLFallbackAndSkipsAttachments(t *testing.T) {
	raw := strings.Join([]string{
		`Content-Type: multipart/mixed; boundary="mixed-1"`,
		`MIME-Version: 1.0`,
		``,
		`--mixed-1`,
		`Content-Type: text/html; charset=UTF-8`,
		`Content-Transfer-Encoding: base64`,
		``,
		`PHA+WW91ciBjb2RlIGlzIDY1NDMyMSAmbmJzcDsgJmFtcDsgcmVhZHkuPC9wPg==`,
		`--mixed-1`,
		`Content-Type: text/plain`,
		`Content-Disposition: attachment; filename="secret.txt"`,
		``,
		`attachment text`,
		`--mixed-1--`,
		``,
	}, "\r\n")
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := readBody(msg)
	if err != nil {
		t.Fatal(err)
	}
	if body != "Your code is 654321 & ready." {
		t.Fatalf("HTML fallback 解析错误: %q", body)
	}
}

func TestNormalizePreviewLimitsRunes(t *testing.T) {
	preview := normalizePreview(strings.Repeat("验", maxPreviewRunes+10))
	if got := len([]rune(strings.TrimSuffix(preview, "..."))); got != maxPreviewRunes {
		t.Fatalf("摘要长度错误: %d", got)
	}
}

func TestIMAPUsernamesPrefersLocalPart(t *testing.T) {
	got := imapUsernames("user@icloud.com")
	if len(got) != 2 || got[0] != "user" || got[1] != "user@icloud.com" {
		t.Fatalf("IMAP 用户名回退顺序错误: %#v", got)
	}
	if got := imapUsernames("user"); len(got) != 1 || got[0] != "user" {
		t.Fatalf("无域名用户名处理错误: %#v", got)
	}
}
