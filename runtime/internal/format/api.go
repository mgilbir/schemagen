package format

import "net/netip"

// The checkers the runtime exports. Each takes the string a document holds and
// returns nil, or an error saying what is wrong with it.

// Date checks a full-date (RFC 3339 section 5.6).
func Date(v string) error { return schemagenFormatDate(v) }

// Time checks a time with an offset (RFC 3339 section 5.6).
func Time(v string) error { return schemagenFormatTime(v) }

// Draft3Time checks draft 3's "time": a time without an offset.
func Draft3Time(v string) error { return schemagenFormatDraft3Time(v) }

// Draft3Color checks draft 3's "color": a CSS 2.1 color.
func Draft3Color(v string) error { return schemagenFormatDraft3Color(v) }

// DateTime checks a date-time (RFC 3339 section 5.6).
func DateTime(v string) error { return schemagenFormatDateTime(v) }

// Duration checks a duration (RFC 3339 appendix A).
func Duration(v string) error { return schemagenFormatDuration(v) }

// Email checks an email address (RFC 5321).
func Email(v string) error { return schemagenFormatEmail(v) }

// IDNEmail checks an internationalized email address (RFC 6531).
func IDNEmail(v string) error { return schemagenFormatIDNEmail(v) }

// Hostname checks a hostname (RFC 1123).
func Hostname(v string) error { return schemagenFormatHostname(v) }

// IDNHostname checks an internationalized hostname (RFC 5890).
func IDNHostname(v string) error { return schemagenFormatIDNHostname(v) }

// URI checks a URI (RFC 3986).
func URI(v string) error { return schemagenFormatURI(v) }

// IRI checks an IRI (RFC 3987).
func IRI(v string) error { return schemagenFormatIRI(v) }

// URIReference checks a URI reference (RFC 3986).
func URIReference(v string) error { return schemagenFormatURIReference(v) }

// IRIReference checks an IRI reference (RFC 3987).
func IRIReference(v string) error { return schemagenFormatIRIReference(v) }

// URITemplate checks a URI template (RFC 6570).
func URITemplate(v string) error { return schemagenFormatURITemplate(v) }

// UUID checks a UUID (RFC 4122).
func UUID(v string) error { return schemagenFormatUUID(v) }

// JSONPointer checks a JSON Pointer (RFC 6901).
func JSONPointer(v string) error { return schemagenFormatJSONPointer(v) }

// RelativeJSONPointer checks a relative JSON Pointer.
func RelativeJSONPointer(v string) error { return schemagenFormatRelativeJSONPointer(v) }

// Regex checks an ECMA-262 regular expression.
func Regex(v string) error { return schemagenFormatRegex(v) }

// IPv4 checks a dotted-quad IPv4 address (RFC 2673).
func IPv4(v string) error { return schemagenFormatIPv4(v) }

// IPv6 checks an IPv6 address (RFC 4291).
func IPv6(v string) error { return schemagenFormatIPv6(v) }

// IPv4Addr checks that a decoded address is an IPv4 one.
func IPv4Addr(a netip.Addr) error { return schemagenFormatIPv4Addr(a) }

// IPv6Addr checks that a decoded address is an IPv6 one.
func IPv6Addr(a netip.Addr) error { return schemagenFormatIPv6Addr(a) }
