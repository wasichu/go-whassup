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
	KindText   // a free-form string (tag text:"true")
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
	// Confirm, if set, is shown for confirmation before the value changes.
	Confirm string
	v       reflect.Value
}

// Name is the dotted name used in messages, e.g. "history.keep_days".
func (f Field) Name() string { return f.Section + "." + f.Key }

// Value renders the current value.
func (f Field) Value() string {
	switch f.Kind {
	case KindBool:
		return strconv.FormatBool(f.v.Bool())
	case KindChoice, KindText:
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
	case KindText:
		f.v.SetString(s)
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
func (f Field) Cycle() { f.step(1) }

// CycleBack moves a choice field to its previous allowed value (wrapping),
// or flips a bool.
func (f Field) CycleBack() { f.step(-1) }

func (f Field) step(dir int) {
	switch f.Kind {
	case KindBool:
		f.v.SetBool(!f.v.Bool())
	case KindChoice:
		n := len(f.Choices)
		i := slices.Index(f.Choices, f.v.String())
		f.v.SetString(f.Choices[((i+dir)%n+n)%n])
	}
}

// Fields lists c's settings in file order. Sections are Config's struct
// fields; settings are their bool and int fields, and string fields with a
// `choices:"a,b,c"` or `text:"true"` tag. A field tagged
// `shown_when:"key=value"` is listed only while the sibling setting key
// has that value (it's still loaded and saved either way).
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
			if !shown(sv, sf.Tag.Get("shown_when")) {
				continue
			}
			f := Field{
				Section:     tagName(section),
				Key:         tagName(sf),
				Comment:     sf.Tag.Get("comment"),
				PairingOnly: sf.Tag.Get("applies") == "pairing",
				Confirm:     sf.Tag.Get("confirm"),
				v:           sv.Field(j),
			}
			switch sf.Type.Kind() {
			case reflect.Bool:
				f.Kind = KindBool
			case reflect.Int:
				f.Kind = KindInt
			case reflect.String:
				switch {
				case sf.Tag.Get("choices") != "":
					f.Kind, f.Choices = KindChoice, strings.Split(sf.Tag.Get("choices"), ",")
				case sf.Tag.Get("text") == "true":
					f.Kind = KindText
				default:
					continue // not editable generically
				}
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

// shown evaluates a shown_when condition ("key=value") against the
// section's other settings. An empty condition is always true.
func shown(section reflect.Value, cond string) bool {
	if cond == "" {
		return true
	}
	key, want, _ := strings.Cut(cond, "=")
	for i := range section.NumField() {
		if tagName(section.Type().Field(i)) == key {
			return section.Field(i).String() == want
		}
	}
	return false
}
