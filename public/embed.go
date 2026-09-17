// Package public holds the web UI, embedded in the binary and served
// under /web/.
package public

import "embed"

//go:embed index.html *.js *.css
var FS embed.FS
