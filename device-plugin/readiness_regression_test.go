// Copyright 2026 The Hyperlight Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestHostDeviceLossBlocksAllocationAndRecovers(t *testing.T) {
	hostDir := filepath.Join(t.TempDir(), "devices")
	requireNoError(t, os.Symlink("/dev", hostDir))
	t.Setenv("HOST_DEVICE_DIR", hostDir)
	p, err := newDevicePluginWithDevice("mshv", "/dev/null", false)
	requireNoError(t, err)
	p.cdiPath = filepath.Join(t.TempDir(), "hyperlight.json")
	p.interval = time.Hour
	stream := watchDevices(t, p)
	ctx := stream.Context()
	expectHealth(t, stream, pluginapi.Healthy)
	requireNoError(t, os.Remove(hostDir))
	req := &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"mshv-0"}}}}
	if _, err := p.Allocate(ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatalf("missing host device allowed allocation: %v", err)
	}
	expectHealth(t, stream, pluginapi.Unhealthy)
	requireNoError(t, os.Mkdir(hostDir, 0755))
	hostPath := filepath.Join(hostDir, "null")
	requireNoError(t, os.WriteFile(hostPath, nil, 0600))
	if _, err := p.Allocate(ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatalf("regular host path allowed: %v", err)
	}
	requireNoError(t, os.Remove(hostPath))
	requireNoError(t, os.Symlink("/dev/null", hostPath))
	if _, err := p.Allocate(ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatalf("host device symlink allowed: %v", err)
	}
	requireNoError(t, os.Remove(hostPath))
	requireNoError(t, os.Remove(hostDir))
	requireNoError(t, os.Symlink("/dev", hostDir))
	if _, err := p.Allocate(ctx, req); err != nil {
		t.Fatalf("restored host device refused: %v", err)
	}
	expectHealth(t, stream, pluginapi.Healthy)
}

func TestCancelledHelperDoesNotPoisonNextCheck(t *testing.T) {
	for i := 0; i < 3; i++ {
		p := testPlugin(t)
		requireNoError(t, p.checkReadiness(context.Background()))
		probe := &deviceProbe{}
		p.probe = func(ctx context.Context) error { return probe.check(ctx, "mshv", "/dev/null") }
		t.Setenv("TEST_PROBE_HANG", "1")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := p.checkReadiness(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancellation: %v", err)
		}
		t.Setenv("TEST_PROBE_HANG", "0")
		if err := p.checkReadiness(context.Background()); err != nil {
			t.Fatalf("next independent check: %v", err)
		}
		if p.readinessErr != nil {
			t.Fatal(p.readinessErr)
		}
	}
}

func TestHostDeviceIdentityMustMatch(t *testing.T) {
	if err := checkDeviceWithHost("mshv", "/dev/null", "/dev/zero"); err == nil {
		t.Fatal("different host device number accepted")
	}
	requireNoError(t, checkDeviceWithHost("mshv", "/dev/null", "/dev/null"))
}

func TestCancellationWhileReapingPreservesHealth(t *testing.T) {
	p := testPlugin(t)
	requireNoError(t, p.checkReadiness(context.Background()))
	pending := make(chan error, 1)
	probe := &deviceProbe{pending: pending}
	p.probe = func(ctx context.Context) error { return probe.check(ctx, "mshv", "/dev/null") }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := p.checkReadiness(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reap cancellation: %v", err)
	}
	if p.readinessErr != nil {
		t.Fatalf("reap cancellation changed health: %v", p.readinessErr)
	}
	pending <- errors.New("killed helper")
	if err := p.checkReadiness(context.Background()); err != nil {
		t.Fatalf("reaped helper blocked recovery: %v", err)
	}
}
