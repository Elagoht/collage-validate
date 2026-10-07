// A collage plugin for form validation in actions: chainable checks per field, a
// refused submission rendered again with status 422, and template functions that
// put each field's message and what was typed back in front of the reader.
//
// It requires collage the way any consumer does, and reaches nothing the framework
// does not offer every plugin.
module github.com/Elagoht/collage-validate

go 1.26

require github.com/Elagoht/collage v0.50.0

retract v0.1.4 // tagged at v0.1.3's commit by mistake
