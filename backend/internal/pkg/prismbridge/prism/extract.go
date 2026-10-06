package prism

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 本文件实现"宽容解析"：在不知道上游确切片字段结构的前提下，
// 尽可能把增量文本捞出来。
//
// 为什么这么做：这套接口是内部 API，字段名会变、包裹层级会变。
// 如果按强类型绑定，上游加一层 {"data": {...}} 我们就全线 500。
// 宽容解析 + 配置化 key 列表可以把"协议漂移"降级成"改一行 YAML"。

// maxWalkDepth 限制递归深度，防止畸形响应导致栈爆炸。
const maxWalkDepth = 12

// FlattenContent 把各种形态的 content 拉平成字符串。
//
// 支持的形态：
//
//	"hello"
//	[{"type":"text","text":"hello"}, {"type":"image_url",...}]
//	{"text":"hello"}
func FlattenContent(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.RawMessage:
		var anyVal any
		if err := json.Unmarshal(t, &anyVal); err == nil {
			return FlattenContent(anyVal)
		}
		return string(t)
	case []byte:
		var anyVal any
		if err := json.Unmarshal(t, &anyVal); err == nil {
			return FlattenContent(anyVal)
		}
		return string(t)
	case json.Number:
		return t.String()
	case float64, bool, int, int64:
		b, _ := json.Marshal(t)
		return string(b)
	case []any:
		var sb strings.Builder
		for _, e := range t {
			s := FlattenContent(e)
			if s == "" {
				continue
			}
			sb.WriteString(s)
		}
		return sb.String()
	case []map[string]any:
		// 注意：[]map[string]any 与 []any 在 Go 的类型断言下是两个不同的类型，
		// 不会互相匹配。内部的 toPrismContent 产出的正是前者，
		// 少了这个分支会导致"多模态消息的文本被整段丢掉"——
		// 而这种丢失是静默的，只会表现为"回答质量莫名其妙变差"。
		var sb strings.Builder
		for _, e := range t {
			if s := FlattenContent(e); s != "" {
				sb.WriteString(s)
			}
		}
		return sb.String()
	case []string:
		return strings.Join(t, "")
	case map[string]any:
		// 常见包裹：{"type":"text","text":"..."} / {"content":"..."} / {"value":"..."}
		for _, k := range []string{"text", "content", "value", "output_text", "message"} {
			if inner, ok := t[k]; ok {
				if s := FlattenContent(inner); s != "" {
					return s
				}
			}
		}
		// 兜底：把所有字符串值拼起来。
		var sb strings.Builder
		for _, k := range sortedKeys(t) {
			sb.WriteString(FlattenContent(t[k]))
		}
		return sb.String()
	}
	return ""
}

// GetPath 按点号路径取值，例如 "data.output.0.text"。
func GetPath(root any, path string) (any, bool) {
	cur := root
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx := 0
			ok := false
			for i := 0; i < len(seg); i++ {
				if seg[i] < '0' || seg[i] > '9' {
					return nil, false
				}
				idx = idx*10 + int(seg[i]-'0')
				ok = true
			}
			if !ok || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// FindKey 广度优先查找第一个命中的 key。
//
// 用 BFS 而不是 DFS 是刻意的：越浅的层级越可能是"我们真正想要的那个字段"
// （顶层 status 优先于某个嵌套 detail.status）。
func FindKey(root any, keys []string, maxDepth int) (any, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[strings.ToLower(k)] = struct{}{}
	}

	type item struct {
		v     any
		depth int
	}
	queue := []item{{root, 0}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.depth > maxDepth {
			continue
		}
		switch node := it.v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(node) {
				if _, hit := set[strings.ToLower(k)]; hit {
					return node[k], true
				}
			}
			for _, k := range sortedKeys(node) {
				switch node[k].(type) {
				case map[string]any, []any:
					queue = append(queue, item{node[k], it.depth + 1})
				}
			}
		case []any:
			for _, e := range node {
				switch e.(type) {
				case map[string]any, []any:
					queue = append(queue, item{e, it.depth + 1})
				}
			}
		}
	}
	return nil, false
}

// FindString 查找第一个命中 key 的字符串值。
func FindString(root any, keys []string) string {
	v, ok := FindKey(root, keys, maxWalkDepth)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return FlattenContent(v)
}

// FindStringDeep 在 RespTextKeys 命中的所有位置里，取"最长的那个"作为正文。
//
// 这一步是为了绕开一个现实问题：响应里往往同时存在
// {"text":"..."} 和 {"content":[{"text":"..."}]} 两套并行结构，
// 而 BFS 只会返回最早命中的那个（可能是空的占位字段）。
// 取最长值在实践中显著更稳。
func FindLongestString(root any, keys []string, maxDepth int) string {
	if len(keys) == 0 {
		return ""
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[strings.ToLower(k)] = struct{}{}
	}

	best := ""
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > maxDepth {
			return
		}
		switch node := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(node) {
				if _, hit := set[strings.ToLower(k)]; hit {
					if s := FlattenContent(node[k]); len(s) > len(best) {
						best = s
					}
				}
			}
			for _, k := range sortedKeys(node) {
				switch node[k].(type) {
				case map[string]any, []any:
					walk(node[k], depth+1)
				}
			}
		case []any:
			for _, e := range node {
				switch e.(type) {
				case map[string]any, []any:
					walk(e, depth+1)
				}
			}
		}
	}
	walk(root, 0)
	return best
}

// Diff 计算 cur 相对 prev 新增的后缀。
//
// 这是把"轮询式协议"转成"流式协议"的核心技巧：
// 不管上游返回的是累计全文还是结构化消息列表，
// 只要它单调增长，我们就能用前缀差分还原出 token 级的增量。
//
// 返回 (delta, reset)。reset 为 true 表示 cur 不是 prev 的延续
// （例如上游重写了整段文本），此时调用方应按"覆盖"语义处理。
func Diff(prev, cur string) (string, bool) {
	if cur == "" {
		return "", false
	}
	if prev == "" {
		return cur, false
	}
	if cur == prev {
		return "", false
	}
	if strings.HasPrefix(cur, prev) {
		return cur[len(prev):], false
	}
	// 非前缀：可能上游改写了前面内容。用公共前缀裁剪后当作增量，
	// 并把 reset 标出去，让上层决定是否发一个"重新开始"的信号。
	cp := commonPrefixLen(prev, cur)
	if cp == 0 {
		return cur, true
	}
	return cur[cp:], true
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func sortedKeys(m map[string]any) []string {
	// 对多数小 map 而言，简单排序比建迭代器更划算，
	// 且顺序稳定能让解析结果可复现（便于测试与抓包比对）。
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// DecodeAny 反序列化并保留数字精度。
func DecodeAny(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// AsMap 尝试把任意值转成 map。
func AsMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// AsSlice 尝试把任意值转成切片。
func AsSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}
