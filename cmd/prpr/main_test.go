package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DragosMocrii/prpr/internal/github"
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
		got, err := parseFlags(tc.args, io.Discard, noEnv)
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
		{"--queues=bors"},
	} {
		if _, err := parseFlags(args, io.Discard, noEnv); err == nil {
			t.Errorf("parseFlags(%q) succeeded", args)
		}
	}
	if _, err := parseFlags([]string{"-h"}, io.Discard, noEnv); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h error = %v, want flag.ErrHelp", err)
	}
}

func TestParseFlagsBots(t *testing.T) {
	opts, err := parseFlags(nil, io.Discard, noEnv)
	if err != nil || len(opts.bots) != 3 {
		t.Fatalf("default bots = %+v, %v", opts.bots, err)
	}
	opts, err = parseFlags([]string{"--bots", "Rabbit=coderabbitai"}, io.Discard, noEnv)
	if err != nil || len(opts.bots) != 1 || opts.bots[0].Name != "Rabbit" {
		t.Fatalf("custom bots = %+v, %v", opts.bots, err)
	}
	opts, err = parseFlags([]string{"--bots="}, io.Discard, noEnv)
	if err != nil || opts.bots != nil {
		t.Fatalf("empty bots = %+v, %v", opts.bots, err)
	}
}

func TestParseFlagsQueues(t *testing.T) {
	opts, err := parseFlags(nil, io.Discard, noEnv)
	if err != nil || len(opts.queues) != 2 || opts.queues[0] != github.QueueTrunk || opts.queues[1] != github.QueueGitHub {
		t.Fatalf("default queues = %+v, %v", opts.queues, err)
	}
	opts, err = parseFlags([]string{"--queues="}, io.Discard, noEnv)
	if err != nil || opts.queues != nil {
		t.Fatalf("empty queues = %+v, %v", opts.queues, err)
	}
	if _, err := parseFlags([]string{"--queues=bors"}, io.Discard, noEnv); err == nil || !strings.Contains(err.Error(), "--queues") {
		t.Fatalf("bad queues error = %v", err)
	}
}

func TestParseFlagsVersion(t *testing.T) {
	if opts, err := parseFlags([]string{"--version"}, io.Discard, noEnv); err != nil || !opts.version {
		t.Fatalf("--version = %+v, %v", opts, err)
	}
	if opts, err := parseFlags(nil, io.Discard, noEnv); err != nil || opts.version {
		t.Fatalf("default = %+v, %v", opts, err)
	}
}

func noEnv(string) string { return "" }

func TestIconsFlagOverridesTheEnvironment(t *testing.T) {
	env := func(value string) func(string) string {
		return func(name string) string {
			if name == "PRPR_ICONS" {
				return value
			}
			return ""
		}
	}
	for _, tc := range []struct {
		args []string
		env  string
		want string
	}{
		{nil, "", ""},
		{nil, "nerd", "nerd"},
		{[]string{"--icons", "unicode"}, "nerd", "unicode"},
		{[]string{"--icons=nerd"}, "", "nerd"},
	} {
		opts, err := parseFlags(tc.args, io.Discard, env(tc.env))
		if err != nil || opts.icons != tc.want {
			t.Errorf("parseFlags(%q) with PRPR_ICONS=%q = %q, %v; want %q", tc.args, tc.env, opts.icons, err, tc.want)
		}
	}
	if _, err := parseFlags([]string{"--icons", "emoji"}, io.Discard, noEnv); err == nil {
		t.Error("an unknown --icons value was accepted")
	}
	if _, err := parseFlags(nil, io.Discard, env("fancy")); err == nil {
		t.Error("an unknown PRPR_ICONS value was accepted")
	}
}

func TestTitleIsOnUnlessTurnedOff(t *testing.T) {
	if opts, err := parseFlags(nil, io.Discard, noEnv); err != nil || !opts.title {
		t.Fatalf("default title = %t, %v", opts.title, err)
	}
	if opts, err := parseFlags([]string{"--title=false"}, io.Discard, noEnv); err != nil || opts.title {
		t.Fatalf("--title=false = %t, %v", opts.title, err)
	}
}
