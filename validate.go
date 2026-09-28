// Package validate is a collage plugin for validating a form an action receives.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{validate.New(validate.Options{})},
//	})
//
// The action checks what was submitted, and refuses it by rendering the form's
// page again with status 422, or accepts it and redirects:
//
//	func signup(ctx context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
//		v := validate.Form(rc)
//		v.Field("email").Required().Email()
//		v.Field("password").Required().MinLen(8)
//		v.Field("confirm").Equal("password")
//		if !v.Valid() {
//			return validate.Refuse(rc, v, signupPage), nil
//		}
//		// ... create the account from v.Value("email") ...
//		return collage.SeeOther("/welcome"), nil
//	}
//
// and the form shows each field's message and what the reader typed:
//
//	<input name="email" value="{{fieldValue "email"}}">
//	{{with fieldError "email"}}<p class="error">{{.}}</p>{{end}}
//
// A form that starts from a record — a profile, an edit — names the stored value
// as a fallback: what was submitted when a submission was refused, the record
// when the form is shown for the first time.
//
//	<input name="fullname" value="{{fieldValue "fullname" .User.FullName}}">
//
// A password is never typed back into the form: a field whose name contains
// "password" is not re-filled.
//
// Field names are one space per render. A page with two forms sharing a field
// name — an add box and an edit box, both "body" — shows a refused field's
// message and value in both; give the fields names of their own.
package validate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Elagoht/collage/pkg/collage"
)

// Name is the plugin's name, and the key its configuration is found under.
const Name = "elagoht/validate"

// The rules a check reports under. They are the keys of Options.Messages.
const (
	RuleRequired = "required"
	RuleMinLen   = "minLen"
	RuleMaxLen   = "maxLen"
	RuleEmail    = "email"
	RuleURL      = "url"
	RuleInt      = "int"
	RuleRange    = "range"
	RuleOneOf    = "oneOf"
	RuleMatches  = "matches"
	RuleEqual    = "equal"
)

// defaultMessages are the English messages a check reports when nothing overrides
// them. A {name} in braces is replaced with the check's own value.
var defaultMessages = map[string]string{
	RuleRequired: "This field is required.",
	RuleMinLen:   "Must be at least {min} characters.",
	RuleMaxLen:   "Must be at most {max} characters.",
	RuleEmail:    "Must be a valid email address.",
	RuleURL:      "Must be a valid URL.",
	RuleInt:      "Must be a whole number.",
	RuleRange:    "Must be between {min} and {max}.",
	RuleOneOf:    "Must be one of: {options}.",
	RuleMatches:  "Is not in the expected format.",
	RuleEqual:    "Does not match.",
}

// Options configures the plugin.
type Options struct {
	// Messages replaces the English message of a rule, for every locale:
	// {"required": "Please fill this in."}. A message may name the check's value
	// in braces — {min}, {max}, {options}, {other} — which is replaced rather than
	// formatted with fmt, so a translation that leaves the value out, or puts it
	// elsewhere, is still correct. A key that is not a rule stops the application
	// from starting.
	Messages map[string]string `json:"messages"`
	// LocaleMessages replaces messages for one locale, the render's:
	// {"tr": {"required": "Bu alan zorunludur."}}. It is looked at before
	// Messages.
	LocaleMessages map[string]map[string]string `json:"localeMessages"`
	// NoRefill names the fields whose submitted value is never put back in the
	// form, matched case-insensitively as parts of the field's name. Unset, it is
	// ["password"]: a password typed back into the page would sit in its HTML,
	// in the browser's history and in any proxy that keeps it. Set, it replaces
	// that default, so a list of your own should say "password" too.
	NoRefill []string `json:"noRefill"`
}

// Keys the refused submission is kept under in the render's shared data.
const (
	errorsKey = "validate:errors"
	valuesKey = "validate:values"
)

// maxMemory is how much of a multipart form is held in memory rather than on disk,
// the amount http.Request.FormValue uses. The body itself is already bounded by
// collage, so this only decides where the parts of an allowed body go.
const maxMemory = 32 << 20

// Plugin carries the application's messages to every validator.
type Plugin struct {
	opts       Options
	noRefill   []string
	configured bool
}

