// Package wonder embeds the default prompts and the public JSON schemas.
package wonder

import "embed"

//go:embed prompts schemas
var Assets embed.FS
