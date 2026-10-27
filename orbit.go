// Package orbit only carries the files of the repository root that the binary embeds.
package orbit

import _ "embed"

// Schema is orbit.schema.json: the JSON schema of orbit.yaml, also used by `orbit validate` to spot unknown keys.
//
//go:embed orbit.schema.json
var Schema []byte

// PluginSchema is orbit-plugin.schema.json: the JSON schema of the manifest of a plugin.
//
//go:embed orbit-plugin.schema.json
var PluginSchema []byte
