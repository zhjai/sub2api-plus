package prism

import (
	"os"
	"testing"
)

// 用真实抓包的终态 status 响应验证 ResponseID / 会话链句柄提取。
//
// 背景（2026-10-02）：强类型 PrismEnvelope 因 codexDeltaFiles.diff 形态
// 漂移（string -> object）整体反序列化失败，请求永远退回宽松分支，
// 而宽松分支此前不提取 payload.id —— 导致 previousResponseId 误用
// request_id（UUID），上游静默忽略，多轮对话全部丢失上下文。
// 本测试以当日真实报文为回归样本，防止再次退化。
func TestParseStatusPayload_ExtractsResponseID(t *testing.T) {
	raw, err := os.ReadFile("testdata-final.json")
	if err != nil {
		t.Skip("无抓包样本:", err)
	}
	c := &Client{}
	st, err := c.ParseStatusPayload(raw, "fallback", "")
	if err != nil || st == nil {
		t.Fatalf("解析失败: st=%v err=%v", st, err)
	}
	t.Logf("RequestID=%s ResponseID=%q ConversationID=%q Status=%s Text=%.40s",
		st.RequestID, st.ResponseID, st.ConversationID, st.Status, st.Text)
	if st.ResponseID == "" {
		t.Fatal("ResponseID 为空 —— payload.id 未被提取")
	}
}
