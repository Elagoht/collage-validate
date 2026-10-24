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

Requires collage v0.61.0 or later. Register it in `Config.Plugins`: it adds
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
| `{{fieldValue "name" .Current}}` | The same, or `.Current` when there is nothing submitted to type back — the form shown for the first time, or refused before its body was read. A refused submission shows what was typed, not the stored value — an empty or blank value too |
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
| `Int()` | a value that is not a whole number; the field becomes an `IntField` | `int` |
| `Float()` | a value that is not a number (`2.5`; `2,5` fails); the field becomes a `FloatField` | `float` |
| `Bool()` | a checkbox value other than `on`, `true`, `1`, `yes`, `off`, `false`, `0`, `no`; the field becomes a `BoolField` | `bool` |
| `Date()` | a value that is not a date as `<input type="date">` sends it, `2026-10-25`; the field becomes a `DateField` | `date` |
| `All()`, `Lines()` | — every value under the name, or the value's lines, become a `ListField` | — |
| `Range(min, max)` | a whole number outside min–max, both included; not a number fails as `Int` | `range` |
| `OneOf(options...)` | a value not in the list | `oneOf` |
| `Matches(re)` | a value `re` does not match — anchor it | `matches` |
| `Equal(other)` | a value that differs from the field `other` | `equal` |
| `Custom(fn)` | when `fn(value)` returns a message; `""` passes | — |

A field keeps the **first** message a check reported: a reader fixing "required"
does not need "too short" as well, so later checks on a failed field do not run.
Every check but `Required` and `Equal` passes an empty value — an optional field
is checked only when it was filled in, and an empty confirmation of a filled-in
password still fails. A string check sees the value as it arrived; the typed
ones, below, read it with the white space around it trimmed.

Every check takes an optional message, said instead of the rule's when it fails:
`Required("Name the recipe.")`, `Int("Whole servings only.")`, `MinLen(8,
"Longer, please.")`. It is `.Message("…")` after the check, written once.
`OneOf` alone takes none — its arguments are the options — so follow it with
`Message`.

## Typed values

A field gives back what was checked. `Value()` on a `Field` is its string, white
space trimmed; `Int`, `Float`, `Bool`, `Date`, `All` and `Lines` turn it into a
typed field whose `Value()` is the type, and whose own checks work on it:

```go
func addRecipe(ctx context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
	v := validate.Form(rc)
	title := v.Field("title").Required("Name the recipe.").MaxLen(80)
	servings := v.Field("servings").Required().Int().Range(1, 50)
	minutes := v.Field("minutes").Int().Min(1)              // optional
	vegan := v.Field("vegan").Bool()                        // a checkbox
	tags := v.Field("tags").All().Required().OneOf(tagNames...)
	steps := v.Field("steps").Required().Lines().MaxItems(40) // one per line
	if !v.Valid() {
		return validate.Refuse(rc, v, rc.Page), nil
	}
	id, err := recipes.Create(ctx, recipes.Input{
		Title:    title.Value(),    // string
		Servings: servings.Value(), // int
		Minutes:  minutes.Ptr(),    // *int: nil when left empty
		Vegan:    vegan.Value(),    // bool
		Tags:     tags.Value(),     // []string
		Steps:    steps.Value(),    // []string
	})
	if err != nil {
		return nil, err
	}
	return collage.SeeOther("/recipes/" + id), nil
}
```

| Field | Checks | `Value()` | `Ptr()` |
| --- | --- | --- | --- |
| `IntField` | `Min(n)`, `Max(n)`, `Range(min, max)`, `Custom(func(int) string)` | `int` | `*int` |
| `FloatField` | `Min(x)`, `Max(x)`, `Range(min, max)`, `Custom(func(float64) string)` | `float64` | `*float64` |
| `BoolField` | — | `bool` | — |
| `DateField` | `Min(t)`, `Max(t)` — by day, the time of day ignored — `Custom(func(time.Time) string)` | `time.Time`, midnight UTC | `*time.Time` |
| `ListField` | `Required()`, `MinItems(n)`, `MaxItems(n)`, `MaxLen(n)` of each item, `OneOf(options...)` of each item, `Custom(func([]string) string)` | `[]string` | — |

Each has `Message` too. A field left empty, or one that failed, gives the zero
value, and `Ptr()` gives nil: for an optional field, nil is "not given".

- `Bool()`: an unticked box sends nothing, which is `false`. `Required()` before
  it is a box that must be ticked:
  `v.Field("terms").Required("Accept the terms to go on.").Bool()`.
- `All()` reads every value sent under the name — a group of checkboxes, a
  `<select multiple>`; `Lines()` splits the value into lines — a `<textarea>` with
  one ingredient per line. Both trim each item and drop empty ones, so their
  `Required()` is the list's, not the first value's.
- `Date()` reads `2006-01-02` (`validate.DateLayout`). The new rules are `min`,
  `max` (numbers), `minDate`, `maxDate`, `minItems` and `maxItems`, with messages
  of their own below.

## Application checks

