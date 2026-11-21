// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package bindengine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// upper implements Unmarshaler: it stores its parameter in upper case.
type upper string

func (u *upper) UnmarshalParam(param string) error {
	*u = upper(strings.ToUpper(param))
	return nil
}

// joined implements MultipleUnmarshaler: it stores every parameter, joined by commas.
type joined string

func (j *joined) UnmarshalParams(params []string) error {
	*j = joined(strings.Join(params, ","))
	return nil
}

// refused implements Unmarshaler and refuses every parameter.
type refused string

var errRefused = errors.New("refused")

func (r *refused) UnmarshalParam(string) error { return errRefused }

func TestBindUnmarshalers(t *testing.T) {
	var dest struct {
		Name upper  `query:"name"`
		Tags joined `query:"tags"`
	}
	data := map[string][]string{"name": {"jo"}, "tags": {"a", "b"}}
	if err := Bind(&dest, data, "query", nil); err != nil {
		t.Fatal(err)
	}
	if dest.Name != "JO" || dest.Tags != "a,b" {
		t.Errorf("got %q and %q, want %q and %q", dest.Name, dest.Tags, "JO", "a,b")
	}
}

func TestBindUnmarshalerRefusal(t *testing.T) {
	var dest struct {
		Name refused `query:"name"`
	}
	err := Bind(&dest, map[string][]string{"name": {"jo"}}, "query", nil)
	if !errors.Is(err, errRefused) || err.Error() != "name: refused" {
		t.Errorf("got %v, want %q wrapping errRefused", err, "name: refused")
	}
}

func TestBindUnsupportedMapValue(t *testing.T) {
	var dest map[string]int
	if err := Bind(&dest, map[string][]string{"a": {"1"}}, "query", nil); !errors.Is(err, ErrUnsupportedMapValue) {
		t.Errorf("got %v, want ErrUnsupportedMapValue", err)
	}
}

func TestBindCachesOneMetaPerType(t *testing.T) {
	type dto struct {
		ID int `query:"id"`
	}
	type other struct {
		ID int `query:"id"`
	}
	data := map[string][]string{"id": {"7"}}
	for range 2 {
		var d dto
		if err := Bind(&d, data, "query", nil); err != nil || d.ID != 7 {
			t.Fatalf("got %d and %v, want 7 and no error", d.ID, err)
		}
	}
	typ := reflect.TypeFor[dto]()
	cached, ok := bindStructCache.Load(typ)
	if !ok {
		t.Fatal("binding dto cached no metadata")
	}
	meta := cached.(*bindStructMeta)
	if bindMetaFor(typ) != meta {
		t.Error("a lookup of a cached type built new metadata")
	}
	if bindMetaFor(reflect.TypeFor[other]()) == meta {
		t.Error("two types share one metadata")
	}
}
