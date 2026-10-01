package main

import (
	"errors"
	"flag"
	"io"
	"testing"
	"time"
)

func TestParseFlagsRefreshInterval(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want time.Duration
	}{
		{nil, 5 * time.Minute},
		{[]string{"--refresh", "0"}, 0},
		{[]string{"--refresh=30s"}, 30 * time.Second},
		{[]string{"-refresh", "1h"}, time.Hour},
	} {
		got, err := parseFlags(tc.args, io.Discard)
		if err != nil || got.refresh != tc.want {
			t.Errorf("parseFlags(%q) = %v, %v; want %v", tc.args, got.refresh, err, tc.want)
		}
	}
}

func TestParseFlagsRejectsInvalidRefresh(t *testing.T) {
	for _, args := range [][]string{
		{"--refresh", "29s"},
		{"--refresh", "-1m"},
		{"--refresh", "soon"},
		{"--refresh", "5"},
		{"extra"},
		{"--bots", "Claude"},
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Errorf("parseFlags(%q) succeeded", args)
		}
	}
	if _, err := parseFlags([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h error = %v, want flag.ErrHelp", err)
	}
}

func TestParseFlagsBots(t *testing.T) {
	opts, err := parseFlags(nil, io.Discard)
	if err != nil || len(opts.bots) != 3 {
		t.Fatalf("default bots = %+v, %v", opts.bots, err)
	}
	opts, err = parseFlags([]string{"--bots", "Rabbit=coderabbitai"}, io.Discard)
	if err != nil || len(opts.bots) != 1 || opts.bots[0].Name != "Rabbit" {
		t.Fatalf("custom bots = %+v, %v", opts.bots, err)
	}
	opts, err = parseFlags([]string{"--bots="}, io.Discard)
	if err != nil || opts.bots != nil {
		t.Fatalf("empty bots = %+v, %v", opts.bots, err)
	}
}

func TestParseFlagsVersion(t *testing.T) {
	if opts, err := parseFlags([]string{"--version"}, io.Discard); err != nil || !opts.version {
		t.Fatalf("--version = %+v, %v", opts, err)
	}
	if opts, err := parseFlags(nil, io.Discard); err != nil || opts.version {
		t.Fatalf("default = %+v, %v", opts, err)
	}
}
