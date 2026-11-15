// Package library is not a main package.
package library

import "net/http"

// Handler serves nothing.
var Handler = http.NotFoundHandler()
