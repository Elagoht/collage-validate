# elagoht/validate

A collage plugin for validating a form an action receives: chainable checks per
field, a refused submission rendered again with status 422, and template
functions that put each field's message, and what the reader typed, back in the
form.

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{validate.New(validate.Options{})},
})
```

Requires collage v0.23.0 or later. Register it in `Config.Plugins`: it adds
template functions, which only a plugin registered there can.

## Refuse or accept

collage's rule for a form is that a refused submission renders the page and an
accepted one redirects. This plugin is the first half:

```go
var signupPage *collage.Page

func signup(ctx context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
	v := validate.Form(rc)
	v.Field("email").Required().Email()
	v.Field("password").Required().MinLen(8)
	v.Field("confirm").Equal("password").Message("The passwords differ.")
	if v.Valid() && accounts.Exists(ctx, v.Value("email")) {
		v.Fail("email", "That address is already registered.")
	}
	if !v.Valid() {
		return validate.Refuse(rc, v, signupPage), nil
	}
	// ... create the account ...
	return collage.SeeOther("/welcome"), nil
}
```

`validate.Form` parses the request's form — URL-encoded or multipart — and
`Refuse` answers with the form's page rendered again, `422 Unprocessable Entity`:
the request was understood and not acted on. `signupPage` is the value you
registered, as for any page an action answers with.

The form reads what the refusal left:

```html
<form method="post">
  {{csrfToken}}
  {{if hasErrors}}<p role="alert">Some fields need another look.</p>{{end}}

  <input name="email" type="email" value="{{fieldValue "email"}}">
  {{with fieldError "email"}}<p class="error">{{.}}</p>{{end}}

  <input name="password" type="password">
  {{with fieldError "password"}}<p class="error">{{.}}</p>{{end}}
</form>
```

| Function | Returns |
| --- | --- |
| `{{fieldError "name"}}` | The field's message, or `""` |
| `{{fieldValue "name"}}` | What was submitted for the field, or `""` |
| `{{fieldValue "name" .Current}}` | The same, or `.Current` when the form is shown for the first time — the value an edit form starts with. A refused submission shows what was typed, not the stored value |
| `{{hasErrors}}` | Whether any field failed |

On a page nobody submitted they are empty, so one template serves the first
render and the refused one — and a page shown for the first time is the same for
every reader, so it stays cacheable. An action's answer is never cached.

## Checks

| Check | Fails | Rule |
| --- | --- | --- |
| `Required()` | an empty value, or only white space | `required` |
| `MinLen(n)`, `MaxLen(n)` | fewer or more than n characters — characters, not bytes | `minLen`, `maxLen` |
| `Email()` | anything but a bare address: `ada@example.com`, not `Ada <ada@example.com>` | `email` |
| `URL()` | anything but an absolute `http` or `https` URL with a host | `url` |
| `Int()` | a value that is not a whole number | `int` |
| `Range(min, max)` | a whole number outside min–max, both included; not a number fails as `Int` | `range` |
| `OneOf(options...)` | a value not in the list | `oneOf` |
| `Matches(re)` | a value `re` does not match — anchor it | `matches` |
| `Equal(other)` | a value that differs from the field `other` | `equal` |
| `Custom(fn)` | when `fn(value)` returns a message; `""` passes | — |

A field keeps the **first** message a check reported: a reader fixing "required"
does not need "too short" as well, so later checks on a failed field do not run.
Every check but `Required` and `Equal` passes an empty value — an optional field
is checked only when it was filled in, and an empty confirmation of a filled-in
password still fails. Values are checked as they arrived; nothing is trimmed.

`v.Fail(field, message)` reports what only the application can know, shown the same
way; `v.Errors()` is every message by field, `v.Valid()` whether there are none,
and `v.Value(field)` what was submitted.

## Messages

The defaults are English:

| Rule | Message |
| --- | --- |
| `required` | This field is required. |
| `minLen` | Must be at least {min} characters. |
| `maxLen` | Must be at most {max} characters. |
| `email` | Must be a valid email address. |
| `url` | Must be a valid URL. |
| `int` | Must be a whole number. |
| `range` | Must be between {min} and {max}. |
| `oneOf` | Must be one of: {options}. |
| `matches` | Is not in the expected format. |
| `equal` | Does not match. ({other} names the other field) |

Three places override them, the first that has one winning:

1. `.Message("…")` after a check replaces that check's message, when it failed —
   for one form, or for a translation of your own:
   `.Required().Message(i18n.T(rc, "signup.email.required"))`.
2. `LocaleMessages` — per locale, chosen by the render's locale, so collage-i18n
   sites can keep every language's messages in configuration.
3. `Messages` — for every locale.

A value goes in braces, `{min}`, and is replaced rather than formatted with `fmt`:
a translation that leaves it out, or needs it elsewhere in the sentence, stays
correct. A message for a rule that does not exist stops the application from
being built, so a misspelt rule is not an English message quietly left in place.

## Passwords are not typed back

A value typed back into the form sits in the page's HTML — in the browser's
history, in anything that keeps the response. A field whose name contains
`password`, in any case, is never re-filled. `NoRefill` replaces that list with
your own, matched the same way; include `password` in it if you still have one.

## Configuration

```go
validate.New(validate.Options{
	Messages: map[string]string{"required": "Please fill this in."},
	LocaleMessages: map[string]map[string]string{
		"tr": {"required": "Bu alan zorunludur.", "minLen": "En az {min} karakter olmalı."},
	},
	NoRefill: []string{"password", "card"},
})
```

```json
{
  "elagoht/validate": {
    "messages": { "required": "Please fill this in." },
    "localeMessages": { "tr": { "required": "Bu alan zorunludur." } },
    "noRefill": ["password", "card"]
  }
}
```

## Limitations

- Field names are one space per render. A page with two forms sharing a field
  name — an add box and an edit box, both `body` — shows a refused field's message
  and value under both; give each form's fields names of their own.
- A field is one value. `Value`, the checks and `fieldValue` read the first one,
  so a group of checkboxes sharing a name is not validated or re-checked as a set;
  read `rc.Request.Form[name]` for that.
- `Refuse` answers with a page. A form that replaces only itself, as collage-live's
  can, sets the same errors and answers with a fragment by building the result
  itself: `res := validate.Refuse(rc, v, nil); res.Page, res.Fragment = nil, f`.
- An uploaded file is not checked: the checks are about text fields.
- Validation in the browser — `required`, `type="email"` — is still worth adding
  for the reader's sake; this is the check that cannot be skipped.

## Changes

### v0.1.2

- `Form` reads the form when a check or `Value` first needs it, not when the
  validator is built. An action refusing a body before reading it — a photo whose
  `Content-Length` is over its size — can build a validator for `Fail` and
  `Refuse`, and the body is never read: a multipart one no longer spills its files
  to disk first. Nothing is typed back for such a refusal.
- Built against collage v0.34.2.
