package runtime

import (
	"encoding/json"
	"net/netip"
)

// jsonIPv4Addr and jsonIPv6Addr are the decode destinations for an asserted
// `format: ipv4` and `format: ipv6`, and they exist because netip.Addr refuses a
// bad address in the parser's words rather than the schema's.
//
// netip.Addr is filled through encoding.TextUnmarshaler, and encoding/json hands
// an Unmarshaler's error straight back, so `{"a":"nope"}` came back as
// `ParseAddr("nope"): unable to parse IP` -- which names no path, no keyword and
// nothing the caller wrote. One sibling string keyword away the same format at
// the same position is held as a string and answered with `"nope" is not a valid
// IPv4 address`, from schemagenFormatIPv4. These say the same thing, so a format
// reads the same however the position it sits at happens to be typed. Issue
// #282.
//
// Neither judges the address family: netip.ParseAddr accepts "::1" for an ipv4
// position and 1.2.3.4 for an ipv6 one, and so did the decode these replace.
// That verdict belongs to schemagenFormatIPv4Addr and schemagenFormatIPv6Addr in
// Validate, and moving it here would change what a document is refused by rather
// than what it is refused in.
type jsonIPv4Addr = IPv4Addr

// IPv4Addr is jsonIPv4Addr, under the name generated code refers to it by.
type IPv4Addr netip.Addr

// SchemagenGenerated marks IPv4Addr as a type whose UnmarshalJSON may be handed
// bytes already checked to be one JSON value. See jsonGenerated.
func (*IPv4Addr) SchemagenGenerated() {}

// UnmarshalJSON reads an IP address into an IPv4Addr, refusing what is not one
// in the format's words. It does not judge the family. See jsonIPv4Addr.
func (j *IPv4Addr) UnmarshalJSON(data []byte) error {
	_a, _err := jsonIPAddrDecode(data, "IPv4")
	if _err != nil {
		return _err
	}
	*j = jsonIPv4Addr(_a)
	return nil
}

type jsonIPv6Addr = IPv6Addr

// IPv6Addr is jsonIPv6Addr, under the name generated code refers to it by.
type IPv6Addr netip.Addr

// SchemagenGenerated marks IPv6Addr as a type whose UnmarshalJSON may be handed
// bytes already checked to be one JSON value. See jsonGenerated.
func (*IPv6Addr) SchemagenGenerated() {}

// UnmarshalJSON reads an IP address into an IPv6Addr, refusing what is not one
// in the format's words. It does not judge the family. See jsonIPv4Addr.
func (j *IPv6Addr) UnmarshalJSON(data []byte) error {
	_a, _err := jsonIPAddrDecode(data, "IPv6")
	if _err != nil {
		return _err
	}
	*j = jsonIPv6Addr(_a)
	return nil
}

// jsonIPAddrDecode reads an address, refusing a string that names none in the
// words the format is asserted in. family is the spelling that goes in the
// message, and is the only thing that differs between the two shadows.
//
// What is accepted is exactly what netip.Addr's own decoder accepted, so that
// this changes the words a document is refused in and never the documents. That
// includes the empty string, which that decoder reads as the zero address and
// reports nothing for.
func jsonIPAddrDecode(data []byte, family string) (netip.Addr, error) {
	var _s string
	if _err := json.Unmarshal(data, &_s); _err != nil {
		// Not a JSON string at all, which is a type error rather than a bad
		// address: encoding/json's own message names the token it saw, and
		// nothing here can recover that once the decode has failed.
		return netip.Addr{}, _err
	}
	if _s == "" {
		return netip.Addr{}, nil
	}
	_a, _err := netip.ParseAddr(_s)
	if _err != nil {
		return netip.Addr{}, jsonValueErrorf("%s is not a valid %s address", _schemagenQuote(_s), family)
	}
	return _a, nil
}
