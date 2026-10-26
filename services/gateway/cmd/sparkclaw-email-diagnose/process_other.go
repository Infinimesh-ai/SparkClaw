//go:build !darwin && !linux

package main

import "os/exec"

func ownProcessGroup(*exec.Cmd) bool { return false }
func stopProcessGroup(*exec.Cmd)     {}
