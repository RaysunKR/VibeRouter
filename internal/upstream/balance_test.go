package upstream

import (
	"context"
	"testing"
)

func TestMsgFollowsClientLang(t *testing.T) {
	ctx := context.Background()
	if got := Msg(ctx, "中文消息", "english message"); got != "中文消息" {
		t.Fatalf("no preference: Msg = %q, want Chinese default", got)
	}
	ctx = WithClientLang(ctx, "en-US,en;q=0.9")
	if got := Msg(ctx, "中文消息", "english message"); got != "english message" {
		t.Fatalf("en client: Msg = %q, want english", got)
	}
	ctx = WithClientLang(ctx, "zh-CN,zh;q=0.9")
	if got := Msg(ctx, "中文消息", "english message"); got != "中文消息" {
		t.Fatalf("zh client: Msg = %q, want Chinese", got)
	}
	ctx = WithClientLang(ctx, "")
	if got := Msg(ctx, "中文消息", "english message"); got != "english message" {
		t.Fatalf("empty header: Msg = %q, want english", got)
	}
}
