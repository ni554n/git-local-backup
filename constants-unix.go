//go:build !windows

package main

import (
	"syscall"
)

const (
	ERROR_ACCESS_DENIED = syscall.EACCES
	ERROR_DIR_NOT_EMPTY = syscall.ENOTEMPTY
)
