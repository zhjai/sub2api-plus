package prism

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
)

// testSchema 直接取生产默认值，保证测试与线上用的是同一套字段映射 ——
// 手抄一份映射是测试最容易腐化的地方。
func testSchema() SchemaOptions {
	s := config.Default().Facade.Schema
	return SchemaOptions{
		StartPath:            s.StartPath,
		StatusPath:           s.StatusPath,
		StopPath:             s.StopPath,
		FieldModel:           s.FieldModel,
		FieldMessages:        s.FieldMessages,
		FieldInstructions:    s.FieldInstructions,
		FieldInput:           s.FieldInput,
		FieldTools:           s.FieldTools,
		FieldStream:          s.FieldStream,
		FieldPreviousRespID:  s.FieldPreviousRespID,
		FieldMetadata:        s.FieldMetadata,
		FieldSessionID:       s.FieldSessionID,
		FieldProjectID:       s.FieldProjectID,
		FieldSandboxID:       s.FieldSandboxID,
		FieldConversationID:  s.FieldConversationID,
		FieldRequestID:       s.FieldRequestID,
		FieldTurnState:       s.FieldTurnState,
		FieldResponseID:      s.FieldResponseID,
		FieldReasoning:       s.FieldReasoning,
		FieldReasoningEffort: s.FieldReasoningEffort,
		FieldExtra:           s.FieldExtra,
		RespIDKeys:           s.RespIDKeys,
		RespStatusKeys:       s.RespStatusKeys,
		RespTextKeys:         s.RespTextKeys,
		RespDeltaKeys:        s.RespDeltaKeys,
		RespMessagesKey:      s.RespMessagesKey,
		RespErrorKeys:        s.RespErrorKeys,
		StatusDone:           s.StatusDone,
		StatusFail:           s.StatusFail,
		StatusRun:            s.StatusRun,
	}
}

func TestDiff(t *testing.T) {
	cases := []struct {
		prev, cur string
		wantDelta string
		wantReset bool
	}{
		{"", "hello", "hello", false},
		{"hello", "hello", "", false},
		{"hello", "hello world", " world", false},
		{"hello", "", "", false},
		{"abc", "abd", "d", true},
		{"完全不同的内容", "whatever", "whatever", true},
	}
	for _, c := range cases {
		d, r := Diff(c.prev, c.cur)
		if d != c.wantDelta || r != c.wantReset {
			t.Errorf("Diff(%q,%q) = (%q,%v), want (%q,%v)", c.prev, c.cur, d, r, c.wantDelta, c.wantReset)
		}
	}
}

