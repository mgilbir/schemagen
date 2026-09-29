//go:build !goexperiment.jsonv2

package tagoracle

// JSONv2 reports which encoding/json this build links: false for the original
// implementation (the default through Go 1.26, and GOEXPERIMENT=nojsonv2).
const JSONv2 = false
