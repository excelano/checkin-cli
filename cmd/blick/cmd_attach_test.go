package main

import (
	"path/filepath"
	"testing"
)

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{3 * 1024 * 1024, "3.0 MB"},
	}
	for _, c := range cases {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestParseAttachRefArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		allowTo bool
		n, idx  string
		to      string
		wantErr bool
	}{
		{"basic", []string{"2", "1"}, true, "2", "1", "", false},
		{"with to path", []string{"2", "1", "--to", "/tmp/x"}, true, "2", "1", "/tmp/x", false},
		{"to before positionals", []string{"--to", "/tmp/x", "3", "2"}, true, "3", "2", "/tmp/x", false},
		{"open ignores to", []string{"2", "1"}, false, "2", "1", "", false},
		{"missing index", []string{"2"}, true, "", "", "", true},
		{"dangling to", []string{"2", "1", "--to"}, true, "", "", "", true},
		{"no args", []string{}, true, "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, idx, to, err := parseAttachRefArgs(tt.args, tt.allowTo)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if n != tt.n || idx != tt.idx || to != tt.to {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)", n, idx, to, tt.n, tt.idx, tt.to)
			}
		})
	}
}

func TestDestinationPath(t *testing.T) {
	// No --to: base name in cwd, path components in the name stripped.
	if got := destinationPath("", "report.pdf"); got != "report.pdf" {
		t.Errorf("no-to = %q, want report.pdf", got)
	}
	if got := destinationPath("", "../../etc/passwd"); got != "passwd" {
		t.Errorf("traversal name = %q, want passwd", got)
	}
	// --to naming a non-existent path is taken as the full target file.
	if got := destinationPath("/tmp/out.pdf", "report.pdf"); got != "/tmp/out.pdf" {
		t.Errorf("to-file = %q, want /tmp/out.pdf", got)
	}
	// --to naming an existing directory gets the base name appended.
	dir := t.TempDir()
	if got := destinationPath(dir, "report.pdf"); got != filepath.Join(dir, "report.pdf") {
		t.Errorf("to-dir = %q, want %q", got, filepath.Join(dir, "report.pdf"))
	}
}

func TestUserAttachments(t *testing.T) {
	all := []Attachment{
		{Name: "doc.pdf", IsInline: false},
		{Name: "logo.png", IsInline: true},
		{Name: "sheet.xlsx", IsInline: false},
	}
	got := userAttachments(all)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 non-inline", len(got))
	}
	if got[0].Name != "doc.pdf" || got[1].Name != "sheet.xlsx" {
		t.Errorf("wrong filter result: %+v", got)
	}
}

func TestAttachmentIsFile(t *testing.T) {
	if !(Attachment{ODataType: fileAttachmentType}).IsFile() {
		t.Error("fileAttachment should be a file")
	}
	if (Attachment{ODataType: "#microsoft.graph.itemAttachment"}).IsFile() {
		t.Error("itemAttachment should not be a file")
	}
}

// A junk hit is refused before any Graph call, so a nil client is safe here:
// reaching loadUserAttachments would panic and fail the test.
func TestPickAttachmentRefusesJunk(t *testing.T) {
	items := []Item{{Kind: "email", Email: &Email{ID: "J", Junk: true}}}
	if _, _, ok := pickAttachment(nil, items, "1", "1"); ok {
		t.Error("pickAttachment on a junk item returned ok=true, want refusal")
	}
}