func TestFlattenContent(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"plain", "plain"},
		{[]any{"a", "b"}, "ab"},
		{[]any{map[string]any{"type": "text", "text": "hi"}}, "hi"},
		{map[string]any{"text": "nested"}, "nested"},
		{map[string]any{"content": "c"}, "c"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := FlattenContent(c.in); got != c.want {
			t.Errorf("FlattenContent(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGetPath(t *testing.T) {
	var root any
	_ = json.Unmarshal([]byte(`{"a":{"b":[{"c":"deep"}]}}`), &root)

	v, ok := GetPath(root, "a.b.0.c")
	if !ok || v != "deep" {
		t.Fatalf("GetPath = (%v,%v), want (deep,true)", v, ok)
	}
	if _, ok := GetPath(root, "a.x"); ok {
		t.Fatal("不存在的路径应当返回 false")
	}
	if _, ok := GetPath(root, "a.b.9"); ok {
		t.Fatal("越界索引应当返回 false")
	}
}

func TestFindLongestString(t *testing.T) {
	// 响应里同时存在空的占位字段与真正的正文，必须取长的那个。
	var root any
	_ = json.Unmarshal([]byte(`{
		"text": "",
		"data": {"message": {"content": "这是真正的回答内容"}},
		"meta": {"text": "短"}
	}`), &root)

	got := FindLongestString(root, []string{"text", "content"}, maxWalkDepth)
	if got != "这是真正的回答内容" {
		t.Fatalf("FindLongestString = %q", got)
	}
}

func TestFindKey_BreadthFirst(t *testing.T) {
	// 顶层与深层都有 status，BFS 应当取顶层的。
	var root any
	_ = json.Unmarshal([]byte(`{"status":"completed","detail":{"status":"running"}}`), &root)
	if got := FindString(root, []string{"status"}); got != "completed" {
		t.Fatalf("BFS 未取到顶层 status，得到 %q", got)
	}
}

// TestParseStatus_Shapes 覆盖我们能想到的上游返回形态。
//
// 这是本包最重要的测试：宽容解析的价值全部体现在"换个 shape 仍然能work"。
func TestParseStatus_Shapes(t *testing.T) {
	c := &Client{schema: testSchema()}

	cases := []struct {
		name      string
		body      string
		prev      string
		wantText  string
		wantDelta string
		wantDone  bool
		wantFail  bool
	}{
		{
			name:      "messages 累计全文",
			body:      `{"response_id":"r1","status":"in_progress","messages":[{"role":"assistant","content":"你好，"} ]}`,
			prev:      "",
			wantText:  "你好，",
			wantDelta: "你好，",
		},
		{
			name:      "messages 前缀增长 -> 差分出增量",
			body:      `{"status":"in_progress","messages":[{"role":"assistant","content":"你好，世界"}]}`,
			prev:      "你好，",
			wantText:  "你好，世界",
			wantDelta: "世界",
		},
		{
			name:      "显式 delta 字段",
			body:      `{"status":"running","delta":"更多内容"}`,
			prev:      "已收到",
			wantText:  "已收到更多内容",
			wantDelta: "更多内容",
		},
		{
			name:     "终态 completed",
			body:     `{"status":"completed","output_text":"最终答案"}`,
			prev:     "最终",
			wantText: "最终答案",
			wantDone: true,
		},
		{
			name:     "done 布尔标记（没有 status 字段）",
			body:     `{"done":true,"text":"完成"}`,
			prev:     "",
			wantText: "完成",
			wantDone: true,
		},
		{
			name:     "深层嵌套包裹",
			body:     `{"data":{"result":{"status":"completed","content":[{"type":"text","text":"深层"}]}}}`,
			prev:     "",
			wantText: "深层",
			wantDone: true,
		},
		{
			name:     "空 body（长轮询超时）",
			body:     ``,
			prev:     "已有内容",
			wantText: "已有内容",
		},
		{
			name:     "纯文本响应",
			body:     `就是一段纯文本`,
			prev:     "",
			wantText: "就是一段纯文本",
		},
		{
			name: "错误状态",
			body: `{"status":"failed","error":{"message":"模型不可用"}}`,
			prev: "",
			// 失败也属于终态：轮询循环必须能退出，否则会一直转下去。
			wantFail: true,
			wantDone: true,
		},
		{
			name:     "多重包裹 + 非标准状态词",
			body:     `{"payload":{"state":"IN_PROGRESS","message":{"text":"继续"}}}`,
			prev:     "",
			wantText: "继续",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := c.ParseStatusPayload([]byte(tc.body), "fallback", tc.prev)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if tc.wantText != "" && st.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", st.Text, tc.wantText)
			}
			if tc.wantDelta != "" && st.Delta != tc.wantDelta {
				t.Errorf("Delta = %q, want %q", st.Delta, tc.wantDelta)
			}
			if st.Done != tc.wantDone {
				t.Errorf("Done = %v, want %v (status=%q)", st.Done, tc.wantDone, st.Status)
			}
			if st.Fail != tc.wantFail {
				t.Errorf("Fail = %v, want %v", st.Fail, tc.wantFail)
			}
			if st.RequestID == "" {
				t.Error("RequestID 不应为空")
			}
		})
	}
}

