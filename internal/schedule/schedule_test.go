package schedule

import (
	"testing"
	"time"
)

func compile(t *testing.T, config Config) Window {
	t.Helper()
	window, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	return window
}

func TestActiveBoundariesAndOvernightWeekdayOwnership(t *testing.T) {
	weekday := compile(t, Config{Enabled: true, Days: []string{"mon"}, Start: "09:00", End: "17:00"})
	loc := time.FixedZone("local", 0)
	for _, test := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before", time.Date(2026, 6, 1, 8, 59, 59, 0, loc), false},
		{"inclusive start", time.Date(2026, 6, 1, 9, 0, 0, 0, loc), true},
		{"inside", time.Date(2026, 6, 1, 16, 59, 59, 0, loc), true},
		{"exclusive end", time.Date(2026, 6, 1, 17, 0, 0, 0, loc), false},
		{"weekend", time.Date(2026, 6, 6, 10, 0, 0, 0, loc), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := weekday.Active(test.at); got != test.want {
				t.Fatalf("Active(%s) = %v, want %v", test.at, got, test.want)
			}
		})
	}

	overnight := compile(t, Config{Enabled: true, Days: []string{"fri"}, Start: "22:00", End: "06:00"})
	if !overnight.Active(time.Date(2026, 6, 5, 23, 0, 0, 0, loc)) || !overnight.Active(time.Date(2026, 6, 6, 5, 59, 0, 0, loc)) {
		t.Fatal("Friday overnight window did not include Friday night and Saturday before 06:00")
	}
	if overnight.Active(time.Date(2026, 6, 6, 6, 0, 0, 0, loc)) || overnight.Active(time.Date(2026, 6, 6, 23, 0, 0, 0, loc)) {
		t.Fatal("Friday overnight window leaked past Saturday 06:00")
	}
}

func TestCompileRejectsInvalidRetainedFields(t *testing.T) {
	base := Config{Days: []string{"mon"}, Start: "09:00", End: "18:00"}
	for _, test := range []struct {
		name   string
		change func(*Config)
	}{
		{"empty days", func(c *Config) { c.Days = nil }},
		{"unknown day", func(c *Config) { c.Days = []string{"Monday"} }},
		{"duplicate day", func(c *Config) { c.Days = []string{"mon", "mon"} }},
		{"bad start", func(c *Config) { c.Start = "9:00" }},
		{"bad end", func(c *Config) { c.End = "24:00" }},
		{"equal endpoints", func(c *Config) { c.End = c.Start }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.change(&config)
			if _, err := Compile(config); err == nil {
				t.Fatal("Compile accepted invalid schedule")
			}
		})
	}
	if got := Default(); got.Enabled || got.Start != "09:00" || got.End != "18:00" || len(got.Days) != 5 {
		t.Fatalf("Default() = %+v", got)
	}
	if !compile(t, Config{Days: []string{"mon"}, Start: "09:00", End: "18:00"}).Active(time.Now()) {
		t.Fatal("disabled schedule is not always active")
	}
}

func TestNextChangeAtCivilBoundaries(t *testing.T) {
	loc := time.FixedZone("local", 0)
	window := compile(t, Config{Enabled: true, Days: []string{"mon"}, Start: "09:00", End: "18:00"})
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)
	if got, want := window.NextChange(at), time.Date(2026, 6, 1, 18, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("next closing boundary = %s, want %s", got, want)
	}
	if got, want := window.NextChange(time.Date(2026, 6, 1, 18, 0, 0, 0, loc)), time.Date(2026, 6, 8, 9, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("next opening boundary = %s, want %s", got, want)
	}
	if !(Window{}).NextChange(at).IsZero() {
		t.Fatal("disabled schedule has a next boundary")
	}
}

func TestNextChangeEndsOvernightWindowOnUnselectedDay(t *testing.T) {
	loc := time.FixedZone("local", 0)
	window := compile(t, Config{Enabled: true, Days: []string{"fri"}, Start: "22:00", End: "02:00"})
	// 2026-06-05 is a Friday; the window ends early on Saturday.
	if got, want := window.NextChange(time.Date(2026, 6, 5, 23, 0, 0, 0, loc)), time.Date(2026, 6, 6, 2, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("overnight end = %s, want %s", got, want)
	}
}

func TestNextChangeAcrossNewYorkDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	window := compile(t, Config{Enabled: true, Days: []string{"sun"}, Start: "02:15", End: "02:45"})
	beforeSpring := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	if window.Active(beforeSpring) {
		t.Fatal("spring-forward window active before its start")
	}
	if got, want := window.NextChange(beforeSpring), time.Date(2026, 3, 15, 2, 15, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("next change after skipped spring window = %s, want %s", got, want)
	}

	fold := compile(t, Config{Enabled: true, Days: []string{"sun"}, Start: "01:15", End: "01:45"})
	first := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	if got, want := fold.NextChange(first), time.Date(2026, 11, 1, 1, 15, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("first repeated-hour opening = %s, want %s", got, want)
	}
	firstEnd := time.Date(2026, 11, 1, 1, 45, 0, 0, loc)
	if got, want := fold.NextChange(firstEnd), time.Date(2026, 11, 1, 1, 15, 0, 0, time.FixedZone("EST", -5*60*60)); !got.Equal(want) {
		t.Fatalf("second repeated-hour opening = %s, want %s", got, want)
	}
	secondEnd := firstEnd.Add(time.Hour)
	if got, want := fold.NextChange(secondEnd), time.Date(2026, 11, 8, 1, 15, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("next weekly opening after repeated hour = %s, want %s", got, want)
	}
}

func TestBusinessWindowUsesLocalTimeAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	window := compile(t, Config{Enabled: true, Days: []string{"mon", "tue", "wed", "thu", "fri"}, Start: "09:00", End: "18:00"})
	for _, day := range []time.Time{time.Date(2026, 3, 6, 9, 0, 0, 0, loc), time.Date(2026, 3, 9, 9, 0, 0, 0, loc)} {
		if !window.Active(day) || window.Active(day.Add(9*time.Hour)) {
			t.Fatalf("local business window wrong around DST at %s", day)
		}
	}
}
