package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseFlagsVersion(t *testing.T) {
	opts, err := parseFlags([]string{"--version"}, io.Discard)
	if err != nil || !opts.version {
		t.Fatalf("parseFlags(--version) = %+v, %v", opts, err)
	}
	if _, err := parseFlags([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseFlags(-h) = %v, want flag.ErrHelp", err)
	}
	if opts, err := parseFlags(nil, io.Discard); err != nil || opts.version {
		t.Fatalf("parseFlags() = %+v, %v", opts, err)
	}
}

func TestRemovedFlagsNameTheirSetting(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		setting string
	}{
		{[]string{"--refresh", "10m"}, "Refresh every"},
		{[]string{"--refresh=0"}, "Refresh every"},
		{[]string{"-refresh", "1h"}, "Refresh every"},
		{[]string{"--bots", "A=a"}, "Review bots"},
		{[]string{"--bots="}, "Review bots"},
		{[]string{"--queues=trunk"}, "Merge queues"},
		{[]string{"--notify"}, "Desktop notifications"},
		{[]string{"-notify=true"}, "Desktop notifications"},
		{[]string{"--icons", "nerd"}, "Icons"},
		{[]string{"--title=false"}, "Terminal title"},
	} {
		_, err := parseFlags(tc.args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "moved to settings") || !strings.Contains(err.Error(), tc.setting) {
			t.Errorf("parseFlags(%q) = %v; want it moved to %s", tc.args, err, tc.setting)
		}
	}
}

func TestParseFlagsRejectsOtherArguments(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"--unknown"}} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Errorf("parseFlags(%q) succeeded", args)
		}
	}
}
