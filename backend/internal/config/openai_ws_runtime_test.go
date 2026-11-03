package config

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSModeRouterV2Runtime(t *testing.T) {
	for _, initial := range []bool{false, true} {
		cfg := &Config{}
		cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = initial
		require.Equal(t, initial, cfg.OpenAIWSModeRouterV2Enabled())
		for _, override := range []bool{true, false} {
			cfg.SetOpenAIWSModeRouterV2Enabled(override)
			require.Equal(t, override, cfg.OpenAIWSModeRouterV2Enabled())
			require.Equal(t, initial, cfg.Gateway.OpenAIWS.ModeRouterV2Enabled)
		}
		copy := *cfg
		copy.SetOpenAIWSModeRouterV2Enabled(true)
		require.True(t, copy.OpenAIWSModeRouterV2Enabled())
		require.False(t, cfg.OpenAIWSModeRouterV2Enabled())
	}
	var absent *Config
	require.False(t, absent.OpenAIWSModeRouterV2Enabled())
	absent.SetOpenAIWSModeRouterV2Enabled(true)
}

func TestOpenAIWSModeRouterV2RuntimeConcurrent(t *testing.T) {
	// Start from a zero-value config: first publication is part of the race test.
	cfg := &Config{}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 1000; n++ {
				cfg.SetOpenAIWSModeRouterV2Enabled((i+n)%2 == 0)
				_ = cfg.OpenAIWSModeRouterV2Enabled()
				// Production resolvers copy the static WS config concurrently.
				ws := cfg.Gateway.OpenAIWS
				if ws.ModeRouterV2Enabled {
					t.Error("static default was mutated")
				}
			}
		}(i)
	}
	wg.Wait()
	cfg.SetOpenAIWSModeRouterV2Enabled(true)
	require.True(t, cfg.OpenAIWSModeRouterV2Enabled())
}
