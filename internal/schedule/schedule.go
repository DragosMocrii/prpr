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

// NextChange returns the earliest future instant where Active changes. Civil
// candidates are solved in each UTC-offset interval so DST gaps and folds do
// not inherit time.Date's arbitrary ambiguous/nonexistent-time choice.
func (w Window) NextChange(now time.Time) time.Time {
	if !w.enabled {
		return time.Time{}
	}
	// Search future civil candidates and zone boundaries until one changes the
	// active predicate; a non-empty weekly mask guarantees a transition soon.
	for daysAhead := 16; ; daysAhead += 16 {
		local := now.In(now.Location())
		lastDate := time.Date(local.Year(), local.Month(), local.Day()+daysAhead, 0, 0, 0, 0, local.Location())
		searchEnd := lastDate.Add(48 * time.Hour)
		best := time.Time{}
		for intervalStart := now; intervalStart.Before(searchEnd); {
			_, offset := intervalStart.Zone()
			_, intervalEnd := intervalStart.ZoneBounds()
			if intervalEnd.IsZero() || intervalEnd.After(searchEnd) {
				intervalEnd = searchEnd
			}
			if !intervalEnd.After(intervalStart) {
				break
			}
			if !intervalEnd.Equal(searchEnd) && isChange(w, intervalEnd) && (best.IsZero() || intervalEnd.Before(best)) {
				best = intervalEnd
			}
			for offsetDays := 0; offsetDays <= daysAhead; offsetDays++ {
				// Every day is a candidate: an overnight window ends on the day
				// after a selected one, and isChange rejects the rest.
				date := time.Date(local.Year(), local.Month(), local.Day()+offsetDays, 0, 0, 0, 0, local.Location())
				for _, minute := range []int{w.start, w.end} {
					wall := time.Date(date.Year(), date.Month(), date.Day(), minute/60, minute%60, 0, 0, time.UTC)
					candidate := wall.Add(-time.Duration(offset) * time.Second)
					if !candidate.After(now) || candidate.Before(intervalStart) || !candidate.Before(intervalEnd) {
						continue
					}
					back := candidate.In(local.Location())
					if back.Year() != date.Year() || back.Month() != date.Month() || back.Day() != date.Day() || back.Hour()*60+back.Minute() != minute || back.Second() != 0 {
						continue
					}
					if isChange(w, back) && (best.IsZero() || candidate.Before(best)) {
						best = candidate
					}
				}
			}
			if !best.IsZero() && intervalEnd.After(best) {
				break
			}
			if intervalEnd.Equal(searchEnd) {
				break
			}
			intervalStart = intervalEnd
		}
		if !best.IsZero() {
			return best
		}
	}
}

func isChange(w Window, candidate time.Time) bool {
	return w.Active(candidate) != w.Active(candidate.Add(-time.Nanosecond))
}