var (
	_ collage.Plugin     = (*Plugin)(nil)
	_ collage.Configurer = (*Plugin)(nil)
)

// New returns a plugin with opts as its starting point, which the application's
// own configuration is then decoded over.
func New(opts Options) *Plugin { return &Plugin{opts: opts} }

func (p *Plugin) Name() string                   { return Name }
func (p *Plugin) Version() string                { return "0.1.2" }
func (p *Plugin) Shutdown(context.Context) error { return nil }

// Configure reads the configuration, refuses a message for a rule that does not
// exist, and adds {{fieldError}}, {{fieldValue}} and {{hasErrors}}.
func (p *Plugin) Configure(_ context.Context, host collage.ConfigHost) error {
	if err := host.Config(&p.opts); err != nil {
		return err
	}
	// A misspelt rule would leave the English message in place and the operator
	// certain it was translated.
	if err := knownRules(p.opts.Messages); err != nil {
		return fmt.Errorf("validate: messages: %w", err)
	}
	for locale, messages := range p.opts.LocaleMessages {
		if err := knownRules(messages); err != nil {
			return fmt.Errorf("validate: localeMessages %q: %w", locale, err)
		}
	}
	p.noRefill = []string{"password"}
	if p.opts.NoRefill != nil {
		p.noRefill = nil
		for _, name := range p.opts.NoRefill {
			if name = strings.ToLower(strings.TrimSpace(name)); name == "" {
				return errors.New("validate: noRefill: an empty name would match every field")
			}
			p.noRefill = append(p.noRefill, name)
		}
	}
	p.configured = true

	funcs := map[string]func(rc *collage.RenderContext) any{ // any: html/template.FuncMap's own value type
		"fieldError": func(rc *collage.RenderContext) any { // any: html/template.FuncMap's own value type
			return func(field string) string { return refused(rc, errorsKey)[field] }
		},
		"fieldValue": func(rc *collage.RenderContext) any { // any: html/template.FuncMap's own value type
			return func(field string, fallback ...string) string {
				if values := refused(rc, valuesKey); values != nil {
					return values[field]
				}
				// Not refused: the page is showing the form for the first time, and
				// the fallback is what an edit form starts with.
				if len(fallback) > 0 {
					return fallback[0]
				}
				return ""
			}
		},
		"hasErrors": func(rc *collage.RenderContext) any { // any: html/template.FuncMap's own value type
			return func() bool { return len(refused(rc, errorsKey)) > 0 }
		},
	}
	for name, factory := range funcs {
		if err := host.AddRenderFunc(name, factory); err != nil {
			return err
		}
	}
	return nil
}

// Init makes the application's messages reachable from Form, through every
// request's context.
func (p *Plugin) Init(_ context.Context, host collage.Host) error {
	if !p.configured {
		return errors.New("validate: register the plugin in Config.Plugins, where Configure runs; {{fieldError}} needs it")
	}
	return host.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pluginKey{}, p)))
		})
	})
}

type pluginKey struct{}

func knownRules(messages map[string]string) error {
	for rule := range messages {
		if _, ok := defaultMessages[rule]; !ok {
			return fmt.Errorf("unknown rule %q", rule)
		}
	}
	return nil
}

func refused(rc *collage.RenderContext, key string) map[string]string {
	if rc == nil {
		return nil
	}
	m, _ := collage.Get[map[string]string](rc, key)
	return m
}

// Validator checks one submitted form. Each field keeps the first message a check
// reported for it: a reader fixing "required" does not need "too short" as well.
type Validator struct {
	// request is the request whose form is read, and form what it held once
	// read; nil until a check or a value asks for it.
	request *http.Request
	form    url.Values
	plugin  *Plugin
	locale  string
	errors  map[string]string
}

// Form returns a validator over rc's request form — URL-encoded or multipart, the
// query included.
//
// The form is read when a check or Value first needs it, not here. An action that
// refuses a body before reading it — a photo whose Content-Length is over its
// size — builds its validator for Fail and Refuse, and the body is never read: a
// multipart one never spills its files to disk. Such a refusal has nothing to
// type back, so {{fieldValue}} shows its fallback, or "".
//
// A body that cannot be parsed leaves the fields empty, which the checks then
// refuse; a body too large for the action is answered by collage with 413.
func Form(rc *collage.RenderContext) *Validator {
	v := &Validator{errors: map[string]string{}}
	if rc == nil || rc.Request == nil {
		v.form = url.Values{}
		return v
	}
	v.request = rc.Request
	v.locale = rc.Locale
	v.plugin, _ = rc.Context().Value(pluginKey{}).(*Plugin)
	return v
}

