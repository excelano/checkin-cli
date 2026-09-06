package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ANSI escapes for the dashboard. Vars (not consts) so the NO_COLOR
// init can zero them out — no-color.org convention: any non-empty
// NO_COLOR env var suppresses all color output.
var (
	bold   = "\033[1m"
	dim    = "\033[2m"
	cyan   = "\033[36m"
	yellow = "\033[33m"
	orange = "\033[38;5;208m"
	green  = "\033[32m"
	red    = "\033[31m"
	reset  = "\033[0m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" {
		bold, dim, cyan, yellow, orange, green, red, reset = "", "", "", "", "", "", "", ""
	}
}

// printRecipientLine renders one line of the reply-all recipient summary,
// e.g. "  To:  Alice, Bob, Carol". Silently omits the line when the list
// is empty (self-filtered down to nothing, or no recipients in that
// group on the original) so single-recipient threads don't show a stub.
func printRecipientLine(label string, rs []Recipient) {
	if len(rs) == 0 {
		return
	}
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Display()
	}
	fmt.Printf("  %s%s:%s %s\n", bold, label, reset, strings.Join(names, ", "))
}

func relativeTime(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func absoluteTime(t time.Time) string {
	now := time.Now()
	local := t.Local()

	if now.Year() == local.Year() && now.YearDay() == local.YearDay() {
		return local.Format("3:04 PM")
	}

	diff := local.Sub(now)
	if diff < 0 {
		diff = -diff
	}
	if diff < 7*24*time.Hour {
		return local.Format("Mon 3:04 PM")
	}
	return local.Format("Jan 2")
}

func untilTime(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "now"
	}

	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "in 1 min"
		}
		return fmt.Sprintf("in %d min", m)
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if h == 0 {
			return fmt.Sprintf("in %d min", m)
		}
		if m == 0 {
			if h == 1 {
				return "in 1 hour"
			}
			return fmt.Sprintf("in %d hours", h)
		}
		return fmt.Sprintf("in %dh %dm", h, m)
	}
}

// flatten collapses a multi-line string onto one line by splitting on
// newlines, trimming each segment, and rejoining with sep. Outlook returns
// addresses in location.displayName as "Street\nCity, ST Zip\nCountry";
// passing sep=", " produces a properly punctuated single-line address.
// Use sep=" " for free-form text like meeting titles.
func flatten(s, sep string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, sep)
}

func truncate(s string, maxLen int) string {
	// Replace newlines with spaces for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func renderDashboard(presence *presenceState, meeting *Meeting, emails []Email, emailErr error, chats []ChatMessage, chatErr error, enableTeams bool) {
	fmt.Println()

	// Presence status line (initial open only; omitted when the fetch failed)
	if presence != nil {
		fmt.Printf("  %s●%s %s%s%s\n\n", presenceColor(presence.Availability), reset, bold, presenceLabel(*presence), reset)
	}

	// Meeting
	if meeting != nil {
		timeColor := green
		until := untilTime(meeting.Start)
		d := time.Until(meeting.Start)
		if d < 15*time.Minute {
			timeColor = yellow
		}
		if d < 3*time.Minute && d >= 0 {
			timeColor = orange
			until = "Starting soon"
		}
		if d < 0 {
			timeColor = red
			until = "now"
		}

		location := ""
		if meeting.Location != "" {
			location = " — " + meeting.Location
		} else if meeting.IsOnline {
			location = " — Online"
		}

		fmt.Printf("  📅 %s%s%s — %s%s%s %s· %s%s%s\n\n",
			bold, meeting.Subject, reset,
			timeColor, until, reset,
			dim, absoluteTime(meeting.Start), location, reset)
	} else {
		fmt.Printf("  📅 %sNo upcoming meetings%s\n\n", dim, reset)
	}

	// Chats (shown above emails; numbered first)
	if !enableTeams {
		fmt.Printf("  💬 %sTeams disabled (set \"enable_teams\": true in config to enable)%s\n\n", dim, reset)
	} else if chatErr != nil {
		fmt.Printf("  💬 %sCould not load chats: %v%s\n\n", red, chatErr, reset)
	} else if len(chats) == 0 {
		fmt.Printf("  💬 %sNo unread chats%s\n\n", dim, reset)
	} else {
		fmt.Printf("  💬 %sunread chats (%d):%s\n", bold, len(chats), reset)
		printChatRows(chats)
		fmt.Println()
	}

	// Emails (numbered after chats)
	if emailErr != nil {
		fmt.Printf("  📧 %sCould not load emails: %v%s\n\n", red, emailErr, reset)
	} else if len(emails) == 0 {
		fmt.Printf("  📧 %sNo unread emails%s\n\n", dim, reset)
	} else {
		fmt.Printf("  📧 %sunread emails (%d):%s\n", bold, len(emails), reset)
		printEmailRows(emails, len(chats))
		fmt.Println()
	}
}

