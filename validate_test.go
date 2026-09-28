package validate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	validate "github.com/Elagoht/collage-validate"
	"github.com/Elagoht/collage/pkg/collage"
)

const form = `<form method="post">{{csrfToken}}` +
	`<input name="email" value="{{fieldValue "email"}}">{{with fieldError "email"}}<p class="e-email">{{.}}</p>{{end}}` +
	`<input name="password" value="{{fieldValue "password"}}">{{with fieldError "password"}}<p class="e-password">{{.}}</p>{{end}}` +
	`<input name="name" value="{{fieldValue "name" "Ada"}}">` +
	`{{if hasErrors}}<p class="summary">Fix the fields below.</p>{{end}}</form>`

// app is a sign-up page whose own action validates it, and a /check action that
// runs checks and answers with their messages as JSON.
func app(t *testing.T, opts validate.Options, checks func(v *validate.Validator)) http.Handler {
	t.Helper()
	a, err := collage.New(config(opts))
	if err != nil {
		t.Fatal(err)
	}
	return register(t, a, checks)
}

func config(opts validate.Options) *collage.Config {
	return &collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Security: collage.SecurityConfig{CSRFKey: bytes.Repeat([]byte("k"), 32)},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/form.html": {Data: []byte(form)}}, Root: "t"},
		Locale:   collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}},
		Plugins:  []collage.Plugin{validate.New(opts)},
	}
}

func register(t *testing.T, a *collage.App, checks func(v *validate.Validator)) http.Handler {
	t.Helper()
	var signup *collage.Page
	signup = collage.NewPage("signup").
		WithContent(collage.NewFragment("form", "form.html").Build()).
		WithPath("en", "/signup").
		WithAction(http.MethodPost, func(_ context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
			v := validate.Form(rc)
			v.Field("email").Required().Email()
			v.Field("password").Required().MinLen(8)
			if !v.Valid() {
				return validate.Refuse(rc, v, signup), nil
			}
			return collage.SeeOther("/welcome"), nil
		}).Build()
	if err := a.RegisterPage(signup); err != nil {
		t.Fatal(err)
	}
	check := collage.NewAction("check").WithPath("en", "/check").WithPath("tr", "/check").
		WithMethods(http.MethodPost).WithoutCSRF().
		WithHandler(func(_ context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
			v := validate.Form(rc)
			if checks != nil {
				checks(v)
			}
			return collage.JSONOf(http.StatusOK, v.Errors())
		}).Build()
	if err := a.RegisterAction(check); err != nil {
		t.Fatal(err)
	}
	return a.Handler()
}

var tokenRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// submit posts values to /signup the way a browser would: with the forgery cookie
// and token the page it came from carried.
func submit(t *testing.T, h http.Handler, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/signup", nil))
	m := tokenRe.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatalf("no token in the form:\n%s", page.Body.String())
	}
	values.Set("_csrf", m[1])
	r := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range page.Result().Cookies() {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func check(t *testing.T, h http.Handler, path string, values url.Values) map[string]string {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var errs map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errs); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return errs
}

