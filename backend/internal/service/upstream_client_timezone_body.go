package service

import (
	"encoding/xml"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"io"
	"strconv"
	"strings"
	"time"
)

type upstreamTimezoneCarrier struct {
	Path, Text  string
	Environment bool
}

func upstreamTimezoneCarriers(body []byte) []upstreamTimezoneCarrier {
	var carriers []upstreamTimezoneCarrier
	for i, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("role").String() != "user" {
			continue
		}
		kinds := item.Get("internal_chat_message_metadata_passthrough.content_item_kinds").Array()
		for j, part := range item.Get("content").Array() {
			if j >= len(kinds) || kinds[j].String() != "environments.environment_context" || part.Get("type").String() != "input_text" {
				continue
			}
			value := part.Get("text")
			if value.Type == gjson.String && strings.Contains(value.String(), "timezone") {
				carriers = append(carriers, upstreamTimezoneCarrier{Path: "input." + strconv.Itoa(i) + ".content." + strconv.Itoa(j) + ".text", Text: value.String(), Environment: true})
			}
		}
	}
	for i, tool := range gjson.GetBytes(body, "tools").Array() {
		kind := tool.Get("type").String()
		if kind != "web_search" && kind != "web_search_preview" && kind != "web_search_preview_2025_03_11" {
			continue
		}
		value := tool.Get("user_location.timezone")
		if value.Type == gjson.String {
			carriers = append(carriers, upstreamTimezoneCarrier{Path: "tools." + strconv.Itoa(i) + ".user_location.timezone", Text: value.String()})
		}
	}
	return carriers
}
func hasUpstreamTimezoneCarrier(body []byte) bool { return len(upstreamTimezoneCarriers(body)) > 0 }
func rewriteUpstreamTimezone(body []byte, location *time.Location, now time.Time) ([]byte, error) {
	original := body
	for _, carrier := range upstreamTimezoneCarriers(body) {
		replacement := location.String()
		var err error
		if carrier.Environment {
			replacement, err = rewriteUpstreamTimezoneEnvironment(carrier.Text, location, now)
		}
		if err != nil {
			return original, err
		}
		if replacement == carrier.Text {
			continue
		}
		body, err = sjson.SetBytes(body, carrier.Path, replacement)
		if err != nil {
			return original, &UpstreamClientTimezoneError{Code: "invalid_protocol_metadata"}
		}
	}
	return body, nil
}

type upstreamTimezoneXMLValue struct {
	Tag        string
	Start, End int
	Value      string
}

func rewriteUpstreamTimezoneEnvironment(text string, location *time.Location, now time.Time) (string, error) {
	invalid := func() (string, error) { return text, &UpstreamClientTimezoneError{Code: "invalid_environment_context"} }
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<environment_context>") || !strings.HasSuffix(trimmed, "</environment_context>") {
		return invalid()
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	depth := 0
	roots := 0
	var active *upstreamTimezoneXMLValue
	var values []upstreamTimezoneXMLValue
	seen := map[string]bool{}
	for {
		start := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return invalid()
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				roots++
				if roots != 1 || element.Name.Local != "environment_context" || element.Name.Space != "" {
					return invalid()
				}
			}
			if depth == 2 && (element.Name.Local == "timezone" || element.Name.Local == "current_date") {
				if element.Name.Space != "" || len(element.Attr) != 0 || seen[element.Name.Local] {
					return invalid()
				}
				seen[element.Name.Local] = true
				offset := int(decoder.InputOffset())
				if offset >= 2 && text[offset-2:offset] == "/>" {
					return invalid()
				}
				active = &upstreamTimezoneXMLValue{Tag: element.Name.Local, Start: offset}
			} else if active != nil {
				return invalid()
			}
		case xml.EndElement:
			if depth == 2 && active != nil {
				active.End = start
				active.Value = text[active.Start:active.End]
				if strings.Contains(active.Value, "<") {
					return invalid()
				}
				values = append(values, *active)
				active = nil
			}
			depth--
		}
	}
	if depth != 0 || !seen["timezone"] {
		return invalid()
	}
	sourceZone := ""
	for _, value := range values {
		if value.Tag == "timezone" {
			sourceZone = strings.TrimSpace(value.Value)
		}
	}
	sourceLocation, sourceErr := loadUpstreamTimezone(sourceZone)
	for i := len(values) - 1; i >= 0; i-- {
		value := values[i]
		replacement := location.String()
		if value.Tag == "current_date" {
			// Historical environment snapshots in a continuation retain their date.
			if sourceErr != nil || strings.TrimSpace(value.Value) != now.In(sourceLocation).Format("2006-01-02") {
				continue
			}
			replacement = now.In(location).Format("2006-01-02")
		}
		left := len(value.Value) - len(strings.TrimLeft(value.Value, " \t\r\n"))
		right := len(strings.TrimRight(value.Value, " \t\r\n"))
		if right < left {
			left = 0
			right = len(value.Value)
		}
		text = text[:value.Start] + value.Value[:left] + replacement + value.Value[right:] + text[value.End:]
	}
	return text, nil
}
