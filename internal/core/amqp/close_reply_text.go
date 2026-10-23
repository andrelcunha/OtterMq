package amqp

import "unicode/utf8"

// Close reply texts are informational and can include a valid 255-byte name
// plus the broker's description. Keep them encodable without splitting a rune;
// other short strings must still be rejected by EncodeShortStr when oversized.
func truncateCloseReplyText(text string) string {
	const limit = 255
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}
