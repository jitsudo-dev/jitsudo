//go:build windows

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package notifications

import "context"

// SIEMSyslogConfig is defined on all platforms so that config and server
// packages compile on Windows. log/syslog is unavailable on Windows, so the
// notifier is a no-op stub.
type SIEMSyslogConfig struct {
	Network  string `yaml:"network"`
	Address  string `yaml:"address"`
	Tag      string `yaml:"tag"`
	Facility string `yaml:"facility"`
}

// SIEMSyslogNotifier is a no-op on Windows because log/syslog is not available.
type SIEMSyslogNotifier struct{}

// NewSIEMSyslogNotifier returns a no-op notifier on Windows.
func NewSIEMSyslogNotifier(_ SIEMSyslogConfig) *SIEMSyslogNotifier {
	return &SIEMSyslogNotifier{}
}

// Notify is a no-op on Windows.
func (s *SIEMSyslogNotifier) Notify(_ context.Context, _ Event) error {
	return nil
}
