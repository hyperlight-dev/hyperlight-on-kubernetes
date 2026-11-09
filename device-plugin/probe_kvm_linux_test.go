// Copyright 2026 The Hyperlight Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"testing"
)

func TestKVMLiveVMCreation(t *testing.T) {
	if os.Getenv("TEST_KVM_LIVE") != "1" {
		t.Skip("set TEST_KVM_LIVE=1 on a KVM host")
	}
	t.Setenv("TEST_PROBE_SUCCESS", "0")
	t.Setenv("TEST_PROBE_HANG", "0")
	requireNoError(t, (&deviceProbe{}).check(t.Context(), "kvm", "/dev/kvm"))
}
