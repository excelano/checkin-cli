package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

const graphBaseURL = "https://graph.microsoft.com/v1.0"

type GraphClient struct {
	tokenSource oauth2.TokenSource
	httpClient  *http.Client
	userID      string
	userMail    string
	tenantID    string
	clientID    string
	junkFolder  string // Junk Email folder ID, resolved lazily by junkFolderID
}

func NewGraphClient(cfg Config, tok *oauth2.Token) (*GraphClient, error) {
	src := oauthConfig(cfg).TokenSource(context.Background(), tok)
	g := &GraphClient{
		tokenSource: src,
		httpClient:  &http.Client{},
		// clientID is the app registration ID, reused as the presence
		// setPresence sessionId (Graph requires the session id be the app's
		// client id).
		clientID: cfg.ClientID,
	}

	// Fetch user ID + primary mail address. userID filters unread chats;
	// userMail filters self out of reply-all recipient displays. mail can
	// be null for shared/guest accounts — userPrincipalName covers that
	// fallback for the rare case.
	data, err := g.get("/me", url.Values{"$select": {"id,mail,userPrincipalName"}})
	if err != nil {
		return nil, fmt.Errorf("fetching user profile: %w", err)
	}
	var me struct {
		ID                string `json:"id"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := json.Unmarshal(data, &me); err != nil {
		return nil, err
	}
	g.userID = me.ID
	g.userMail = me.Mail
	if g.userMail == "" {
		g.userMail = me.UserPrincipalName
	}

	// Pull the home tenant id off the access token. markChatReadForUser
	// needs a teamworkUserIdentity with both id and tenantId in the body,
	// and the token's `tid` claim is the authoritative home tenant of the
	// signed-in account (matches what MSAL exposes as homeAccountId.tenantId
	// in the iOS app). Falls back to the configured tenant id if the token
	// isn't a JWT — Graph access tokens are JWTs in practice.
	g.tenantID = tidFromToken(tok.AccessToken)
	if g.tenantID == "" {
		g.tenantID = cfg.TenantID
	}

	return g, nil
}

// tidFromToken extracts the `tid` claim from a JWT access token without
// validating the signature (we trust the token because we just acquired it).
// Returns "" if the token isn't a parseable JWT.
func tidFromToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		TID string `json:"tid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.TID
}

func (g *GraphClient) get(path string, query url.Values) ([]byte, error) {
	u := graphBaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	return g.do(req)
}

func (g *GraphClient) patch(path string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH", graphBaseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = g.do(req)
	return err
}

func (g *GraphClient) post(path string, body interface{}) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", graphBaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return g.do(req)
}

func (g *GraphClient) do(req *http.Request) ([]byte, error) {
	tok, err := g.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("getting token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("graph API %s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(body))
	}

	return body, nil
}
