// server.go holds the business logic for notification-service.
//
// Note what is absent from this file: no TCP, no HTTP/2, no Protobuf encoding,
// no ports, no Docker. Every method takes a Go struct and returns a Go struct.
// That separation is deliberate — it means these methods can be unit-tested by
// calling them directly, with no server running and no network involved.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	pb "github.com/demo/grpc-demo/proto/notifpb"
)

// notifServer implements pb.NotificationServiceServer.
//
// The method signatures below must match the generated interface exactly; if
// they do not, this type stops satisfying the interface and the build fails.
// That compile error is the .proto contract being enforced by the Go compiler.
type notifServer struct {
	// Embedding the generated Unimplemented type supplies a default
	// "not implemented" method for every RPC in the contract. Two benefits:
	// RPCs can be implemented one at a time and still compile, and when someone
	// adds an RPC to notification.proto next month this service keeps building
	// — callers of the new method get a clean Unimplemented error instead of
	// the whole service failing to compile. That is what lets the contract
	// evolve without an all-services-at-once deploy.
	pb.UnimplementedNotificationServiceServer

	// gRPC serves each request in its own goroutine, so two calls can touch
	// this state genuinely in parallel. Without the mutex, concurrent appends
	// race: lost writes, a corrupted slice header, or a crash. RWMutex rather
	// than Mutex because reads may safely run concurrently with each other —
	// only writes need exclusivity.
	mu sync.RWMutex

	// Demo-only storage. In memory means it is lost on restart, and it only
	// works because there is exactly one replica: run two copies behind a load
	// balancer and each would hold a different half of the data. Real state
	// belongs in a database all replicas share.
	notifications []*pb.Notification

	// Likewise demo-only. Two replicas would hand out identical IDs; production
	// uses UUIDs or lets the database generate them.
	counter int
}

// SendNotification stores a notification for a user and returns its new ID.
// Called over gRPC by user-service when a user is created.
func (s *notifServer) SendNotification(_ context.Context, req *pb.SendNotificationRequest) (*pb.SendNotificationResponse, error) {
	// Deferring the unlock is fine here because nothing between the lock and
	// the return touches the network — this is all in-memory work. Compare
	// user-service's CreateUser, which must unlock before its outbound RPC.
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	id := fmt.Sprintf("notif-%d", s.counter)

	s.notifications = append(s.notifications, &pb.Notification{
		NotificationId: id,
		UserId:         req.UserId,
		Message:        req.Message,
	})

	log.Printf("[notification-service] stored notif=%s user=%s msg=%q", id, req.UserId, req.Message)
	return &pb.SendNotificationResponse{NotificationId: id}, nil
}

// GetNotifications returns every notification belonging to one user.
// Reached from outside via user-service's GET /notifications/{user_id}.
func (s *notifServer) GetNotifications(_ context.Context, req *pb.GetNotificationsRequest) (*pb.GetNotificationsResponse, error) {
	// RLock, not Lock: several concurrent readers are safe, and blocking them
	// against each other would serialize reads for no reason.
	s.mu.RLock()
	defer s.mu.RUnlock()

	// A linear scan is acceptable for a demo holding a handful of records. A
	// real implementation queries an index rather than walking every row.
	var result []*pb.Notification
	for _, n := range s.notifications {
		if n.UserId == req.UserId {
			result = append(result, n)
		}
	}

	log.Printf("[notification-service] GetNotifications user=%s found=%d", req.UserId, len(result))
	return &pb.GetNotificationsResponse{Notifications: result}, nil
}
