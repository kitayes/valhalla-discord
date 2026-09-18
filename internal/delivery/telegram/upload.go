package telegram

import (
	"context"
	"errors"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// stagingChatID picks the admin chat used to turn bytes into file IDs.
//
// The lowest ID wins rather than whichever the map yields first, so the same
// chat is used every time and a misconfigured admin is easy to spot.
func (b *Bot) stagingChatID() int64 {
	var chosen int64
	for id := range b.adminIDs {
		if chosen == 0 || id < chosen {
			chosen = id
		}
	}
	return chosen
}

// UploadPhoto stores a screenshot in Telegram and returns the file ID.
//
// The Bot API has no upload-only endpoint: bytes earn a file ID only by being
// sent to a chat. So the photo is posted to an admin chat silently and deleted
// immediately afterwards — a file ID outlives the message that carried it, so
// the referee's copy still resolves while nobody is left with a stray photo in
// their history.
func (b *Bot) UploadPhoto(ctx context.Context, data []byte, filename string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("telegram: refusing to upload an empty screenshot")
	}
	chatID := b.stagingChatID()
	if chatID == 0 {
		return "", errors.New("telegram: no admin chat configured to stage screenshot uploads")
	}

	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{Name: filename, Bytes: data})
	msg.DisableNotification = true
	sent, err := b.bot.Send(msg)
	if err != nil {
		return "", fmt.Errorf("telegram: failed to stage screenshot: %w", err)
	}
	if len(sent.Photo) == 0 {
		return "", errors.New("telegram: staged screenshot came back without any photo sizes")
	}

	// Telegram lists renditions smallest first; the last one is the full-size
	// image the referee needs to read a scoreboard.
	fileID := sent.Photo[len(sent.Photo)-1].FileID

	if _, err := b.bot.Request(tgbotapi.NewDeleteMessage(chatID, sent.MessageID)); err != nil {
		// Not fatal: the file ID is already valid, the admin just keeps a copy.
		b.logger.Warn("telegram: failed to clean up staged screenshot %d in chat %d: %v", sent.MessageID, chatID, err)
	}
	return fileID, nil
}
