// Package protocol embeds the frozen M1 resources for working-directory independent validation.
package protocol

import _ "embed"

const Version = "bridge-m1-1"

//go:embed schema.json
var Schema []byte

//go:embed golden-vectors.json
var Golden []byte
