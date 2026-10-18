// Package store owns go-whassup's own chat and message schema.
//
// whatsmeow persists only session keys, contacts and similar protocol state;
// it does NOT store message content. Chats and messages are written here from
// history-sync and live events, using modernc.org/sqlite (pure Go, no cgo).
package store
