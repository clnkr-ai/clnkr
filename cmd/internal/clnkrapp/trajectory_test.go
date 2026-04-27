package clnkrapp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestWriteTrajectoryWritesIndentedMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trajectory.json")
	messages := []clnkr.Message{{Role: "user", Content: "hello"}}

	if err := WriteTrajectory(path, messages); err != nil {
		t.Fatalf("WriteTrajectory: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte("\n  ")) {
		t.Fatalf("trajectory was not indented: %q", data)
	}

	var got []clnkr.Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, messages) {
		t.Fatalf("messages = %#v, want %#v", got, messages)
	}
}