func TestParseStatus_Usage(t *testing.T) {
	c := &Client{schema: testSchema()}
	st, err := c.ParseStatusPayload(
		[]byte(`{"status":"completed","text":"ok","usage":{"input_tokens":10,"output_tokens":5}}`), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Usage == nil {
		t.Fatal("未解析出 usage")
	}
	if st.Usage.InputTokens != 10 || st.Usage.OutputTokens != 5 || st.Usage.TotalTokens != 15 {
		t.Fatalf("usage 解析错误: %+v", st.Usage)
	}
}

func TestParseStatus_OnlyAssistantMessagesCounted(t *testing.T) {
	c := &Client{schema: testSchema()}
	st, err := c.ParseStatusPayload(
		[]byte(`{"status":"in_progress","messages":[
			{"role":"user","content":"用户的问题"},
			{"role":"assistant","content":"回答"}
		]}`), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Text != "回答" {
		t.Fatalf("应当只统计 assistant 内容，得到 %q", st.Text)
	}
}

// TestBuildStartPayload_RealShape 固化实测出来的请求体形状。
//
// 上游对字段的要求很硬：input 必须是数组（否则回 "input must be an array"），
// model 与 reasoning_effort 在 metadata 里而不是顶层。
func TestBuildStartPayload_RealShape(t *testing.T) {
	c := &Client{schema: testSchema()}

	p := c.buildStartPayload(&StartRequest{
		Input:           []InputItem{NewUserItem("你好")},
		Model:           "gpt-5.6-sol",
		ReasoningEffort: "high",
		Metadata:        map[string]any{"projectId": "proj-1"},
	})

	// 1) input 必须是数组
	in, ok := p["input"].([]InputItem)
	if !ok {
		t.Fatalf("input 必须是数组，得到 %T", p["input"])
	}
	if len(in) != 1 || in[0].Role != "user" {
		t.Fatalf("input 内容错误: %+v", in)
	}
	if in[0].Content[0].Type != BlockInputText {
		t.Errorf("用户内容块类型应为 input_text，得到 %q", in[0].Content[0].Type)
	}

	// 2) 模型参数必须在 metadata 里
	if _, ok := p["model"]; ok {
		t.Error("model 不应出现在请求体顶层（真实报文里它在 metadata 内）")
	}
	meta, ok := p["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 metadata: %+v", p)
	}
	if meta["model"] != "gpt-5.6-sol" {
		t.Errorf("metadata.model = %v", meta["model"])
	}
	if meta["reasoning_effort"] != "high" {
		t.Errorf("metadata.reasoning_effort = %v", meta["reasoning_effort"])
	}
	if meta["projectId"] != "proj-1" {
		t.Errorf("metadata.projectId 丢失: %v", meta)
	}

	// 3) 空的 previousResponseId 不该出现
	if _, ok := p["previousResponseId"]; ok {
		t.Error("空的 previousResponseId 不应出现在请求体里")
	}
}

// TestBuildStartPayload_UserID 覆盖调用方身份的透传。
//
// 为什么单独测：user 字段此前在 facade 层被解析出来又丢掉了
// （请求体里有 json tag，但没有任何地方把它送下去），
// 属于"静默失效"——客户端传了身份，上游却一无所知。
// 这类 bug 只能靠断言"字段真的出现在报文里"来防。
func TestBuildStartPayload_UserID(t *testing.T) {
	// 1) 有 user 时必须进 metadata
	c := &Client{schema: testSchema()}
	p := c.buildStartPayload(&StartRequest{
		Input:  []InputItem{NewUserItem("你好")},
		Model:  "gpt-5.6-sol",
		UserID: "u-42",
	})
	meta, ok := p["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 metadata: %+v", p)
	}
	if meta["userId"] != "u-42" {
		t.Errorf("metadata.userId = %v, want u-42", meta["userId"])
	}
	// userId 只进 metadata，不能出现在顶层
	if _, bad := p["userId"]; bad {
		t.Error("userId 不应出现在请求体顶层")
	}

	// 2) 没给 user 时不要发空字段 —— 上游可能据此判定为匿名并降级
	p2 := c.buildStartPayload(&StartRequest{Input: []InputItem{NewUserItem("你好")}})
	meta2, _ := p2["metadata"].(map[string]any)
	if _, bad := meta2["userId"]; bad {
		t.Errorf("未传 user 时不应出现 userId: %+v", meta2)
	}

	// 3) 字段名可配置（推断值，协议漂移时改 YAML 即可）
	cs := testSchema()
	cs.FieldUserID = "user_id"
	c3 := &Client{schema: cs}
	p3 := c3.buildStartPayload(&StartRequest{Input: []InputItem{NewUserItem("x")}, UserID: "u-9"})
	meta3, _ := p3["metadata"].(map[string]any)
	if meta3["user_id"] != "u-9" {
		t.Errorf("metadata.user_id = %v, want u-9", meta3["user_id"])
	}

	// 4) "-" 表示整体关闭这个字段
	cs4 := testSchema()
	cs4.FieldUserID = "-"
	c4 := &Client{schema: cs4}
	p4 := c4.buildStartPayload(&StartRequest{Input: []InputItem{NewUserItem("x")}, UserID: "u-9"})
	meta4, _ := p4["metadata"].(map[string]any)
	if _, bad := meta4["userId"]; bad {
		t.Errorf("field_user_id=- 时应完全不发该字段: %+v", meta4)
	}
}

