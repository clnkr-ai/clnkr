package gitfeedback

import (
	"fmt"
	"strings"
	"testing"
)

func TestTruncateSummaryKeepsChangedFilesPathOnly(t *testing.T) {
	files := make([]string, 0, maxFiles+2)
	for i := 0; i < maxFiles+2; i++ {
		files = append(files, fmt.Sprintf("file-%02d.txt", i))
	}

	gotFiles, gotDiff := truncateSummary(files, "")
	if len(gotFiles) != maxFiles {
		t.Fatalf("len(gotFiles) = %d, want %d", len(gotFiles), maxFiles)
	}
	for _, path := range gotFiles {
		if strings.HasPrefix(path, "... (") {
			t.Fatalf("changed files should not contain truncation marker, got %q", path)
		}
	}
	if !strings.Contains(gotDiff, "2 more files omitted") {
		t.Fatalf("diff note = %q, want omitted-files marker", gotDiff)
	}
}

func TestTruncateSummaryKeepsOmittedFilesMarkerUnderDiffCap(t *testing.T) {
	files := make([]string, 0, maxFiles+3)
	for i := 0; i < maxFiles+3; i++ {
		files = append(files, fmt.Sprintf("file-%02d.txt", i))
	}
	diff := strings.Repeat("x", maxDiffSize)

	_, gotDiff := truncateSummary(files, diff)
	if !strings.Contains(gotDiff, "3 more files omitted") {
		t.Fatalf("diff note = %q, want omitted-files marker", gotDiff)
	}
	if !strings.Contains(gotDiff, diffTruncatedMarker) {
		t.Fatalf("diff note = %q, want diff truncation marker", gotDiff)
	}
	if len(gotDiff) > maxDiffSize {
		t.Fatalf("len(gotDiff) = %d, want <= %d", len(gotDiff), maxDiffSize)
	}
}
