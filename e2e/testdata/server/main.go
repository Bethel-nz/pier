// Command server is the app the end-to-end test asks Pier to run: it answers
// every request on 127.0.0.1:$PORT with a fixed line and the path.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := "127.0.0.1:" + os.Getenv("PORT")
	fmt.Println("listening on", addr)
	log.Fatal(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "pier e2e ok %s", r.URL.Path)
	})))
}
