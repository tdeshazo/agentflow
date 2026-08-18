package engine

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusToTTYStylesHumanOutputWithoutChangingPlainText(t *testing.T) {
	repo := newSelfHostingRepo(t)
	e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), &selfHostingFakeProvider{})

	var plain bytes.Buffer
	if err := e.StatusTo(&plain, false, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain status contains ANSI: %q", plain.String())
	}
	if !strings.Contains(plain.String(), "state: uninitialized") {
		t.Fatalf("plain status = %q", plain.String())
	}

	var styled bytes.Buffer
	if err := e.StatusTo(&styled, true, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled.String(), "\x1b[") || !strings.Contains(styled.String(), "uninitialized") {
		t.Fatalf("styled status = %q", styled.String())
	}

	var noColor bytes.Buffer
	if err := e.StatusTo(&noColor, true, false); err != nil {
		t.Fatal(err)
	}
	if got := noColor.String(); got != plain.String() {
		t.Fatalf("TTY no-color status changed text contract:\nplain=%q\nno-color=%q", plain.String(), got)
	}
}
