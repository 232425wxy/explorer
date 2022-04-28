package core

const (
	EventTypeMsg    = "event-msg"    // 用户发言
)

// 聊天室事件定义
type Event struct {
	Type      string `json:"type"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
	Text      string `json:"text"`
}
