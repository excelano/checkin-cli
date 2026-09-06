package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Recipient struct {
	Name    string
	Address string
}

// Display returns the visible label for a recipient: prefer Name, fall
// back to Address when the name is missing or duplicates the address.
func (r Recipient) Display() string {
	if r.Name != "" && r.Name != r.Address {
		return r.Name
	}
	return r.Address
}

type Email struct {
	ID             string
	Subject        string
	From           string
	Preview        string
	Received       time.Time
	To             []Recipient
	Cc             []Recipient
	HasAttachments bool
	// Junk is set when the message sits in the Junk Email folder. Only search
	// results can carry it — the list views are Inbox-only by construction —
	// and the renderers use it to mark the row, keep links inert in the body,
	// and refuse attachment save/open.
	Junk bool
}

// inboxEmailTop caps how many messages the inbox history view pulls in one
// window. The caller surfaces a note when the cap is hit so the truncation
// isn't silent.
const inboxEmailTop = 50

// messageSelect is the shared $select for the message list views — the fields
// the dashboard and inbox rows render, plus parentFolderId so search can tell
// a Junk Email hit from an Inbox one.
const messageSelect = "id,subject,from,toRecipients,ccRecipients,bodyPreview,receivedDateTime,hasAttachments,parentFolderId"

// inboxMessages is the Graph path for the Inbox folder alone. The list views
// read it rather than /me/messages, which spans every folder in the mailbox:
// Junk Email, Deleted Items, Archive, and anything an inbox rule filed away.
// Unread mail in those folders is not an action-now item — Outlook's badge
// and the iOS app both count the Inbox only — and Junk in particular must not
// reach the body renderer, which extracts links and unwraps SafeLinks. Search
// deliberately stays on /me/messages (see SearchEmails).
const inboxMessages = "/me/mailFolders/inbox/messages"

// UnreadEmails returns the unread Inbox messages, newest first — the email
// half of the dashboard.
func (g *GraphClient) UnreadEmails() ([]Email, error) {
	query := url.Values{
		"$filter":  {"isRead eq false"},
		"$orderby": {"receivedDateTime desc"},
		"$top":     {"10"},
		"$select":  {messageSelect},
	}

	data, err := g.get(inboxMessages, query)
	if err != nil {
		return nil, err
	}
	return parseEmails(data, "")
}

// EmailsSince returns Inbox messages received at or after `since`, read
// included, newest first — the email half of the inbox history view. Capped
// at inboxEmailTop; the caller notes when the window overflows.
func (g *GraphClient) EmailsSince(since time.Time) ([]Email, error) {
	query := url.Values{
		"$filter":  {fmt.Sprintf("receivedDateTime ge %s", since.UTC().Format(time.RFC3339))},
		"$orderby": {"receivedDateTime desc"},
		"$top":     {strconv.Itoa(inboxEmailTop)},
		"$select":  {messageSelect},
	}

	data, err := g.get(inboxMessages, query)
	if err != nil {
		return nil, err
	}
	return parseEmails(data, "")
}

// SearchEmails runs a Graph mailbox $search over /me/messages and returns the
// matches. Unlike the list views this spans every folder on purpose: search is
// where a legitimate message that landed in Junk gets found and rescued.
// Graph ranks $search by relevance and forbids $orderby alongside it,
// so results come back relevance-ordered, not by date. kql is a KQL expression
// like `from:alice` or a free-text term; it's wrapped in the double quotes
// $search requires. Capped at searchEmailTop; the caller notes when the cap is
// hit. Covered by Mail.Read — no extra scope.
func (g *GraphClient) SearchEmails(kql string) ([]Email, error) {
	query := url.Values{
		"$search": {`"` + kql + `"`},
		"$top":    {strconv.Itoa(searchEmailTop)},
		"$select": {messageSelect},
	}

	junkID, err := g.junkFolderID()
	if err != nil {
		return nil, err
	}
	data, err := g.get("/me/messages", query)
	if err != nil {
		return nil, err
	}
	return parseEmails(data, junkID)
}

