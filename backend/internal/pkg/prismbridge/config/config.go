// Package config contains protocol configuration adapted from OAIprism.
package config

import "time"

type UpstreamConfig struct {
	BaseURL string `yaml:"base_url"`

	// 单请求整体超时；流式场景由 Context 控制，这里是兜底。
	Timeout time.Duration `yaml:"timeout"`

	// 连接层。
	DialTimeout           time.Duration `yaml:"dial_timeout"`
	KeepAlive             time.Duration `yaml:"keep_alive"`
	TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"`

	// 连接池。反代场景 MaxIdleConnsPerHost 是吞吐的第一瓶颈，
	// 默认值 2 会导致高频建连，这里给 256。
	MaxIdleConns        int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost int           `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost     int           `yaml:"max_conns_per_host"`
	IdleConnTimeout     time.Duration `yaml:"idle_conn_timeout"`

	// ForceHTTP2 强制尝试 HTTP/2（prism 是 HTTPS，默认即可协商成功）。
	ForceHTTP2 bool `yaml:"force_http2"`

	// DisableCompression 关闭 Go 自动 gzip 解压：我们要原样透传字节，
	// 让客户端自己协商压缩。开启自动解压会强制 Accept-Encoding 并丢掉原始编码。
	DisableCompression bool `yaml:"disable_compression"`

	// 读写缓冲：32KB 比默认 4KB 显著减少 syscall 次数。
	ReadBufferSize  int `yaml:"read_buffer_size"`
	WriteBufferSize int `yaml:"write_buffer_size"`

	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`

	// HTTPProxy 支持 per-upstream 出站代理（http/https/socks5）。
	HTTPProxy string `yaml:"http_proxy"`

	// SentinelProfile 指向自定义的浏览器指纹（JSON，形状同 internal/sentinel/profile_default.json）。
	// 留空用内置指纹。base_url 指向 prism.openai.com 时，出站自动走内置的 Chrome 指纹传输
	// 并用纯 Go 签发 Sentinel token（internal/upstream），User-Agent 等浏览器头也以指纹为准。
	SentinelProfile string `yaml:"sentinel_profile"`

	UserAgent string            `yaml:"user_agent"`
	Origin    string            `yaml:"origin"`
	Referer   string            `yaml:"referer"`
	Headers   map[string]string `yaml:"headers"`

	// 重试策略。
	MaxRetries    int           `yaml:"max_retries"`
	RetryBackoff  time.Duration `yaml:"retry_backoff"`
	RetryMaxDelay time.Duration `yaml:"retry_max_delay"`

	// 熔断：连续失败多少次后打开断路器。
	BreakerThreshold int           `yaml:"breaker_threshold"`
	BreakerCooldown  time.Duration `yaml:"breaker_cooldown"`
}

// CredsConfig 描述"凭据从哪来"。
type CredsConfig struct {
	// Mode: static | file | passthrough | hybrid
	//   static      - 凭据写在配置文件/env 里
	//   file        - 凭据放在独立 JSON 文件，按 mtime 热重载（推荐）
	//   passthrough - 由调用方在每个请求上携带，不落盘
	//   hybrid      - 池里优先用静态账号，缺号时透传
	Mode string `yaml:"mode"`

	File           string        `yaml:"file"`
	ReloadInterval time.Duration `yaml:"reload_interval"`

	// 凭据自愈。
	AutoRefresh       bool          `yaml:"auto_refresh"`     // 会话过期自动刷新
	RefreshSkew       time.Duration `yaml:"refresh_skew"`     // 提前多久刷新
	RefreshInterval   time.Duration `yaml:"refresh_interval"` // 后台巡检周期
	SessionPath       string        `yaml:"session_path"`     // 默认 /api/auth/session
	OAuthTokenURL     string        `yaml:"oauth_token_url"`  // refresh_token 换发地址
	OAuthClientID     string        `yaml:"oauth_client_id"`  // 默认 codex 客户端
	OAuthScope        string        `yaml:"oauth_scope"`
	PersistRefresh    bool          `yaml:"persist_refresh"`     // 刷新结果写回文件
	PersistRefreshMin time.Duration `yaml:"persist_refresh_min"` // 写回节流

	Accounts []AccountConfig `yaml:"accounts"`
}

