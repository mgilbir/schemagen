package runtime

import (
	"net/netip"

	"github.com/mgilbir/schemagen/runtime/internal/format"
)

// The "format" checkers and the content vocabulary. Each takes the string the
// document holds and returns nil, or an error saying what is wrong with it.
// None exempts the empty string: it is accepted only where it genuinely is a
// value of the format.

// FormatDate checks a full-date (RFC 3339 section 5.6).
func FormatDate(v string) error { return format.Date(v) }

// FormatTime checks a time with an offset (RFC 3339 section 5.6).
func FormatTime(v string) error { return format.Time(v) }

// FormatDraft3Time checks draft 3's "time": a time without an offset.
func FormatDraft3Time(v string) error { return format.Draft3Time(v) }

// FormatDraft3Color checks draft 3's "color": a CSS 2.1 color.
func FormatDraft3Color(v string) error { return format.Draft3Color(v) }

// FormatDateTime checks a date-time (RFC 3339 section 5.6).
func FormatDateTime(v string) error { return format.DateTime(v) }

// FormatDuration checks a duration (RFC 3339 appendix A).
func FormatDuration(v string) error { return format.Duration(v) }

// FormatEmail checks an email address (RFC 5321).
func FormatEmail(v string) error { return format.Email(v) }

// FormatIDNEmail checks an internationalized email address (RFC 6531).
func FormatIDNEmail(v string) error { return format.IDNEmail(v) }

// FormatHostname checks a hostname (RFC 1123).
func FormatHostname(v string) error { return format.Hostname(v) }

// FormatIDNHostname checks an internationalized hostname (RFC 5890).
func FormatIDNHostname(v string) error { return format.IDNHostname(v) }

// FormatURI checks a URI (RFC 3986).
func FormatURI(v string) error { return format.URI(v) }

// FormatIRI checks an IRI (RFC 3987).
func FormatIRI(v string) error { return format.IRI(v) }

// FormatURIReference checks a URI reference (RFC 3986).
func FormatURIReference(v string) error { return format.URIReference(v) }

// FormatIRIReference checks an IRI reference (RFC 3987).
func FormatIRIReference(v string) error { return format.IRIReference(v) }

// FormatURITemplate checks a URI template (RFC 6570).
func FormatURITemplate(v string) error { return format.URITemplate(v) }

// FormatUUID checks a UUID (RFC 4122).
func FormatUUID(v string) error { return format.UUID(v) }

// FormatJSONPointer checks a JSON Pointer (RFC 6901).
func FormatJSONPointer(v string) error { return format.JSONPointer(v) }

// FormatRelativeJSONPointer checks a relative JSON Pointer.
func FormatRelativeJSONPointer(v string) error { return format.RelativeJSONPointer(v) }

// FormatRegex checks an ECMA-262 regular expression.
func FormatRegex(v string) error { return format.Regex(v) }

// FormatIPv4 checks a dotted-quad IPv4 address (RFC 2673).
func FormatIPv4(v string) error { return format.IPv4(v) }

// FormatIPv6 checks an IPv6 address (RFC 4291).
func FormatIPv6(v string) error { return format.IPv6(v) }

// FormatIPv4Addr checks that a decoded address is an IPv4 one.
func FormatIPv4Addr(a netip.Addr) error { return format.IPv4Addr(a) }

// FormatIPv6Addr checks that a decoded address is an IPv6 one.
func FormatIPv6Addr(a netip.Addr) error { return format.IPv6Addr(a) }

// ContentString applies the content vocabulary to a string: encoding names how
// to turn it into bytes, and mediaType what those bytes have to parse as. Either
// may be empty, meaning the schema stated no such keyword.
func ContentString(value, encoding, mediaType string) error {
	return schemagenContentString(value, encoding, mediaType)
}