// junkFolderID resolves the Junk Email folder's ID, cached for the life of
// the client. Graph exposes it under the well-known name `junkemail`; the ID
// is stable for the mailbox, unlike message IDs, which change on move.
// Covered by Mail.Read. A failure here is fatal to the caller on purpose: a
// search that cannot tell junk from Inbox would render junk as normal mail.
func (g *GraphClient) junkFolderID() (string, error) {
	if g.junkFolder != "" {
		return g.junkFolder, nil
	}
	data, err := g.get("/me/mailFolders/junkemail", url.Values{"$select": {"id"}})
	if err != nil {
		return "", fmt.Errorf("resolving Junk Email folder: %w", err)
	}
	var folder struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &folder); err != nil {
		return "", err
	}
	if folder.ID == "" {
		return "", fmt.Errorf("resolving Junk Email folder: Graph returned no id")
	}
	g.junkFolder = folder.ID
	return g.junkFolder, nil
}

// parseEmails decodes a Graph message list. junkFolderID, when non-empty,
// marks messages in that folder as Junk; the Inbox-only list views pass ""
// since nothing they return can be junk.
func parseEmails(data []byte, junkFolderID string) ([]Email, error) {
	var result struct {
		Value []struct {
			ID   string `json:"id"`
			From struct {
				EmailAddress struct {
					Name string `json:"name"`
				} `json:"emailAddress"`
			} `json:"from"`
			ToRecipients     []graphRecipient `json:"toRecipients"`
			CcRecipients     []graphRecipient `json:"ccRecipients"`
			Subject          string           `json:"subject"`
			BodyPreview      string           `json:"bodyPreview"`
			ReceivedDateTime string           `json:"receivedDateTime"`
			HasAttachments   bool             `json:"hasAttachments"`
			ParentFolderID   string           `json:"parentFolderId"`
		} `json:"value"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	emails := make([]Email, len(result.Value))
	for i, e := range result.Value {
		received, _ := time.Parse(time.RFC3339Nano, e.ReceivedDateTime)
		emails[i] = Email{
			ID:             e.ID,
			Subject:        e.Subject,
			From:           e.From.EmailAddress.Name,
			Preview:        e.BodyPreview,
			Received:       received,
			To:             toRecipients(e.ToRecipients),
			Cc:             toRecipients(e.CcRecipients),
			HasAttachments: e.HasAttachments,
			Junk:           junkFolderID != "" && e.ParentFolderID == junkFolderID,
		}
	}

	return emails, nil
}

type graphRecipient struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

func toRecipients(in []graphRecipient) []Recipient {
	out := make([]Recipient, 0, len(in))
	for _, r := range in {
		out = append(out, Recipient{
			Name:    r.EmailAddress.Name,
			Address: r.EmailAddress.Address,
		})
	}
	return out
}

// withoutAddress returns recipients with any entry matching addr removed
// (case-insensitive on the SMTP address). Used to filter the signed-in
// user out of reply-all displays so the lists reflect who actually gets
// the reply.
func withoutAddress(rs []Recipient, addr string) []Recipient {
	if addr == "" {
		return rs
	}
	out := make([]Recipient, 0, len(rs))
	for _, r := range rs {
		if !strings.EqualFold(r.Address, addr) {
			out = append(out, r)
		}
	}
	return out
}

func (g *GraphClient) GetEmailBody(id string) (string, error) {
	query := url.Values{
		"$select": {"body"},
	}

	data, err := g.get(fmt.Sprintf("/me/messages/%s", id), query)
	if err != nil {
		return "", err
	}

	var result struct {
		Body struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}

	if result.Body.ContentType == "html" {
		return stripHTML(result.Body.Content), nil
	}
	return result.Body.Content, nil
}

func (g *GraphClient) MarkEmailRead(id string) error {
	return g.patch(fmt.Sprintf("/me/messages/%s", id), map[string]bool{"isRead": true})
}

// ReplyAllToEmail posts a reply-all to the message. Graph's /replyAll
// self-degrades to reply-to-sender when the original has no other
// recipients, so this is safe to use unconditionally — matches the iOS
// Blick app's "reply defaults to reply-all" behavior.
func (g *GraphClient) ReplyAllToEmail(id, comment string) error {
	html := strings.ReplaceAll(comment, "\n", "<br>")
	body := map[string]interface{}{
		"comment": html + "<br><br>",
	}
	_, err := g.post(fmt.Sprintf("/me/messages/%s/replyAll", id), body)
	return err
}

// ForwardEmail forwards the message to the given addresses with an optional
// comment. Graph builds the "Fwd:" subject and quotes the original body
// server-side via /forward, so we send only the recipients and the comment —
// newlines mapped to <br> to match the HTML the original body renders as,
// same as ReplyAllToEmail. An empty comment forwards with no added note.
func (g *GraphClient) ForwardEmail(id, comment string, to []string) error {
	if len(to) == 0 {
		return fmt.Errorf("no recipients")
	}
	body := map[string]interface{}{
		"toRecipients": recipientList(to),
	}
	if comment != "" {
		body["comment"] = strings.ReplaceAll(comment, "\n", "<br>")
	}
	_, err := g.post(fmt.Sprintf("/me/messages/%s/forward", id), body)
	return err
}

// recipientList builds the Graph recipient array [{emailAddress:{address}}]
// from SMTP addresses. Shared by SendMail (to/cc/bcc) and ForwardEmail.
func recipientList(addrs []string) []map[string]interface{} {
	out := make([]map[string]interface{}, len(addrs))
	for i, addr := range addrs {
		out[i] = map[string]interface{}{
			"emailAddress": map[string]string{"address": addr},
		}
	}
	return out
}

// SendMail composes and sends a new message in one shot via /me/sendMail
// (saveToSentItems defaults to true so the message lands in Sent like any
// other Outlook send). cc/bcc are added only when non-empty. Content type is
// Text — the keyboard-first compose flow doesn't deal in HTML.
func (g *GraphClient) SendMail(to, cc, bcc []string, subject, body string, attachments []OutgoingAttachment) error {
	if len(to) == 0 {
		return fmt.Errorf("no recipients")
	}
	message := map[string]interface{}{
		"subject":      subject,
		"body":         map[string]string{"contentType": "Text", "content": body},
		"toRecipients": recipientList(to),
	}
	if len(cc) > 0 {
		message["ccRecipients"] = recipientList(cc)
	}
	if len(bcc) > 0 {
		message["bccRecipients"] = recipientList(bcc)
	}
	if len(attachments) > 0 {
		encoded := make([]map[string]interface{}, len(attachments))
		for i, a := range attachments {
			encoded[i] = map[string]interface{}{
				"@odata.type":  fileAttachmentType,
				"name":         a.Name,
				"contentType":  a.ContentType,
				"contentBytes": base64.StdEncoding.EncodeToString(a.Content),
			}
		}
		message["attachments"] = encoded
	}
	payload := map[string]interface{}{
		"message":         message,
		"saveToSentItems": true,
	}
	_, err := g.post("/me/sendMail", payload)
	return err
}

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	htmlEntityRe = regexp.MustCompile(`&[a-zA-Z0-9#]+;`)
	whitespaceRe = regexp.MustCompile(`\n{3,}`)
	// Matches an <a href="..."> ... </a> pair (case-insensitive, dot spans
	// newlines) capturing the destination and the visible inner content.
	anchorRe = regexp.MustCompile(`(?is)<a\b[^>]*?\bhref\s*=\s*["']([^"']*)["'][^>]*>(.*?)</a>`)
)

var entityMap = map[string]string{
	"&amp;":  "&",
	"&lt;":   "<",
	"&gt;":   ">",
	"&nbsp;": " ",
	"&#39;":  "'",
	"&quot;": "\"",
	"&apos;": "'",
}

// safeLinkSuffix is the host suffix of Outlook ATP SafeLinks wrapper URLs,
// e.g. nam12.safelinks.protection.outlook.com.
const safeLinkSuffix = ".safelinks.protection.outlook.com"

// unwrapSafeLink returns the original destination when href is an Outlook ATP
// SafeLinks wrapper (https://<x>.safelinks.protection.outlook.com/?url=...),
// otherwise href unchanged. Guarded tightly: only that host and only when the
// url param is present are touched, so every other URL passes through as-is.
// Runs before entity decoding, when the href still carries &amp; separators;
// url.Values skips those amp;… segments (they hold a semicolon) and still
// recovers the clean, percent-decoded url param.
func unwrapSafeLink(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	host := strings.ToLower(u.Hostname())
	if host != "safelinks.protection.outlook.com" &&
		!strings.HasSuffix(host, safeLinkSuffix) {
		return href
	}
	if orig := u.Query().Get("url"); orig != "" {
		return orig
	}
	return href
}

// unsafeSchemes carry executable content rather than a destination. The reason
// this matters in a terminal is the same reason keeping the URL is useful at
// all: terminals that auto-linkify will make whatever survives clickable, and
// a click on data:text/html or javascript: runs the sender's markup in the
// reader's browser. Mail is attacker-supplied, so the list is checked rather
// than assumed.
var unsafeSchemes = []string{"javascript:", "data:", "vbscript:"}

// hasUnsafeScheme reports whether href names a scheme from unsafeSchemes. The
// caller checks this after unwrapping Safe Links, so a hostile scheme carried
// inside an ATP wrapper is caught on the destination rather than on the
// harmless outlook.com URL around it.
func hasUnsafeScheme(href string) bool {
	lower := strings.ToLower(href)
	for _, scheme := range unsafeSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// rewriteAnchors turns <a href="URL">text</a> into visible text that keeps the
// destination, so the general tag strip in stripHTML doesn't discard the URL.
// Terminals that auto-linkify make the surviving URL clickable again. Named
// anchors, # fragments, and the executable schemes carry no destination worth
// keeping, so only their visible text survives.
func rewriteAnchors(s string) string {
	return anchorRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := anchorRe.FindStringSubmatch(m)
		href := unwrapSafeLink(strings.TrimSpace(sub[1]))
		text := strings.TrimSpace(htmlTagRe.ReplaceAllString(sub[2], ""))
		if href == "" || strings.HasPrefix(href, "#") || hasUnsafeScheme(href) {
			return text
		}
		display := href
		if strings.HasPrefix(strings.ToLower(display), "mailto:") {
			display = display[len("mailto:"):]
		}
		if text == "" || text == display || text == href {
			return shieldAngles(display)
		}
		return text + " (" + shieldAngles(display) + ")"
	})
}

// shieldAngles escapes < and > in a URL so the tag-strip pass in stripHTML
// (which deletes anything matching <...>) can't truncate a URL that contains
// literal angle brackets — e.g. a SafeLink whose decoded destination held
// %3C...%3E. The later entity-decode pass turns &lt;/&gt; back into </>, so the
// URL is restored intact. & is left alone so existing &amp; entities in the URL
// still decode normally.
func shieldAngles(url string) string {
	url = strings.ReplaceAll(url, "<", "&lt;")
	url = strings.ReplaceAll(url, ">", "&gt;")
	return url
}

func stripHTML(s string) string {
	// Preserve link destinations before the tag strip removes the anchors.
	s = rewriteAnchors(s)
	// Replace block elements with newlines
	for _, tag := range []string{"</p>", "</div>", "</tr>", "<br>", "<br/>", "<br />"} {
		s = strings.ReplaceAll(s, tag, "\n")
	}
	s = htmlTagRe.ReplaceAllString(s, "")
	s = htmlEntityRe.ReplaceAllStringFunc(s, func(entity string) string {
		if r, ok := entityMap[strings.ToLower(entity)]; ok {
			return r
		}
		return entity
	})
	s = whitespaceRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
