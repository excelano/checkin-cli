package main

import "testing"

func TestParseEmails(t *testing.T) {
	data := []byte(`{"value":[
		{"id":"AAA","subject":"Lunch?","from":{"emailAddress":{"name":"Alice"}},
		 "toRecipients":[{"emailAddress":{"name":"Bob","address":"bob@example.com"}}],
		 "ccRecipients":[],"bodyPreview":"want to grab lunch",
		 "receivedDateTime":"2026-07-01T14:30:00Z","hasAttachments":true,
		 "parentFolderId":"INBOX"},
		{"id":"BBB","subject":"","from":{"emailAddress":{"name":""}},
		 "toRecipients":[],"ccRecipients":[],"bodyPreview":"",
		 "receivedDateTime":"2026-07-01T09:00:00Z","hasAttachments":false,
		 "parentFolderId":"JUNK"}
	]}`)

	emails, err := parseEmails(data, "JUNK")
	if err != nil {
		t.Fatalf("parseEmails: %v", err)
	}
	if len(emails) != 2 {
		t.Fatalf("got %d emails, want 2", len(emails))
	}

	a := emails[0]
	if a.ID != "AAA" || a.Subject != "Lunch?" || a.From != "Alice" || a.Preview != "want to grab lunch" {
		t.Errorf("email[0] fields wrong: %+v", a)
	}
	if !a.HasAttachments {
		t.Errorf("email[0] HasAttachments = false, want true")
	}
	if len(a.To) != 1 || a.To[0].Display() != "Bob" {
		t.Errorf("email[0] To = %+v, want single Bob", a.To)
	}
	if a.Received.UTC().Format("2006-01-02T15:04:05Z") != "2026-07-01T14:30:00Z" {
		t.Errorf("email[0] Received = %v, want 2026-07-01T14:30:00Z", a.Received.UTC())
	}
	if emails[1].HasAttachments {
		t.Errorf("email[1] HasAttachments = true, want false")
	}
	if a.Junk {
		t.Errorf("email[0] Junk = true, want false (Inbox folder)")
	}
	if !emails[1].Junk {
		t.Errorf("email[1] Junk = false, want true (Junk folder)")
	}
}

// The Inbox-only list views pass no junk folder ID; nothing they return may
// be flagged, even a message whose parentFolderId happens to be empty.
func TestParseEmailsNoJunkIDNeverFlags(t *testing.T) {
	data := []byte(`{"value":[
		{"id":"AAA","parentFolderId":"JUNK","receivedDateTime":"2026-07-01T14:30:00Z"},
		{"id":"BBB","receivedDateTime":"2026-07-01T14:30:00Z"}
	]}`)
	emails, err := parseEmails(data, "")
	if err != nil {
		t.Fatalf("parseEmails: %v", err)
	}
	for _, e := range emails {
		if e.Junk {
			t.Errorf("email %s Junk = true with no junk folder ID, want false", e.ID)
		}
	}
}

func TestParseEmailsEmpty(t *testing.T) {
	emails, err := parseEmails([]byte(`{"value":[]}`), "")
	if err != nil {
		t.Fatalf("parseEmails: %v", err)
	}
	if len(emails) != 0 {
		t.Errorf("got %d emails, want 0", len(emails))
	}
}

func TestParseEmailsMalformed(t *testing.T) {
	if _, err := parseEmails([]byte(`{not json`), ""); err == nil {
		t.Error("want error on malformed JSON, got nil")
	}
}