// values returns the submitted form, reading it the first time it is asked for.
func (v *Validator) values() url.Values {
	if v.form != nil {
		return v.form
	}
	v.form = url.Values{}
	if r := v.request; r != nil {
		if err := r.ParseMultipartForm(maxMemory); errors.Is(err, http.ErrNotMultipart) {
			_ = r.ParseForm()
		}
		if r.Form != nil {
			v.form = r.Form
		}
	}
	return v.form
}

// Value is what was submitted for field: its first value, or "" when there was none.
func (v *Validator) Value(field string) string { return v.values().Get(field) }

// Valid reports whether every check passed.
func (v *Validator) Valid() bool { return len(v.errors) == 0 }

// Errors is each failed field's message, keyed by field name. The map is a copy.
func (v *Validator) Errors() map[string]string {
	out := make(map[string]string, len(v.errors))
	for field, message := range v.errors {
		out[field] = message
	}
	return out
}

// Fail reports message for field, unless a check already reported one. It is for
// what only the application can know — an address already registered, a coupon
// that expired — so it is shown the same way as a failed check.
func (v *Validator) Fail(field, message string) {
	if _, failed := v.errors[field]; !failed {
		v.errors[field] = message
	}
}

// Field starts the checks for one field. Each check returns the field again, so
// they chain:
//
//	v.Field("age").Required().Range(18, 130)
//
// Every check but Required and Equal passes an empty value, so an optional field
// is only checked when it was filled in.
func (v *Validator) Field(name string) *Field {
	return &Field{v: v, name: name, value: v.Value(name)}
}

// message is what a failed rule says: the application's message for the render's
// locale, its message for every locale, or the English default, with the check's
// values put in.
func (v *Validator) message(rule string, params []string) string {
	text := defaultMessages[rule]
	if p := v.plugin; p != nil {
		if m, ok := p.opts.Messages[rule]; ok {
			text = m
		}
		if m, ok := p.opts.LocaleMessages[v.locale][rule]; ok {
			text = m
		}
	}
	if len(params) == 0 {
		return text
	}
	return strings.NewReplacer(params...).Replace(text)
}

func (v *Validator) refill() map[string]string {
	noRefill := []string{"password"}
	if v.plugin != nil {
		noRefill = v.plugin.noRefill
	}
	// A form nothing read is not read now: what refused it was decided without
	// it, and reading it here would undo that.
	values := make(map[string]string, len(v.form))
	for field := range v.form {
		lower := strings.ToLower(field)
		secret := false
		for _, part := range noRefill {
			if strings.Contains(lower, part) {
				secret = true
				break
			}
		}
		if !secret {
			values[field] = v.form.Get(field)
		}
	}
	return values
}

// Field is one field's checks.
type Field struct {
	v     *Validator
	name  string
	value string
	// last is whether the check just made reported the field's message, which is
	// the only message Message may replace.
	last bool
}

// Message replaces the message of the check just before it, when that check
// failed:
//
//	v.Field("name").Required().Message("Tell us what to call you.")
//
// It is how one form says something more specific than the rule's usual message,
// or passes a translation of its own — i18n.T(rc, "signup.name.required").
func (f *Field) Message(text string) *Field {
	if f.last {
		f.v.errors[f.name] = text
	}
	return f
}

// check records rule's message when ok is false. It is skipped when the field has
// already failed, and, unless always is set, when the field is empty. params are
// placeholder and value pairs for the message: "{min}", "8".
func (f *Field) check(rule string, always bool, ok func() bool, params ...string) *Field {
	f.last = false
	if _, failed := f.v.errors[f.name]; failed {
		return f
	}
	if !always && f.value == "" {
		return f
	}
	if !ok() {
		f.v.errors[f.name] = f.v.message(rule, params)
		f.last = true
	}
	return f
}

