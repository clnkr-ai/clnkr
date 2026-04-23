package main

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestStrippedSearchMatchesUsePlainOffsetsForANSIContent(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true).Render("hello") + " world"

	matches := strippedSearchMatches(styled, "world")
	if len(matches) != 1 {
		t.Fatalf("match count = %d, want 1", len(matches))
	}
	if got, want := matches[0][0], 6; got != want {
		t.Fatalf("match start = %d, want %d", got, want)
	}
	if got, want := matches[0][1], 11; got != want {
		t.Fatalf("match end = %d, want %d", got, want)
	}
}

func TestStrippedSearchMatchesReturnNilForEmptyQuery(t *testing.T) {
	if matches := strippedSearchMatches("hello world", ""); matches != nil {
		t.Fatalf("matches = %#v, want nil", matches)
	}
}
