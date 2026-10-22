// Copyright 2026 The Hyperlight Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestMSHVCreatePartitionABI(t *testing.T) {
	args := mshvCreatePartition{}
	if unsafe.Sizeof(args) != 16 || unsafe.Offsetof(args.isolation) != 8 {
		t.Fatal("mshv_create_partition does not match the Linux UAPI")
	}
	if mshvCreatePartitionIOCTL != 1<<30|16<<16|0xb8<<8 {
		t.Fatal("incorrect MSHV_CREATE_PARTITION request")
	}
}

func TestMSHVProbeClosesPartition(t *testing.T) {
	device, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	requireNoError(t, err)
	defer device.Close()
	partition, err := syscall.Dup(int(device.Fd()))
	requireNoError(t, err)
	t.Cleanup(func() {
		if partition >= 0 {
			syscall.Close(partition)
		}
	})
	err = probeMSHV(int(device.Fd()), func(fd int, args *mshvCreatePartition) (int, error) {
		if fd != int(device.Fd()) || args.flags != 7 || args.isolation != 0 {
			t.Fatalf("unexpected partition request: fd=%d args=%+v", fd, args)
		}
		return partition, nil
	})
	requireNoError(t, err)
	var stat syscall.Stat_t
	if err := syscall.Fstat(partition, &stat); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("partition descriptor still open: %v", err)
	}
	partition = -1
	requireNoError(t, syscall.Fstat(int(device.Fd()), &stat))
}

func TestMSHVProbePropagatesCreationFailure(t *testing.T) {
	for _, failure := range []syscall.Errno{syscall.ENOTTY, syscall.EPERM, syscall.EINVAL, syscall.ENOMEM} {
		t.Run(failure.Error(), func(t *testing.T) {
			err := probeMSHV(-1, func(int, *mshvCreatePartition) (int, error) {
				return -1, failure
			})
			if !errors.Is(err, failure) {
				t.Fatalf("creation failure was lost: %v", err)
			}
		})
	}
}

func TestMSHVProbePropagatesCloseFailure(t *testing.T) {
	err := probeMSHV(-1, func(int, *mshvCreatePartition) (int, error) { return -1, nil })
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("close failure was lost: %v", err)
	}
}

func TestMSHVRejectsNonHypervisor(t *testing.T) {
	if err := checkDevice("mshv", "/dev/null"); !errors.Is(err, syscall.ENOTTY) {
		t.Fatalf("non-hypervisor must fail MSHV creation: %v", err)
	}
}

func TestDeviceProbeClosesDevice(t *testing.T) {
	for _, failure := range []error{nil, syscall.EPERM} {
		fd := -1
		err := checkDeviceWithHostProbe("mshv", "/dev/null", "/dev/null", func(_ string, device int) error {
			fd = device
			return failure
		})
		if !errors.Is(err, failure) {
			t.Fatalf("unexpected probe result: %v", err)
		}
		var stat syscall.Stat_t
		if fd < 0 || !errors.Is(syscall.Fstat(fd, &stat), syscall.EBADF) {
			t.Fatal("device descriptor was not closed")
		}
	}
}

func TestMSHVLivePartitionCreation(t *testing.T) {
	if os.Getenv("TEST_MSHV_LIVE") != "1" {
		t.Skip("set TEST_MSHV_LIVE=1 on an MSHV host")
	}
	t.Setenv("TEST_PROBE_SUCCESS", "0")
	t.Setenv("TEST_PROBE_HANG", "0")
	requireNoError(t, (&deviceProbe{}).check(t.Context(), "mshv", "/dev/mshv"))
}
