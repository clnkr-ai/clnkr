package main

import "syscall"

const getTerm, setTerm = syscall.TIOCGETA, syscall.TIOCSETA

func selectStdin(set *syscall.FdSet, timeout *syscall.Timeval) error {
	return syscall.Select(1, set, nil, nil, timeout)
}
