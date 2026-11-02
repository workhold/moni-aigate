package productvalue

import "time"

const utc6 = "2006-01-02T15:04:05.000000Z"

// Term is a mathematical half-open month window, without purchase authority.
type Term struct {
	StartsAt string
	EndsAt   string
}

type TermState string

const (
	Scheduled TermState = "scheduled"
	Active    TermState = "active"
	Expired   TermState = "expired"
)

func parseUTC6(s string) (time.Time, bool) {
	if len(s) != 27 {
		return time.Time{}, false
	}
	t, err := time.Parse(utc6, s)
	if err != nil || t.Year() < 1 || t.Year() > 9999 || t.Format(utc6) != s {
		return time.Time{}, false
	}
	return t, true
}

// MonthAt advances from the original anchor, retaining its day when possible.
// monthOffset is a nonnegative canonical decimal mathematical offset.
func MonthAt(anchorUTC6, monthOffset string) (string, error) {
	anchor, ok := parseUTC6(anchorUTC6)
	if !ok {
		return "", ErrInvalidTime
	}
	offset, ok := decimal(monthOffset)
	if !ok {
		return "", ErrInvalidValue
	}
	t, err := monthAt(anchor, offset)
	if err != nil {
		return "", err
	}
	return t.Format(utc6), nil
}

func monthAt(anchor time.Time, offset int64) (time.Time, error) {
	const maxMonthIndex = 9999*12 - 1
	base := int64((anchor.Year()-1)*12 + int(anchor.Month()) - 1)
	if offset > maxMonthIndex-base {
		return time.Time{}, ErrMonthOverflow
	}
	index := base + offset
	year, month := int(index/12)+1, time.Month(index%12)+1
	day := min(anchor.Day(), daysInMonth(year, month))
	return time.Date(year, month, day, anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond(), time.UTC), nil
}

func daysInMonth(year int, month time.Month) int {
	switch month {
	case time.April, time.June, time.September, time.November:
		return 30
	case time.February:
		if year%400 == 0 || (year%4 == 0 && year%100 != 0) {
			return 29
		}
		return 28
	default:
		return 31
	}
}

// TermAt returns consecutive boundaries calculated from the same original anchor.
// The zero-based monthOffset does not define any persisted business term index.
func TermAt(anchorUTC6, monthOffset string) (Term, error) {
	anchor, ok := parseUTC6(anchorUTC6)
	if !ok {
		return Term{}, ErrInvalidTime
	}
	offset, ok := decimal(monthOffset)
	if !ok {
		return Term{}, ErrInvalidValue
	}
	start, err := monthAt(anchor, offset)
	if err != nil {
		return Term{}, err
	}
	// start has already bounded offset below 120000, so offset+1 cannot overflow.
	end, err := monthAt(anchor, offset+1)
	if err != nil {
		return Term{}, err
	}
	return Term{StartsAt: start.Format(utc6), EndsAt: end.Format(utc6)}, nil
}

// TermStateAt classifies an explicit as-of instant using [start,end) semantics.
// No supplied time is authenticated as a database clock or an actual paid term.
func TermStateAt(anchorUTC6, monthOffset, asOfUTC6 string) (TermState, error) {
	term, err := TermAt(anchorUTC6, monthOffset)
	if err != nil {
		return "", err
	}
	asOf, ok := parseUTC6(asOfUTC6)
	if !ok {
		return "", ErrInvalidTime
	}
	start, _ := parseUTC6(term.StartsAt)
	end, _ := parseUTC6(term.EndsAt)
	if asOf.Before(start) {
		return Scheduled, nil
	}
	if asOf.Before(end) {
		return Active, nil
	}
	return Expired, nil
}
