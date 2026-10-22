// Package botx is a client for the eXpress BotX API: chats, chat membership,
// user lookup, and sending and editing messages on behalf of a bot.
//
// Build a Client with the bot's ID and secret key; it obtains the bot token
// itself and renews it once when BotX answers a call with HTTP 401:
//
//	c, err := botx.New("express.example.com", botID, secret)
//	if err != nil {
//		return err
//	}
//	chatID, err := c.CreateChat(ctx, &botx.CreateChatRequest{
//		Name:     "alerts-demo",
//		ChatType: botx.ChatTypeGroup,
//		Members:  []string{userHUID},
//	})
//
// # Errors
//
// A response that is not a success is an *APIError carrying the HTTP status and
// the BotX status and reason, also when its body could not be read in full.
// Match it with errors.Is against ErrUnauthorized, ErrNotFound, ErrNoPermission,
// ErrAdminsNotChanged and ErrInvalidResponse. One error can match several:
// a 2xx response with status "error" and reason "no_permission_for_operation"
// matches both ErrNoPermission and ErrInvalidResponse, so check the specific
// sentinels first. ErrNotFound covers every reason ending in "_not_found";
// look at Op and Reason before acting on it. A 403 with another reason is not
// ErrNoPermission. Network failures are returned wrapped as they come from net/http.
//
// Errors of obtaining a token never contain the signature or the secret: they
// are masked in the message, in the fields of *APIError and in wrapped errors,
// while context.Canceled and context.DeadlineExceeded stay detectable.
//
// A failed Send or EditMessage with ErrInvalidResponse may still have been
// applied by BotX; do not repeat it automatically.
//
// The package writes no logs of its own; diagnostics come only through returned errors.
//
// # BotX behavior worth knowing
//
//   - The bot that creates a chat is its only admin; members given to CreateChat are not.
//   - AddUsers for a current member succeeds; AddAdmins for a current admin fails with ErrAdminsNotChanged.
//   - BotX has no calls to delete or archive a chat, or to revoke admin rights.
//   - A bot that removes itself from a chat cannot manage it any more (ErrNoPermission)
//     but still sees it in ListChats and GetChatInfo; check ChatDetails.Members.
package botx
