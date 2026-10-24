package validate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	validate "github.com/Elagoht/collage-validate"
	"github.com/Elagoht/collage/pkg/collage"
	"github.com/Elagoht/collage/pkg/collagetest"
)

// recipe is what the /recipe action read: the typed values and the messages.
type recipe struct {
	Title       string            `json:"title"`
	Servings    int               `json:"servings"`
	Minutes     *int              `json:"minutes"`
	Grams       float64           `json:"grams"`
	Rating      *float64          `json:"rating"`
	Vegan       bool              `json:"vegan"`
	Cooked      *time.Time        `json:"cooked"`
	Tags        []string          `json:"tags"`
	Ingredients []string          `json:"ingredients"`
	Valid       bool              `json:"valid"`
	Errors      map[string]string `json:"errors"`
}

// recipeSite has a /recipe action that checks a recipe form and answers with
// what it read.
func recipeSite(t *testing.T) http.Handler {
	t.Helper()
	act := collage.NewAction("recipe").WithPath("en", "/recipe").WithPath("tr", "/recipe").
		WithMethods(http.MethodPost).WithoutCSRF().
		WithHandler(func(_ context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
			v := validate.Form(rc)
			title := v.Field("title").Required("Name the recipe.").MaxLen(20)
			servings := v.Field("servings").Required().Int("Whole servings only.").Range(1, 50)
			minutes := v.Field("minutes").Int().Min(1)
			grams := v.Field("grams").Float().Max(5000)
			rating := v.Field("rating").Float().Range(0, 5)
			vegan := v.Field("vegan").Bool()
			cooked := v.Field("cooked").Date().Max(time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC))
			tags := v.Field("tags").All().Required().OneOf("soup", "dessert", "quick").MaxItems(2)
			ingredients := v.Field("ingredients").Lines().MinItems(2).MaxLen(30)
			return collage.JSON(http.StatusOK, recipe{
				Title: title.Value(), Servings: servings.Value(), Minutes: minutes.Ptr(),
				Grams: grams.Value(), Rating: rating.Ptr(), Vegan: vegan.Value(), Cooked: cooked.Ptr(),
				Tags: tags.Value(), Ingredients: ingredients.Value(),
				Valid: v.Valid(), Errors: v.Errors(),
			})
		}).Build()
	return collagetest.NewSite(t, siteOptions(validate.Options{}, nil, act)...).App.Handler()
}

func postRecipe(t *testing.T, h http.Handler, values url.Values) recipe {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/recipe", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var got recipe
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return got
}

