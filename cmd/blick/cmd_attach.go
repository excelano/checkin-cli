package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// replAttach dispatches the `attach` verb family in the REPL. Attachments
// live on emails and are indexed against the current dashboard list, so
// these are REPL-only (like view/reply/done) — there's no item N from the
// shell. Forms:
//
//	attach N                     list attachments on item N
//	attach save N <#> [--to p]   save the #-th attachment to disk
//	attach open N <#>            open the #-th attachment in the default app
func replAttach(client *GraphClient, items []Item, args []string) {
	if len(args) == 0 {
		attachUsage()
		return
	}
	switch args[0] {
	case "save":
		replAttachSave(client, items, args[1:])
	case "open":
		replAttachOpen(client, items, args[1:])
	default:
		replAttachList(client, items, args[0])
	}
}

func attachUsage() {
	fmt.Printf("  Usage: %sattach N%s  |  %sattach save N <#> [--to path]%s  |  %sattach open N <#>%s\n",
		cyan, reset, cyan, reset, cyan, reset)
}

// replAttachList prints the non-inline attachments on item N. Short-circuits
// on HasAttachments=false so a plain message doesn't cost a Graph round-trip.
func replAttachList(client *GraphClient, items []Item, nToken string) {
	email, err := resolveAttachTarget(items, nToken)
	if err != nil {
		fmt.Printf("  %s%v%s\n", red, err, reset)
		return
	}
	if !email.HasAttachments {
		fmt.Println("  No attachments.")
		return
	}

	atts, err := loadUserAttachments(client, email.ID)
	if err != nil {
		fmt.Printf("  %sError loading attachments: %v%s\n", red, err, reset)
		return
	}
	if len(atts) == 0 {
		fmt.Println("  No attachments.")
		return
	}

	fmt.Println()
	for i, a := range atts {
		label := a.Name
		if label == "" {
			label = "(unnamed)"
		}
		fmt.Printf("  %s[%d]%s %s %s(%s)%s\n", cyan, i+1, reset, label, dim, humanSize(a.Size), reset)
	}
	fmt.Println()
}

// replAttachSave writes the chosen attachment to disk. Default destination is
// the current working directory under the attachment's own name; --to
// overrides with either a directory (name appended) or an explicit file path.
func replAttachSave(client *GraphClient, items []Item, args []string) {
	nToken, idxToken, toPath, err := parseAttachRefArgs(args, true)
	if err != nil {
		fmt.Printf("  %s%v%s\n", red, err, reset)
		return
	}
	att, email, ok := pickAttachment(client, items, nToken, idxToken)
	if !ok {
		return
	}
	if !att.IsFile() {
		fmt.Printf("  %s[%s] is an embedded item, not a file — can't save.%s\n", dim, att.Name, reset)
		return
	}

	name, content, err := client.GetAttachmentContent(email.ID, att.ID)
	if err != nil {
		fmt.Printf("  %sError downloading attachment: %v%s\n", red, err, reset)
		return
	}

	dest := destinationPath(toPath, name)
	if err := os.WriteFile(dest, content, 0644); err != nil {
		fmt.Printf("  %sError writing file: %v%s\n", red, err, reset)
		return
	}
	fmt.Printf("  %sSaved%s %s %s(%s)%s\n", green, reset, dest, dim, humanSize(len(content)), reset)
}

// replAttachOpen writes the attachment to a temp directory and hands it to
// the OS opener. The file is left in place for the OS to reap on session
// end — this is the throwaway "just let me look at the PDF" path.
func replAttachOpen(client *GraphClient, items []Item, args []string) {
	nToken, idxToken, _, err := parseAttachRefArgs(args, false)
	if err != nil {
		fmt.Printf("  %s%v%s\n", red, err, reset)
		return
	}
	att, email, ok := pickAttachment(client, items, nToken, idxToken)
	if !ok {
		return
	}
	if !att.IsFile() {
		fmt.Printf("  %s[%s] is an embedded item, not a file — can't open.%s\n", dim, att.Name, reset)
		return
	}

	name, content, err := client.GetAttachmentContent(email.ID, att.ID)
	if err != nil {
		fmt.Printf("  %sError downloading attachment: %v%s\n", red, err, reset)
		return
	}

	dir := filepath.Join(tempBaseDir(), "blick")
	if err := os.MkdirAll(dir, 0700); err != nil {
		fmt.Printf("  %sError preparing temp dir: %v%s\n", red, err, reset)
		return
	}
	path := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(path, content, 0644); err != nil {
		fmt.Printf("  %sError writing temp file: %v%s\n", red, err, reset)
		return
	}

	fmt.Printf("  %sOpening%s %s...\n", green, reset, name)
	if err := openURL(path); err != nil {
		fmt.Printf("  %sCould not open: %v%s\n", red, err, reset)
		fmt.Printf("  Saved at: %s\n", path)
	}
}

