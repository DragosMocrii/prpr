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
		if err != nil || got != tc.want {
			t.Errorf("parseFlags(%q) = %v, %v; want %v", tc.args, got, err, tc.want)
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
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Errorf("parseFlags(%q) succeeded", args)
		}
	}
	if _, err := parseFlags([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h error = %v, want flag.ErrHelp", err)
	}
}
