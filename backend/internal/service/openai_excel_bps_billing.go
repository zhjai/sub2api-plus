package service

import (
	"bufio"
	"bytes"
	"io"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NormalizeExcelBPSUsage is called only after an actual BPS request. InputTokens
// already includes cache creation; zeroing its sub-bucket bills it once as input.
func NormalizeExcelBPSUsage(a *Account, usage *OpenAIUsage) *OpenAIUsage {
	if usage == nil {
		return nil
	}
	original := *usage
	if a.IsExcelBPSCacheCreationAsInputEnabled() {
		usage.CacheCreationInputTokens = 0
	}
	return &original
}

type ExcelBPSBillingMeasurement struct{ UpstreamUsage *OpenAIUsage }
type excelBPSBillingReader struct {
	original    io.ReadCloser
	reader      *bufio.Reader
	account     *Account
	measurement *ExcelBPSBillingMeasurement
	pending     []byte
	terminal    error
}

func NewExcelBPSBillingReader(a *Account, body io.ReadCloser) (io.ReadCloser, *ExcelBPSBillingMeasurement) {
	measurement := &ExcelBPSBillingMeasurement{}
	return &excelBPSBillingReader{original: body, reader: bufio.NewReader(body), account: a, measurement: measurement}, measurement
}
func (r *excelBPSBillingReader) Close() error { return r.original.Close() }
func (r *excelBPSBillingReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 && r.terminal == nil {
		line, err := r.reader.ReadBytes('\n')
		r.terminal = err
		if bytes.HasPrefix(line, []byte("data:")) {
			raw := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			r.pending = append([]byte("data: "), NormalizeExcelBPSUsageJSON(r.account, raw, r.measurement)...)
			if bytes.HasSuffix(line, []byte("\n")) {
				r.pending = append(r.pending, '\n')
			}
		} else {
			r.pending = line
		}
	}
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	return 0, r.terminal
}

// NormalizeExcelBPSUsageJSON preserves raw measurement under upstream_usage and
// changes every recognized creation field so precedence cannot revive a bucket.
func NormalizeExcelBPSUsageJSON(a *Account, raw []byte, measurement *ExcelBPSBillingMeasurement) []byte {
	if !gjson.ValidBytes(raw) {
		return raw
	}
	usage, ok := extractOpenAIUsageFromJSONBytes(raw)
	if ok && measurement != nil {
		measurement.UpstreamUsage = &usage
	}
	if !a.IsExcelBPSCacheCreationAsInputEnabled() {
		return raw
	}
	for _, path := range []string{"usage", "response.usage"} {
		value := gjson.GetBytes(raw, path)
		if !value.IsObject() {
			continue
		}
		raw, _ = sjson.SetRawBytes(raw, path+".upstream_usage", []byte(value.Raw))
		for _, key := range []string{"cache_creation_input_tokens", "cache_write_input_tokens", "cache_write_tokens", "cache_creation_tokens", "input_tokens_details.cache_write_tokens", "input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_creation_tokens"} {
			if value.Get(key).Exists() {
				if next, err := sjson.SetBytes(raw, path+"."+key, 0); err == nil {
					raw = next
				}
			}
		}
	}
	return raw
}

var _ io.ReadCloser = (*excelBPSBillingReader)(nil)
