package performance

import "time"

// PeriodWindow freezes calendar labels and UTC instants for one assessment
// period. PeriodEnd is an inclusive local date; EndAt is exclusive. Generation
// covers [StartAt, CutoffAt), leaving [UncoveredStartAt, UncoveredEndAt) unassessed.
type PeriodWindow struct {
	Cadence          string    `json:"cadence"`
	Timezone         string    `json:"timezone"`
	PeriodStart      string    `json:"period_start"`
	PeriodEnd        string    `json:"period_end"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	TriggerAt        time.Time `json:"trigger_at"`
	CutoffAt         time.Time `json:"cutoff_at"`
	UncoveredStartAt time.Time `json:"uncovered_start_at"`
	UncoveredEndAt   time.Time `json:"uncovered_end_at"`
}

// periodWindow selects the natural calendar period containing anchor's instant
// in timezone. A missing local midnight is rejected rather than normalized to
// another date or time by time.Date.
func periodWindow(cadence, timezone string, anchor time.Time) (PeriodWindow, error) {
	var empty PeriodWindow
	if anchor.IsZero() || timezone == "" || timezone == "Local" {
		return empty, ErrInvalid
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return empty, ErrInvalid
	}
	local := anchor.In(loc)
	if local.Year() < 1 || local.Year() > 9999 {
		return empty, ErrInvalid
	}
	// UTC here represents date arithmetic only, avoiding normalization through
	// a daylight-saving transition before the midnight validation below.
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	var end time.Time
	switch cadence {
	case "WEEK":
		start = start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
		end = start.AddDate(0, 0, 7)
	case "MONTH":
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, 0)
	case "QUARTER":
		month := time.Month((int(start.Month())-1)/3*3 + 1)
		start = time.Date(start.Year(), month, 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 3, 0)
	case "YEAR":
		start = time.Date(start.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(1, 0, 0)
	default:
		return empty, ErrInvalid
	}
	last := end.AddDate(0, 0, -1)
	startAt, err := periodMidnight(start, loc)
	if err != nil {
		return empty, err
	}
	endAt, err := periodMidnight(end, loc)
	if err != nil {
		return empty, err
	}
	cutoff, err := periodMidnight(last, loc)
	if err != nil {
		return empty, err
	}
	return PeriodWindow{
		Cadence: cadence, Timezone: timezone,
		PeriodStart: start.Format(time.DateOnly), PeriodEnd: last.Format(time.DateOnly),
		StartAt: startAt, EndAt: endAt, TriggerAt: cutoff, CutoffAt: cutoff,
		UncoveredStartAt: cutoff, UncoveredEndAt: endAt,
	}, nil
}

func periodMidnight(date time.Time, loc *time.Location) (time.Time, error) {
	if date.Year() < 1 || date.Year() > 9999 {
		return time.Time{}, ErrInvalid
	}
	v := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	if v.Year() != date.Year() || v.Month() != date.Month() || v.Day() != date.Day() || v.Hour() != 0 || v.Minute() != 0 || v.Second() != 0 {
		return time.Time{}, ErrInvalid
	}
	return v.UTC(), nil
}

// nextPeriodWindow previews the first trigger strictly after now. At or after
// the current period's trigger, it selects the following calendar period.
func nextPeriodWindow(cadence, timezone string, now time.Time) (PeriodWindow, error) {
	window, err := periodWindow(cadence, timezone, now)
	if err != nil || window.TriggerAt.After(now) {
		return window, err
	}
	return periodWindow(cadence, timezone, window.EndAt)
}