// junkAttachmentNotice is why save/open refuse on a Junk Email hit. Listing
// still works; only the paths that put bytes on disk are closed.
const junkAttachmentNotice = "Item is in Junk Email — attachments are blocked. Move it to the Inbox in Outlook first."

// pickAttachment resolves item N to an email, loads its user-facing
// attachments, and returns the idx-th (1-based). Reports its own errors and
// returns ok=false on any failure so callers just bail. Junk hits are refused
// before any attachment is fetched.
func pickAttachment(client *GraphClient, items []Item, nToken, idxToken string) (Attachment, *Email, bool) {
	email, err := resolveAttachTarget(items, nToken)
	if err != nil {
		fmt.Printf("  %s%v%s\n", red, err, reset)
		return Attachment{}, nil, false
	}
	if email.Junk {
		fmt.Printf("  %s%s%s\n", yellow, junkAttachmentNotice, reset)
		return Attachment{}, nil, false
	}
	idx, err := strconv.Atoi(idxToken)
	if err != nil || idx < 1 {
		fmt.Printf("  %sInvalid attachment number: %s%s\n", red, idxToken, reset)
		return Attachment{}, nil, false
	}
	atts, err := loadUserAttachments(client, email.ID)
	if err != nil {
		fmt.Printf("  %sError loading attachments: %v%s\n", red, err, reset)
		return Attachment{}, nil, false
	}
	if idx > len(atts) {
		fmt.Printf("  %sItem %s has %d attachment(s).%s\n", red, nToken, len(atts), reset)
		return Attachment{}, nil, false
	}
	return atts[idx-1], email, true
}

// resolveAttachTarget maps a dashboard item number to its Email. Chats don't
// carry attachments in this tool, so those are rejected with a clear reason.
func resolveAttachTarget(items []Item, nToken string) (*Email, error) {
	n, err := strconv.Atoi(nToken)
	if err != nil || n < 1 || n > len(items) {
		return nil, fmt.Errorf("Invalid item: %s", nToken)
	}
	item := items[n-1]
	if item.Kind != "email" {
		return nil, fmt.Errorf("Item %d is a chat — attachments are on emails only.", n)
	}
	return item.Email, nil
}

// loadUserAttachments fetches and filters to the non-inline set the user
// indexes against.
func loadUserAttachments(client *GraphClient, messageID string) ([]Attachment, error) {
	all, err := client.ListAttachments(messageID)
	if err != nil {
		return nil, err
	}
	return userAttachments(all), nil
}

// parseAttachRefArgs pulls the item number and attachment index out of the
// save/open argument tail, plus an optional --to path when allowTo is set.
func parseAttachRefArgs(args []string, allowTo bool) (nToken, idxToken, toPath string, err error) {
	positional := []string{}
	for i := 0; i < len(args); i++ {
		if allowTo && args[i] == "--to" {
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("--to requires a path")
			}
			toPath = args[i+1]
			i++
			continue
		}
		positional = append(positional, args[i])
	}
	if len(positional) < 2 {
		return "", "", "", fmt.Errorf("need an item number and an attachment number, e.g. %sattach save 2 1%s", cyan, reset)
	}
	return positional[0], positional[1], toPath, nil
}

// destinationPath resolves where a saved attachment lands. With no --to it's
// the attachment's base name in the cwd. A --to that names an existing
// directory gets the base name appended; otherwise --to is taken as the full
// target path. filepath.Base on the attachment name blocks a hostile name
// from escaping the target dir via path separators.
func destinationPath(toPath, name string) string {
	base := filepath.Base(name)
	if toPath == "" {
		return base
	}
	if info, err := os.Stat(toPath); err == nil && info.IsDir() {
		return filepath.Join(toPath, base)
	}
	return toPath
}

// tempBaseDir prefers the per-user runtime dir (tmpfs, auto-cleared on
// logout) and falls back to the OS temp dir.
func tempBaseDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

// humanSize renders a byte count as B / KB / MB with one decimal place for
// the larger units.
func humanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
