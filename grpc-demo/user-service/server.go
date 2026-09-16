package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	notifpb "github.com/demo/grpc-demo/proto/notifpb"
	userpb "github.com/demo/grpc-demo/proto/userpb"
)

type User struct {
	ID    string
	Name  string
	Email string
}

type userServer struct {
	userpb.UnimplementedUserServiceServer
	mu          sync.RWMutex
	users       map[string]*User
	counter     int
	notifClient notifpb.NotificationServiceClient
}

func (s *userServer) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	s.mu.Lock()
	s.counter++
	id := fmt.Sprintf("user-%d", s.counter)
	s.users[id] = &User{ID: id, Name: req.Name, Email: req.Email}
	s.mu.Unlock()

	log.Printf("[user-service] created user=%s name=%s", id, req.Name)

	resp, err := s.notifClient.SendNotification(ctx, &notifpb.SendNotificationRequest{
		UserId:  id,
		Message: fmt.Sprintf("Welcome %s! Your account has been created.", req.Name),
	})
	if err != nil {
		log.Printf("[user-service] notification failed: %v", err)
	} else {
		log.Printf("[user-service] notification sent notif_id=%s", resp.NotificationId)
	}

	return &userpb.CreateUserResponse{UserId: id, Message: "user created successfully"}, nil
}

func (s *userServer) GetUser(_ context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[req.UserId]
	if !ok {
		return nil, fmt.Errorf("user %s not found", req.UserId)
	}

	log.Printf("[user-service] GetUser id=%s", req.UserId)
	return &userpb.GetUserResponse{UserId: u.ID, Name: u.Name, Email: u.Email}, nil
}
