// Command master runs the traffic-keeper master server.
//
// At skeleton stage it only exposes a /healthz endpoint. The control plane
// (node management, policy dispatch, single-user auth) and the data plane
// (HTTP upload receiver that counts bytes and discards bodies) will be added
// in subsequent tasks.
package main

import (
	"log"
	"net/http"
)

func main() {
	addr := ":8080"

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("traffic-keeper master listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("master server error: %v", err)
	}
}
