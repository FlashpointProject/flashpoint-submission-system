package integration_tests

import (
	"fmt"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"time"
)

type MockDiscordRoleReader struct{}

func (m *MockDiscordRoleReader) GetFlashpointRoles() ([]types.DiscordRole, error) {
	return []types.DiscordRole{}, nil
}

func (m *MockDiscordRoleReader) GetFlashpointRoleIDsForUser(uid int64) ([]string, error) {
	return []string{"1"}, nil // Return some dummy role ID
}

func (m *MockDiscordRoleReader) GetFlashpointUserInfo(uid int64, roles []types.DiscordRole) (*types.FlashpointDiscordUser, error) {
	return nil, nil
}

func (m *MockDiscordRoleReader) GetJoinedAtForUser(uid int64) (time.Time, error) {
	return time.Now().Add(-24 * time.Hour * 365), nil // Joined 1 year ago
}

type MockDiscordNotificationSender struct{}

func (m *MockDiscordNotificationSender) SendNotification(msg, notificationType string) error {
	fmt.Printf("Mock Notification: %s - %s\n", notificationType, msg)
	return nil
}

func (m *MockDiscordNotificationSender) SendCurationFeedMessage(msg string) error {
	fmt.Printf("Mock Curation Feed: %s\n", msg)
	return nil
}