// renderInbox draws the inbox history view — chats above emails, read
// included — sharing the row format and chats-first numbering with the
// dashboard so the same view/reply/done/attach verbs line up. emailsTruncated
// adds a note when the window overflowed inboxEmailTop.
func renderInbox(daysBack int, emails []Email, emailErr error, chats []ChatMessage, chatErr error, enableTeams, emailsTruncated, chatsTruncated bool) {
	fmt.Println()

	// The window always includes today, so it spans daysBack+1 calendar days.
	span := "today"
	switch total := daysBack + 1; {
	case total == 2:
		span = "today and yesterday"
	case total > 2:
		span = fmt.Sprintf("the last %d days", total)
	}
	fmt.Printf("  📥 %sInbox — %s%s %s(read included)%s\n\n", bold, span, reset, dim, reset)

	// Chats (numbered first)
	if !enableTeams {
		fmt.Printf("  💬 %sTeams disabled (set \"enable_teams\": true in config to enable)%s\n\n", dim, reset)
	} else if chatErr != nil {
		fmt.Printf("  💬 %sCould not load chats: %v%s\n\n", red, chatErr, reset)
	} else if len(chats) == 0 {
		fmt.Printf("  💬 %sNo chats in this window%s\n\n", dim, reset)
	} else {
		fmt.Printf("  💬 %schats (%d):%s\n", bold, len(chats), reset)
		printChatRows(chats)
		if chatsTruncated {
			fmt.Printf("    %s(showing the %d most recently active — older in-window chats may be omitted)%s\n", dim, len(chats), reset)
		}
		fmt.Println()
	}

	// Emails (numbered after chats)
	if emailErr != nil {
		fmt.Printf("  📧 %sCould not load emails: %v%s\n\n", red, emailErr, reset)
	} else if len(emails) == 0 {
		fmt.Printf("  📧 %sNo emails in this window%s\n\n", dim, reset)
	} else {
		fmt.Printf("  📧 %semails (%d):%s\n", bold, len(emails), reset)
		printEmailRows(emails, len(chats))
		if emailsTruncated {
			fmt.Printf("    %s(showing the %d most recent — narrow the window for older mail)%s\n", dim, len(emails), reset)
		}
		fmt.Println()
	}
}

// renderSearch draws mail search results — emails only, numbered from 1,
// sharing the row format with the dashboard and inbox so view/reply/done/
// attach/forward line up. desc is the KQL echoed back in the header.
func renderSearch(desc string, emails []Email, err error) {
	fmt.Println()
	fmt.Printf("  🔎 %sSearch — %s%s%s\n\n", bold, reset, desc, reset)

	if err != nil {
		fmt.Printf("  📧 %sSearch failed: %v%s\n\n", red, err, reset)
		return
	}
	if len(emails) == 0 {
		fmt.Printf("  📧 %sNo matches%s\n\n", dim, reset)
		return
	}
	fmt.Printf("  📧 %smatches (%d):%s\n", bold, len(emails), reset)
	printEmailRows(emails, 0)
	if len(emails) >= searchEmailTop {
		fmt.Printf("    %s(showing the first %d by relevance — refine the search for more)%s\n", dim, len(emails), reset)
	}
	fmt.Println()
}

// printChatRows prints the numbered chat lines shared by the dashboard and
// inbox views. Chats are always numbered from 1 — they lead both lists.
func printChatRows(chats []ChatMessage) {
	for i, c := range chats {
		fmt.Printf("    %s%d.%s %s — %q  %s(%s · %s)%s\n",
			cyan, i+1, reset,
			c.Topic,
			truncate(c.Preview, 40),
			dim, relativeTime(c.Sent), absoluteTime(c.Sent), reset)
	}
}

// printEmailRows prints the numbered email lines shared by the dashboard,
// inbox, and search views. offset is the count of chats printed before them,
// so the numbering continues past the chat block. Junk hits (search only)
// carry a marker so a rescued message is visibly flagged before you act on it.
func printEmailRows(emails []Email, offset int) {
	for i, e := range emails {
		clip := ""
		if e.HasAttachments {
			clip = " 📎"
		}
		if e.Junk {
			clip += " " + yellow + "[junk]" + reset
		}
		fmt.Printf("    %s%d.%s %s — %q%s  %s(%s · %s)%s\n",
			cyan, offset+i+1, reset,
			e.From,
			truncate(e.Subject, 50),
			clip,
			dim, relativeTime(e.Received), absoluteTime(e.Received), reset)
	}
}

