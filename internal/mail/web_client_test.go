package mail

import (
	"errors"
	"testing"
)

func TestFindByAliasRejectsUnverifiableThreadDigest(t *testing.T) {
	c := &WebClient{}
	messages, err := c.FindByAlias("unused@icloud.com", 20)
	if messages != nil || !errors.Is(err, ErrRecipientFilterUnavailable) {
		t.Fatalf("应拒绝缺少真实收件人的 Web API 摘要: messages=%v err=%v", messages, err)
	}
}