// Required fails an empty field, and one holding only white space.
func (f *Field) Required() *Field {
	return f.check(RuleRequired, true, func() bool { return strings.TrimSpace(f.value) != "" })
}

// MinLen fails a value shorter than n characters. Characters, not bytes: "şişe" is
// four.
func (f *Field) MinLen(n int) *Field {
	return f.check(RuleMinLen, false, func() bool { return utf8.RuneCountInString(f.value) >= n },
		"{min}", strconv.Itoa(n))
}

// MaxLen fails a value longer than n characters.
func (f *Field) MaxLen(n int) *Field {
	return f.check(RuleMaxLen, false, func() bool { return utf8.RuneCountInString(f.value) <= n },
		"{max}", strconv.Itoa(n))
}

// Email fails anything but a bare address: "ada@example.com", not
// "Ada <ada@example.com>".
func (f *Field) Email() *Field {
	return f.check(RuleEmail, false, func() bool {
		addr, err := mail.ParseAddress(f.value)
		return err == nil && addr.Name == "" && addr.Address == f.value
	})
}

// URL fails anything but an absolute http or https URL with a host. Other schemes
// are refused on purpose: a "javascript:" link typed into a profile is how a
// page ends up running a stranger's script.
func (f *Field) URL() *Field {
	return f.check(RuleURL, false, func() bool {
		u, err := url.Parse(f.value)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	})
}

// Int fails a value that is not a whole number.
func (f *Field) Int() *Field {
	return f.check(RuleInt, false, func() bool {
		_, err := strconv.Atoi(f.value)
		return err == nil
	})
}

// Range fails a value that is not a whole number from min to max, both included.
// A value that is not a number at all fails as Int does, which says what is
// actually wrong with it.
func (f *Field) Range(min, max int) *Field {
	f.Int()
	if f.last {
		return f
	}
	return f.check(RuleRange, false, func() bool {
		n, _ := strconv.Atoi(f.value)
		return n >= min && n <= max
	}, "{min}", strconv.Itoa(min), "{max}", strconv.Itoa(max))
}

// OneOf fails a value that is not one of options. A select or a set of radio
// buttons offers a fixed list, and nothing stops a request from sending another
// value.
func (f *Field) OneOf(options ...string) *Field {
	return f.check(RuleOneOf, false, func() bool {
		for _, option := range options {
			if f.value == option {
				return true
			}
		}
		return false
	}, "{options}", strings.Join(options, ", "))
}

// Matches fails a value re does not match. Anchor the expression — ^[a-z0-9-]+$ —
// or a value containing a match anywhere passes.
func (f *Field) Matches(re *regexp.Regexp) *Field {
	return f.check(RuleMatches, false, func() bool { return re.MatchString(f.value) })
}

// Equal fails a value that differs from other field's: a password and its
// confirmation. It is checked when empty too, so an empty confirmation of a
// filled-in password fails.
func (f *Field) Equal(other string) *Field {
	return f.check(RuleEqual, true, func() bool { return f.value == f.v.Value(other) }, "{other}", other)
}

// Custom runs check on the value, and fails the field with the message it returns;
// "" passes.
//
//	v.Field("username").Custom(func(s string) string {
//		if strings.HasPrefix(s, "admin") {
//			return "That name is reserved."
//		}
//		return ""
//	})
func (f *Field) Custom(check func(value string) string) *Field {
	f.last = false
	if _, failed := f.v.errors[f.name]; failed || f.value == "" {
		return f
	}
	if message := check(f.value); message != "" {
		f.v.errors[f.name] = message
		f.last = true
	}
	return f
}

// Refuse answers a refused submission: page — the form's own page, the value that
// was registered — rendered again with status 422, the request understood and not
// acted on. The render shows each field's message with {{fieldError}} and what was
// submitted with {{fieldValue}}, a password excepted.
//
//	return validate.Refuse(rc, v, signupPage), nil
func Refuse(rc *collage.RenderContext, v *Validator, page *collage.Page) *collage.ActionResult {
	if rc != nil && v != nil {
		rc.Set(errorsKey, v.Errors())
		rc.Set(valuesKey, v.refill())
	}
	result := collage.RenderPage(page)
	result.Status = http.StatusUnprocessableEntity
	return result
}
