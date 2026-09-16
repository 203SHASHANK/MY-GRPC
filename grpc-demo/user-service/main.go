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
	notifAddr := os.Getenv("NOTIFICATION_SERVICE_ADDR")
	if notifAddr == "" {
		notifAddr = "localhost:9090"
	}

	notifConn, err := grpc.NewClient(notifAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to notification-service: %v", err)
	}
	defer notifConn.Close()

	notifClient := notifpb.NewNotificationServiceClient(notifConn)

	srv := &userServer{
		users:       make(map[string]*User),
		notifClient: notifClient,
	}

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

	http.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
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

	http.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		resp, err := srv.GetUser(r.Context(), &userpb.GetUserRequest{UserId: id})
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

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
