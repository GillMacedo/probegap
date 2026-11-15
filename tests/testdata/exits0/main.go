// Command exits0 returns before starting any server.
package main

import (
	"fmt"
	"net/http"
)

func main() {
	_ = http.NewServeMux()
	fmt.Println("nothing to do")
}
