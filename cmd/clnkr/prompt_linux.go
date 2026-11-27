package main

import "syscall"

const getTerm, setTerm = syscall.TCGETS, syscall.TCSETS

func selectStdin(set *syscall.FdSet, timeout *syscall.Timeval) error {
	_, err := syscall.Select(1, set, nil, nil, timeout)
	return err
}
