package config

// DefaultMaxMessageLength is the documented BotX cap on notification body
// length, counted in runes.
const DefaultMaxMessageLength = 4096

// DefaultTruncateSuffix marks a body that was cut to fit the cap.
const DefaultTruncateSuffix = "…"

// BotDeliveryPolicy resolves the body cap configured for one bot. It is read
// back by bot name rather than projected into the flat per-bot fields that
// ForBot and ApplyChatBot populate, so a new projection site cannot silently
// drop it.
//
// A negative limit is treated as disabled rather than rejected, so a typo
// cannot hand the send builder a negative budget.
func (c *Config) BotDeliveryPolicy(botName string) (int, string) {
	limit := DefaultMaxMessageLength
	suffix := DefaultTruncateSuffix

	bot, ok := c.Bots[botName]
	if !ok {
		return limit, suffix
	}
	if bot.MaxMessageLength != nil {
		limit = *bot.MaxMessageLength
		if limit < 0 {
			limit = 0
		}
	}
	if bot.TruncateSuffix != nil {
		suffix = *bot.TruncateSuffix
	}
	return limit, suffix
}
