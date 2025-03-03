//go:build windows

package main

import (
	"syscall"
)

const (
	ERROR_ACCESS_DENIED = syscall.ERROR_ACCESS_DENIED
	ERROR_DIR_NOT_EMPTY = syscall.ERROR_DIR_NOT_EMPTY
)
