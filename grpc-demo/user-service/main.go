// Command user-service runs the user microservice.
//
// It plays three roles at once, which is the normal condition for a service in
// the middle of a system:
//
//	HTTP server on :8080   — what you curl; translates JSON to Go structs
//	gRPC server on :50051  — how other services would call it
//	gRPC client            — it calls notification-service when a user is created
//
// This file is wiring only: addresses, connections, server startup, and the
// HTTP handlers that adapt requests onto the methods in server.go. No business
// rules live here.
package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"

	notifpb "github.com/demo/grpc-demo/proto/notifpb"
	userpb "github.com/demo/grpc-demo/proto/userpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Where notification-service lives comes from the environment, never from
	// a hardcoded address. The same binary then runs unchanged against
	// localhost outside Docker, "notification-service:9090" under compose
	// (Docker's internal DNS resolves the service name), and a Kubernetes
	// service name in production. Only the environment changes.
	notifAddr := os.Getenv("NOTIFICATION_SERVICE_ADDR")
	if notifAddr == "" {
		notifAddr = "localhost:9090"
	}

	// Become a gRPC CLIENT of notification-service.
	//
	// NewClient does not dial. It builds a managed, reconnecting, load-balancing
	// handle and returns immediately; the TCP connection is established on the
	// first actual RPC. That is why this service starts cleanly even when
	// notification-service is not up yet, and why a bad address surfaces at
	// call time rather than here.
	//
	// insecure.NewCredentials() means no TLS. The name is deliberately
	// uncomfortable: it is fine on a private Docker network with nothing
	// published, and wrong for anything crossing a real network, where you want
	// mutual TLS so each service proves its identity to the other.
	notifConn, err := grpc.NewClient(notifAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to notification-service: %v", err)
	}
	defer notifConn.Close()

	// One connection, created once and shared for the process lifetime. A
	// ClientConn is safe for concurrent use by many goroutines — opening one
	// per request would pay the TCP and HTTP/2 handshake every time and throw
	// away all connection pooling.
	notifClient := notifpb.NewNotificationServiceClient(notifConn)

	// Dependency injection: the client is built here and handed to the server.
	// That is why server.go has no idea where notification-service lives, and
	// why a test can pass in a fake client with no network at all.
	srv := &userServer{
		users:       make(map[string]*User),
		notifClient: notifClient,
	}

	// Run the gRPC server in a goroutine, because Serve and ListenAndServe both
	// block forever and both must be listening. One takes a goroutine, the
	// other holds the main thread. Either calling log.Fatal kills the whole
	// process, which is what you want: a half-dead service is worse than a dead
	// one, because load balancers keep sending it traffic.
	//
	// Nothing in this demo actually calls :50051 — it is here to show the shape
	// of a service reachable both ways, and is the entry point a future
	// order-service would use.
	go func() {
		lis, err := net.Listen("tcp", ":50051")
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		s := grpc.NewServer()
		userpb.RegisterUserServiceServer(s, srv)
		log.Println("user-service gRPC listening on :50051")
		log.Fatal(s.Serve(lis))
	}()

	// The HTTP handlers below are adapters, nothing more: decode JSON, call a
	// method on srv, encode the result. They invoke srv directly as ordinary
	// in-process Go calls — no gRPC hop, because the same method serves both
	// transports. The single real gRPC network call in this system happens
	// inside CreateUser, and in the GetNotifications handler below.
	//
	// The JSON keys come from the struct tags protoc put on the generated
	// types, which is why responses read {"user_id": ...} and not {"UserId":
	// ...}. Those tags also carry omitempty, so zero-valued fields disappear
	// from the response entirely.

	// POST /users — create a user, which also triggers the notification.
	http.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		// r.Context() is passed through so that a client hanging up cancels the
		// work downstream, all the way into the call to notification-service.
		resp, err := srv.CreateUser(r.Context(), &userpb.CreateUserRequest{
			Name:  body.Name,
			Email: body.Email,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// GET /users/{id} — read a user back out of this service's own store.
	http.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		resp, err := srv.GetUser(r.Context(), &userpb.GetUserRequest{UserId: id})
		if err != nil {
			// Mapping every failure to 404 is a demo shortcut. It works only
			// because GetUser has exactly one failure mode; a real handler
			// inspects the gRPC status code to choose the HTTP status.
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// GET /notifications/{user_id} — proof that the cross-service call worked.
	//
	// This handler talks to notifClient directly rather than going through srv,
	// because user-service owns no notification state to add: it is a pure
	// pass-through that fetches over gRPC and re-encodes the answer as JSON.
	http.HandleFunc("GET /notifications/{user_id}", func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("user_id")
		resp, err := notifClient.GetNotifications(r.Context(), &notifpb.GetNotificationsRequest{
			UserId: userID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	log.Println("user-service HTTP listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
