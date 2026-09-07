package tsgraph

import _ "embed"

// The face every drawing is lettered in: Maple Mono, cut down to printable
// ASCII. Carrying it rather than reaching for an installed font is what makes
// a drawing look the same on every machine that renders it; cutting it to the
// range a graph actually uses is what keeps that from costing 20 MB. See
// fonts/README.md for the provenance and the rebuild command.
//
//go:embed fonts/maple-mono-regular.ttf
var monoTTF []byte