func TestStripHTMLPreservesLinks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "named link keeps url in parens",
			in:   `See <a href="https://example.com/report">the report</a> today.`,
			want: "See the report (https://example.com/report) today.",
		},
		{
			name: "link text equal to url is not duplicated",
			in:   `<a href="https://example.com">https://example.com</a>`,
			want: "https://example.com",
		},
		{
			name: "empty anchor text falls back to url",
			in:   `<a href="https://example.com"></a>`,
			want: "https://example.com",
		},
		{
			name: "mailto strips scheme",
			in:   `<a href="mailto:alice@example.com">Alice</a>`,
			want: "Alice (alice@example.com)",
		},
		{
			name: "query entities decoded in url",
			in:   `<a href="https://x.com/s?a=1&amp;b=2">go</a>`,
			want: "go (https://x.com/s?a=1&b=2)",
		},
		{
			name: "single-quoted href",
			in:   `<a href='https://example.com/x'>click here</a>`,
			want: "click here (https://example.com/x)",
		},
		{
			name: "nested tags inside anchor text",
			in:   `<a href="https://example.com"><span>click <b>here</b></span></a>`,
			want: "click here (https://example.com)",
		},
		{
			name: "named anchor without destination drops to text",
			in:   `<a name="top">Top</a>`,
			want: "Top",
		},
		{
			name: "javascript href drops to text",
			in:   `<a href="javascript:void(0)">Menu</a>`,
			want: "Menu",
		},
		{
			name: "fragment href drops to text",
			in:   `<a href="#section">Section</a>`,
			want: "Section",
		},
		{
			name: "data href drops to text",
			in:   `<a href="data:text/html;base64,PHNjcmlwdD4=">Invoice</a>`,
			want: "Invoice",
		},
		{
			name: "vbscript href drops to text",
			in:   `<a href="vbscript:msgbox(1)">Open</a>`,
			want: "Open",
		},
		{
			name: "scheme match ignores case",
			in:   `<a href="DaTa:text/html,<h1>hi</h1>">Report</a>`,
			want: "Report",
		},
		{
			name: "data href inside a safe link drops to text",
			in:   `<a href="https://na01.safelinks.protection.outlook.com/?url=data%3Atext%2Fhtml%2C%3Ch1%3Ehi%3C%2Fh1%3E&amp;data=x">Statement</a>`,
			want: "Statement",
		},
		{
			name: "two links in one line",
			in:   `<a href="https://a.com">A</a> and <a href="https://b.com">B</a>`,
			want: "A (https://a.com) and B (https://b.com)",
		},
		{
			name: "angle brackets in url survive the tag strip",
			in:   `<a href="https://example.com/x?a=<b>c">link</a>`,
			want: "link (https://example.com/x?a=<b>c)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripHTML(c.in); got != c.want {
				t.Errorf("stripHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestUnwrapSafeLink(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "amp-encoded separators",
			in:   `https://nam12.safelinks.protection.outlook.com/?url=https%3A%2F%2Fexample.com%2Freport&amp;data=05%7Cabc&amp;reserved=0`,
			want: "https://example.com/report",
		},
		{
			name: "literal ampersand separators",
			in:   `https://nam12.safelinks.protection.outlook.com/?url=https%3A%2F%2Fexample.com&data=05%7Cabc`,
			want: "https://example.com",
		},
		{
			name: "tenant subdomain host",
			in:   `https://contoso.safelinks.protection.outlook.com/?url=https%3A%2F%2Fexample.com%2Fx&amp;data=z`,
			want: "https://example.com/x",
		},
		{
			name: "uppercase host still matches",
			in:   `https://NAM06.SafeLinks.Protection.Outlook.com/?url=https%3A%2F%2Fexample.com&amp;data=z`,
			want: "https://example.com",
		},
		{
			name: "double-encoded nested query recovered",
			in:   `https://nam06.safelinks.protection.outlook.com/?url=https%3A%2F%2Fexample.com%2Fa%3Fx%3D1%26y%3D2&amp;data=z`,
			want: "https://example.com/a?x=1&y=2",
		},
		{
			name: "non-safelinks url with url param untouched",
			in:   `https://example.com/redirect?url=https%3A%2F%2Fevil.com`,
			want: `https://example.com/redirect?url=https%3A%2F%2Fevil.com`,
		},
		{
			name: "safelinks host without url param untouched",
			in:   `https://nam12.safelinks.protection.outlook.com/`,
			want: `https://nam12.safelinks.protection.outlook.com/`,
		},
		{
			name: "plain url untouched",
			in:   "https://example.com/foo",
			want: "https://example.com/foo",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unwrapSafeLink(c.in); got != c.want {
				t.Errorf("unwrapSafeLink(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStripHTMLUnwrapsSafeLinks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "named safelink keeps original destination",
			in:   `See <a href="https://nam12.safelinks.protection.outlook.com/?url=https%3A%2F%2Fexample.com%2Freport&amp;data=05%7Cabc&amp;reserved=0">the report</a>.`,
			want: "See the report (https://example.com/report).",
		},
		{
			name: "safelink whose text is the original url collapses",
			in:   `<a href="https://nam12.safelinks.protection.outlook.com/?url=https%3A%2F%2Fexample.com&amp;data=z">https://example.com</a>`,
			want: "https://example.com",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripHTML(c.in); got != c.want {
				t.Errorf("stripHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStripHTMLBasics(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"paragraph breaks", "<p>one</p><p>two</p>", "one\ntwo"},
		{"br to newline", "line1<br>line2", "line1\nline2"},
		{"entities decoded", "a &amp; b &lt;c&gt;", "a & b <c>"},
		{"tags removed", "<b>bold</b> plain", "bold plain"},
		{"collapse blank runs", "a\n\n\n\nb", "a\n\nb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripHTML(c.in); got != c.want {
				t.Errorf("stripHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