func TestBuildStartPayload_EmptyInputIsStillArray(t *testing.T) {
	// 空 input 也必须是 []，不能是 null ——
	// 上游的类型校验只认数组，null 会被判成 "input must be an array"。
	c := &Client{schema: testSchema()}
	p := c.buildStartPayload(&StartRequest{})
	if _, ok := p["input"].([]InputItem); !ok {
		t.Fatalf("input 必须是数组，得到 %T", p["input"])
	}
}

func TestBuildStartPayload_PreviousAndConversation(t *testing.T) {
	c := &Client{schema: testSchema()}
	p := c.buildStartPayload(&StartRequest{
		Input:              []InputItem{NewUserItem("继续")},
		PreviousResponseID: "req-prev",
		ConversationID:     "conv-1",
	})
	if p["previousResponseId"] != "req-prev" {
		t.Errorf("previousResponseId 丢失: %+v", p)
	}
	// 大小写必须保持 camelCase：上游 start 用 conversationId，
	// status 用 conversation_id，写错了就是静默失效。
	if p["conversationId"] != "conv-1" {
		t.Errorf("conversationId 字段名/大小写错误: %+v", p)
	}
	if _, ok := p["conversation_id"]; ok {
		t.Error("start 请求不应使用 snake_case 的 conversation_id")
	}
}

func TestBuildStatusPayload(t *testing.T) {
	c := &Client{schema: testSchema()}
	p := c.buildStatusPayload(&StatusRequest{
		RequestID: "req-9",
		TurnState: json.RawMessage(`{"cursor":"x"}`),
	})
	if p["request_id"] != "req-9" {
		t.Errorf("status 请求必须用 snake_case 的 request_id: %+v", p)
	}
	raw, ok := p["turn_state"].(json.RawMessage)
	if !ok || string(raw) != `{"cursor":"x"}` {
		t.Errorf("turn_state 必须原样回传: %+v", p["turn_state"])
	}
}

