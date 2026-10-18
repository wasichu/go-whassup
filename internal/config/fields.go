package config

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// FieldKind is a setting's value type.
type FieldKind int

const (
	KindBool FieldKind = iota
	KindInt
	KindChoice // a string limited to Choices
)

// Field is one setting, discovered from Config's struct tags so editors
// (the TUI's Settings screen) never hard-code the list. It refers into the
// Config it came from: Set changes that Config.
type Field struct {
	Section string // e.g. "history"
	Key     string // e.g. "keep_days"
	Comment string // the same text Save writes into the file
	Kind    FieldKind
	// PairingOnly marks settings that only take effect when linking a device.
	PairingOnly bool
	// Choices lists the allowed values of a KindChoice field, in display order.
	Choices []string
	v       reflect.Value
}

// Name is the dotted name used in messages, e.g. "history.keep_days".
func (f Field) Name() string { return f.Section + "." + f.Key }

// Value renders the current value.
func (f Field) Value() string {
	switch f.Kind {
	case KindBool:
		return strconv.FormatBool(f.v.Bool())
	case KindChoice:
		return f.v.String()
	}
	return strconv.FormatInt(f.v.Int(), 10)
}

// Set parses s into the field. It checks syntax only; use Config.Validate
// for range and cross-field rules.
func (f Field) Set(s string) error {
	s = strings.TrimSpace(s)
	switch f.Kind {
	case KindBool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("%s: want true or false", f.Name())
		}
		f.v.SetBool(b)
	case KindInt:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%s: want a whole number", f.Name())
		}
		f.v.SetInt(int64(n))
	case KindChoice:
		if !slices.Contains(f.Choices, s) {
			return fmt.Errorf("%s: want one of %s", f.Name(), strings.Join(f.Choices, ", "))
		}
		f.v.SetString(s)
	}
	return nil
}

// Cycle moves a choice field to its next allowed value (wrapping), or
// flips a bool.
func (f Field) Cycle() {
	switch f.Kind {
	case KindBool:
		f.v.SetBool(!f.v.Bool())
	case KindChoice:
		i := slices.Index(f.Choices, f.v.String())
		f.v.SetString(f.Choices[(i+1)%len(f.Choices)])
	}
}

// Fields lists c's settings in file order. Sections are Config's struct
// fields; settings are their bool and int fields, and string fields with a
// `choices:"a,b,c"` tag.
func (c *Config) Fields() []Field {
	var out []Field
	root := reflect.ValueOf(c).Elem()
	for i := range root.NumField() {
		section := root.Type().Field(i)
		sv := root.Field(i)
		if sv.Kind() != reflect.Struct {
			continue
		}
		for j := range sv.NumField() {
			sf := sv.Type().Field(j)
			f := Field{
				Section:     tagName(section),
				Key:         tagName(sf),
				Comment:     sf.Tag.Get("comment"),
				PairingOnly: sf.Tag.Get("applies") == "pairing",
				v:           sv.Field(j),
			}
			switch sf.Type.Kind() {
			case reflect.Bool:
				f.Kind = KindBool
			case reflect.Int:
				f.Kind = KindInt
			case reflect.String:
				choices := sf.Tag.Get("choices")
				if choices == "" {
					continue // free-form strings aren't editable generically
				}
				f.Kind, f.Choices = KindChoice, strings.Split(choices, ",")
			default:
				continue // not editable generically
			}
			out = append(out, f)
		}
	}
	return out
}

func tagName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	if name == "" {
		return f.Name
	}
	return name
}
