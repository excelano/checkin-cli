package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/chzyer/readline"
)

type Item struct {
	Kind  string // "email" or "chat"
	Email *Email
	Chat  *ChatMessage
}

var debug bool

// Populated at build time via -ldflags by goreleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--debug" {
		debug = true
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-V") {
		fmt.Println(versionLine())
		os.Exit(0)
	}
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Println("Usage: blick [command]")
		fmt.Println()
		fmt.Println("Check unread Outlook emails, Teams chats, and your next meeting.")
		fmt.Println("With no command, opens the interactive dashboard.")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  today                          Show today's calendar and exit")
		fmt.Println("  inbox [days]                   Show today's chats & emails, read included (days back)")
		fmt.Println("  search [--from|--subject|--text|words]  Search mail (KQL); prints matches")
		fmt.Println("  join                           Open the current or next online meeting")
		fmt.Println("  presence [state]               Set Teams presence (available/busy/dnd/brb/away/offline)")
		fmt.Println("  contacts ...                   Manage the address book (list, add, remove, show, seed)")
		fmt.Println("  email <contact> [--subject]    Compose and send a message (--cc, --bcc, --attach)")
		fmt.Println("  chat <contact> [--topic]       Send a Teams chat (group when >1 contact)")
		fmt.Println("  logout                         Clear cached credentials")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  -h, --help      Show this help")
		fmt.Println("  -V, --version   Show version")
		fmt.Println("      --debug     Verbose Graph request logging")
		fmt.Println()
		fmt.Println("Config:   ~/.config/blick/config.json")
		fmt.Println("          {\"client_id\": \"...\", \"tenant_id\": \"...\"}")
		fmt.Println("Contacts: ~/.config/blick/contacts.json")
		fmt.Println()
		fmt.Println("See README.md for Azure AD app registration.")
		os.Exit(0)
	}

	// `logout` is the inverse of authentication, so it explicitly must
	// not gate on auth. Handle before the device-code flow.
	if len(os.Args) > 1 && os.Args[1] == "logout" {
		runLogout()
		return
	}

	// Local-only contacts commands (everything except `seed`) don't need
	// Graph or auth — handle them before the device-code flow so the user
	// can curate the address book offline or pre-auth.
	if len(os.Args) > 1 && os.Args[1] == "contacts" {
		if needsGraphForContacts(os.Args[2:]) {
			// fall through to auth, dispatch happens below
		} else {
			runContacts(nil, os.Args[2:])
			return
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	tok, err := authenticate(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Save refreshed token
	_ = saveCachedToken(tok)

	client, err := NewGraphClient(cfg, tok)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] == "today" {
		showToday(client)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "join" {
		runJoin(client)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "inbox" {
		showInbox(client, cfg.EnableTeams, os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "presence" {
		runPresence(client, os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "search" {
		runSearch(client, os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "contacts" {
		runContacts(client, os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "email" {
		runEmail(client, os.Args[2:])
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "chat" {
		if !cfg.EnableTeams {
			fmt.Fprintln(os.Stderr, "Teams is disabled in your config (\"enable_teams\": false). Set it to true and re-authenticate to use chat.")
			os.Exit(1)
		}
		runChat(client, os.Args[2:])
		return
	}

	items := fetchAndDisplay(client, cfg.EnableTeams, true)
	renderHelp()

	if err := setupReadline(replHistoryPath(), loadContactKeys()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			// Ctrl-C at the top-level prompt: empty buffer exits
			// (matches the old sigCh-fires-and-we-return behavior).
			// A partial line just gets discarded — readline already
			// rendered ^C, redraw and continue.
			if len(line) == 0 {
				return
			}
			continue
		}
		if err != nil {
			// io.EOF (Ctrl-D) — clean exit.
			return
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// String-argument commands sit outside parseCommand's (cmd, int)
		// shape — peel them off before the int-arg dispatcher.
		fields := strings.Fields(input)
		if len(fields) > 0 && (fields[0] == "email" || fields[0] == "e") {
			replEmail(client, fields[1:])
			continue
		}
		if len(fields) > 0 && (fields[0] == "chat" || fields[0] == "c") {
			if !cfg.EnableTeams {
				fmt.Printf("  %sTeams is disabled in your config.%s\n", dim, reset)
				continue
			}
			replChat(client, fields[1:])
			continue
		}
		if len(fields) > 0 && fields[0] == "attach" {
			replAttach(client, items, fields[1:])
			continue
		}
		if len(fields) > 0 && fields[0] == "forward" {
			replForward(client, items, fields[1:])
			continue
		}
		if len(fields) > 0 && (fields[0] == "presence" || fields[0] == "p") {
			replPresence(client, fields[1:])
			continue
		}
		if len(fields) > 0 && (fields[0] == "inbox" || fields[0] == "i") {
			items = replInbox(client, cfg.EnableTeams, fields[1:], items)
			continue
		}
		if len(fields) > 0 && fields[0] == "search" {
			items = replSearch(client, fields[1:], items)
			continue
		}

		cmd, n := parseCommand(input)

		switch cmd {
		case "quit":
			return

		case "refresh":
			items = fetchAndDisplay(client, cfg.EnableTeams, false)
			renderHelp()

		case "exit":
			markAllRead(client, items)
			return

		case "help":
			renderFullHelp()

		case "today":
			showToday(client)

		case "join":
			replJoin(client)

		case "view":
			if n < 1 || n > len(items) {
				fmt.Printf("  Invalid item: %s\n", input)
				continue
			}
			viewItem(client, items[n-1], n, false)

		case "view-full":
			if n < 1 || n > len(items) {
				fmt.Printf("  Invalid item: %s\n", input)
				continue
			}
			viewItem(client, items[n-1], n, true)

		case "reply":
			if n < 1 || n > len(items) {
				fmt.Printf("  Invalid item: %s\n", input)
				continue
			}
			replyTo(client, items[n-1])

		case "done":
			if n < 1 || n > len(items) {
				fmt.Printf("  Invalid item: %s\n", input)
				continue
			}
			markRead(client, items[n-1])

		default:
			fmt.Printf("  Unknown command. Type %sh%s for help.\n", cyan, reset)
		}
	}
}

// parseCommand normalizes REPL input into a (command, item-number) pair.
// Item-number is -1 when the command doesn't take one. Returns
// ("unknown", -1) when the input doesn't map to any command.
//
// Short forms map to their long-form equivalents so the dispatcher only
// has to handle one name per action:
//
//	r5, r 5, reply 5  -> ("reply", 5)
//	d3, d 3, done 3   -> ("done",  3)
//	7, view 7         -> ("view",  7)
//	r, refresh        -> ("refresh", -1)
//	x, exit           -> ("exit",   -1)
//	q, quit           -> ("quit",   -1)
//	h, help           -> ("help",   -1)
//	t, today          -> ("today",  -1)
func parseCommand(input string) (string, int) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "unknown", -1
	}
	first := fields[0]
	rest := fields[1:]

	if num, err := strconv.Atoi(first); err == nil {
		return "view", num
	}

	// ed/ved-style: address first, then action letter — e.g. 5r, 5d, 5f.
	// This is the canonical form shown in the overview help.
	if len(first) > 1 {
		last := first[len(first)-1]
		if last == 'r' || last == 'd' || last == 'f' {
			if num, err := strconv.Atoi(first[:len(first)-1]); err == nil {
				switch last {
				case 'r':
					return "reply", num
				case 'd':
					return "done", num
				case 'f':
					return "view-full", num
				}
			}
		}
	}

	// Legacy: letter first, then number — e.g. r5, d5. Still accepted
	// so muscle memory from before the ed-style flip keeps working.
	if len(first) > 1 {
		if num, err := strconv.Atoi(first[1:]); err == nil {
			switch first[0] {
			case 'r':
				return "reply", num
			case 'd':
				return "done", num
			}
		}
	}

	switch first {
	case "q":
		return "quit", -1
	case "x":
		return "exit", -1
	case "h":
		return "help", -1
	case "t":
		return "today", -1
	case "j":
		return "join", -1
	case "r":
		if len(rest) == 0 {
			return "refresh", -1
		}
		if num, err := strconv.Atoi(rest[0]); err == nil {
			return "reply", num
		}
		return "unknown", -1
	case "d":
		if len(rest) == 0 {
			return "unknown", -1
		}
		if num, err := strconv.Atoi(rest[0]); err == nil {
			return "done", num
		}
		return "unknown", -1
	}

	switch first {
	case "view", "reply", "done":
		if len(rest) == 0 {
			return "unknown", -1
		}
		num, err := strconv.Atoi(rest[0])
		if err != nil {
			return "unknown", -1
		}
		// `view N full` → "view-full" so the dispatcher renders
		// without folding the quoted history.
		if first == "view" && len(rest) >= 2 && rest[1] == "full" {
			return "view-full", num
		}
		return first, num
	case "today", "refresh", "exit", "help", "quit", "join":
		return first, -1
	}

	return "unknown", -1
}

func showToday(client *GraphClient) {
	events, err := client.TodaysMeetings()
	if err != nil {
		fmt.Printf("  %sError: %v%s\n", red, err, reset)
		return
	}
	renderToday(events)
}

// fetchAndDisplay loads and renders the dashboard. withPresence fetches and
// shows the current Teams status line at the top — done on the initial open
// but skipped on refresh, so re-running the dashboard doesn't keep re-reading
// presence.
func fetchAndDisplay(client *GraphClient, enableTeams, withPresence bool) []Item {
	var (
		presence   *presenceState
		meeting    *Meeting
		emails     []Email
		chats      []ChatMessage
		meetingErr error
		emailErr   error
		chatErr    error
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		meeting, meetingErr = client.NextMeeting()
		if meetingErr != nil {
			meeting = nil
		}
	}()

	go func() {
		defer wg.Done()
		emails, emailErr = client.UnreadEmails()
	}()

	if enableTeams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chats, chatErr = client.UnreadChats()
		}()
	}

	// Presence is best-effort: a failure just omits the status line rather
	// than disrupting the dashboard.
	if withPresence {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := client.getPresence(); err == nil {
				presence = &p
			}
		}()
	}

	wg.Wait()

	renderDashboard(presence, meeting, emails, emailErr, chats, chatErr, enableTeams)

	// Chats are numbered ahead of emails, matching renderDashboard's order.
	var items []Item
	for i := range chats {
		items = append(items, Item{Kind: "chat", Chat: &chats[i]})
	}
	for i := range emails {
		items = append(items, Item{Kind: "email", Email: &emails[i]})
	}

	return items
}

func markRead(client *GraphClient, item Item) {
	switch item.Kind {
	case "email":
		if err := client.MarkEmailRead(item.Email.ID); err != nil {
			fmt.Printf("  %sError: %v%s\n", red, err, reset)
			return
		}
		fmt.Printf("  Marked as read: %s\n", truncate(item.Email.Subject, 50))
	case "chat":
		if err := client.MarkChatRead(item.Chat.ChatID); err != nil {
			fmt.Printf("  %sError: %v%s\n", red, err, reset)
			return
		}
		fmt.Printf("  Marked as read: %s\n", truncate(item.Chat.Topic, 50))
	}
}

func markAllRead(client *GraphClient, items []Item) {
	if len(items) == 0 {
		return
	}
	for _, item := range items {
		switch item.Kind {
		case "email":
			if err := client.MarkEmailRead(item.Email.ID); err != nil {
				fmt.Printf("  %sError marking %q: %v%s\n", red, item.Email.Subject, err, reset)
			}
		case "chat":
			if err := client.MarkChatRead(item.Chat.ChatID); err != nil {
				fmt.Printf("  %sError marking %q: %v%s\n", red, item.Chat.Topic, err, reset)
			}
		}
	}
	fmt.Printf("  %sAll marked as read.%s\n", green, reset)
}

func replyTo(client *GraphClient, item Item) {
	switch item.Kind {
	case "email":
		fmt.Printf("  Reply-all to %s — %q:\n", item.Email.From, truncate(item.Email.Subject, 40))
		printRecipientLine("To", withoutAddress(item.Email.To, client.userMail))
		printRecipientLine("Cc", withoutAddress(item.Email.Cc, client.userMail))
	case "chat":
		fmt.Printf("  Reply in %s:\n", item.Chat.Topic)
	}
	fmt.Printf("  %s(end with `.` on a line by itself, or Ctrl-C to cancel)%s\n", dim, reset)

	enterBodyMode()
	body, ok := readBodyDraft()
	exitBodyMode()
	if !ok {
		fmt.Println("  (cancelled)")
		return
	}
	text := strings.TrimRight(body, " \t\n")
	if text == "" {
		fmt.Println("  (cancelled)")
		return
	}

	switch item.Kind {
	case "email":
		if err := client.ReplyAllToEmail(item.Email.ID, text); err != nil {
			fmt.Printf("  %sError: %v%s\n", red, err, reset)
			return
		}
		_ = client.MarkEmailRead(item.Email.ID)
		fmt.Printf("  %sSent & marked as read.%s\n", green, reset)
	case "chat":
		if err := client.SendChatMessage(item.Chat.ChatID, text); err != nil {
			fmt.Printf("  %sError: %v%s\n", red, err, reset)
			return
		}
		fmt.Printf("  %sSent.%s\n", green, reset)
	}
}

// junkViewNotice heads the body of a Junk Email hit. Links render as text
// only and attachments refuse to save or open, so the reader knows why.
const junkViewNotice = "In Junk Email — links shown as text only, attachments blocked. Move it to the Inbox in Outlook to restore them."

func viewItem(client *GraphClient, item Item, index int, showFull bool) {
	switch item.Kind {
	case "email":
		fmt.Printf("\n  %sFrom:%s %s\n", bold, reset, item.Email.From)
		fmt.Printf("  %sSubject:%s %s\n", bold, reset, item.Email.Subject)
		fmt.Printf("  %sReceived:%s %s\n", bold, reset, item.Email.Received.Local().Format("Mon Jan 2 3:04 PM"))
		if item.Email.Junk {
			fmt.Printf("  %s%s%s\n", yellow, junkViewNotice, reset)
		}
		fmt.Println()

		body, err := client.GetEmailBody(item.Email.ID, item.Email.Junk)
		if err != nil {
			fmt.Printf("  %sError loading body: %v%s\n", red, err, reset)
			return
		}

		// Fold the quoted reply history on the default render so the
		// new content isn't buried under accumulated thread. `view N
		// full` (or `Nf`) skips folding.
		render := body
		var hidden int
		if !showFull {
			visible, _, n := splitQuotedHistory(body)
			render, hidden = visible, n
		}

		for _, line := range strings.Split(render, "\n") {
			fmt.Printf("  %s\n", line)
		}
		if hidden > 0 {
			noun := "lines"
			if hidden == 1 {
				noun = "line"
			}
			fmt.Printf("\n  %s[%d quoted %s hidden — type %s%df%s to expand]%s\n",
				dim, hidden, noun, cyan, index, dim, reset)
		}
		fmt.Println()

	case "chat":
		fmt.Printf("\n  %sChat:%s %s\n\n", bold, reset, item.Chat.Topic)

		messages, err := client.GetChatMessages(item.Chat.ChatID, 5)
		if err != nil {
			fmt.Printf("  %sError loading messages: %v%s\n", red, err, reset)
			return
		}

		// Show messages oldest-first
		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]
			fmt.Printf("  %s%s%s %s(%s)%s\n", bold, m.From, reset, dim, relativeTime(m.Sent), reset)
			for _, line := range strings.Split(m.Preview, "\n") {
				fmt.Printf("    %s\n", line)
			}
			fmt.Println()
		}
	}
}
