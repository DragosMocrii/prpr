// Package schedule evaluates a weekly active-hours window in the caller's
// local time zone.
package schedule

import (
	"fmt"
	"strconv"
	"time"
)

// Config is a saved weekly polling window.
type Config struct {
	Enabled bool     `json:"enabled"`
	Days    []string `json:"days"`
	Start   string   `json:"start"`
	End     string   `json:"end"`
}

// Default returns a disabled weekday schedule. Opening an editor must not
// enable or persist it implicitly.
func Default() Config {
	return Config{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Start: "09:00", End: "18:00"}
}

type Window struct {
	enabled bool
	days    uint8
	start   int
	end     int
}

var weekdays = [...]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Compile validates a saved configuration and returns its immutable evaluator.
func Compile(config Config) (Window, error) {
	window := Window{enabled: config.Enabled}
	for _, day := range config.Days {
		index := -1
		for i, name := range weekdays {
			if day == name {
				index = i
				break
			}
		}
		if index < 0 {
			return Window{}, fmt.Errorf("invalid schedule weekday %q", day)
		}
		mask := uint8(1 << index)
		if window.days&mask != 0 {
			return Window{}, fmt.Errorf("duplicate schedule weekday %q", day)
		}
		window.days |= mask
	}
	if window.days == 0 {
		return Window{}, fmt.Errorf("schedule needs at least one weekday")
	}
	var err error
	if window.start, err = parseClock(config.Start); err != nil {
		return Window{}, fmt.Errorf("invalid schedule start: %w", err)
	}
	if window.end, err = parseClock(config.End); err != nil {
		return Window{}, fmt.Errorf("invalid schedule end: %w", err)
	}
	if window.start == window.end {
		return Window{}, fmt.Errorf("schedule start and end must differ")
	}
	return window, nil
}

func parseClock(value string) (int, error) {
	if len(value) != 5 || value[2] != ':' {
		return 0, fmt.Errorf("%q must use HH:mm", value)
	}
	for i, char := range value {
		if i != 2 && (char < '0' || char > '9') {
			return 0, fmt.Errorf("%q must use HH:mm", value)
		}
	}
	hour, _ := strconv.Atoi(value[:2])
	minute, _ := strconv.Atoi(value[3:])
	if hour > 23 || minute > 59 {
		return 0, fmt.Errorf("%q is outside 00:00–23:59", value)
	}
	return hour*60 + minute, nil
}

func (w Window) selected(day time.Weekday) bool { return w.days&(1<<uint(day)) != 0 }

// Active reports whether now falls inside the weekly window. Overnight hours
// belong to the weekday on which the window starts.
func (w Window) Active(now time.Time) bool {
	if !w.enabled {
		return true
	}
	minute := now.Hour()*60 + now.Minute()
	weekday := now.Weekday()
	if w.start < w.end {
		return w.selected(weekday) && minute >= w.start && minute < w.end
	}
	if minute >= w.start {
		return w.selected(weekday)
	}
	if minute < w.end {
		return w.selected((weekday + 6) % 7)
	}
	return false
}

// NextChange returns the earliest future instant where Active changes, or
// zero for a disabled window. Candidates are each day's start and end under
// each UTC offset in force that day, and each zone transition, solved
// directly so DST gaps and folds do not inherit time.Date's choice. They
// cover 16 days: a window inside a skipped hour can miss a whole week.
func (w Window) NextChange(now time.Time) time.Time {
	if !w.enabled {
		return time.Time{}
	}
	loc := now.Location()
	best := time.Time{}
	consider := func(t time.Time) {
		if t.After(now) && isChange(w, t) && (best.IsZero() || t.Before(best)) {
			best = t
		}
	}
	for day := 0; day <= 15; day++ {
		// date names the civil day; time.Date in loc may move a midnight
		// that a transition skips.
		date := time.Date(now.Year(), now.Month(), now.Day()+day, 0, 0, 0, 0, time.UTC)
		dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
		dayEnd := time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
		_, before := dayStart.Zone()
		_, after := dayEnd.Zone()
		for _, minute := range []int{w.start, w.end} {
			wall := date.Add(time.Duration(minute) * time.Minute)
			for _, offset := range []int{before, after} {
				back := wall.Add(-time.Duration(offset) * time.Second).In(loc)
				if back.Day() == date.Day() && back.Hour()*60+back.Minute() == minute {
					consider(back)
				}
			}
		}
		if _, transition := dayStart.ZoneBounds(); !transition.IsZero() && transition.Before(dayEnd) {
			consider(transition)
		}
	}
	return best
}

func isChange(w Window, candidate time.Time) bool {
	return w.Active(candidate) != w.Active(candidate.Add(-time.Nanosecond))
}
