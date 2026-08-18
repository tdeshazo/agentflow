package clioutput

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWriteJSONFormattingModes(t *testing.T) {
	value := struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}{Name: "agentflow", Active: true}

	tests := []struct {
		name   string
		tty    bool
		pretty bool
	}{
		{name: "terminal is indented", tty: true, pretty: true},
		{name: "redirected is compact", tty: false, pretty: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := WriteJSONWithTTY(&output, value, test.tty); err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(output.String(), "\n") {
				t.Fatalf("output does not end with newline: %q", output.String())
			}
			body := strings.TrimSuffix(output.String(), "\n")
			if strings.Contains(body, "\n") != test.pretty {
				t.Fatalf("pretty=%v output = %q", test.pretty, output.String())
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(body), &decoded); err != nil {
				t.Fatalf("output is not valid JSON: %q: %v", output.String(), err)
			}
		})
	}
}

func TestWriteJSONFormattingModesDecodeEquivalently(t *testing.T) {
	value := map[string]any{
		"schema_version": 1,
		"workflows": []any{
			map[string]any{"workflow": "alpha", "complete": false},
		},
	}

	var pretty, compact bytes.Buffer
	if err := WriteJSONWithTTY(&pretty, value, true); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONWithTTY(&compact, value, false); err != nil {
		t.Fatal(err)
	}
	var prettyValue, compactValue any
	if err := json.Unmarshal(pretty.Bytes(), &prettyValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(compact.Bytes(), &compactValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prettyValue, compactValue) {
		t.Fatalf("pretty and compact values differ: pretty=%v compact=%v", prettyValue, compactValue)
	}
}

func TestIsTTYTreatsUnknownWriterAsNonTTY(t *testing.T) {
	if IsTTY(&bytes.Buffer{}) {
		t.Fatal("bytes.Buffer was detected as a TTY")
	}
}
