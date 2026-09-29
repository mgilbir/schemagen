//go:build goexperiment.jsonv2

package tagoracle

// JSONv2 reports which encoding/json this build links: true for the one
// backed by encoding/json/v2 (GOEXPERIMENT=jsonv2, the default from Go 1.27).
const JSONv2 = true
