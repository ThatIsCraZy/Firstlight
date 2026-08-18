package console

import (
	"context"
	"errors"

	"ilo-kvm/internal/ilo"
)

func (s *Session) ManagementStatus(ctx context.Context) (ManagementStatus, error) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	client, err := s.activeManagementClient()
	if err != nil {
		return ManagementStatus{}, err
	}
	status, err := client.GetManagementStatus(ctx)
	if err != nil {
		return ManagementStatus{}, err
	}
	return ManagementStatus{
		PowerState:   status.PowerState,
		BootOverride: toConsoleBootOverride(status.BootOverride),
	}, nil
}

func (s *Session) SetOneTimeBoot(ctx context.Context, device string) (OneTimeBootResult, error) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	client, err := s.activeManagementClient()
	if err != nil {
		return OneTimeBootResult{}, err
	}
	change, err := client.SetOneTimeBoot(ctx, device)
	result := OneTimeBootResult{
		Device:   change.Device,
		Before:   toConsoleBootOverride(change.Before),
		Current:  toConsoleBootOverride(change.Current),
		Verified: change.Verified,
	}
	return result, err
}

func (s *Session) activeManagementClient() (*ilo.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.client == nil || s.client.SessionKey() == "" {
		return nil, errors.New("console is disconnected")
	}
	return s.client, nil
}

func toConsoleBootOverride(value ilo.BootOverrideStatus) BootOverrideStatus {
	return BootOverrideStatus{
		Target:  value.Target,
		Enabled: value.Enabled,
		Mode:    value.Mode,
	}
}
