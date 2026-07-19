package brk

import (
	"fmt"
	"time"
)

const (
	defaultBackoffMultiplier = 2.0
	defaultJitterFraction    = 0.20
	minimumSharedKeyBytes    = 32
)

// DefaultRetryConfig returns the documented retry settings.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		QueueLength:       Qlength,
		RetryInterval:     5 * time.Second,
		AckTimeout:        2 * time.Second,
		MaxAttempts:       0,
		DuplicateTTL:      5 * time.Minute,
		MaxPending:        Qlength,
		BackoffMultiplier: defaultBackoffMultiplier,
		MaxRetryDelay:     30 * time.Second,
		JitterFraction:    defaultJitterFraction,
		DeliveryTimeout:   time.Minute,
		WireVersion:       ProtocolV1,
		ReassemblyTTL:     5 * time.Minute,
		// Ten seconds covers a lost predecessor's first retransmission
		// (AckTimeout plus one RetryInterval scan) with margin.
		OrderingHoldTimeout: 10 * time.Second,
	}
}

// ResolveRetryConfig fills zero-valued settings with defaults and rejects negative values.
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

	if config.MaxPending == 0 {
		config.MaxPending = defaults.MaxPending
	} else if config.MaxPending < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected max pending messages greater than zero, received %d", config.MaxPending)
	}

	if config.BackoffMultiplier == 0 {
		config.BackoffMultiplier = defaults.BackoffMultiplier
	} else if config.BackoffMultiplier < 1 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected backoff multiplier at least 1, received %v", config.BackoffMultiplier)
	}

	if config.MaxRetryDelay == 0 {
		config.MaxRetryDelay = defaults.MaxRetryDelay
	} else if config.MaxRetryDelay < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected max retry delay greater than zero, received %v", config.MaxRetryDelay)
	}

	if config.JitterFraction < 0 || config.JitterFraction >= 1 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected jitter fraction in [0,1), received %v", config.JitterFraction)
	} else if config.DisableJitter {
		config.JitterFraction = 0
	} else if config.JitterFraction == 0 {
		config.JitterFraction = defaults.JitterFraction
	}

	if config.DeliveryTimeout < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected delivery timeout greater than zero, received %v", config.DeliveryTimeout)
	} else if config.DisableDeliveryTimeout {
		config.DeliveryTimeout = 0
	} else if config.DeliveryTimeout == 0 {
		config.DeliveryTimeout = defaults.DeliveryTimeout
	}

	if len(config.AuthenticationKey) > 0 && len(config.AuthenticationKey) < minimumSharedKeyBytes {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected authentication key to be empty or at least %d bytes, received %d bytes", minimumSharedKeyBytes, len(config.AuthenticationKey))
	}
	config.AuthenticationKey = append([]byte(nil), config.AuthenticationKey...)

	if config.WireVersion == ProtocolLegacy {
		config.WireVersion = defaults.WireVersion
	} else if config.WireVersion != ProtocolV1 && config.WireVersion != ProtocolV2 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected wire version %d or %d, received %d", ProtocolV1, ProtocolV2, config.WireVersion)
	}

	if config.FragmentPayloadBytes < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected fragment payload bytes zero or greater, received %d", config.FragmentPayloadBytes)
	} else if config.FragmentPayloadBytes > maxFragmentPayload(config.WireVersion) {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected fragment payload bytes at most %d for wire version %d, received %d", maxFragmentPayload(config.WireVersion), config.WireVersion, config.FragmentPayloadBytes)
	}

	if config.ReassemblyTTL == 0 {
		config.ReassemblyTTL = defaults.ReassemblyTTL
	} else if config.ReassemblyTTL < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected reassembly TTL greater than zero, received %v", config.ReassemblyTTL)
	}

	if config.OrderingHoldTimeout == 0 {
		config.OrderingHoldTimeout = defaults.OrderingHoldTimeout
	} else if config.OrderingHoldTimeout < 0 {
		return RetryConfig{}, fmt.Errorf("retry config invalid: expected ordering hold timeout greater than zero, received %v", config.OrderingHoldTimeout)
	}

	return config, nil
}

// DefaultSTUNConfig returns the documented STUN server and timeout.
func DefaultSTUNConfig() STUNConfig {
	return STUNConfig{
		Server:  DefaultSTUNServer,
		Timeout: 3 * time.Second,
	}
}

// ResolveSTUNConfig fills zero-valued settings with defaults and rejects a negative timeout.
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

// DefaultPunchConfig returns eight attempts with a 250 millisecond response interval.
func DefaultPunchConfig() PunchConfig {
	return PunchConfig{Attempts: 8, Interval: 250 * time.Millisecond}
}

// ResolvePunchConfig fills zero-valued settings with defaults and rejects negative values.
func ResolvePunchConfig(config PunchConfig) (PunchConfig, error) {
	defaults := DefaultPunchConfig()
	if config.Attempts == 0 {
		config.Attempts = defaults.Attempts
	} else if config.Attempts < 0 {
		return PunchConfig{}, fmt.Errorf("punch config invalid: expected attempts greater than zero, received %d", config.Attempts)
	}
	if config.Interval == 0 {
		config.Interval = defaults.Interval
	} else if config.Interval < 0 {
		return PunchConfig{}, fmt.Errorf("punch config invalid: expected interval greater than zero, received %v", config.Interval)
	}
	return config, nil
}

// DefaultKeepaliveConfig returns a 20 second peer keepalive interval.
func DefaultKeepaliveConfig() KeepaliveConfig {
	return KeepaliveConfig{Interval: 20 * time.Second}
}

// ResolveKeepaliveConfig fills a zero interval with the default and rejects a negative interval.
func ResolveKeepaliveConfig(config KeepaliveConfig) (KeepaliveConfig, error) {
	defaults := DefaultKeepaliveConfig()
	if config.Interval == 0 {
		config.Interval = defaults.Interval
	} else if config.Interval < 0 {
		return KeepaliveConfig{}, fmt.Errorf("keepalive config invalid: expected interval greater than zero, received %v", config.Interval)
	}
	return config, nil
}
