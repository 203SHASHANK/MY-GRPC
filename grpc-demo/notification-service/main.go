package main

import (
	"log"
	"net"

	pb "github.com/demo/grpc-demo/proto/notifpb"
	"google.golang.org/grpc"
)

func main() {
	lis, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterNotificationServiceServer(s, &notifServer{})

	log.Println("notification-service gRPC listening on :9090")
	log.Fatal(s.Serve(lis))
}
