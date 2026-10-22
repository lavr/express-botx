package botx_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/lavr/express-botx/botx"
)

const (
	botID    = "8b0f6c1e-3d2a-4f5b-9c7d-1e2f3a4b5c6d"
	aliceID  = "1f2e3d4c-5b6a-4978-8a9b-0c1d2e3f4a5b"
	bobID    = "2a3b4c5d-6e7f-4a8b-9c0d-1e2f3a4b5c6d"
	newChat  = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"
	okResult = `{"status":"ok","result":`
)

// fakeServer stands in for BotX in the examples.
func fakeServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = fmt.Fprint(w, okResult+`"token"}`)
		case r.URL.Path == "/api/v3/botx/chats/create":
			_, _ = fmt.Fprint(w, okResult+`{"chat_id":"`+newChat+`"}}`)
		case r.URL.Path == "/api/v3/botx/chats/info":
			_, _ = fmt.Fprint(w, okResult+`{"group_chat_id":"`+newChat+`","name":"alerts-demo","chat_type":"group_chat",`+
				`"creator":"`+botID+`","members":[`+
				`{"user_huid":"`+botID+`","user_kind":"botx","admin":true},`+
				`{"user_huid":"`+aliceID+`","user_kind":"user","admin":false}]}}`)
		case r.URL.Path == "/api/v3/botx/chats/add_user":
			_, _ = fmt.Fprint(w, okResult+`true}`)
		case r.URL.Path == "/api/v3/botx/chats/add_admin":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"status":"error","reason":"admins_not_changed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

// Create a chat, then bring its members in line with the wanted set.
func Example() {
	srv := fakeServer()
	defer srv.Close()
	ctx := context.Background()

	c, err := botx.New("express.example.com", botID, "secret", botx.WithBaseURL(srv.URL))
	if err != nil {
		log.Fatal(err)
	}

	chatID, err := c.CreateChat(ctx, &botx.CreateChatRequest{
		Name:     "alerts-demo",
		ChatType: botx.ChatTypeGroup,
		Members:  []string{aliceID},
	})
	if err != nil {
		log.Fatal(err)
	}

	info, err := c.GetChatInfo(ctx, chatID)
	if err != nil {
		log.Fatal(err)
	}
	// Being the creator is not enough: a bot that left the chat still is its
	// creator. It can manage the chat only while it is a member and an admin.
	present := make(map[string]bool)
	canManage := false
	for _, m := range info.Members {
		present[m.UserHUID] = true
		if strings.EqualFold(m.UserHUID, botID) && m.Admin {
			canManage = true
		}
	}
	if !canManage {
		log.Fatal("this bot is not an admin of the chat")
	}
	ours := info.Creator != nil && strings.EqualFold(*info.Creator, botID)
	var missing []string
	for _, huid := range []string{aliceID, bobID} {
		if !present[huid] {
			missing = append(missing, huid)
		}
	}
	if len(missing) > 0 {
		if err := c.AddUsers(ctx, chatID, missing); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("chat:", chatID)
	fmt.Println("created by this bot:", ours)
	fmt.Println("added:", len(missing))
	// Output:
	// chat: 3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f
	// created by this bot: true
	// added: 1
}

// AddAdmins is not idempotent: granting rights to an admin again is an error to tell apart.
func ExampleClient_AddAdmins() {
	srv := fakeServer()
	defer srv.Close()

	c := botx.NewWithToken(srv.URL, "token")
	err := c.AddAdmins(context.Background(), newChat, []string{aliceID})
	switch {
	case errors.Is(err, botx.ErrAdminsNotChanged):
		fmt.Println("already an admin")
	case err != nil:
		log.Fatal(err)
	}
	// Output: already an admin
}
