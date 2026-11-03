package cpaeval

import "testing"

func TestExtractCode(t *testing.T) {
	page := "<!doctype html><svg></svg>"
	for _, tc := range []struct{ answer, language, want string }{
		{"说明\n```css\nbody {}\n```\n```HTML\n" + page + "\n```", "html", page},
		{"```html\n<p></p>\n```\n```svg\n<svg/>\n```", "svg", "<svg/>"},
		{"```\n" + page + "\n```", "html", page},
		{"  " + page + "\n", "html", page},
		{"```js\nlet svg = 1\n```\n" + page, "html", ""},
		{"下面是页面：" + page, "html", ""},
	} {
		if got := extractCode(tc.answer, tc.language); got != tc.want {
			t.Errorf("extractCode(%q, %q) = %q, want %q", tc.answer, tc.language, got, tc.want)
		}
	}
}
