package config

import "sync"

// Keep the lock outside Config so copying configuration never copies a lock.
// All access to the immutable override pointer, including its first publication,
// is protected. Static configuration must be finalized before concurrent use.
var openAIWSModeRouterV2Mu sync.RWMutex

// OpenAIWSModeRouterV2Enabled returns the live override or the static default.
func (c *Config) OpenAIWSModeRouterV2Enabled() bool {
	if c == nil {
		return false
	}
	openAIWSModeRouterV2Mu.RLock()
	defer openAIWSModeRouterV2Mu.RUnlock()
	if value := c.openAIWSModeRouterV2EnabledLive; value != nil {
		return *value
	}
	return c.Gateway.OpenAIWS.ModeRouterV2Enabled
}

// SetOpenAIWSModeRouterV2Enabled publishes an override without mutating the
// static ModeRouterV2Enabled field or any other gateway configuration.
func (c *Config) SetOpenAIWSModeRouterV2Enabled(enabled bool) {
	if c == nil {
		return
	}
	openAIWSModeRouterV2Mu.Lock()
	c.openAIWSModeRouterV2EnabledLive = &enabled
	openAIWSModeRouterV2Mu.Unlock()
}
