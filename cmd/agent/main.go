// Command agent runs the traffic-keeper agent on a managed VPS.
//
// At skeleton stage it only prints a banner and exits. Registration,
// heartbeat, policy polling and the upload executor will be added in
// subsequent tasks.
package main

import (
	"log"
)

func main() {
	log.Println("traffic-keeper agent starting (skeleton)")
	log.Println("registration / heartbeat / upload executor will be added next")
}
