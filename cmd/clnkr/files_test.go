package main

import (
	"testing"
)

func TestExtractFilesFromGitDiff(t *testing.T) {
	output := `diff --git a/foo.go b/foo.go
index 1234567..abcdefg 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
+package main
diff --git a/bar/baz.go b/bar/baz.go
index 1234567..abcdefg 100644`

	files := extractModifiedFiles("git diff", output)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != "foo.go" || files[1] != "bar/baz.go" {
		t.Errorf("expected [foo.go bar/baz.go], got %v", files)
	}
}

func TestExtractFilesFromTouch(t *testing.T) {
	files := extractModifiedFiles("touch newfile.txt", "")
	if len(files) != 1 || files[0] != "newfile.txt" {
		t.Errorf("expected [newfile.txt], got %v", files)
	}
}

func TestExtractFilesFromTee(t *testing.T) {
	files := extractModifiedFiles("echo hello | tee output.log", "")
	if len(files) != 1 || files[0] != "output.log" {
		t.Errorf("expected [output.log], got %v", files)
	}
}

func TestExtractFilesFromRedirect(t *testing.T) {
	files := extractModifiedFiles("echo hello > output.txt", "")
	if len(files) != 1 || files[0] != "output.txt" {
		t.Errorf("expected [output.txt], got %v", files)
	}
}

func TestExtractFilesFromSed(t *testing.T) {
	files := extractModifiedFiles("sed -i 's/foo/bar/g' config.yaml", "")
	if len(files) != 1 || files[0] != "config.yaml" {
		t.Errorf("expected [config.yaml], got %v", files)
	}
}

func TestExtractFilesFromCp(t *testing.T) {
	files := extractModifiedFiles("cp src.go dst.go", "")
	if len(files) != 1 || files[0] != "dst.go" {
		t.Errorf("expected [dst.go], got %v", files)
	}
}

func TestExtractFilesFromMv(t *testing.T) {
	files := extractModifiedFiles("mv old.go new.go", "")
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestExtractFilesNoMatch(t *testing.T) {
	files := extractModifiedFiles("ls -la", "file1\nfile2\n")
	if len(files) != 0 {
		t.Errorf("ls should not extract files, got %v", files)
	}
}

func TestFileTrackerDeduplicates(t *testing.T) {
	ft := &fileTracker{}
	ft.track("foo.go")
	ft.track("bar.go")
	ft.track("foo.go") // duplicate

	if len(ft.files) != 2 {
		t.Errorf("expected 2 unique files, got %d: %v", len(ft.files), ft.files)
	}
}

func TestFileTrackerFromCommandEvent(t *testing.T) {
	ft := &fileTracker{}
	ft.trackFromCommand("touch hello.txt", "")

	if len(ft.files) != 1 || ft.files[0] != "hello.txt" {
		t.Errorf("expected [hello.txt], got %v", ft.files)
	}
}
