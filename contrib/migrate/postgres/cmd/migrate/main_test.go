package main

import "testing"

func TestParseArgsAcceptsFlagsAfterCommand(t *testing.T) {
	cmd, opts, err := parseArgs([]string{"-dir", "db/migrations", "create", "-name", "init"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "create" || opts.dir != "db/migrations" || opts.name != "init" {
		t.Fatalf("cmd=%q opts=%+v", cmd, opts)
	}
}

func TestParseArgsAcceptsFlagsBeforeCommand(t *testing.T) {
	cmd, opts, err := parseArgs([]string{"-steps", "2", "down"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "down" || opts.steps != 2 {
		t.Fatalf("cmd=%q opts=%+v", cmd, opts)
	}
}

func TestParseArgsRequiresCommand(t *testing.T) {
	if _, _, err := parseArgs([]string{"-dir", "migrations"}); err == nil {
		t.Fatal("expected missing command error")
	}
}

func TestParseArgsDoesNotTreatFlagValueAsCommand(t *testing.T) {
	cmd, opts, err := parseArgs([]string{"create", "-name", "up"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "create" || opts.name != "up" {
		t.Fatalf("cmd=%q opts=%+v", cmd, opts)
	}
}
