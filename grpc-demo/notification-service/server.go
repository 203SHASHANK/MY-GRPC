package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	pb "github.com/demo/grpc-demo/proto/notifpb"
)

type notifServer struct {
	pb.UnimplementedNotificationServiceServer
	mu            sync.RWMutex
	notifications []*pb.Notification
	counter       int
}

func (s *notifServer) SendNotification(_ context.Context, req *pb.SendNotificationRequest) (*pb.SendNotificationResponse, error) {
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

func (s *notifServer) GetNotifications(_ context.Context, req *pb.GetNotificationsRequest) (*pb.GetNotificationsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*pb.Notification
	for _, n := range s.notifications {
		if n.UserId == req.UserId {
			result = append(result, n)
		}
	}

	log.Printf("[notification-service] GetNotifications user=%s found=%d", req.UserId, len(result))
	return &pb.GetNotificationsResponse{Notifications: result}, nil
}
