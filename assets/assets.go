// Package assets embarque le gabarit HTML et le skill Claude Code.
package assets

import _ "embed"

//go:embed template.html
var Template string

//go:embed SKILL.md
var Skill string
