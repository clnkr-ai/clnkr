//go:build !linux && !darwin

package main

import "os"

func interactiveInput(_ string, _ bool) promptReader { return newLineReader(os.Stdin) }