// TestParseEnvelope_RealProtocol 固化实测到的响应包络。
//
// 这是本项目最关键的一组断言：上游用 HTTP 200 + response.status:"error"
// 表达失败，用 status:"pending" + 新的 turn_state 表达"继续轮询"。
// 任何一处判断错，代理都会给客户端返回错误的结果，而且看起来"成功"。
func TestParseEnvelope_RealProtocol(t *testing.T) {
	c := &Client{schema: testSchema()}

	t.Run("started 带 turn_state", func(t *testing.T) {
		body := `{"status":"started","request_id":"req-1","conversation_id":null,` +
			`"turn_state":{"cursor":"abc","seq":3}}`
		st, err := c.ParseStatusPayload([]byte(body), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if st.RequestID != "req-1" {
			t.Errorf("RequestID = %q", st.RequestID)
		}
		if st.Status != "started" || st.Done || st.Fail {
			t.Errorf("状态错误: %+v", st)
		}
		if len(st.TurnState) == 0 {
			t.Fatal("turn_state 必须被原样保留，否则无法继续轮询")
		}
		if !strings.Contains(string(st.TurnState), "cursor") {
			t.Errorf("turn_state 内容失真: %s", st.TurnState)
		}
	})

	t.Run("pending 带新 turn_state", func(t *testing.T) {
		body := `{"status":"pending","request_id":"req-1","turn_state":{"cursor":"def"}}`
		st, _ := c.ParseStatusPayload([]byte(body), "req-1", "")
		if st.Done || st.Fail {
			t.Errorf("pending 不应是终态: %+v", st)
		}
		if !strings.Contains(string(st.TurnState), "def") {
			t.Errorf("turn_state 未更新: %s", st.TurnState)
		}
	})

	t.Run("completed + success 抽取正文", func(t *testing.T) {
		body := `{"status":"completed","request_id":"req-1","response":{"status":"success","payload":{"output":[` +
			`{"type":"reasoning","summary":[{"type":"summary_text","text":"先想一下"}]},` +
			`{"type":"message","role":"assistant","content":[` +
			`{"type":"output_text","text":"答案是 42"},{"type":"output_text","text":"。"}]}]}}}`
		st, _ := c.ParseStatusPayload([]byte(body), "req-1", "")
		if !st.Done || st.Fail {
			t.Fatalf("应为成功终态: %+v", st)
		}
		if st.Text != "答案是 42。" {
			t.Errorf("正文抽取错误: %q", st.Text)
		}
		if st.Reasoning != "先想一下" {
			t.Errorf("思维链抽取错误: %q", st.Reasoning)
		}
	})

	t.Run("completed + error 必须判为失败", func(t *testing.T) {
		// 最容易踩的坑：HTTP 200 + completed，但 response.status 是 error。
		// 只认顶层 status 的实现会返回一个"成功的空回答"。
		body := `{"status":"completed","request_id":"req-1",` +
			`"response":{"status":"error","payload":{"reason":"unknown","message":"User not found"}}}`
		st, _ := c.ParseStatusPayload([]byte(body), "req-1", "")
		if !st.Fail {
			t.Fatal("嵌套的 response.status=error 必须被判为失败")
		}
		if !st.Done {
			t.Error("失败也是终态，必须置 Done")
		}
		if st.Error != "User not found" {
			t.Errorf("错误信息丢失: %q", st.Error)
		}
		if st.ErrorReason != "unknown" {
			t.Errorf("错误原因丢失: %q", st.ErrorReason)
		}
	})

	t.Run("只取最后一条 output 避免历史污染", func(t *testing.T) {
		body := `{"status":"completed","request_id":"req-1","response":{"status":"success","payload":{"output":[` +
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"上一轮的回答"}]},` +
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"这一轮的回答"}]}]}}}`
		st, _ := c.ParseStatusPayload([]byte(body), "req-1", "")
		if st.Text != "这一轮的回答" {
			t.Fatalf("应只取最后一条 output，得到 %q", st.Text)
		}
	})

	t.Run("input_image 不计入正文", func(t *testing.T) {
		body := `{"status":"completed","request_id":"r","response":{"status":"success","payload":{"output":[` +
			`{"type":"message","role":"assistant","content":[` +
			`{"type":"input_image","text":"[图片]"},` +
			`{"type":"output_text","text":"正文"}]}]}}}`
		st, _ := c.ParseStatusPayload([]byte(body), "r", "")
		if st.Text != "正文" {
			t.Fatalf("用户输入回显混进了正文: %q", st.Text)
		}
	})

	t.Run("codexDeltaFiles 确定性强类型提取", func(t *testing.T) {
		body := `{"status":"completed","request_id":"req-delta","response":{"status":"success","payload":{"output":[` +
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"已创建文件"}]}],` +
			`"codexDeltaFiles":[{"file_path":"hello.txt","status":"added","diff":"--- /dev/null\n+++ hello.txt\n@@ -0,0 +1 @@\n+hello world"}]}}}`
		st, err := c.ParseStatusPayload([]byte(body), "req-delta", "")
		if err != nil {
			t.Fatalf("解析错误: %v", err)
		}
		if len(st.DeltaFiles) != 1 {
			t.Fatalf("预期提取 1 个 DeltaFile，实际得到 %d", len(st.DeltaFiles))
		}
		df := st.DeltaFiles[0]
		if df.FilePath != "hello.txt" || df.Status != "added" || !strings.Contains(df.DiffString(), "+hello world") {
			t.Fatalf("DeltaFile 字段解析错误: %+v", df)
		}
	})
}

func TestMessageRoundTrip(t *testing.T) {
	raw := `{"role":"assistant","content":"hi","tool_calls":[{"id":"c1"}],"extra_field":123}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.Role != "assistant" || m.TextContent() != "hi" {
		t.Fatalf("解析错误: %+v", m)
	}
	if m.Extra["extra_field"] != float64(123) {
		t.Fatalf("未知字段未保留: %+v", m.Extra)
	}

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"role", "content", "tool_calls", "extra_field"} {
		if !strings.Contains(string(out), k) {
			t.Errorf("序列化丢字段 %s: %s", k, out)
		}
	}
}

