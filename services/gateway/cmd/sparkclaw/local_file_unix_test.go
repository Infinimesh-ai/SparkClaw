//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
)

type differentOwnerInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (info differentOwnerInfo) Sys() any { return &info.stat }
func TestLocalManagementRejectsDifferentFileOwner(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid++
	if localFileOwnedByCurrentUser(differentOwnerInfo{FileInfo: info, stat: stat}) {
		t.Fatal("different owner accepted")
	}
	if !localFileOwnedByCurrentUser(info) {
		t.Fatal("deployment user ownership rejected")
	}
}
