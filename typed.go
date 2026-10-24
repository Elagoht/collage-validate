package validate

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DateLayout is how a date is written: what <input type="date"> sends.
const DateLayout = "2006-01-02"

// IntField is a field checked to hold a whole number. Its checks pass an empty
// value, as a Field's do, and Value is the number once the form is valid.
type IntField struct {
	f *Field
	n int
}

// Message replaces the message of the check just before it, when it failed.
func (i *IntField) Message(text string) *IntField {
	i.f.Message(text)
	return i
}

// Min fails a number below min.
func (i *IntField) Min(min int, message ...string) *IntField {
	i.f.check(RuleMin, false, message, func() bool { return i.n >= min }, "{min}", strconv.Itoa(min))
	return i
}

// Max fails a number above max.
func (i *IntField) Max(max int, message ...string) *IntField {
	i.f.check(RuleMax, false, message, func() bool { return i.n <= max }, "{max}", strconv.Itoa(max))
	return i
}

// Range fails a number outside min to max, both included.
func (i *IntField) Range(min, max int, message ...string) *IntField {
	i.f.check(RuleRange, false, message, func() bool { return i.n >= min && i.n <= max },
		"{min}", strconv.Itoa(min), "{max}", strconv.Itoa(max))
	return i
}

// Custom runs check on the number, and fails the field with the message it
// returns; "" passes.
func (i *IntField) Custom(check func(n int) string) *IntField {
	i.f.Custom(func(string) string { return check(i.n) })
	return i
}

// Value is the number: 0 when the field was left empty or failed a check.
func (i *IntField) Value() int {
	if i.f.failed() {
		return 0
	}
	return i.n
}

// Ptr is the number, or nil when the field was left empty or failed a check: an
// optional field's "not given".
func (i *IntField) Ptr() *int {
	if i.f.failed() || i.f.value == "" {
		return nil
	}
	n := i.n
	return &n
}

// Float fails a value that is not a number, whole or with a decimal point —
// "2.5", as <input type="number"> sends it; "2,5" fails — and turns the field
// into a FloatField. Infinity and NaN fail too.
func (f *Field) Float(message ...string) *FloatField {
	ff := &FloatField{f: f}
	f.check(RuleFloat, false, message, func() bool {
		x, err := strconv.ParseFloat(strings.TrimSpace(f.value), 64)
		if err != nil || math.IsInf(x, 0) || math.IsNaN(x) {
			return false
		}
		ff.x = x
		return true
	})
	return ff
}

// FloatField is a field checked to hold a number.
type FloatField struct {
	f *Field
	x float64
}

// Message replaces the message of the check just before it, when it failed.
func (fl *FloatField) Message(text string) *FloatField {
	fl.f.Message(text)
	return fl
}

// Min fails a number below min.
func (fl *FloatField) Min(min float64, message ...string) *FloatField {
	fl.f.check(RuleMin, false, message, func() bool { return fl.x >= min }, "{min}", formatFloat(min))
	return fl
}

// Max fails a number above max.
func (fl *FloatField) Max(max float64, message ...string) *FloatField {
	fl.f.check(RuleMax, false, message, func() bool { return fl.x <= max }, "{max}", formatFloat(max))
	return fl
}

// Range fails a number outside min to max, both included.
func (fl *FloatField) Range(min, max float64, message ...string) *FloatField {
	fl.f.check(RuleRange, false, message, func() bool { return fl.x >= min && fl.x <= max },
		"{min}", formatFloat(min), "{max}", formatFloat(max))
	return fl
}

// Custom runs check on the number, and fails the field with the message it
// returns; "" passes.
func (fl *FloatField) Custom(check func(x float64) string) *FloatField {
	fl.f.Custom(func(string) string { return check(fl.x) })
	return fl
}

// Value is the number: 0 when the field was left empty or failed a check.
func (fl *FloatField) Value() float64 {
	if fl.f.failed() {
		return 0
	}
	return fl.x
}

// Ptr is the number, or nil when the field was left empty or failed a check.
func (fl *FloatField) Ptr() *float64 {
	if fl.f.failed() || fl.f.value == "" {
		return nil
	}
	x := fl.x
	return &x
}

