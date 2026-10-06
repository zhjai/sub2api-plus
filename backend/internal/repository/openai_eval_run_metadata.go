package repository

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Keep run provenance in the existing outcome JSON so old readers can ignore
// these fields without a schema migration. It must survive every progress and
// terminal write, including a disable switch applied during the run.
type openAIEvalStoredOutcome struct {
	service.OpenAIEvalOutcome
	DiagnosticOnly bool   `json:"diagnostic_only,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
}

func marshalOpenAIEvalRunOutcome(run *service.OpenAIEvalRun) ([]byte, error) {
	return json.Marshal(openAIEvalStoredOutcome{run.Outcome, run.DiagnosticOnly, run.Protocol})
}

func unmarshalOpenAIEvalRunOutcome(raw []byte, run *service.OpenAIEvalRun) error {
	var stored openAIEvalStoredOutcome
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	run.Outcome = stored.OpenAIEvalOutcome
	run.DiagnosticOnly, run.Protocol = stored.DiagnosticOnly, stored.Protocol
	return nil
}
