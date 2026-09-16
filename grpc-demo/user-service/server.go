// server.go holds the business logic for user-service.
//
// As in notification-service, this file contains no transport code: no HTTP, no
// TCP, no Protobuf encoding, no ports. Methods take Go structs and return Go
// structs, which is why the same CreateUser serves both the HTTP handler in
// main.go and any future gRPC caller on :50051.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	notifpb "github.com/demo/grpc-demo/proto/notifpb"
	userpb "github.com/demo/grpc-demo/proto/userpb"
)

// User is this service's internal representation, kept separate from the
// generated userpb types on purpose. The proto types are the wire contract; the
// domain model is free to diverge from them (to carry a password hash or a
// created-at timestamp that no caller should ever see, for instance).
type User struct {
	ID    string
	Name  string
	Email string
}

// userServer implements userpb.UserServiceServer.
type userServer struct {
	// Supplies a default implementation for every RPC in user.proto, so new
	// RPCs added to the contract do not break this build. See the longer note
	// in notification-service/server.go.
	userpb.UnimplementedUserServiceServer

	// Guards users and counter. gRPC — and net/http — run every request in its
	// own goroutine, so concurrent access here is the default, not an edge case.
	mu sync.RWMutex

	// Demo-only in-memory store: lost on restart, and correct only while there
	// is a single replica. Production uses a shared database.
	users   map[string]*User
	counter int

	// Injected by main.go. Holding the interface rather than a concrete type is
	// what makes this struct testable: a fake implementation satisfies it
	// without any network.
	notifClient notifpb.NotificationServiceClient
}

// CreateUser stores a new user and then notifies them, by calling
// notification-service over gRPC.
//
// The call to s.notifClient.SendNotification below reads like an ordinary local
// method call. It is not: between the parentheses, a struct is encoded to
// Protobuf binary, pushed over HTTP/2 to another container, decoded, executed,
// and the answer returned the same way. None of that machinery is written by
// hand — it comes from the generated *.pb.go files.
func (s *userServer) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	s.mu.Lock()
	s.counter++
	id := fmt.Sprintf("user-%d", s.counter)
	s.users[id] = &User{ID: id, Name: req.Name, Email: req.Email}
	// Unlocked explicitly here rather than with defer, and that placement
	// matters. A deferred unlock would hold the lock across the network call
	// below, queueing every other CreateUser in this process behind a remote
	// service we do not control. The rule: lock, mutate memory, unlock, then
	// do I/O — never hold a lock across a network call.
	s.mu.Unlock()

	log.Printf("[user-service] created user=%s name=%s", id, req.Name)

	// The one real cross-service call in this system.
	//
	// ctx is passed straight through, so a cancellation or deadline upstream
	// propagates downstream: if the HTTP client hangs up, this in-flight call
	// is cancelled too. Never substitute context.Background() to silence an
	// error — that switches off cancellation for the whole chain.
	//
	// Missing here, and the single most valuable line to add: an explicit
	// deadline, so a hung dependency cannot hang this service with it.
	//
	//   ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	//   defer cancel()
	resp, err := s.notifClient.SendNotification(ctx, &notifpb.SendNotificationRequest{
		UserId:  id,
		Message: fmt.Sprintf("Welcome %s! Your account has been created.", req.Name),
	})
	if err != nil {
		// Logged, not returned — a deliberate decision. The user was already
		// created, so failing the whole request would be a lie, and a caller
		// retrying would create a duplicate user. The cost is real: this
		// notification is now silently lost forever. A production version
		// writes it to an outbox table or a queue so a retry can pick it up.
		//
		// The question to ask at every cross-service call is whether the call
		// is essential to the operation or merely desirable. Essential means
		// propagate the error; desirable means log it and compensate elsewhere.
		// What you must not do is decide this by accident.
		log.Printf("[user-service] notification failed: %v", err)
	} else {
		log.Printf("[user-service] notification sent notif_id=%s", resp.NotificationId)
	}

	return &userpb.CreateUserResponse{UserId: id, Message: "user created successfully"}, nil
}

// GetUser looks up a user by ID.
func (s *userServer) GetUser(_ context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[req.UserId]
	if !ok {
		// A plain Go error reaches a gRPC caller as codes.Unknown, which is
		// indistinguishable from a crash and tells the caller nothing about
		// whether retrying is worthwhile. Production returns a real status:
		//
		//   status.Errorf(codes.NotFound, "user %s not found", req.UserId)
		//
		// NotFound says "do not retry, this will never succeed"; Unavailable
		// says "retry with backoff". Unknown leaves clients guessing.
		return nil, fmt.Errorf("user %s not found", req.UserId)
	}

	log.Printf("[user-service] GetUser id=%s", req.UserId)

	// The domain User is mapped back onto the generated wire type here. That
	// translation is the boundary: internal fields stay internal.
	return &userpb.GetUserResponse{UserId: u.ID, Name: u.Name, Email: u.Email}, nil
}