func renderHelp() {
	rows := [][4]string{
		{"<N>", "view", "r", "refresh"},
		{"<N>r", "reply-all", "q", "quit"},
		{"<N>d", "done", "h", "help"},
	}
	fmt.Printf("  %sCommon commands:%s\n", bold, reset)
	for _, r := range rows {
		fmt.Printf("    %s%-8s%s %-18s %s%-8s%s %s\n",
			cyan, r[0], reset, r[1],
			cyan, r[2], reset, r[3])
	}
	fmt.Println()
}

func renderFullHelp() {
	fmt.Println()
	fmt.Printf("  %sCommands:%s\n\n", bold, reset)
	fmt.Printf("    %s%-8s  %-13s  %s%s\n", dim, "Short", "Long", "What it does", reset)
	rows := []struct{ short, long, desc string }{
		{"<N>", "view N", "Open the Nth item from the list"},
		{"<N>f", "view N full", "Open the Nth item with quoted history expanded"},
		{"<N>r", "reply N", "Reply-all to the Nth item (ed-style editor)"},
		{"<N>d", "done N", "Mark the Nth item as read"},
		{"", "forward N", "Forward the Nth email to new recipients"},
		{"", "attach N", "List attachments on the Nth item (save/open <#>)"},
		{"i", "inbox [N]", "Today's chats & emails, read included (N days back)"},
		{"", "search ...", "Search mail: --from, --subject, --text, or bare words"},
		{"e <c>", "email <c>", "Compose a new email (--cc, --bcc, --attach)"},
		{"c <c>", "chat <c>", "Send a 1:1 or group Teams chat (--topic for group)"},
		{"r", "refresh", "Reload the dashboard"},
		{"t", "today", "Show today's calendar"},
		{"j", "join", "Open the current or next online meeting in the browser"},
		{"p", "presence [state]", "Set Teams presence (available/busy/dnd/brb/away/offline)"},
		{"x", "exit", "Mark all items as read & quit"},
		{"h", "help", "Show this help"},
		{"q", "quit", "Quit"},
	}
	for _, r := range rows {
		fmt.Printf("    %s%-8s%s  %s%-13s%s  %s\n",
			cyan, r.short, reset,
			cyan, r.long, reset,
			r.desc)
	}
	fmt.Println()
}

func renderToday(events []Meeting) {
	fmt.Println()
	today := time.Now().Local()
	fmt.Printf("  %s%s%s\n\n", bold, today.Format("Monday, January 2, 2006"), reset)

	if len(events) == 0 {
		fmt.Printf("  %sNo meetings today%s\n\n", dim, reset)
		return
	}

	var allDay, timed []Meeting
	for _, e := range events {
		if e.IsAllDay {
			allDay = append(allDay, e)
		} else {
			timed = append(timed, e)
		}
	}

	for _, e := range allDay {
		fmt.Printf("    %sall day%s              %s\n", dim, reset, e.Subject)
	}

	now := time.Now()
	var totalDuration time.Duration

	for _, e := range timed {
		startStr := e.Start.Local().Format("3:04 PM")
		endStr := e.End.Local().Format("3:04 PM")
		timeRange := fmt.Sprintf("%8s – %-8s", startStr, endStr)

		subject := e.Subject
		var style string
		switch {
		case e.End.Before(now):
			style = dim
		case e.Start.Before(now) || e.Start.Equal(now):
			style = bold
			subject = subject + " · now"
		}

		loc := ""
		if e.IsOnline {
			loc = "Online"
		} else if e.Location != "" {
			loc = e.Location
		}

		line := fmt.Sprintf("    %-19s  %-30s  %s", timeRange, subject, loc)
		line = strings.TrimRight(line, " ")

		if style != "" {
			fmt.Printf("%s%s%s\n", style, line, reset)
		} else {
			fmt.Println(line)
		}

		totalDuration += e.End.Sub(e.Start)
	}

	noun := "events"
	if len(events) == 1 {
		noun = "event"
	}
	fmt.Printf("\n  %s%d %s · %s scheduled%s\n\n",
		dim, len(events), noun, formatDuration(totalDuration), reset)
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
