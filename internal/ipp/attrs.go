package ipp

import "github.com/OpenPrinting/goipp"

// Attribute lookup helpers.
//
// goipp hands attributes back as a slice of names paired with tagged values,
// which is faithful to the wire and tedious to read from. These turn that into
// ordinary Go values.
//
// They are deliberately forgiving: an attribute that is missing yields the zero
// value rather than an error. Old printers omit a great deal, and treating every
// omission as a failure would make this software useless on exactly the hardware
// it exists to support.

func find(attrs goipp.Attributes, name string) (goipp.Attribute, bool) {
	for _, a := range attrs {
		if a.Name == name {
			return a, true
		}
	}
	return goipp.Attribute{}, false
}

// str returns the first value of a text attribute, or "" when it is absent.
func str(attrs goipp.Attributes, name string) string {
	a, ok := find(attrs, name)
	if !ok || len(a.Values) == 0 {
		return ""
	}
	s, _ := a.Values[0].V.(goipp.String)
	return string(s)
}

// strs returns every value of a multi-valued text attribute.
func strs(attrs goipp.Attributes, name string) []string {
	a, ok := find(attrs, name)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(a.Values))
	for _, v := range a.Values {
		if s, ok := v.V.(goipp.String); ok {
			out = append(out, string(s))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// integer returns the first value of an integer or enum attribute. The second
// result reports whether it was present and of the expected type.
func integer(attrs goipp.Attributes, name string) (int32, bool) {
	a, ok := find(attrs, name)
	if !ok || len(a.Values) == 0 {
		return 0, false
	}
	n, ok := a.Values[0].V.(goipp.Integer)
	return int32(n), ok
}

// integers returns every value of a multi-valued integer attribute.
func integers(attrs goipp.Attributes, name string) []int32 {
	a, ok := find(attrs, name)
	if !ok {
		return nil
	}
	out := make([]int32, 0, len(a.Values))
	for _, v := range a.Values {
		if n, ok := v.V.(goipp.Integer); ok {
			out = append(out, int32(n))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// boolean returns the first value of a boolean attribute. The second result
// reports whether it was present and of the expected type.
func boolean(attrs goipp.Attributes, name string) (bool, bool) {
	a, ok := find(attrs, name)
	if !ok || len(a.Values) == 0 {
		return false, false
	}
	b, ok := a.Values[0].V.(goipp.Boolean)
	return bool(b), ok
}

// requestedAttributes builds the attribute that tells CUPS which fields to
// return. Asking explicitly rather than taking everything keeps responses small,
// which matters on a Pi with a dozen queues configured.
func requestedAttributes(names ...string) goipp.Attribute {
	attr := goipp.Attribute{Name: "requested-attributes"}
	for _, n := range names {
		attr.Values.Add(goipp.TagKeyword, goipp.String(n))
	}
	return attr
}
