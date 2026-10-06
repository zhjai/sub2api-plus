package service

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type openAIEvalChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			apicompat.ChatMessage
			Refusal string `json:"refusal"`
		} `json:"message"`
		Delta struct {
			apicompat.ChatDelta
			Refusal      string                      `json:"refusal"`
			FunctionCall *apicompat.ChatFunctionCall `json:"function_call"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *apicompat.ChatUsage `json:"usage"`
	Error *openAIEvalWireError `json:"error"`
}

func readOpenAIEvalChatCompletionsResponse(ctx context.Context, resp *http.Response) (*OpenAIEvalSampleResponse, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readOpenAIEvalResponse(ctx, resp, false, true)
	}
	result := &OpenAIEvalSampleResponse{HTTPStatus: resp.StatusCode}
	limited := &io.LimitedReader{R: resp.Body, N: openAIEvalResponseLimit + 1}
	reader := bufio.NewReader(limited)
	prefix, _ := reader.Peek(5)
	isSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") ||
		strings.HasPrefix(string(prefix), "data:") || strings.HasPrefix(string(prefix), "event") || strings.HasPrefix(string(prefix), ":")
	var text strings.Builder
	finishReason, refusal := "", ""
	apply := func(raw []byte) error {
		var wire openAIEvalChatResponse
		if json.Unmarshal(raw, &wire) != nil {
			return newOpenAIEvalRequestError("invalid_response", "upstream returned invalid Chat Completions JSON", resp.StatusCode)
		}
		if wire.Error != nil {
			return openAIEvalWireFailure(wire.Error, "upstream_error", "upstream returned an error", resp.StatusCode)
		}
		if wire.Model != "" {
			result.Model = wire.Model
		}
		if wire.Usage != nil {
			result.InputTokens, result.OutputTokens = int64(wire.Usage.PromptTokens), int64(wire.Usage.CompletionTokens)
		}
		for _, choice := range wire.Choices {
			if choice.Index != 0 {
				continue
			}
			if len(choice.Message.ToolCalls) != 0 || choice.Message.FunctionCall != nil || len(choice.Delta.ToolCalls) != 0 || choice.Delta.FunctionCall != nil {
				return newOpenAIEvalRequestError("unexpected_tool_call", "text evaluation returned a tool call", resp.StatusCode)
			}
			if choice.Message.Role != "" && choice.Message.Role != "assistant" || choice.Delta.Role != "" && choice.Delta.Role != "assistant" {
				return newOpenAIEvalRequestError("invalid_response", "text evaluation returned a non-assistant message", resp.StatusCode)
			}
			if len(choice.Message.Content) != 0 && string(choice.Message.Content) != "null" {
				var content string
				if json.Unmarshal(choice.Message.Content, &content) != nil {
					var parts []apicompat.ChatContentPart
					if json.Unmarshal(choice.Message.Content, &parts) != nil {
						return newOpenAIEvalRequestError("invalid_response", "invalid Chat Completions message content", resp.StatusCode)
					}
					for _, part := range parts {
						if part.Type == "text" {
							content += part.Text
						}
					}
				}
				text.WriteString(content)
			}
			if choice.Delta.Content != nil {
				text.WriteString(*choice.Delta.Content)
			}
			refusal += choice.Message.Refusal + choice.Delta.Refusal
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
		}
		result.Text = text.String()
		return nil
	}
	complete := func() (*OpenAIEvalSampleResponse, error) {
		if refusal != "" {
			return result, newOpenAIEvalRequestError("refusal", refusal, resp.StatusCode)
		}
		code := "invalid_response"
		switch finishReason {
		case "stop":
			if strings.TrimSpace(result.Text) == "" {
				return result, newOpenAIEvalRequestError("empty_output", "completed response contained no output text", resp.StatusCode)
			}
			result.CompletedAt = time.Now().UTC()
			return result, nil
		case "length":
			code = "max_output_tokens"
		case "content_filter":
			code = "content_filter"
		case "tool_calls", "function_call":
			code = "unexpected_tool_call"
		case "":
			code = "missing_terminal"
		}
		return result, newOpenAIEvalRequestError(code, "Chat Completions evaluation did not finish normally: "+finishReason, resp.StatusCode)
	}
	if !isSSE {
		body, err := io.ReadAll(reader)
		if ctx.Err() != nil {
			return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
		}
		if err != nil {
			return result, openAIEvalIOError(ctx, err, resp.StatusCode)
		}
		if limited.N == 0 {
			return result, newOpenAIEvalRequestError("response_too_large", "response exceeded 2 MiB", resp.StatusCode)
		}
		if err := apply(body); err != nil {
			return result, err
		}
		return complete()
	}
	// A few compatible providers stream despite stream=false. Keep their usage
	// frame, but require both finish_reason and [DONE] before accepting a sample.
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), openAIEvalResponseLimit+1)
	var data strings.Builder
	dispatch := func() (bool, error) {
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "" {
			return false, nil
		}
		if raw == "[DONE]" {
			_, err := complete()
			return true, err
		}
		return false, apply([]byte(raw))
	}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
		}
		if limited.N == 0 {
			return result, newOpenAIEvalRequestError("response_too_large", "stream exceeded 2 MiB", resp.StatusCode)
		}
		line := scanner.Text()
		if line == "" {
			if done, err := dispatch(); done || err != nil {
				return result, err
			}
		} else if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			data.WriteByte('\n')
		}
	}
	if ctx.Err() != nil {
		return result, openAIEvalIOError(ctx, ctx.Err(), resp.StatusCode)
	}
	if limited.N == 0 {
		return result, newOpenAIEvalRequestError("response_too_large", "stream exceeded 2 MiB", resp.StatusCode)
	}
	if err := scanner.Err(); err != nil {
		return result, openAIEvalIOError(ctx, err, resp.StatusCode)
	}
	if done, err := dispatch(); done || err != nil {
		return result, err
	}
	return result, newOpenAIEvalRequestError("missing_terminal", "Chat Completions stream ended before [DONE]", resp.StatusCode)
}