// A valid form gives each field as its type, optional ones left out as nil.
func TestTyped_Values(t *testing.T) {
	got := postRecipe(t, recipeSite(t), url.Values{
		"title":       {"  Lentil soup "},
		"servings":    {" 4"},
		"grams":       {"250.5"},
		"vegan":       {"on"},
		"cooked":      {"2026-10-25"},
		"tags":        {"soup", " ", "quick"},
		"ingredients": {"lentils\r\n\n  onion  \r\nsalt"},
	})
	if !got.Valid {
		t.Fatalf("refused: %v", got.Errors)
	}
	cooked := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	want := recipe{
		Title: "Lentil soup", Servings: 4, Grams: 250.5, Vegan: true, Cooked: &cooked,
		Tags: []string{"soup", "quick"}, Ingredients: []string{"lentils", "onion", "salt"},
		Valid: true, Errors: map[string]string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got.Minutes != nil || got.Rating != nil {
		t.Errorf("empty optional fields: minutes %v, rating %v, want nil", got.Minutes, got.Rating)
	}
}

// Each typed check reports its rule, or the message given to it; a failed field
// gives its zero value.
func TestTyped_Refusals(t *testing.T) {
	got := postRecipe(t, recipeSite(t), url.Values{
		"servings":    {"four"},
		"minutes":     {"0"},
		"grams":       {"2,5"},
		"rating":      {"9"},
		"vegan":       {"maybe"},
		"cooked":      {"2026-10-26"},
		"tags":        {"soup", "stew"},
		"ingredients": {"just water"},
	})
	want := map[string]string{
		"title":       "Name the recipe.",
		"servings":    "Whole servings only.",
		"minutes":     "Must be at least 1.",
		"grams":       "Must be a number.",
		"rating":      "Must be between 0 and 5.",
		"vegan":       "Must be yes or no.",
		"cooked":      "Must be on or before 2026-10-25.",
		"tags":        "Must be one of: soup, dessert, quick.",
		"ingredients": "Must have at least 2 items.",
	}
	if got.Valid || !reflect.DeepEqual(got.Errors, want) {
		t.Errorf("errors:\n got  %v\n want %v", got.Errors, want)
	}
	if got.Servings != 0 || got.Minutes != nil || got.Rating != nil || got.Vegan || got.Cooked != nil || got.Tags != nil || got.Ingredients != nil {
		t.Errorf("a failed field gave a value: %+v", got)
	}
}

// A list's own checks: an empty list fails Required, too many items MaxItems,
// a long item MaxLen; a checkbox left unticked is false.
func TestTyped_Lists(t *testing.T) {
	h := recipeSite(t)
	got := postRecipe(t, h, url.Values{"title": {"x"}, "servings": {"2"}, "tags": {" "}})
	if got.Errors["tags"] != "This field is required." || got.Vegan {
		t.Errorf("empty tags: %v, vegan %v", got.Errors, got.Vegan)
	}
	got = postRecipe(t, h, url.Values{"tags": {"soup", "dessert", "quick"},
		"ingredients": {"a\n" + strings.Repeat("ş", 31)}})
	if got.Errors["tags"] != "Must have at most 2 items." || got.Errors["ingredients"] != "Must be at most 30 characters." {
		t.Errorf("errors: %v", got.Errors)
	}
}

// Required before Bool is a box that must be ticked; Custom on a typed field
// sees the typed value.
func TestTyped_RequiredBoolAndCustom(t *testing.T) {
	var terms *validate.BoolField
	checks := func(v *validate.Validator) {
		terms = v.Field("terms").Required("Accept the terms.").Bool()
		v.Field("n").Int().Custom(func(n int) string {
			if n%2 != 0 {
				return "Must be even."
			}
			return ""
		})
		v.Field("tags").All().Custom(func(items []string) string {
			if len(items) == 2 && items[0] == items[1] {
				return "No repeats."
			}
			return ""
		})
		v.Field("day").Date().Custom(func(d time.Time) string {
			if d.Weekday() == time.Sunday {
				return "Closed on Sundays."
			}
			return ""
		})
	}
	h := app(t, validate.Options{}, checks)
	errs := check(t, h, "/check", url.Values{"n": {"3"}, "tags": {"a", "a"}, "day": {"2026-10-25"}})
	want := map[string]string{"terms": "Accept the terms.", "n": "Must be even.", "tags": "No repeats.", "day": "Closed on Sundays."}
	if !reflect.DeepEqual(errs, want) {
		t.Errorf("errors:\n got  %v\n want %v", errs, want)
	}
	if errs := check(t, h, "/check", url.Values{"terms": {"on"}, "n": {"4"}}); len(errs) != 0 || !terms.Value() {
		t.Errorf("ticked: %v, value %v", errs, terms.Value())
	}
}

// A message given to a check, or by Message after it, replaces the rule's only
// when that check failed; the plugin's messages apply to the new rules too.
func TestTyped_Messages(t *testing.T) {
	h := app(t, validate.Options{
		Messages:       map[string]string{"float": "Sayı olmalı."},
		LocaleMessages: map[string]map[string]string{"tr": {"minItems": "En az {min} satır."}},
	}, func(v *validate.Validator) {
		v.Field("a").Required("A?").MinLen(2, "Longer.")
		v.Field("b").Required("B?").Int().Message("Whole.")
		v.Field("c").Float()
		v.Field("d").Lines().MinItems(3)
	})
	errs := check(t, h, "/check", url.Values{"a": {"x"}, "b": {"1.5"}, "c": {"x"}, "d": {"one"}})
	want := map[string]string{"a": "Longer.", "b": "Whole.", "c": "Sayı olmalı.", "d": "Must have at least 3 items."}
	if !reflect.DeepEqual(errs, want) {
		t.Errorf("en:\n got  %v\n want %v", errs, want)
	}
	if errs := check(t, h, "/tr/check", url.Values{"a": {"xy"}, "b": {"1"}, "d": {"one"}}); errs["d"] != "En az 3 satır." {
		t.Errorf("tr: %v", errs)
	}
}
