package main

import (
	"encoding/base64"
	"os"
	"unicode"
)

// state contains only literal protocol fields. All variable text goes through msg.
func writeProgramStatus(state, text string) {
	if !isTerminal(os.Stderr.Fd()) {
		return
	}
	runes := []rune(text)
	for i, r := range runes {
		if unicode.IsControl(r) {
			runes[i] = ' '
		}
	}
	// 512 runes fit the protocol's 2048-byte decoded message limit.
	text = base64.StdEncoding.EncodeToString([]byte(string(runes[:min(len(runes), 512)])))
	// One File.Write keeps concurrent terminal rendering out of the sequence.
	_, _ = os.Stderr.WriteString("\x1b]7501;state=" + state + ":app=clnkr:msg=" + text + "\x1b\\")
}
