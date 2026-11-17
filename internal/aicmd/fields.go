package aicmd

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Field is how the command line passes one field of an input (see the
// package documentation for the tags).
type Field struct {
	// Index is the field's index in the input struct.
	Index int
	Name  string
	Type  reflect.Type
	// Arg is the position of a positional argument, -1 for none.
	Arg int
	// Optional arguments may be left out; Words take every argument from
	// Arg on (a text).
	Optional, Words bool
	// Flag is the flag's name, "" for none; for a text it is the file
	// flag.
	Flag, Help, Default string
	Required            bool
	// Text is set for a text; AllowEmpty allows an empty one.
	Text, AllowEmpty bool
	// StdinHint is set when the flag's value - reads standard input.
	StdinHint string
}

var (
	stringType      = reflect.TypeFor[string]()
	stringPtrType   = reflect.TypeFor[*string]()
	intType         = reflect.TypeFor[int]()
	intPtrType      = reflect.TypeFor[*int]()
	boolType        = reflect.TypeFor[bool]()
	stringsType     = reflect.TypeFor[[]string]()
	stringsPtrType  = reflect.TypeFor[*[]string]()
	intsType        = reflect.TypeFor[[]int]()
	flagTypes       = []reflect.Type{stringType, stringPtrType, intType, intPtrType, boolType, stringsType, stringsPtrType, intsType}
	positionalTypes = []reflect.Type{stringType, intType}
)

// Fields returns how the command line passes each field of the input type
// t, or why t is not a valid input.
func Fields(t reflect.Type) ([]Field, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s is not a struct", t)
	}
	var out []Field
	args := map[int]bool{}
	for i := range t.NumField() {
		sf := t.Field(i)
		f := Field{Index: i, Name: sf.Name, Type: sf.Type, Arg: -1}
		if _, ok := sf.Tag.Lookup("json"); !ok || !sf.IsExported() {
			return nil, fmt.Errorf("%s.%s has no json tag", t, sf.Name)
		}
		if _, ok := sf.Tag.Lookup("jsonschema"); !ok {
			return nil, fmt.Errorf("%s.%s has no jsonschema tag", t, sf.Name)
		}
		if arg, ok := sf.Tag.Lookup("arg"); ok {
			pos, opt, _ := strings.Cut(arg, ",")
			if p, words := strings.CutSuffix(pos, "..."); words {
				pos, f.Words = p, true
			}
			n, err := strconv.Atoi(pos)
			if err != nil || n < 0 || args[n] {
				return nil, fmt.Errorf("%s.%s: bad or repeated arg tag %q", t, sf.Name, arg)
			}
			args[n] = true
			f.Arg = n
			switch opt {
			case "":
			case "optional":
				f.Optional = true
			default:
				return nil, fmt.Errorf("%s.%s: unknown arg option %q", t, sf.Name, opt)
			}
		}
		f.Flag = sf.Tag.Get("flag")
		f.Help, f.Default = sf.Tag.Get("help"), sf.Tag.Get("default")
		f.Required = sf.Tag.Get("required") == "true"
		f.StdinHint = sf.Tag.Get("stdin")
		if text, ok := sf.Tag.Lookup("text"); ok {
			if f.Flag != "" {
				return nil, fmt.Errorf("%s.%s: a text names its file flag in the text tag", t, sf.Name)
			}
			name, opt, _ := strings.Cut(text, ",")
			f.Flag, f.Text, f.AllowEmpty = name, true, opt == "empty"
			if name == "" || (opt != "" && opt != "empty") {
				return nil, fmt.Errorf("%s.%s: bad text tag %q", t, sf.Name, text)
			}
			if sf.Type != stringType && sf.Type != stringPtrType {
				return nil, fmt.Errorf("%s.%s: a text is a string or *string", t, sf.Name)
			}
		}
		switch {
		case f.Arg < 0 && f.Flag == "":
			return nil, fmt.Errorf("%s.%s is neither an argument nor a flag", t, sf.Name)
		case f.Arg >= 0 && f.Flag != "" && !f.Text:
			return nil, fmt.Errorf("%s.%s is both an argument and a flag", t, sf.Name)
		case f.Words && !f.Text:
			return nil, fmt.Errorf("%s.%s: only a text takes words", t, sf.Name)
		case f.Arg >= 0 && !f.Text && !slices.Contains(positionalTypes, sf.Type):
			return nil, fmt.Errorf("%s.%s: an argument is a string or an int", t, sf.Name)
		case f.Flag != "" && !slices.Contains(flagTypes, sf.Type):
			return nil, fmt.Errorf("%s.%s: unsupported flag type %s", t, sf.Name, sf.Type)
		case f.Flag != "" && f.Help == "":
			return nil, fmt.Errorf("%s.%s: flag --%s has no help", t, sf.Name, f.Flag)
		case f.StdinHint != "" && (f.Text || (sf.Type != stringType && sf.Type != stringPtrType)):
			return nil, fmt.Errorf("%s.%s: only a string flag reads standard input", t, sf.Name)
		}
		out = append(out, f)
	}
	for n := range len(args) {
		if !args[n] {
			return nil, fmt.Errorf("%s: argument %d is missing", t, n)
		}
	}
	// Optional arguments follow the required ones; words come last.
	byArg := map[int]Field{}
	for _, f := range out {
		if f.Arg >= 0 {
			byArg[f.Arg] = f
		}
	}
	for n := 1; n < len(byArg); n++ {
		prev, cur := byArg[n-1], byArg[n]
		if prev.Words || (prev.Optional && !cur.Optional && !cur.Words) {
			return nil, fmt.Errorf("%s: argument %d cannot follow argument %d", t, n, n-1)
		}
	}
	return out, nil
}
