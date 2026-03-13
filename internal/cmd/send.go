package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lavr/express-bot/internal/botapi"
	"github.com/lavr/express-bot/internal/config"
	"github.com/lavr/express-bot/internal/input"
)

func runSend(args []string, deps Deps) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var from string
	var filePath string
	var fileName string
	var status string
	var silent bool
	var stealth bool
	var forceDND bool
	var noNotify bool
	var metadata string

	globalFlags(fs, &flags)
	fs.StringVar(&flags.ChatID, "chat-id", "", "target chat UUID or alias")
	fs.StringVar(&from, "body-from", "", "read message text from file")
	fs.StringVar(&filePath, "file", "", "path to file to attach (or - for stdin)")
	fs.StringVar(&fileName, "file-name", "", "file name (required when --file -)")
	fs.StringVar(&status, "status", "ok", "notification status: ok or error")
	fs.BoolVar(&silent, "silent", false, "no push notification to recipient")
	fs.BoolVar(&stealth, "stealth", false, "stealth mode (message visible only to bot)")
	fs.BoolVar(&forceDND, "force-dnd", false, "deliver even if recipient has DND")
	fs.BoolVar(&noNotify, "no-notify", false, "do not send notification at all")
	fs.StringVar(&metadata, "metadata", "", "arbitrary JSON for notification.metadata")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, `Usage: express-bot send [options] [message]

Send a message and/or file to an eXpress chat.

Message sources (in priority order):
  --body-from FILE   Read message from file
  [message]     Positional argument
  stdin         Pipe input (auto-detected, only if --file is not -)

Options:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}
	if err := cfg.RequireChatID(); err != nil {
		return err
	}

	// Validate status
	if status != "ok" && status != "error" {
		return fmt.Errorf("--status must be ok or error, got %q", status)
	}

	// Read file attachment if requested
	var fileAttachment *botapi.SendFile
	if filePath != "" {
		var data []byte
		var name string

		if filePath == "-" {
			// Read file from stdin
			if fileName == "" {
				return fmt.Errorf("--file-name is required when using --file -")
			}
			data, err = io.ReadAll(deps.Stdin)
			if err != nil {
				return fmt.Errorf("reading file from stdin: %w", err)
			}
			if len(data) == 0 {
				return fmt.Errorf("empty file from stdin")
			}
			name = fileName
		} else {
			data, err = os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file %q: %w", filePath, err)
			}
			name = filepath.Base(filePath)
			if fileName != "" {
				name = fileName
			}
		}

		fileAttachment = botapi.BuildFileAttachment(name, data)
	}

	// Read message text (optional if file is present)
	var message string
	stdinAvailable := filePath != "-" // stdin already consumed by file
	if from != "" || fs.NArg() > 0 {
		message, err = input.ReadMessage(from, fs.Args(), deps.Stdin, deps.IsTerminal)
		if err != nil {
			return err
		}
	} else if stdinAvailable && !deps.IsTerminal {
		message, err = input.ReadMessage("", nil, deps.Stdin, false)
		if err != nil {
			// If file is present, empty stdin is ok
			if fileAttachment != nil {
				message = ""
			} else {
				return err
			}
		}
	}

	// Must have at least text or file
	if message == "" && fileAttachment == nil {
		return fmt.Errorf("nothing to send: provide a message and/or --file")
	}

	// Build SendRequest
	sr := &botapi.SendRequest{
		GroupChatID: cfg.ChatID,
	}

	if message != "" {
		sr.Notification = &botapi.SendNotification{
			Status: status,
			Body:   message,
		}
		if silent {
			sr.Notification.Opts = &botapi.NotificationMsgOpts{
				SilentResponse: true,
			}
		}
		if metadata != "" {
			raw := json.RawMessage(metadata)
			if !json.Valid(raw) {
				return fmt.Errorf("--metadata is not valid JSON")
			}
			sr.Notification.Metadata = raw
		}
	}

	if fileAttachment != nil {
		sr.File = fileAttachment
	}

	if stealth || forceDND || noNotify {
		sr.Opts = &botapi.SendOpts{
			StealthMode: stealth,
		}
		if forceDND || noNotify {
			sr.Opts.NotificationOpts = &botapi.DeliveryOpts{
				ForceDND: forceDND,
			}
			if noNotify {
				f := false
				sr.Opts.NotificationOpts.Send = &f
			}
		}
	}

	// Authenticate and send
	tok, cache, err := authenticate(cfg)
	if err != nil {
		return err
	}

	client := botapi.NewClient(cfg.Host, tok)
	err = client.Send(context.Background(), sr)
	if err != nil {
		if errors.Is(err, botapi.ErrUnauthorized) {
			tok, err = refreshToken(cfg, cache)
			if err != nil {
				return fmt.Errorf("refreshing token: %w", err)
			}
			client.Token = tok
			err = client.Send(context.Background(), sr)
		}
		if err != nil {
			return fmt.Errorf("sending: %w", err)
		}
	}

	return nil
}
