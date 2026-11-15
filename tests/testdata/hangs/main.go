// Command hangs waits forever before it listens, like a program retrying a
// dependency.
package main

import (
	"net/http"
	"time"
)

func main() {
	time.Sleep(time.Hour)
	http.ListenAndServe(":8080", nil)
}
