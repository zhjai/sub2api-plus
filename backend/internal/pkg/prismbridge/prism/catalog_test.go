package prism

import (
	"reflect"
	"testing"
)

func TestParseModelCatalog(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []UpstreamModel
	}{
		{"array", `[{"id":" model-a ","label":" Model A "}]`, []UpstreamModel{{ID: "model-a", Label: "Model A"}}},
		{"models wrapper", `{"models":[{"id":"model-a","label":"Model A"}]}`, []UpstreamModel{{ID: "model-a", Label: "Model A"}}},
		{"data wrapper", `{"data":[{"id":"model-a","label":"Model A"}]}`, []UpstreamModel{{ID: "model-a", Label: "Model A"}}},
		{"empty array", `[]`, []UpstreamModel{}},
		{"empty models", `{"models":[]}`, []UpstreamModel{}},
		{"empty data", `{"data":[]}`, []UpstreamModel{}},
		{"duplicates and invalid items", `[null,7,{"id":4,"label":"bad"},{"id":"missing-label"},{"id":"model-a","label":"First"},{"id":"model-a","label":"Second"},{"id":"model-b","label":"Next"}]`, []UpstreamModel{{ID: "model-a", Label: "First"}, {ID: "model-b", Label: "Next"}}},
		{"effort metadata", `[{"id":"model-a","label":"A","reasoning_efforts":["low","high"],"default_reasoning_effort":" high "},{"id":"model-b","label":"B","supported_reasoning_efforts":[{"value":"low"},{"effort":"high"},{"id":"max"}]}]`, []UpstreamModel{{ID: "model-a", Label: "A", Efforts: []string{"low", "high"}, DefaultEffort: "high"}, {ID: "model-b", Label: "B", Efforts: []string{"low", "high", "max"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseModelCatalog([]byte(tc.raw))
			for i := range got {
				if len(got[i].Efforts) == 0 {
					got[i].Efforts = nil
				}
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseModelCatalog() = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestParseModelCatalogRejectsInvalidResponses(t *testing.T) {
	for _, raw := range []string{
		``, `not-json`, `null`, `42`, `"models"`, `{}`, `{"error":"unauthorized"}`,
		`{"models":null}`, `{"models":{}}`, `{"models":"invalid"}`, `{"data":null}`,
		`[{"id":"no-label"}]`, `[{"label":"no-id"}]`, `[null,false,7]`,
	} {
		t.Run(raw, func(t *testing.T) {
			if got, err := ParseModelCatalog([]byte(raw)); err == nil {
				t.Fatalf("invalid response accepted as %#v", got)
			}
		})
	}
}
