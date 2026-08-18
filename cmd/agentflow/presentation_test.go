package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusAllTextTTYStylingPreservesNoColorText(t *testing.T) {
	repo := newCLIStatusRepo(t)

	var plain bytes.Buffer
	if err := runAllStatusTo(repo.Root, false, &plain, false, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain status contains ANSI: %q", plain.String())
	}

	var styled bytes.Buffer
	if err := runAllStatusTo(repo.Root, false, &styled, true, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled.String(), "\x1b[") || !strings.Contains(styled.String(), "repository:") {
		t.Fatalf("styled status = %q", styled.String())
	}

	var noColor bytes.Buffer
	if err := runAllStatusTo(repo.Root, false, &noColor, true, false); err != nil {
		t.Fatal(err)
	}
	if got := noColor.String(); got != plain.String() {
		t.Fatalf("TTY no-color status changed text contract:\nplain=%q\nno-color=%q", plain.String(), got)
	}
}

func TestStatusAllJSONNeverContainsANSI(t *testing.T) {
	repo := newCLIStatusRepo(t)
	var out bytes.Buffer
	if err := runAllStatusTo(repo.Root, true, &out, true, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("JSON output contains ANSI: %q", out.String())
	}
	if !strings.Contains(out.String(), "\n  \"") {
		t.Fatalf("TTY JSON was not indented: %q", out.String())
	}
}
