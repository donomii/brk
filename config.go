package brk

import (
	"fmt"
	"time"
)

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		QueueLength:   Qlength,
		RetryInterval: 5 * time.Second,
		AckTimeout:    2 * time.Second,
		MaxAttempts:   0,
		DuplicateTTL:  5 * time.Minute,
	}
}

func ResolveRetryConfig(config RetryConfig) (RetryConfig, error) {
	defaults := DefaultRetryConfig()
	if config.QueueLength == 0 {
		config.QueueLength = defaults.QueueLength
	} else if config.QueueLength < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected queue length greater than zero, received %d", config.QueueLength)
	}

	if config.RetryInterval == 0 {
		config.RetryInterval = defaults.RetryInterval
	} else if config.RetryInterval < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected retry interval greater than zero, received %v", config.RetryInterval)
	}

	if config.AckTimeout == 0 {
		config.AckTimeout = defaults.AckTimeout
	} else if config.AckTimeout < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected ack timeout greater than zero, received %v", config.AckTimeout)
	}

	if config.MaxAttempts < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected max attempts to be zero or greater, received %d", config.MaxAttempts)
	}

	if config.DuplicateTTL == 0 {
		config.DuplicateTTL = defaults.DuplicateTTL
	} else if config.DuplicateTTL < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected duplicate TTL greater than zero, received %v", config.DuplicateTTL)
	}

	return config, nil
}

func DefaultSTUNConfig() STUNConfig {
	return STUNConfig{
		Server:  DefaultSTUNServer,
		Timeout: 3 * time.Second,
	}
}

func ResolveSTUNConfig(config STUNConfig) (STUNConfig, error) {
	defaults := DefaultSTUNConfig()
	if config.Server == "" {
		config.Server = defaults.Server
	}
	if config.Timeout == 0 {
		config.Timeout = defaults.Timeout
	} else if config.Timeout < 0 {
		return STUNConfig{}, fmt.Errorf("STUN config invalid: expected timeout greater than zero, received %v", config.Timeout)
	}
	return config, nil
}