// AccountConfig 是单个账号的静态定义。
//
// 认证字段按"能拿到什么填什么"设计，四种形态任意组合：
//
//	Cookies       - 浏览器里整串 Cookie（最省事，包含 session-token）
//	SessionToken  - 只给 __Secure-next-auth.session-token 的值
//	AccessToken   - 直接给 JWT（chatgpt accessToken）
//	RefreshToken  - 给 OAuth refresh_token，由代理自动换 accessToken
type AccountConfig struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Enabled *bool  `yaml:"enabled" json:"enabled"` // 指针以便区分"未设置"与"显式 false"

	Cookies      string            `yaml:"cookies" json:"cookies"`
	CookieMap    map[string]string `yaml:"cookie_map" json:"cookie_map"`
	SessionToken string            `yaml:"session_token" json:"session_token"`
	AccessToken  string            `yaml:"access_token" json:"access_token"`
	RefreshToken string            `yaml:"refresh_token" json:"refresh_token"`
	ExpiresAt    *time.Time        `yaml:"expires_at" json:"expires_at"`

	AccountID string `yaml:"account_id" json:"account_id"`
	Email     string `yaml:"email" json:"email"`
	Plan      string `yaml:"plan" json:"plan"`

	// 网络出口。留空走 Upstream.HTTPProxy。
	Proxy string `yaml:"proxy" json:"proxy"`

	// 单账号并发上限。Prism 对同一账号并发比较敏感，默认保守。
	MaxConcurrency int `yaml:"max_concurrency" json:"max_concurrency"`

	// 速率限制：每秒补充 tokens 个令牌，桶容量 burst。
	RatePerSecond float64 `yaml:"rate_per_second" json:"rate_per_second"`
	RateBurst     int     `yaml:"rate_burst" json:"rate_burst"`

	// Weight 用于加权轮询。
	Weight int `yaml:"weight" json:"weight"`

	Headers map[string]string `yaml:"headers" json:"headers"`
	Tags    []string          `yaml:"tags" json:"tags"`
}

// IsEnabled 处理 *bool 的三态。
func (a AccountConfig) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

type SchemaConfig struct {
	StartPath  string `yaml:"start_path"`
	StatusPath string `yaml:"status_path"`
	// StopPath 用于主动取消进行中的生成（前端"停止"按钮走的就是它）。
	StopPath string `yaml:"stop_path"`

	// 请求字段名。
	FieldModel          string `yaml:"field_model"`
	FieldMessages       string `yaml:"field_messages"`
	FieldInstructions   string `yaml:"field_instructions"`
	FieldInput          string `yaml:"field_input"`
	FieldTools          string `yaml:"field_tools"`
	FieldStream         string `yaml:"field_stream"`
	FieldSessionID      string `yaml:"field_session_id"`
	FieldProjectID      string `yaml:"field_project_id"`
	FieldSandboxID      string `yaml:"field_sandbox_id"`
	FieldConversationID string `yaml:"field_conversation_id"`
	// FieldPreviousRespID / FieldMetadata 对应真实请求体里的
	// previousResponseId 与 metadata —— 多轮延续与模型参数都走 metadata。
	FieldPreviousRespID string `yaml:"field_previous_response_id"`
	FieldMetadata       string `yaml:"field_metadata"`
	// FieldUserID 是调用方身份（OpenAI 的 user / Anthropic 的 metadata.user_id）。
	// 它只进 metadata，不进请求体顶层。
	//
	// 注意：这个名字来自对端实现，尚未经真实报文验证（与 schema 里其它
	// 字段一样属于推断值）。设成 "-" 即可整体关闭这个字段。
	FieldUserID string `yaml:"field_user_id"`
	// FieldRequestID / FieldTurnState 只出现在 status / stop 请求里。
	// turn_state 是服务端下发的不透明状态对象，必须原样回传，
	// 自己构造会被上游拒绝（实测：任何自造值都回 "turn_state is required"）。
	FieldRequestID       string         `yaml:"field_request_id"`
	FieldTurnState       string         `yaml:"field_turn_state"`
	FieldResponseID      string         `yaml:"field_response_id"`
	FieldReasoning       string         `yaml:"field_reasoning"`
	FieldReasoningEffort string         `yaml:"field_reasoning_effort"`
	FieldExtra           map[string]any `yaml:"field_extra"`

	// 响应字段名（用于抽取增量文本）。
	RespIDKeys      []string `yaml:"resp_id_keys"`
	RespStatusKeys  []string `yaml:"resp_status_keys"`
	RespTextKeys    []string `yaml:"resp_text_keys"`
	RespDeltaKeys   []string `yaml:"resp_delta_keys"`
	RespMessagesKey string   `yaml:"resp_messages_key"`
	RespErrorKeys   []string `yaml:"resp_error_keys"`

	// 终态状态值。
	StatusDone []string `yaml:"status_done"`
	StatusFail []string `yaml:"status_fail"`
	StatusRun  []string `yaml:"status_running"`
}

