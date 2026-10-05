package bundle

import (
	"maps"
	"slices"
	"strconv"
)

// SignedContent is the canonical byte string covered by the main signature.
// It never includes experiment data.
func SignedContent(b *Bundle) []byte {
	buf := appendHeader(make([]byte, 0, 256+64*len(b.Strings)), b)
	buf = append(buf, `,"strings":{`...)
	for i, key := range slices.Sorted(maps.Keys(b.Strings)) {
		if i > 0 {
			buf = append(buf, ',')
		}
		e := b.Strings[key]
		buf = appendString(buf, key)
		buf = append(buf, `:{"format":`...)
		buf = appendString(buf, e.Format)
		buf = append(buf, `,"value":`...)
		buf = appendString(buf, e.Value)
		buf = append(buf, '}')
	}
	return append(buf, "}}"...)
}

// ExperimentsSignedContent is the canonical byte string covered by experiments_signature.
func ExperimentsSignedContent(b *Bundle) []byte {
	buf := appendHeader(make([]byte, 0, 256), b)
	buf = append(buf, `,"experiments":{`...)
	first := true
	for _, key := range slices.Sorted(maps.Keys(b.Strings)) {
		x := b.Strings[key].Experiment
		if x == nil {
			continue
		}
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = appendString(buf, key)
		buf = append(buf, `:{"allocation":{`...)
		for i, name := range slices.Sorted(maps.Keys(x.Allocation)) {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendString(buf, name)
			buf = append(buf, ':')
			buf = strconv.AppendInt(buf, x.Allocation[name], 10)
		}
		buf = append(buf, `},"id":`...)
		buf = appendString(buf, x.ID)
		buf = append(buf, `,"variants":{`...)
		for i, name := range slices.Sorted(maps.Keys(x.Variants)) {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendString(buf, name)
			buf = append(buf, ':')
			buf = appendString(buf, x.Variants[name])
		}
		buf = append(buf, "}}"...)
	}
	return append(buf, "}}"...)
}

func appendHeader(buf []byte, b *Bundle) []byte {
	buf = append(buf, `{"format_version":`...)
	buf = strconv.AppendInt(buf, b.FormatVersion, 10)
	buf = append(buf, `,"project_id":`...)
	buf = appendString(buf, b.ProjectID)
	buf = append(buf, `,"locale":`...)
	buf = appendString(buf, b.Locale)
	buf = append(buf, `,"revision":`...)
	buf = strconv.AppendInt(buf, b.Revision, 10)
	buf = append(buf, `,"created_at":`...)
	return appendString(buf, b.CreatedAt)
}

const hex = "0123456789abcdef"

func appendString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			buf = append(buf, '\\', '"')
		case c == '\\':
			buf = append(buf, '\\', '\\')
		case c >= 0x20:
			buf = append(buf, c)
		case c == '\b':
			buf = append(buf, '\\', 'b')
		case c == '\t':
			buf = append(buf, '\\', 't')
		case c == '\n':
			buf = append(buf, '\\', 'n')
		case c == '\f':
			buf = append(buf, '\\', 'f')
		case c == '\r':
			buf = append(buf, '\\', 'r')
		default:
			buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		}
	}
	return append(buf, '"')
}
