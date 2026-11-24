// Command client watches the inventory service over a long-lived stream
// that is quiet between changes, and wants dead connections noticed fast.
package main

import (
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const pingEvery = 15 * time.Second

func main() {
	conn, err := grpc.NewClient("inventory:7070",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                pingEvery,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
}