func TestProjectKey(t *testing.T) {
	if (&Project{UUID: "u", ID: "i"}).Key() != "u" {
		t.Error("应优先 uuid")
	}
	if (&Project{ID: "i"}).Key() != "i" {
		t.Error("无 uuid 时用 id")
	}
	var p *Project
	if p.Key() != "" {
		t.Error(" nil 项目应返回空串")
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("5"); d.Seconds() != 5 {
		t.Errorf("秒数解析错误: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("空值应为 0: %v", d)
	}
	if d := parseRetryAfter("not-a-date"); d != 0 {
		t.Errorf("非法值应为 0: %v", d)
	}
}

func BenchmarkDiff(b *testing.B) {
	prev := strings.Repeat("这是一段已经收到的内容。", 200)
	cur := prev + "这是新增的一小段。"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Diff(prev, cur)
	}
}

func BenchmarkParseStatus(b *testing.B) {
	c := &Client{schema: testSchema()}
	body := []byte(`{"response_id":"r1","status":"in_progress","messages":[
		{"role":"user","content":"请解释一下拉格朗日中值定理"},
		{"role":"assistant","content":"` + strings.Repeat("根据定理，存在一点使得导数等于平均变化率。", 20) + `"}
	]}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.ParseStatusPayload(body, "fallback", "")
	}
}

// TestFlattenContent_MapSlice 是回归测试。
//
// 背景：[]map[string]any 与 []any 是两个不同的类型，类型断言不会互相匹配。
// 而 facade 的 toPrismContent 产出的正是 []map[string]any，
// 少了这个分支会让多模态消息的文本被静默丢掉，
// 表现为"回答质量莫名其妙变差"，极难定位。
func TestFlattenContent_MapSlice(t *testing.T) {
	in := []map[string]any{
		{"type": "text", "text": "第一段"},
		{"type": "text", "text": "第二段"},
	}
	if got := FlattenContent(in); got != "第一段第二段" {
		t.Fatalf("FlattenContent([]map[string]any) = %q", got)
	}

	// 嵌套在消息里也必须能取出来。
	m := Message{Role: "user", Content: in}
	if got := m.TextContent(); got != "第一段第二段" {
		t.Fatalf("Message.TextContent = %q", got)
	}
}

func TestFlattenContent_StringSlice(t *testing.T) {
	if got := FlattenContent([]string{"a", "b"}); got != "ab" {
		t.Fatalf("FlattenContent([]string) = %q", got)
	}
}

func TestExtractPrismErrorBody(t *testing.T) {
	cases := []struct {
		input []byte
		want  string
	}{
		{[]byte(`{"payload":{"message":"Invalid model","reason":"invalid_model"}}`), "Invalid model"},
		{[]byte(`{"payload":{"error":"something went wrong"}}`), "something went wrong"},
		{[]byte(`{"detail":"Not Found"}`), "Not Found"},
		{[]byte(`{"payload":{"rootCause":"internal failure"}}`), "internal failure"},
		{[]byte(`plain error text`), "plain error text"},
	}

	for _, c := range cases {
		got := extractPrismErrorBody(c.input)
		if got != c.want {
			t.Errorf("extractPrismErrorBody(%s) = %q, want %q", c.input, got, c.want)
		}
	}
}