func formatFloat(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

// Bool reads a checkbox, and turns the field into a BoolField. An unchecked box
// sends nothing, which is false; "on" — what a box without a value attribute
// sends — "true", "1" and "yes" are true, "off", "false", "0" and "no" false,
// and anything else fails. Required before it makes the box one that must be
// ticked: v.Field("terms").Required("Accept the terms to go on.").Bool().
func (f *Field) Bool(message ...string) *BoolField {
	b := &BoolField{f: f}
	f.check(RuleBool, false, message, func() bool {
		switch strings.ToLower(strings.TrimSpace(f.value)) {
		case "on", "true", "1", "yes":
			b.on = true
			return true
		case "off", "false", "0", "no":
			return true
		}
		return false
	})
	return b
}

// BoolField is a checkbox.
type BoolField struct {
	f  *Field
	on bool
}

// Message replaces the message of the check just before it, when it failed.
func (b *BoolField) Message(text string) *BoolField {
	b.f.Message(text)
	return b
}

// Value is whether the box was ticked: false when it was not, or the field
// failed a check.
func (b *BoolField) Value() bool { return b.on && !b.f.failed() }

// Date fails a value that is not a date written as <input type="date"> sends
// it, "2026-10-25", and turns the field into a DateField. The date is midnight
// UTC of that day.
func (f *Field) Date(message ...string) *DateField {
	d := &DateField{f: f}
	f.check(RuleDate, false, message, func() bool {
		t, err := time.Parse(DateLayout, strings.TrimSpace(f.value))
		d.t = t
		return err == nil
	})
	return d
}

// DateField is a field checked to hold a date.
type DateField struct {
	f *Field
	t time.Time
}

// Message replaces the message of the check just before it, when it failed.
func (d *DateField) Message(text string) *DateField {
	d.f.Message(text)
	return d
}

// Min fails a date before min's day; min's time of day is ignored.
func (d *DateField) Min(min time.Time, message ...string) *DateField {
	day := min.Format(DateLayout)
	d.f.check(RuleMinDate, false, message, func() bool { return d.t.Format(DateLayout) >= day }, "{min}", day)
	return d
}

// Max fails a date after max's day; max's time of day is ignored.
func (d *DateField) Max(max time.Time, message ...string) *DateField {
	day := max.Format(DateLayout)
	d.f.check(RuleMaxDate, false, message, func() bool { return d.t.Format(DateLayout) <= day }, "{max}", day)
	return d
}

// Custom runs check on the date, and fails the field with the message it
// returns; "" passes.
func (d *DateField) Custom(check func(t time.Time) string) *DateField {
	d.f.Custom(func(string) string { return check(d.t) })
	return d
}

// Value is the date: the zero time when the field was left empty or failed a
// check.
func (d *DateField) Value() time.Time {
	if d.f.failed() {
		return time.Time{}
	}
	return d.t
}

// Ptr is the date, or nil when the field was left empty or failed a check.
func (d *DateField) Ptr() *time.Time {
	if d.f.failed() || d.f.value == "" {
		return nil
	}
	t := d.t
	return &t
}

// All turns the field into a ListField of every value sent under its name — a
// group of checkboxes, a <select multiple> — white space around each trimmed,
// empty ones dropped:
//
//	tags := v.Field("tags").All().Required().OneOf(allowed...)
func (f *Field) All() *ListField {
	return f.list(f.v.values()[f.name])
}

// Lines turns the field into a ListField of its value's lines — a <textarea>
// with one ingredient per line — white space around each trimmed, empty ones
// dropped.
func (f *Field) Lines() *ListField {
	return f.list(strings.Split(f.value, "\n"))
}

func (f *Field) list(values []string) *ListField {
	l := &ListField{f: f}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			l.items = append(l.items, value)
		}
	}
	return l
}

// ListField is a field holding a list of values. Its checks other than Required
// pass an empty list, as a Field's do.
type ListField struct {
	f     *Field
	items []string
}

// Message replaces the message of the check just before it, when it failed.
func (l *ListField) Message(text string) *ListField {
	l.f.Message(text)
	return l
}

// Required fails an empty list.
func (l *ListField) Required(message ...string) *ListField {
	l.f.checkIf(false, RuleRequired, message, func() bool { return len(l.items) > 0 })
	return l
}

// MinItems fails a list of fewer than n items.
func (l *ListField) MinItems(n int, message ...string) *ListField {
	l.f.checkIf(len(l.items) == 0, RuleMinItems, message, func() bool { return len(l.items) >= n },
		"{min}", strconv.Itoa(n))
	return l
}

// MaxItems fails a list of more than n items.
func (l *ListField) MaxItems(n int, message ...string) *ListField {
	l.f.checkIf(len(l.items) == 0, RuleMaxItems, message, func() bool { return len(l.items) <= n },
		"{max}", strconv.Itoa(n))
	return l
}

// MaxLen fails a list with an item longer than n characters.
func (l *ListField) MaxLen(n int, message ...string) *ListField {
	l.f.checkIf(len(l.items) == 0, RuleMaxLen, message, func() bool {
		for _, item := range l.items {
			if utf8.RuneCountInString(item) > n {
				return false
			}
		}
		return true
	}, "{max}", strconv.Itoa(n))
	return l
}

// OneOf fails a list with an item that is not one of options. Like a Field's,
// it takes no message of its own: follow it with Message.
func (l *ListField) OneOf(options ...string) *ListField {
	l.f.checkIf(len(l.items) == 0, RuleOneOf, nil, func() bool {
		for _, item := range l.items {
			found := false
			for _, option := range options {
				if item == option {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}, "{options}", strings.Join(options, ", "))
	return l
}

// Custom runs check on the items, and fails the field with the message it
// returns; "" passes.
func (l *ListField) Custom(check func(items []string) string) *ListField {
	l.f.last = false
	if l.f.failed() || len(l.items) == 0 {
		return l
	}
	if message := check(l.items); message != "" {
		l.f.v.errors[l.f.name] = message
		l.f.last = true
	}
	return l
}

// Value is the items: nil when there were none or the field failed a check.
func (l *ListField) Value() []string {
	if l.f.failed() {
		return nil
	}
	return l.items
}