`v.Fail(field, message)` reports what only the application can know, shown the same
way; `v.Errors()` is every message by field, `v.Valid()` whether there are none,
and `v.Value(field)` what was submitted.

## Values already read

`validate.Form` reads the request's body. When something else has read it — the
non-file fields `uploads.Receive` hands back after streaming a multipart body —
the form is empty by then. `validate.Values` checks those values instead, and
`Refuse` types them back the same way, passwords excepted:

```go
received, err := up.Receive(rc, spec)
if err != nil {
	return nil, err
}
v := validate.Values(rc, received.Values)
v.Field("title").Required().MaxLen(80)
if !v.Valid() {
	return validate.Refuse(rc, v, uploadPage), nil
}
```

`rc` still gives the render's locale and the application's messages; its body is
not read again.

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
| `float` | Must be a number. |
| `bool` | Must be yes or no. |
| `date` | Must be a date. |
| `min`, `max` | Must be at least {min}. / Must be at most {max}. |
| `minDate`, `maxDate` | Must be on or after {min}. / Must be on or before {max}. |
| `minItems`, `maxItems` | Must have at least {min} items. / Must have at most {max} items. |

Three places override them, the first that has one winning:

1. A message given to the check, `Required("…")`, or `.Message("…")` after it,
   replaces that check's message, when it failed —
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
`password`, in any case, is never re-filled. `NoRefill` adds names of your own,
matched the same way — `["pin", "card"]` — and `password` stays in the list
whatever it says.

## Configuration

```go
validate.New(validate.Options{
	Messages: map[string]string{"required": "Please fill this in."},
	LocaleMessages: map[string]map[string]string{
		"tr": {"required": "Bu alan zorunludur.", "minLen": "En az {min} karakter olmalı."},
	},
	NoRefill: []string{"pin", "card"},
})
```

```json
{
  "elagoht/validate": {
    "messages": { "required": "Please fill this in." },
    "localeMessages": { "tr": { "required": "Bu alan zorunludur." } },
    "noRefill": ["pin", "card"]
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
- A size check of your own — a photo over 5 MB refused with `v.Fail` — is only
  reached when the action's body limit is above it. collage answers a body over
  the limit, 4 MiB unless `Server.MaxBodyBytes` says otherwise, with its own 413
  before the action runs, so a 5 MB rule under the default limit is never seen
  and a 4.5 MB photo is refused as too large for the server. Give the action
  room for the file and the rest of the form:
  `WithMaxBodyBytes(maxPhoto + 64<<10)`. Anything past that gets collage's 413.
- Validation in the browser — `required`, `type="email"` — is still worth adding
  for the reader's sake; this is the check that cannot be skipped.

## Changes

### Changed in v0.3.0

- Typed values: `Field.Value()`, and `Int`, `Float`, `Bool`, `Date`, `All` and
  `Lines` turning a field into an `IntField`, `FloatField`, `BoolField`,
  `DateField` or `ListField` whose `Value()` (and `Ptr()`) is the type.
- **`Int()` returns an `*IntField`**, not a `*Field`: a string check after it
  (`.Int().MaxLen(3)`) no longer compiles. `.Int().Message(…)` and
  `.Int().Range(…)` work as before, and `Field.Range` is unchanged. `Int` now
  ignores white space around the number.
- Every check but `OneOf` takes an optional message: `Required("…")`.
- New rules: `float`, `bool`, `date`, `min`, `max`, `minDate`, `maxDate`,
  `minItems`, `maxItems`.

### Changed in v0.2.0

- `noRefill` adds to `password` instead of replacing it: `"noRefill": ["pin"]`
  used to start typing passwords back into the form; now `password` is always
  left out and `pin` is left out too. A list that named `password` itself still
  works.
- New `validate.Values(rc, values)`: a validator over values already read, such
  as the fields `uploads.Receive` returns, with `Refuse` and the template
  functions working as for `Form`.
- Requires collage v0.61.0.

### v0.1.7

- Requires collage v0.60.0 (sealed public API). No behaviour change.

### v0.1.6

- The editor snippets `cfield` and `cfieldt` use generic placeholders (field, Label, text). Nothing else changes.

### v0.1.5

- v0.1.4 was tagged at v0.1.3's commit by mistake and is retracted.
- Requires collage v0.50.0. A refused submission is kept under typed keys
  (`collage.NewKey`), and the configuration is read with `collage.PluginConfig`.

### v0.1.3

- A refusal made before the body was read — the guard `Form`'s lazy reading is
  for — shows each field's `{{fieldValue}}` fallback, as v0.1.2 documented,
  instead of `""`. A refusal that read the form still types back what was sent,
  an empty or blank value included.
- The limitations name the body limit a form's own size check has to fit under.

### v0.1.2

- `Form` reads the form when a check or `Value` first needs it, not when the
  validator is built. An action refusing a body before reading it — a photo whose
  `Content-Length` is over its size — can build a validator for `Fail` and
  `Refuse`, and the body is never read: a multipart one no longer spills its files
  to disk first. Nothing is typed back for such a refusal.
- Built against collage v0.34.2.
