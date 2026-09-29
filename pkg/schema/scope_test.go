package schema

import (
	"encoding/json"
	"net/url"
	"testing"
)

// TestTheResourceGraphAndTheResolverAgreeOnScope: which nodes start a resource
// of their own is asked by ComputeBaseURIs -- and so by the resource graph --
// and by the resolver's anchor search, and the two used to answer separately.
// An id url.Parse refuses was a scope for the resolver and not for
// ComputeBaseURIs, and a draft-4 "id" was a scope for ComputeBaseURIs and not
// for the resolver; either way an anchor under it was in one index and not the
// other.
func TestTheResourceGraphAndTheResolverAgreeOnScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		mid  *Schema // the node between the root and the anchor
	}{
		{"an id url.Parse refuses", &Schema{ID: "http://[::1"}},
		{"a draft 4 id only", &Schema{LegacyID: "http://example.test/mid"}},
		{"a scope-changing $id", &Schema{ID: "http://example.test/mid"}},
		{"a plain-name id", &Schema{ID: "#mid"}},
		{"no id", &Schema{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := &Schema{Anchor: "leaf"}
			tc.mid.Properties = map[string]*Schema{"x": leaf}
			root := &Schema{ID: "http://example.test/root", Defs: map[string]*Schema{"mid": tc.mid}}

			base, _ := url.Parse("http://example.test/root")
			graph := BuildResourceGraph(root, base, Draft202012)
			inGraph := graph.Resources[canonicalResourceURI(root)].Anchors["leaf"] == leaf
			found, err := NewLocalResolver(root).Resolve("#leaf")
			inResolver := err == nil && found == leaf
			if inGraph != inResolver {
				t.Errorf("the resource graph says %v and the resolver says %v", inGraph, inResolver)
			}
		})
	}

	// Parsed from JSON, the unparseable id is also refused as malformed.
	var s Schema
	if err := json.Unmarshal([]byte(`{"$defs":{"mid":{"$id":"http://[::1","properties":{"x":{"$anchor":"leaf"}}}}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Normalize()
	if bad := s.Defs["mid"].MalformedKeywords(); len(bad) != 1 || bad[0].Keyword != "$id" {
		t.Errorf("malformed = %v, want $id", bad)
	}
}