// A refused submission is the form again, 422, with each field's message and what
// was typed — except the password.
func TestRefusedRendersTheFormAgain(t *testing.T) {
	h := app(t, validate.Options{}, nil)
	rec := submit(t, h, url.Values{"email": {`ada@<example>`}, "password": {"hunter2"}, "name": {"Grace"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`value="ada@&lt;example&gt;"`, // typed back, escaped
		`<p class="e-email">Must be a valid email address.</p>`,
		`<p class="e-password">Must be at least 8 characters.</p>`,
		`<input name="name" value="Grace">`, // what was sent, not the fallback
		`<p class="summary">`,
		`name="_csrf"`, // the form works for the next attempt
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s\n%s", want, body)
		}
	}
	if strings.Contains(body, "hunter2") {
		t.Errorf("the password was typed back into the page:\n%s", body)
	}
}

// An accepted submission redirects.
func TestAcceptedRedirects(t *testing.T) {
	h := app(t, validate.Options{}, nil)
	rec := submit(t, h, url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/welcome" {
		t.Errorf("%d %q, want 303 /welcome", rec.Code, rec.Header().Get("Location"))
	}
}

// The form shown for the first time has no messages, and its fields their
// fallbacks.
func TestFirstRender(t *testing.T) {
	h := app(t, validate.Options{}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signup", nil))
	body := rec.Body.String()
	if strings.Contains(body, `class="e-`) || strings.Contains(body, "summary") {
		t.Errorf("messages on a fresh form:\n%s", body)
	}
	if !strings.Contains(body, `<input name="email" value="">`) || !strings.Contains(body, `<input name="name" value="Ada">`) {
		t.Errorf("fields not at their fallbacks:\n%s", body)
	}
}

func TestChecks(t *testing.T) {
	h := app(t, validate.Options{}, func(v *validate.Validator) {
		v.Field("required").Required()
		v.Field("blank").Required()
		v.Field("optional").MinLen(3).Email() // empty: not checked
		v.Field("short").MinLen(5)
		v.Field("runes").MinLen(4).MaxLen(4) // "şişe" is four characters, eight bytes
		v.Field("long").MaxLen(3)
		v.Field("email").Email()
		v.Field("named").Email()
		v.Field("url").URL()
		v.Field("js").URL()
		v.Field("int").Int()
		v.Field("range").Range(1, 10)
		v.Field("rangeNaN").Range(1, 10)
		v.Field("inRange").Range(1, 10)
		v.Field("oneOf").OneOf("red", "green")
		v.Field("slug").Matches(regexp.MustCompile(`^[a-z-]+$`))
		v.Field("confirm").Equal("password")
		v.Field("custom").Custom(func(s string) string {
			if s == "admin" {
				return "That name is reserved."
			}
			return ""
		})
		v.Field("first").Required().MinLen(10).Email() // only the first message
		v.Fail("email", "overridden?")                 // a check's message stands
		v.Fail("taken", "Already registered.")
	})
	got := check(t, h, "/check", url.Values{
		"blank": {"   "}, "short": {"abc"}, "runes": {"şişe"}, "long": {"abcd"},
		"email": {"nope"}, "named": {"Ada <ada@example.com>"},
		"url": {"example.com"}, "js": {"javascript:alert(1)"},
		"int": {"4.5"}, "range": {"11"}, "rangeNaN": {"x"}, "inRange": {"10"},
		"oneOf": {"blue"}, "slug": {"Not A Slug"},
		"password": {"secret"}, "confirm": {""}, "custom": {"admin"}, "first": {"abc"},
	})
	want := map[string]string{
		"required": "This field is required.",
		"blank":    "This field is required.",
		"short":    "Must be at least 5 characters.",
		"long":     "Must be at most 3 characters.",
		"email":    "Must be a valid email address.",
		"named":    "Must be a valid email address.",
		"url":      "Must be a valid URL.",
		"js":       "Must be a valid URL.",
		"int":      "Must be a whole number.",
		"range":    "Must be between 1 and 10.",
		"rangeNaN": "Must be a whole number.",
		"oneOf":    "Must be one of: red, green.",
		"slug":     "Is not in the expected format.",
		"confirm":  "Does not match.",
		"custom":   "That name is reserved.",
		"first":    "Must be at least 10 characters.",
		"taken":    "Already registered.",
	}
	for field, message := range want {
		if got[field] != message {
			t.Errorf("%s: %q, want %q", field, got[field], message)
		}
	}
	for field := range got {
		if _, ok := want[field]; !ok {
			t.Errorf("%s failed: %q", field, got[field])
		}
	}
}

// A form's own message, the application's, the locale's, the default — in that
// order.
func TestMessages(t *testing.T) {
	opts := validate.Options{
		Messages:       map[string]string{"required": "Fill it in.", "minLen": "{min} or more, please."},
		LocaleMessages: map[string]map[string]string{"tr": {"required": "Bu alan zorunludur.", "minLen": "En az {min} karakter."}},
	}
	checks := func(v *validate.Validator) {
		v.Field("a").Required()
		v.Field("b").MinLen(3)
		v.Field("c").Required().Message("Tell us your name.").MinLen(2).Message("never")
		v.Field("d").Email()
		v.Field("e").Required().Message("not this one") // passes, so nothing replaced
	}
	values := url.Values{"b": {"x"}, "d": {"x"}, "e": {"filled"}}

	en := check(t, app(t, opts, checks), "/check", values)
	if en["a"] != "Fill it in." || en["b"] != "3 or more, please." || en["c"] != "Tell us your name." ||
		en["d"] != "Must be a valid email address." || en["e"] != "" {
		t.Errorf("en: %v", en)
	}
	tr := check(t, app(t, opts, checks), "/tr/check", values)
	if tr["a"] != "Bu alan zorunludur." || tr["b"] != "En az 3 karakter." || tr["c"] != "Tell us your name." ||
		tr["d"] != "Must be a valid email address." {
		t.Errorf("tr: %v", tr)
	}
}

// A message for a rule that does not exist stops the application from being built.
func TestMisconfigurationRefused(t *testing.T) {
	for _, opts := range []validate.Options{
		{Messages: map[string]string{"requird": "x"}},
		{LocaleMessages: map[string]map[string]string{"tr": {"emial": "x"}}},
		{NoRefill: []string{" "}},
	} {
		if _, err := collage.New(config(opts)); err == nil || !strings.Contains(err.Error(), "validate:") {
			t.Errorf("%+v: New = %v", opts, err)
		}
	}
	// So does a misspelt rule in the application's configuration.
	cfg := config(validate.Options{})
	cfg.PluginConfig = map[string]json.RawMessage{validate.Name: []byte(`{"messages": {"minlen": "x"}}`)}
	if _, err := collage.New(cfg); err == nil {
		t.Error("a misspelt rule in the configuration was accepted")
	}
}

// Registered too late for its template functions, the plugin stops the
// application from starting.
func TestRegisteredLate(t *testing.T) {
	cfg := config(validate.Options{})
	cfg.Plugins = nil
	cfg.Template.FS = fstest.MapFS{"t/form.html": {Data: []byte(`<form></form>`)}}
	a, err := collage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterPlugin(validate.New(validate.Options{})); err == nil {
		t.Error("RegisterPlugin accepted a plugin whose Configure cannot run")
	}
}

// Configuration overlays the options it is given.
func TestConfiguration(t *testing.T) {
	cfg := config(validate.Options{Messages: map[string]string{"required": "from Go"}})
	cfg.PluginConfig = map[string]json.RawMessage{validate.Name: []byte(`{"messages": {"required": "from JSON"}}`)}
	a, err := collage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := register(t, a, func(v *validate.Validator) { v.Field("x").Required() })
	if got := check(t, h, "/check", url.Values{}); got["x"] != "from JSON" {
		t.Errorf("configured message: %q", got["x"])
	}
}

// NoRefill replaces the default list.
func TestNoRefill(t *testing.T) {
	h := app(t, validate.Options{NoRefill: []string{"EMAIL"}}, nil)
	body := submit(t, h, url.Values{"email": {"ada@example.com"}, "password": {"short"}}).Body.String()
	if !strings.Contains(body, `<input name="email" value="">`) || !strings.Contains(body, `<input name="password" value="short">`) {
		t.Errorf("NoRefill not applied:\n%s", body)
	}
}

// A multipart form is read like a URL-encoded one.
func TestMultipart(t *testing.T) {
	h := app(t, validate.Options{}, func(v *validate.Validator) {
		v.Field("title").Required()
		v.Field("count").Int()
	})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", "Hello")
	_ = mw.WriteField("count", "many")
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/check", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if got := rec.Body.String(); got != `{"count":"Must be a whole number."}` {
		t.Errorf("multipart: %s", got)
	}
}

// Outside a request, a validator sees an empty form, and Refuse still answers.
func TestOutsideARequest(t *testing.T) {
	v := validate.Form(nil)
	v.Field("x").Required()
	if v.Valid() || v.Value("x") != "" {
		t.Errorf("valid = %v", v.Valid())
	}
	if res := validate.Refuse(nil, v, nil); res.Status != http.StatusUnprocessableEntity {
		t.Errorf("status %d", res.Status)
	}
}

// untouched is a request body that records whether anything read it.
type untouched struct {
	strings.Reader
	read bool
}

func (b *untouched) Read(p []byte) (int, error) {
	b.read = true
	return b.Reader.Read(p)
}

// Form reads nothing until a check or a value needs the form. An action that
// refuses a body before parsing it — a photo over its size, told by
// Content-Length — builds a validator for Fail and Refuse without the body being
// read, and a multipart one without its files spilling to disk.
func TestFormParsesOnlyWhenAFieldIsRead(t *testing.T) {
	a, err := collage.New(config(validate.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	upload := collage.NewAction("upload").WithPath("en", "/upload").WithMethods(http.MethodPost).
		WithoutCSRF().WithMaxBodyBytes(-1).
		WithHandler(func(_ context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
			v := validate.Form(rc)
			if rc.Request.ContentLength > 16 {
				v.Fail("photo", "The photo is too large.")
				res := validate.Refuse(rc, v, nil)
				res.Page, res.Body, res.ContentType = nil, []byte(v.Errors()["photo"]), "text/plain"
				return res, nil
			}
			v.Field("title").Required()
			return collage.JSONOf(http.StatusOK, v.Errors())
		}).Build()
	if err := a.RegisterAction(upload); err != nil {
		t.Fatal(err)
	}
	h := a.Handler()

	sent := "title=" + strings.Repeat("x", 64)
	body := &untouched{Reader: *strings.NewReader(sent)}
	r := httptest.NewRequest(http.MethodPost, "/upload", body)
	r.ContentLength = int64(len(sent)) // what a client declares; httptest cannot know it for this reader
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnprocessableEntity || rec.Body.String() != "The photo is too large." {
		t.Fatalf("refused early: %d %q", rec.Code, rec.Body.String())
	}
	if body.read {
		t.Error("the body was read by a validator no check asked of")
	}

	// A check reads the form, as before.
	if errs := check(t, app(t, validate.Options{}, func(v *validate.Validator) { v.Field("title").Required() }),
		"/check", url.Values{"title": {"Hello"}}); len(errs) != 0 {
		t.Errorf("a filled-in field failed: %v", errs)
	}
}
