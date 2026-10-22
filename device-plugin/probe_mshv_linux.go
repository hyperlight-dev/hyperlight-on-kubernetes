// Copyright 2026 The Hyperlight Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Linux UAPI: _IOW(0xB8, 0x00, struct mshv_create_partition).
const mshvCreatePartitionIOCTL = 0x4010b800

const (
	mshvPartitionLAPIC = 1 << iota
	mshvPartitionX2APIC
	mshvPartitionGPASuperPages
)

// The v1 ABI leaves CPU/XSAVE feature selection to the kernel.
type mshvCreatePartition struct {
	flags     uint64
	isolation uint64
}

func createMSHVPartition(fd int, args *mshvCreatePartition) (int, error) {
	partition, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), mshvCreatePartitionIOCTL, uintptr(unsafe.Pointer(args)))
	if errno != 0 {
		return -1, errno
	}
	return int(partition), nil
}

func probeMSHV(fd int, create func(int, *mshvCreatePartition) (int, error)) error {
	args := mshvCreatePartition{flags: mshvPartitionLAPIC | mshvPartitionX2APIC | mshvPartitionGPASuperPages}
	partition, err := create(fd, &args)
	if err != nil {
		return fmt.Errorf("MSHV_CREATE_PARTITION: %w", err)
	}
	if err := syscall.Close(partition); err != nil {
		return fmt.Errorf("close MSHV partition: %w", err)
	}
	return nil
}