type Config struct {
	Upstream UpstreamConfig
	Creds    CredsConfig
	Facade   struct{ Schema SchemaConfig }
}

func Default() *Config {
	return &Config{
		Upstream: UpstreamConfig{
			BaseURL:               "https://prism.openai.com",
			Timeout:               0,
			DialTimeout:           10 * time.Second,
			KeepAlive:             30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          512,
			MaxIdleConnsPerHost:   256,
			MaxConnsPerHost:       0,
			IdleConnTimeout:       90 * time.Second,
			ForceHTTP2:            true,
			DisableCompression:    true,
			ReadBufferSize:        32 << 10,
			WriteBufferSize:       32 << 10,
			UserAgent:             DefaultUserAgent,
			Origin:                "https://prism.openai.com",
			Referer:               "https://prism.openai.com/",
			MaxRetries:            3,
			RetryBackoff:          200 * time.Millisecond,
			RetryMaxDelay:         5 * time.Second,
			BreakerThreshold:      12,
			BreakerCooldown:       30 * time.Second,
		},
		Creds: CredsConfig{
			Mode:            "file",
			File:            "secrets/accounts.json",
			ReloadInterval:  5 * time.Second,
			AutoRefresh:     true,
			RefreshSkew:     5 * time.Minute,
			RefreshInterval: 10 * time.Minute,
			SessionPath:     "/api/auth/session",
			OAuthTokenURL:   "https://auth.openai.com/oauth/token",
			OAuthClientID:   DefaultOAuthClientID,
			OAuthScope:      "openid profile email offline_access",
		},
		Facade: struct{ Schema SchemaConfig }{Schema: defaultSchema()},
	}
}
func defaultSchema() SchemaConfig {
	return SchemaConfig{
		StartPath:  "/api/llm/response_with_tools_start",
		StatusPath: "/api/llm/response_with_tools_status",
		StopPath:   "/api/llm/response_with_tools_stop",

		FieldModel:        "model",
		FieldMessages:     "messages",
		FieldInstructions: "instructions",
		// 真实请求体里的对话内容字段叫 input（数组），不是 messages。
		FieldInput:  "input",
		FieldTools:  "tools",
		FieldStream: "stream",
		// 多轮延续与模型参数。
		FieldPreviousRespID: "previousResponseId",
		FieldMetadata:       "metadata",
		FieldUserID:         "userId",

		FieldSessionID: "session_id",
		FieldProjectID: "project_id",
		FieldSandboxID: "sandbox_id",
		// 注意大小写：start 请求体里是 camelCase 的 conversationId，
		// 而 status 请求里又是 snake_case 的 request_id ——
		// 上游自己不一致，我们照抄，不要"顺手统一"。
		FieldConversationID:  "conversationId",
		FieldRequestID:       "request_id",
		FieldTurnState:       "turn_state",
		FieldResponseID:      "response_id",
		FieldReasoning:       "reasoning",
		FieldReasoningEffort: "effort",

		RespIDKeys:     []string{"request_id", "id", "response_id", "stream_id"},
		RespStatusKeys: []string{"status", "state", "phase"},
		// 正文路径：response.payload.output[-1].content[-1].text
		RespTextKeys:    []string{"text", "output_text", "content", "message"},
		RespDeltaKeys:   []string{"delta", "delta_text", "chunk", "output_text_delta"},
		RespMessagesKey: "messages",
		RespErrorKeys:   []string{"error", "errors"},

		// 实测状态机：start 回 started，status 回 pending，终态是 completed。
		StatusDone: []string{"completed", "complete", "done", "succeeded", "success", "finished", "final"},
		StatusFail: []string{"failed", "error", "cancelled", "canceled", "expired"},
		StatusRun:  []string{"started", "pending", "running", "in_progress", "queued", "streaming", "generating"},
	}
}

const DefaultOAuthClientID = "app_jqKb52JverFFcl5GP4axT8QY"
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
