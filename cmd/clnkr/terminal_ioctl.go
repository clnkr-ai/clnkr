//go:build linux || darwin

package main

import (
	"syscall"
	"unsafe"
)

func isTerminal(fd uintptr) bool {
	// TIOCGWINSZ only needs a writable winsize-shaped buffer to prove fd is a TTY.
	var winsize [4]uint16
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&winsize)))
	return errno == 0
}
